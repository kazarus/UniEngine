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
	if ev.Table != "tbl_verify_row" || ev.Method != "SelectL" || ev.Mode != 2 || ev.Row != 0 || ev.Accepted {
		t.Fatalf("unexpected status payload: %+v", ev)
	}
	if ev.Err == nil || ev.Err.Error() != eror.Error() {
		t.Fatalf("status err should be the failure returned to caller: %v vs %v", ev.Err, eror)
	}
	//#脱敏:错误文本只带表名,不得出现行值
	if strings.Contains(ev.Err.Error(), "mast") {
		t.Fatalf("status err must not leak row values: %q", ev.Err.Error())
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
	if !ev.Accepted || ev.Mode != 1 || ev.Row != 0 || ev.Method != "SelectL" {
		t.Fatalf("unexpected status payload: %+v", ev)
	}
	if ev.Err == nil || !strings.Contains(ev.Err.Error(), "integrity check failed") {
		t.Fatalf("accepted status should still carry the failure reason, got %v", ev.Err)
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
	if !strings.Contains(events[0].Err.Error(), "no mac value") {
		t.Fatalf("unexpected status err: %v", events[0].Err)
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
	if len(events) != 1 || events[0].Accepted || !strings.Contains(events[0].Err.Error(), "invalid") {
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
	if len(events) != 1 || !strings.Contains(events[0].Err.Error(), "not base64") {
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
	if ev.Row != -1 || ev.Accepted || ev.Method != "SelectL" || ev.Table != "tbl_verify_row" {
		t.Fatalf("unexpected status payload: %+v", ev)
	}
	if !errors.Is(ev.Err, ErrIncompleteVerification) {
		t.Fatalf("status err should wrap ErrIncompleteVerification, got %v", ev.Err)
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
	if len(events) != 1 || events[0].Method != "Select" {
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
	if events[0].Table != "hook_verify" || events[0].Method != "SelectL" || events[0].Row != 0 || events[0].Accepted {
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
	if events[0].Engine != eng || events[0].Table != "tbl_verify_row" {
		t.Fatalf("unexpected status payload: %+v", events[0])
	}
}
