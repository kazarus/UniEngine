// UniStatus_test
// #完整性校验失败通知钩子(StatusHook)测试

package UniEngine

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"log"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// #statusRecorder 线程安全地记录 StatusHook 事件(引擎在查询 goroutine 内同步回调,
// #并发查询时钩子会被多 goroutine 同时调用)
type statusRecorder struct {
	mu     sync.Mutex
	events []TUniStatus
}

func (r *statusRecorder) hook(Status TUniStatus) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, Status)
}

func (r *statusRecorder) snapshot() []TUniStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]TUniStatus, len(r.events))
	copy(out, r.events)
	return out
}

// #tamperedVerifyValues 返回"库内 data_stat 被直改 1→0、MAC 仍是旧值"的驱动行。
func tamperedVerifyValues(t *testing.T, eng *TUniEngine) []driver.Value {
	t.Helper()
	signed := TVerifyRow{WhoBuild: 1, UserIndx: 2, DataStat: 1, MastName: "mast"}
	return []driver.Value{int64(1), int64(2), int64(0), "mast", verifyExpect(t, eng, signed)}
}

// #goodVerifyValues 返回与签名一致、可验签通过的一行。
func goodVerifyValues(t *testing.T, eng *TUniEngine) []driver.Value {
	t.Helper()
	row := TVerifyRow{WhoBuild: 1, UserIndx: 2, DataStat: 1, MastName: "mast"}
	return []driver.Value{int64(1), int64(2), int64(1), "mast", verifyExpect(t, eng, row)}
}

// #测试1:严格模式 MAC 不匹配——钩子拿到"未放行"事件,错误即调用方拿到的同一个错误
func TestStatusHookStrictMismatch(t *testing.T) {
	d := &mockDriver{columns: verifyRowColumns()}
	eng := newVerifyTestEngine(d)
	eng.VerificationMode = 2
	d.queryRows = [][]driver.Value{tamperedVerifyValues(t, eng)}

	rec := &statusRecorder{}
	eng.StatusHook = rec.hook

	var rows []TVerifyRow
	eror := eng.SelectL(&rows, "select "+verifyRowCols+" from tbl_verify_row")
	if eror == nil || !strings.Contains(eror.Error(), "integrity check failed") {
		t.Fatalf("strict mode should reject tampered row, got %v", eror)
	}

	events := rec.snapshot()
	if len(events) != 1 {
		t.Fatalf("StatusHook should fire exactly once, got %d", len(events))
	}
	ev := events[0]
	if ev.TablFrom != "tbl_verify_row" || ev.MethodIn != "SelectL" || ev.Mode != 2 || ev.Row != 0 || ev.Accepted {
		t.Fatalf("unexpected status payload: %+v", ev)
	}
	if ev.ErrorOut == nil || ev.ErrorOut.Error() != eror.Error() {
		t.Fatalf("status err should be the failure returned to caller: %v vs %v", ev.ErrorOut, eror)
	}
	//#脱敏:错误文本只带表名,不得出现行值
	if strings.Contains(ev.ErrorOut.Error(), "mast") {
		t.Fatalf("status err must not leak row values: %q", ev.ErrorOut.Error())
	}
	//#Engine + 主键:下游可据此定位并落审计表
	if ev.Engine != eng {
		t.Fatalf("status should carry the engine instance")
	}
	wantKeys := []TUniPkey{{Column: "who_build", Value: int64(1)}, {Column: "user_indx", Value: int64(2)}}
	if !reflect.DeepEqual(ev.Pkeys, wantKeys) {
		t.Fatalf("status pkeys should locate the failing row, got %+v", ev.Pkeys)
	}
}

// #测试2:宽容模式 MAC 不匹配——行被放行,但仍以 Accepted=true 通知一次,告警日志保留
func TestStatusHookLenientMismatchAccepted(t *testing.T) {
	d := &mockDriver{columns: verifyRowColumns()}
	eng := newVerifyTestEngine(d)
	eng.VerificationMode = 1
	d.queryRows = [][]driver.Value{tamperedVerifyValues(t, eng)}

	rec := &statusRecorder{}
	eng.StatusHook = rec.hook

	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)

	var rows []TVerifyRow
	if eror := eng.SelectL(&rows, "select "+verifyRowCols+" from tbl_verify_row"); eror != nil {
		t.Fatalf("lenient mode must accept the row: %v", eror)
	}
	if len(rows) != 1 || rows[0].DataStat != 0 {
		t.Fatalf("lenient mode must return the row to caller: %+v", rows)
	}
	if !strings.Contains(buf.String(), "MAC mismatch") {
		t.Fatalf("lenient warning should be kept, got %q", buf.String())
	}

	events := rec.snapshot()
	if len(events) != 1 {
		t.Fatalf("StatusHook should fire exactly once, got %d", len(events))
	}
	ev := events[0]
	if !ev.Accepted || ev.Mode != 1 || ev.Row != 0 || ev.MethodIn != "SelectL" {
		t.Fatalf("unexpected status payload: %+v", ev)
	}
	if ev.ErrorOut == nil || !strings.Contains(ev.ErrorOut.Error(), "integrity check failed") {
		t.Fatalf("accepted status should still carry the failure reason, got %v", ev.ErrorOut)
	}
}

// #测试3:严格模式无 MAC(存量行)——钩子事件携带 no mac value
func TestStatusHookStrictNoMac(t *testing.T) {
	d := &mockDriver{
		columns:   verifyRowColumns(),
		queryRows: [][]driver.Value{{int64(1), int64(2), int64(1), "mast", ""}},
	}
	eng := newVerifyTestEngine(d)
	eng.VerificationMode = 2

	rec := &statusRecorder{}
	eng.StatusHook = rec.hook

	var rows []TVerifyRow
	eror := eng.SelectL(&rows, "select "+verifyRowCols+" from tbl_verify_row")
	if eror == nil || !strings.Contains(eror.Error(), "no mac value") {
		t.Fatalf("strict mode must reject legacy rows, got %v", eror)
	}

	events := rec.snapshot()
	if len(events) != 1 || events[0].Accepted {
		t.Fatalf("legacy row failure should be reported as rejected: %+v", events)
	}
	if !strings.Contains(events[0].ErrorOut.Error(), "no mac value") {
		t.Fatalf("unexpected status err: %v", events[0].ErrorOut)
	}
}

// #测试4:非空无标签值——宽容模式同样报错(旧语义),钩子按"未放行"记录
func TestStatusHookBadPrefix(t *testing.T) {
	d := &mockDriver{
		columns:   verifyRowColumns(),
		queryRows: [][]driver.Value{{int64(1), int64(2), int64(1), "mast", "not-a-mac"}},
	}
	eng := newVerifyTestEngine(d)
	eng.VerificationMode = 1

	rec := &statusRecorder{}
	eng.StatusHook = rec.hook

	var rows []TVerifyRow
	eror := eng.SelectL(&rows, "select "+verifyRowCols+" from tbl_verify_row")
	if eror == nil || !strings.Contains(eror.Error(), "invalid") {
		t.Fatalf("bad prefix must fail even in lenient mode, got %v", eror)
	}

	events := rec.snapshot()
	if len(events) != 1 || events[0].Accepted || !strings.Contains(events[0].ErrorOut.Error(), "invalid") {
		t.Fatalf("unexpected status payload: %+v", events)
	}
}

// #测试5:Base64 解码失败——钩子事件携带 not base64
func TestStatusHookBadBase64(t *testing.T) {
	d := &mockDriver{
		columns:   verifyRowColumns(),
		queryRows: [][]driver.Value{{int64(1), int64(2), int64(1), "mast", UniVerifyTag + "%%%"}},
	}
	eng := newVerifyTestEngine(d)

	rec := &statusRecorder{}
	eng.StatusHook = rec.hook

	var rows []TVerifyRow
	eror := eng.SelectL(&rows, "select "+verifyRowCols+" from tbl_verify_row")
	if eror == nil || !strings.Contains(eror.Error(), "not base64") {
		t.Fatalf("bad base64 must fail, got %v", eror)
	}

	events := rec.snapshot()
	if len(events) != 1 || !strings.Contains(events[0].ErrorOut.Error(), "not base64") {
		t.Fatalf("unexpected status payload: %+v", events)
	}
}

// #测试6:严格模式列投影不足——尚未读行即失败,钩子 Row=-1,错误可 errors.Is 判别
func TestStatusHookIncompleteProjection(t *testing.T) {
	d := &mockDriver{
		columns:   []string{"who_build", "user_indx"},
		queryRows: [][]driver.Value{{int64(1), int64(2)}},
	}
	eng := newVerifyTestEngine(d)
	eng.VerificationMode = 2

	rec := &statusRecorder{}
	eng.StatusHook = rec.hook

	var rows []TVerifyRow
	eror := eng.SelectL(&rows, "select who_build,user_indx from tbl_verify_row")
	if !errors.Is(eror, ErrIncompleteVerification) {
		t.Fatalf("strict partial select should fail with ErrIncompleteVerification, got %v", eror)
	}

	events := rec.snapshot()
	if len(events) != 1 {
		t.Fatalf("StatusHook should fire exactly once, got %d", len(events))
	}
	ev := events[0]
	if ev.Row != -1 || ev.Accepted || ev.MethodIn != "SelectL" || ev.TablFrom != "tbl_verify_row" {
		t.Fatalf("unexpected status payload: %+v", ev)
	}
	if !errors.Is(ev.ErrorOut, ErrIncompleteVerification) {
		t.Fatalf("status err should wrap ErrIncompleteVerification, got %v", ev.ErrorOut)
	}
	//#还没读到行:主键未知,但引擎实例仍要带上
	if ev.Engine != eng || ev.Pkeys != nil {
		t.Fatalf("projection failure should carry engine and no pkeys, got engine=%v pkeys=%+v", ev.Engine, ev.Pkeys)
	}
}

// #测试7:多行结果按行号上报——第 0/2 行通过、第 1 行失配,只产生一条 Row=1 的事件
func TestStatusHookRowIndex(t *testing.T) {
	d := &mockDriver{columns: verifyRowColumns()}
	eng := newVerifyTestEngine(d)
	eng.VerificationMode = 1
	d.queryRows = [][]driver.Value{
		goodVerifyValues(t, eng),
		tamperedVerifyValues(t, eng),
		goodVerifyValues(t, eng),
	}

	rec := &statusRecorder{}
	eng.StatusHook = rec.hook

	var rows []TVerifyRow
	if eror := eng.SelectL(&rows, "select "+verifyRowCols+" from tbl_verify_row"); eror != nil {
		t.Fatalf("lenient mode must accept all rows: %v", eror)
	}
	if len(rows) != 3 {
		t.Fatalf("all rows should be returned, got %d", len(rows))
	}

	events := rec.snapshot()
	if len(events) != 1 || events[0].Row != 1 || !events[0].Accepted {
		t.Fatalf("only the tampered row should be reported, got %+v", events)
	}
	wantKeys := []TUniPkey{{Column: "who_build", Value: int64(1)}, {Column: "user_indx", Value: int64(2)}}
	if !reflect.DeepEqual(events[0].Pkeys, wantKeys) {
		t.Fatalf("row index event should carry its own pkeys, got %+v", events[0].Pkeys)
	}
}

// #测试8:单 struct 入口——Method 记录为 Select(不是内部公共核心名)
func TestStatusHookMethodSelect(t *testing.T) {
	d := &mockDriver{columns: verifyRowColumns()}
	eng := newVerifyTestEngine(d)
	eng.VerificationMode = 2
	d.queryRows = [][]driver.Value{tamperedVerifyValues(t, eng)}

	rec := &statusRecorder{}
	eng.StatusHook = rec.hook

	var one TVerifyRow
	if eror := eng.Select(&one, "select "+verifyRowCols+" from tbl_verify_row"); eror == nil {
		t.Fatal("strict mode should reject tampered row")
	}

	events := rec.snapshot()
	if len(events) != 1 || events[0].MethodIn != "Select" {
		t.Fatalf("method should be Select, got %+v", events)
	}
}

// #测试9:结果钩子(SetSqlResult)路径同样上报——钩子填好结构体后的补验签失败
func TestStatusHookResultHookPath(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.VerifyOn, eng.VerificationMode, eng.VerifyBy = 1, 2, "status-hook-key"
	eng.RegisterClass(setSqlResultRow{}, "hook_verify").SetKeys("user_name").
		SetVerify("password", "user_name")

	signed := setSqlResultRow{UserName: "kazarus"}
	if eror := eng.VerifySignRow(&signed); eror != nil {
		t.Fatal(eror)
	}

	d.columns = []string{"user_name", "password"}
	d.queryRows = [][]driver.Value{{"tampered", signed.Password}}

	rec := &statusRecorder{}
	eng.StatusHook = rec.hook

	var rows []setSqlResultRow
	eror := eng.SelectL(&rows, "select user_name,password from hook_verify")
	if eror == nil || !strings.Contains(eror.Error(), "integrity check failed") {
		t.Fatalf("result hook path should verify and fail, got %v", eror)
	}

	events := rec.snapshot()
	if len(events) != 1 {
		t.Fatalf("StatusHook should fire exactly once, got %d", len(events))
	}
	if events[0].TablFrom != "hook_verify" || events[0].MethodIn != "SelectL" || events[0].Row != 0 || events[0].Accepted {
		t.Fatalf("unexpected status payload: %+v", events[0])
	}
	wantKeys := []TUniPkey{{Column: "user_name", Value: "tampered"}}
	if !reflect.DeepEqual(events[0].Pkeys, wantKeys) {
		t.Fatalf("result hook row pkeys mismatch: %+v", events[0].Pkeys)
	}
}

// #测试10:验签通过不产生事件;宽容模式投影不足(跳过验签)也不算失败,同样不产生事件
func TestStatusHookSilentWhenNoFailure(t *testing.T) {
	d := &mockDriver{columns: verifyRowColumns()}
	eng := newVerifyTestEngine(d)
	eng.VerificationMode = 1
	d.queryRows = [][]driver.Value{goodVerifyValues(t, eng)}

	rec := &statusRecorder{}
	eng.StatusHook = rec.hook

	var rows []TVerifyRow
	if eror := eng.SelectL(&rows, "select "+verifyRowCols+" from tbl_verify_row"); eror != nil {
		t.Fatalf("verified row should pass: %v", eror)
	}

	//#宽容模式部分列查询沿用"跳过验签"语义:不是失败,不应上报
	d.columns = []string{"who_build", "user_indx"}
	d.queryRows = [][]driver.Value{{int64(1), int64(2)}}
	if eror := eng.SelectL(&rows, "select who_build,user_indx from tbl_verify_row"); eror != nil {
		t.Fatalf("lenient partial select should skip verification: %v", eror)
	}

	if events := rec.snapshot(); len(events) != 0 {
		t.Fatalf("no status should be reported without a failure, got %+v", events)
	}
}

// #测试11:并发查询下钩子被多 goroutine 调用——事件条数正确且记录器无竞态(-race 下验证)
func TestStatusHookConcurrent(t *testing.T) {
	d := &mockDriver{columns: verifyRowColumns()}
	eng := newVerifyTestEngine(d)
	eng.VerificationMode = 1
	d.queryRows = [][]driver.Value{tamperedVerifyValues(t, eng)}

	rec := &statusRecorder{}
	eng.StatusHook = rec.hook

	const workers, rounds = 8, 5
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < rounds; j++ {
				var rows []TVerifyRow
				if eror := eng.SelectL(&rows, "select "+verifyRowCols+" from tbl_verify_row"); eror != nil {
					t.Errorf("lenient concurrent query failed: %v", eror)
					return
				}
			}
		}()
	}
	wg.Wait()

	if events := rec.snapshot(); len(events) != workers*rounds {
		t.Fatalf("expect %d events, got %d", workers*rounds, len(events))
	}
}

// #测试12:主键载荷的 JSON 形态稳定——审计表按列存储/检索依赖 column/value 契约
func TestStatusPkeyJSON(t *testing.T) {
	Keys, eror := json.Marshal([]TUniPkey{
		{Column: "who_build", Value: int64(1)},
		{Column: "user_indx", Value: int64(2)},
	})
	if eror != nil {
		t.Fatal(eror)
	}

	want := `[{"column":"who_build","value":1},{"column":"user_indx","value":2}]`
	if string(Keys) != want {
		t.Fatalf("pkey json contract changed:\n got %s\nwant %s", Keys, want)
	}
}

// #测试13:VerifyUnkeyed 且查询未投影主键时,不得把结构体零值当成主键上报
func TestStatusHookUnkeyedPkeysNotProjected(t *testing.T) {
	d := &mockDriver{columns: []string{"data_stat", "mast_name", "mac_text"}}
	eng := newMockEngine(d)
	eng.VerifyOn, eng.VerificationMode, eng.VerifyBy = 1, 2, "test-verify-key"

	Tbl := eng.RegisterClass(TVerifyRow{}, "tbl_verify_row").SetKeys("who_build", "user_indx")
	Tbl.VerifyUnkeyed = true //#显式放弃行级绑定:保护列可不含主键
	Tbl = Tbl.SetVerify("mac_text", "data_stat", "mast_name")
	if Tbl.LastError != nil {
		t.Fatal(Tbl.LastError)
	}

	//#签名时 data_stat=1,库内被改成 0;查询只投影保护列与 MAC 列,不含主键
	Signed := TVerifyRow{DataStat: 1, MastName: "mast"}
	Payload, eror := verifyPayload("tbl_verify_row", []string{"data_stat", "mast_name"}, []int{2, 3}, reflect.ValueOf(Signed))
	if eror != nil {
		t.Fatal(eror)
	}
	Sum, eror := eng.verifySign(Payload)
	if eror != nil {
		t.Fatal(eror)
	}
	d.queryRows = [][]driver.Value{{int64(0), "mast", verifyTextOf(Sum)}}

	rec := &statusRecorder{}
	eng.StatusHook = rec.hook

	var rows []TVerifyRow
	if eror := eng.SelectL(&rows, "select data_stat,mast_name,mac_text from tbl_verify_row"); eror == nil {
		t.Fatal("strict mode should reject the tampered row")
	}

	events := rec.snapshot()
	if len(events) != 1 {
		t.Fatalf("StatusHook should fire exactly once, got %d", len(events))
	}
	if len(events[0].Pkeys) != 0 {
		t.Fatalf("unprojected pkeys must not be reported as zero values: %+v", events[0].Pkeys)
	}
	if events[0].Engine != eng || events[0].TablFrom != "tbl_verify_row" {
		t.Fatalf("unexpected status payload: %+v", events[0])
	}
}

// #statusHookRow 结果钩子用例类:按列名回填,容忍结果列被裁剪(不越界取 fields)
type statusHookRow struct {
	UserName string `db:"user_name"`
	Password string `db:"password"`
}

// #statusText 结果钩子取列值:驱动可能返回 string 或 []byte
func statusText(Value interface{}) string {
	switch Item := Value.(type) {
	case string:
		return Item
	case []byte:
		return string(Item)
	}
	return ""
}

func (u statusHookRow) SetSqlResult(_ *TUniEngine, obj interface{}, column []string, fields []interface{}) {
	p, Ok := obj.(*statusHookRow)
	if !Ok {
		return
	}
	for i, name := range column {
		Text := statusText(fields[i])
		switch strings.ToLower(name) {
		case "user_name":
			p.UserName = Text
		case "password":
			p.Password = Text
		}
	}
}

// #newStatusHookEngine 构造"SetSqlResult 结果钩子类 + 激活验签"的引擎。
func newStatusHookEngine(d *mockDriver, mode int64) *TUniEngine {
	eng := newMockEngine(d)
	eng.VerifyOn, eng.VerificationMode, eng.VerifyBy = 1, mode, "status-hook-key"
	eng.RegisterClass(statusHookRow{}, "hook_verify").SetKeys("user_name").
		SetVerify("password", "user_name")
	return eng
}

// #测试14:结果钩子 + 宽容模式 + 列覆盖完整——行放行,StatusHook 以 Accepted=true 回调
func TestStatusHookResultHookLenientAccepted(t *testing.T) {
	d := &mockDriver{}
	eng := newStatusHookEngine(d, 1)

	Signed := statusHookRow{UserName: "kazarus"}
	if eror := eng.VerifySignRow(&Signed); eror != nil {
		t.Fatal(eror)
	}

	d.columns = []string{"user_name", "password"}
	d.queryRows = [][]driver.Value{{"tampered", Signed.Password}}

	rec := &statusRecorder{}
	eng.StatusHook = rec.hook

	var rows []statusHookRow
	if eror := eng.SelectL(&rows, "select user_name,password from hook_verify"); eror != nil {
		t.Fatalf("lenient mode must accept the hook row: %v", eror)
	}
	if len(rows) != 1 || rows[0].UserName != "tampered" {
		t.Fatalf("lenient mode must return the row: %+v", rows)
	}

	events := rec.snapshot()
	if len(events) != 1 || !events[0].Accepted || events[0].Row != 0 || events[0].MethodIn != "SelectL" {
		t.Fatalf("unexpected status payload: %+v", events)
	}
	if !strings.Contains(events[0].ErrorOut.Error(), "integrity check failed") {
		t.Fatalf("unexpected status err: %v", events[0].ErrorOut)
	}
}

// #测试15:结果钩子 + 严格模式 + 列覆盖不足——查询在读到行前失败,钩子 Row=-1 且无主键
func TestStatusHookResultHookIncompleteStrict(t *testing.T) {
	d := &mockDriver{columns: []string{"user_name"}, queryRows: [][]driver.Value{{"kazarus"}}}
	eng := newStatusHookEngine(d, 2)

	rec := &statusRecorder{}
	eng.StatusHook = rec.hook

	var rows []statusHookRow
	eror := eng.SelectL(&rows, "select user_name from hook_verify")
	if !errors.Is(eror, ErrIncompleteVerification) {
		t.Fatalf("strict hook select missing MAC column should fail, got %v", eror)
	}

	events := rec.snapshot()
	if len(events) != 1 || events[0].Row != -1 || events[0].Accepted || events[0].TablFrom != "hook_verify" {
		t.Fatalf("unexpected status payload: %+v", events)
	}
	if !errors.Is(events[0].ErrorOut, ErrIncompleteVerification) || len(events[0].Pkeys) != 0 {
		t.Fatalf("unexpected status err/pkeys: %+v", events[0])
	}
}

// #测试16:结果钩子 + 宽容模式 + 列覆盖不足——按设计跳过验签:不报错,也不产生 StatusHook 事件
func TestStatusHookResultHookIncompleteLenientSkips(t *testing.T) {
	d := &mockDriver{columns: []string{"user_name"}, queryRows: [][]driver.Value{{"kazarus"}}}
	eng := newStatusHookEngine(d, 1)

	rec := &statusRecorder{}
	eng.StatusHook = rec.hook

	var rows []statusHookRow
	if eror := eng.SelectL(&rows, "select user_name from hook_verify"); eror != nil {
		t.Fatalf("lenient hook select should skip verification, got %v", eror)
	}
	if len(rows) != 1 || rows[0].UserName != "kazarus" {
		t.Fatalf("hook should still fill the row: %+v", rows)
	}
	if events := rec.snapshot(); len(events) != 0 {
		t.Fatalf("skipped verification is not a failure, got %+v", events)
	}
}

// #hookSkipMacRow 故意不回填 MAC 列的结果钩子类(验证回填守卫)
type hookSkipMacRow struct {
	UserName string `db:"user_name"`
	Password string `db:"password"`
}

func (u hookSkipMacRow) SetSqlResult(_ *TUniEngine, obj interface{}, column []string, fields []interface{}) {
	p, Ok := obj.(*hookSkipMacRow)
	if !Ok {
		return
	}
	for i, name := range column {
		if strings.ToLower(name) == "user_name" {
			Text, _ := fields[i].(string)
			p.UserName = Text
		}
		//#故意漏掉 password(MAC 列)
	}
}

// #newHookSkipMacEngine 构造"漏回填 MAC 的结果钩子类 + 激活验签"的引擎。
func newHookSkipMacEngine(d *mockDriver, Mode int64) *TUniEngine {
	eng := newMockEngine(d)
	eng.VerifyOn, eng.VerificationMode, eng.VerifyBy = 1, Mode, "hook-guard-key"
	eng.RegisterClass(hookSkipMacRow{}, "hook_guard").SetKeys("user_name").
		SetVerify("password", "user_name")
	return eng
}

// #hookGuardMac 按 hook_guard 表计算一行合法 MAC(保护列仅 user_name)。
func hookGuardMac(t *testing.T, eng *TUniEngine, UserName string) string {
	t.Helper()
	Payload, eror := verifyPayload("hook_guard", []string{"user_name"}, []int{0}, reflect.ValueOf(hookSkipMacRow{UserName: UserName}))
	if eror != nil {
		t.Fatal(eror)
	}
	Sum, eror := eng.verifySign(Payload)
	if eror != nil {
		t.Fatal(eror)
	}
	return verifyTextOf(Sum)
}

// #测试17:结果钩子漏回填 MAC(库中有值)——两种模式都 fail-fast,并经 StatusHook 上报
func TestVerifyHookAssignGuard(t *testing.T) {
	for _, Mode := range []int64{1, 2} {
		d := &mockDriver{columns: []string{"user_name", "password"}}
		eng := newHookSkipMacEngine(d, Mode)
		Tbl := eng.GetTableByClass("UniEngine.hookSkipMacRow")
		if Tbl == nil {
			t.Fatal("guard table not registered")
		}
		if Tbl = Tbl.SetModule("audit"); Tbl.LastError != nil {
			t.Fatal(Tbl.LastError)
		}
		d.queryRows = [][]driver.Value{{"kazarus", hookGuardMac(t, eng, "kazarus")}}

		rec := &statusRecorder{}
		eng.StatusHook = rec.hook

		var rows []hookSkipMacRow
		eror := eng.SelectL(&rows, "select user_name,password from hook_guard")
		if eror == nil || !strings.Contains(eror.Error(), "SetSqlResult did not assign MAC column[password]") {
			t.Fatalf("mode=%d: hook missing MAC must fail fast, got %v", Mode, eror)
		}

		events := rec.snapshot()
		if len(events) != 1 {
			t.Fatalf("mode=%d: expect one status event, got %+v", Mode, events)
		}
		ev := events[0]
		if ev.TablFrom != "hook_guard" || ev.ModuleBy != "audit" || ev.MethodIn != "SelectL" || ev.Row != 0 || ev.Accepted {
			t.Fatalf("mode=%d: unexpected status payload: %+v", Mode, ev)
		}
		if !strings.Contains(ev.ErrorOut.Error(), "did not assign MAC column") {
			t.Fatalf("mode=%d: unexpected status err: %v", Mode, ev.ErrorOut)
		}
		//#回填缺失时结构体主键不可信,事件不带 Pkeys
		if len(ev.Pkeys) != 0 {
			t.Fatalf("mode=%d: guard event must not carry pkeys, got %+v", Mode, ev.Pkeys)
		}
	}
}

// #测试18:库中 MAC 为空(存量行)——守卫不误报,仍按原语义分流
func TestVerifyHookAssignGuardAllowsEmptyRawMac(t *testing.T) {
	//#宽容:存量直通
	d := &mockDriver{columns: []string{"user_name", "password"}, queryRows: [][]driver.Value{{"kazarus", ""}}}
	eng := newHookSkipMacEngine(d, 1)

	rec := &statusRecorder{}
	eng.StatusHook = rec.hook

	var rows []hookSkipMacRow
	if eror := eng.SelectL(&rows, "select user_name,password from hook_guard"); eror != nil {
		t.Fatalf("empty raw MAC should keep legacy skip semantics: %v", eror)
	}
	if len(rows) != 1 || rows[0].UserName != "kazarus" {
		t.Fatalf("row should be returned: %+v", rows)
	}
	if events := rec.snapshot(); len(events) != 0 {
		t.Fatalf("empty MAC skip is not a failure: %+v", events)
	}

	//#严格:仍报 no mac value(守卫不改变既有判定)
	d2 := &mockDriver{columns: []string{"user_name", "password"}, queryRows: [][]driver.Value{{"kazarus", ""}}}
	eng2 := newHookSkipMacEngine(d2, 2)
	var rows2 []hookSkipMacRow
	eror := eng2.SelectL(&rows2, "select user_name,password from hook_guard")
	if eror == nil || !strings.Contains(eror.Error(), "no mac value") {
		t.Fatalf("strict mode should still report no mac value, got %v", eror)
	}
}

// #测试19:钩子正常回填 MAC 时守卫不干预(结果集 MAC 为 []byte 也能识别)
func TestVerifyHookAssignGuardAcceptsAssignedMac(t *testing.T) {
	d := &mockDriver{columns: []string{"user_name", "password"}}
	eng := newStatusHookEngine(d, 2)
	Signed := statusHookRow{UserName: "kazarus"}
	if eror := eng.VerifySignRow(&Signed); eror != nil {
		t.Fatal(eror)
	}
	//#驱动返回 []byte 形态的 MAC 值,守卫按字符串比较
	d.queryRows = [][]driver.Value{{"kazarus", []byte(Signed.Password)}}

	var rows []statusHookRow
	if eror := eng.SelectL(&rows, "select user_name,password from hook_verify"); eror != nil {
		t.Fatalf("assigned MAC should pass verification: %v", eror)
	}
	if len(rows) != 1 || rows[0].Password != Signed.Password {
		t.Fatalf("hook should fill MAC column: %+v", rows)
	}
}

// #测试20:状态事件携带表所属模块(ModuleBy)——行级与非行级事件都带
func TestStatusModuleBy(t *testing.T) {
	//#行级:严格失配
	d := &mockDriver{columns: verifyRowColumns()}
	eng := newVerifyTestEngine(d)
	eng.VerificationMode = 2
	Tbl := eng.GetTableByClass("UniEngine.TVerifyRow")
	if Tbl == nil {
		t.Fatal("table not registered")
	}
	if Tbl = Tbl.SetModule("core"); Tbl.LastError != nil {
		t.Fatal(Tbl.LastError)
	}
	d.queryRows = [][]driver.Value{tamperedVerifyValues(t, eng)}

	rec := &statusRecorder{}
	eng.StatusHook = rec.hook

	var rows []TVerifyRow
	if eror := eng.SelectL(&rows, "select "+verifyRowCols+" from tbl_verify_row"); eror == nil {
		t.Fatal("strict mode should reject the tampered row")
	}
	events := rec.snapshot()
	if len(events) != 1 || events[0].ModuleBy != "core" {
		t.Fatalf("row event should carry ModuleBy, got %+v", events)
	}

	//#非行级:严格模式列投影不足
	d2 := &mockDriver{
		columns:   []string{"who_build", "user_indx"},
		queryRows: [][]driver.Value{{int64(1), int64(2)}},
	}
	eng2 := newVerifyTestEngine(d2)
	eng2.VerificationMode = 2
	Tbl2 := eng2.GetTableByClass("UniEngine.TVerifyRow")
	if Tbl2 = Tbl2.SetModule("risk"); Tbl2.LastError != nil {
		t.Fatal(Tbl2.LastError)
	}

	rec2 := &statusRecorder{}
	eng2.StatusHook = rec2.hook

	var rows2 []TVerifyRow
	if eror := eng2.SelectL(&rows2, "select who_build,user_indx from tbl_verify_row"); eror == nil {
		t.Fatal("strict partial select should fail")
	}
	events2 := rec2.snapshot()
	if len(events2) != 1 || events2[0].ModuleBy != "risk" || events2[0].Row != -1 {
		t.Fatalf("projection event should carry ModuleBy, got %+v", events2)
	}
}
