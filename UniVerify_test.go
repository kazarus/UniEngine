// UniVerify_test
// #存储完整性校验测试

package UniEngine

import (
	"database/sql"
	"database/sql/driver"
	"strconv"
	"strings"
	"testing"
)

// ----------------------------------------
// #测试类:授权绑定行 + MAC 伴随列
// ----------------------------------------

type TVerifyRow struct {
	WhoBuild int64  `db:"who_build"`
	UserIndx int64  `db:"user_indx"`
	DataStat int64  `db:"data_stat"`
	MastName string `db:"mast_name"`
	MacText  string `db:"mac_text"`
}

const verifyRowCols = "who_build,user_indx,data_stat,mast_name,mac_text"

func verifyRowColumns() []string {
	return []string{"who_build", "user_indx", "data_stat", "mast_name", "mac_text"}
}

func newVerifyTestEngine(d *mockDriver) *TUniEngine {
	db := sql.OpenDB(mockConnector{d: d})
	eng := &TUniEngine{Db: db, ColLabel: "db", ColParam: "$", Provider: DtPOSTGR,
		VerifyOn: 1, VerifyMode: 1, VerifyBy: "test-verify-key"}
	eng.Initialize()
	eng.RegisterClass(TVerifyRow{}, "tbl_verify_row").
		SetKeys("who_build", "user_indx").
		SetVerify("mac_text", "who_build", "user_indx", "data_stat", "mast_name")
	return eng
}

// verifyExpect 按内置算法计算给定行的期望 MAC 伴随值
func verifyExpect(t *testing.T, eng *TUniEngine, row TVerifyRow) string {
	t.Helper()
	payload := "tbl_verify_row" + verifySep +
		strconv.FormatInt(row.WhoBuild, 10) + verifySep +
		strconv.FormatInt(row.UserIndx, 10) + verifySep +
		strconv.FormatInt(row.DataStat, 10) + verifySep +
		row.MastName
	sum, eror := eng.verifySign([]byte(payload))
	if eror != nil {
		t.Fatal(eror)
	}
	return verifyTextOf(sum)
}

// #测试1:SetVerify 登记(置位/顺序/错误路径)
func TestSetVerifyRegister(t *testing.T) {
	eng := newVerifyTestEngine(&mockDriver{})

	tab, ok := eng.HashTabl["UniEngine.TVerifyRow"]
	if !ok {
		t.Fatal("class not registered")
	}
	if !tab.VerifyOn || tab.VerifyAt != "mac_text" {
		t.Fatalf("SetVerify should mark table: %+v", tab)
	}
	if len(tab.ColVerify) != 4 {
		t.Fatalf("SetVerify should keep 4 protected fields, got %d", len(tab.ColVerify))
	}
	//#登记顺序=载荷顺序,不得漂移
	want := []string{"who_build", "user_indx", "data_stat", "mast_name"}
	for i, name := range want {
		if tab.ColVerify[i].FieldName != name {
			t.Fatalf("protected order broken at %d: %s != %s", i, tab.ColVerify[i].FieldName, name)
		}
	}
}

// #测试2:SetVerify 校验错误路径(链式 Err 短路)
func TestSetVerifyValidation(t *testing.T) {
	cases := []struct {
		name string
		call func(tab *TUniTable)
	}{
		{"mac列未登记", func(tab *TUniTable) { tab.SetVerify("no_such", "who_build") }},
		{"mac列是主键", func(tab *TUniTable) { tab.SetVerify("who_build", "user_indx") }},
		{"mac列自身入保护列", func(tab *TUniTable) { tab.SetVerify("mac_text", "mac_text") }},
		{"保护列未登记", func(tab *TUniTable) { tab.SetVerify("mac_text", "no_such") }},
		{"保护列重复", func(tab *TUniTable) { tab.SetVerify("mac_text", "who_build", "who_build") }},
		{"保护列为空", func(tab *TUniTable) { tab.SetVerify("mac_text") }},
	}

	for _, item := range cases {
		eng := &TUniEngine{ColLabel: "db", ColParam: "$", Provider: DtPOSTGR}
		eng.Initialize()
		tab := eng.RegisterClass(TVerifyRow{}, "tbl_case").SetKeys("who_build", "user_indx")

		item.call(tab)

		if tab.LastError == nil {
			t.Fatalf("case[%s] should fail", item.name)
		}
		if tab.VerifyOn {
			t.Fatalf("case[%s] should not mark table", item.name)
		}
	}
}

// #测试3:Insert 自动签名回填 MAC 伴随列
func TestVerifyInsertSigns(t *testing.T) {
	d := &mockDriver{}
	eng := newVerifyTestEngine(d)

	row := TVerifyRow{WhoBuild: 1, UserIndx: 2, DataStat: 1, MastName: "mast"}
	if eror := eng.Insert(&row); eror != nil {
		t.Fatal(eror)
	}

	if len(d.insertValues) != 1 {
		t.Fatalf("expected 1 exec, got %d", len(d.insertValues))
	}
	mac, ok := d.insertValues[0][4].(string) //insert列序=声明序,mac_text第5列
	if !ok || !strings.HasPrefix(mac, UniVerifyTag) {
		t.Fatalf("insert should carry tagged mac, got %v", d.insertValues[0])
	}
	if mac != verifyExpect(t, eng, row) {
		t.Fatalf("mac mismatch: got %s", mac)
	}

	//#同值同 MAC(确定性),异值异 MAC
	row2 := TVerifyRow{WhoBuild: 1, UserIndx: 2, DataStat: 0, MastName: "mast"}
	if eror := eng.Insert(&row2); eror != nil {
		t.Fatal(eror)
	}
	mac2, _ := d.insertValues[1][4].(string)
	if mac2 == mac {
		t.Fatal("different protected values must yield different mac")
	}

	//#结构体伴随字段被回填,调用方可读
	if !strings.HasPrefix(row.MacText, UniVerifyTag) {
		t.Fatalf("struct mac field should be filled, got %q", row.MacText)
	}
}

// #测试4:Update/SaveIt/InsertL 全部写路径签名
func TestVerifyAllWritePaths(t *testing.T) {
	row := TVerifyRow{WhoBuild: 1, UserIndx: 2, DataStat: 0, MastName: "updated"}

	//#Update:参数序=SET列(data_stat,mast_name,mac_text)+主键(who_build,user_indx)
	d := &mockDriver{}
	eng := newVerifyTestEngine(d)
	if eror := eng.Update(&row); eror != nil {
		t.Fatal(eror)
	}
	if mac, _ := d.insertValues[0][2].(string); mac != verifyExpect(t, eng, row) {
		t.Fatalf("update mac mismatch: %v", d.insertValues[0])
	}

	//#SaveIt:PG 原生 UPSERT,参数序=主键+数据列
	d2 := &mockDriver{}
	eng2 := newVerifyTestEngine(d2)
	if eror := eng2.SaveIt(&row); eror != nil {
		t.Fatal(eror)
	}
	if mac, _ := d2.insertValues[0][4].(string); mac != verifyExpect(t, eng2, row) {
		t.Fatalf("saveIt mac mismatch: %v", d2.insertValues[0])
	}

	//#InsertL:批量逐行,两行 MAC 互异且与单行一致
	d3 := &mockDriver{}
	eng3 := newVerifyTestEngine(d3)
	rows := []TVerifyRow{
		{WhoBuild: 1, UserIndx: 2, DataStat: 1, MastName: "a"},
		{WhoBuild: 1, UserIndx: 3, DataStat: 1, MastName: "b"},
	}
	if eror := eng3.InsertL(&rows); eror != nil {
		t.Fatal(eror)
	}
	if len(d3.insertValues) != 1 {
		t.Fatalf("InsertL is a single multi-row exec, got %d", len(d3.insertValues))
	}
	//#单条多行语句:参数平铺[行0五列+行1五列],mac 分别在第5/第10位
	macA, _ := d3.insertValues[0][4].(string)
	macB, _ := d3.insertValues[0][9].(string)
	if macA == macB {
		t.Fatal("batch rows must yield different macs")
	}
	if macA != verifyExpect(t, eng3, rows[0]) || macB != verifyExpect(t, eng3, rows[1]) {
		t.Fatal("batch macs must match per-row expectation")
	}
}

// #测试4b:CopyInL 批量装载同样签名(PG COPY 路径,逐行 Exec+收尾空 Exec)
func TestVerifyCopyInLSigns(t *testing.T) {
	d := &mockDriver{}
	eng := newVerifyTestEngine(d)
	rows := []TVerifyRow{
		{WhoBuild: 1, UserIndx: 2, DataStat: 1, MastName: "a"},
		{WhoBuild: 1, UserIndx: 3, DataStat: 1, MastName: "b"},
	}

	if eror := eng.CopyInL(&rows); eror != nil {
		t.Fatal(eror)
	}

	//#逐行 Exec:第 m 次 Exec 的第5个值是第 m 行的 mac;最后是收尾空 Exec
	if len(d.insertValues) != 3 {
		t.Fatalf("CopyInL should exec per row plus final, got %d", len(d.insertValues))
	}
	macA, _ := d.insertValues[0][4].(string)
	macB, _ := d.insertValues[1][4].(string)
	if macA != verifyExpect(t, eng, rows[0]) || macB != verifyExpect(t, eng, rows[1]) {
		t.Fatalf("copyin macs mismatch: %q %q", macA, macB)
	}
	if len(d.insertValues[2]) != 0 {
		t.Fatalf("final exec should be empty, got %v", d.insertValues[2])
	}
}

// #测试5:SelectL 逐行验签(正确 MAC 通过)
func TestVerifySelectVerifyPass(t *testing.T) {
	eng := newVerifyTestEngine(&mockDriver{})
	row := TVerifyRow{WhoBuild: 1, UserIndx: 2, DataStat: 1, MastName: "mast"}

	d := &mockDriver{
		columns:   verifyRowColumns(),
		queryRows: [][]driver.Value{{int64(1), int64(2), int64(1), "mast", verifyExpect(t, eng, row)}},
	}
	eng.Db = sql.OpenDB(mockConnector{d: d})

	var rows []TVerifyRow
	if eror := eng.SelectL(&rows, "select "+verifyRowCols+" from tbl_verify_row"); eror != nil {
		t.Fatal(eror)
	}
	if len(rows) != 1 || rows[0].MastName != "mast" {
		t.Fatalf("selectL should return the row: %+v", rows)
	}
}

// #测试6:篡改行验签失败(fail-closed)
func TestVerifySelectTamperFails(t *testing.T) {
	eng := newVerifyTestEngine(&mockDriver{})
	row := TVerifyRow{WhoBuild: 1, UserIndx: 2, DataStat: 1, MastName: "mast"}

	//#库中被直改 data_stat 1→0,伴随 MAC 还是旧值
	d := &mockDriver{
		columns:   verifyRowColumns(),
		queryRows: [][]driver.Value{{int64(1), int64(2), int64(0), "mast", verifyExpect(t, eng, row)}},
	}
	eng.Db = sql.OpenDB(mockConnector{d: d})

	var rows []TVerifyRow
	eror := eng.SelectL(&rows, "select "+verifyRowCols+" from tbl_verify_row")
	if eror == nil {
		t.Fatal("tampered row must fail verification")
	}
	if !strings.Contains(eror.Error(), "integrity check failed") {
		t.Fatalf("error should be integrity failure, got: %v", eror)
	}
}

// #测试7:存量无标签行——宽容模式直通/严格模式报错
func TestVerifyLegacyRowMode(t *testing.T) {
	base := []driver.Value{int64(1), int64(2), int64(1), "mast", ""}

	//#宽容(缺省):回填期存量行直通
	eng := newVerifyTestEngine(&mockDriver{columns: verifyRowColumns(), queryRows: [][]driver.Value{base}})
	var rows []TVerifyRow
	if eror := eng.SelectL(&rows, "select "+verifyRowCols+" from tbl_verify_row"); eror != nil {
		t.Fatalf("lenient mode must pass legacy rows: %v", eror)
	}

	//#严格:回填完成后切换,无标签行按篡改处理
	eng2 := newVerifyTestEngine(&mockDriver{columns: verifyRowColumns(), queryRows: [][]driver.Value{base}})
	eng2.VerifyMode = 2
	eror := eng2.SelectL(&rows, "select "+verifyRowCols+" from tbl_verify_row")
	if eror == nil || !strings.Contains(eror.Error(), "no mac value") {
		t.Fatalf("strict mode must reject legacy rows, got: %v", eror)
	}
}

// #测试8:非空无标签值属非法,宽容模式也报错
func TestVerifyBadPrefixFails(t *testing.T) {
	d := &mockDriver{
		columns:   verifyRowColumns(),
		queryRows: [][]driver.Value{{int64(1), int64(2), int64(1), "mast", "not-a-mac"}},
	}
	eng := newVerifyTestEngine(d)

	var rows []TVerifyRow
	eror := eng.SelectL(&rows, "select "+verifyRowCols+" from tbl_verify_row")
	if eror == nil || !strings.Contains(eror.Error(), "invalid") {
		t.Fatalf("bad prefix must fail in lenient mode, got: %v", eror)
	}
}

// #测试9:partial select 不覆盖保护列时跳过验签
func TestVerifyPartialSelectSkips(t *testing.T) {
	//#查询列缺 mac_text 与部分保护列:无法还原载荷,跳过验签不报错
	d := &mockDriver{
		columns:   []string{"who_build", "user_indx"},
		queryRows: [][]driver.Value{{int64(1), int64(2)}},
	}
	eng := newVerifyTestEngine(d)

	var rows []TVerifyRow
	if eror := eng.SelectL(&rows, "select who_build,user_indx from tbl_verify_row"); eror != nil {
		t.Fatalf("partial select must skip verification: %v", eror)
	}
}

// #测试10:VerifyOn=0 引擎级关闭,读写零影响(存量行为回归)
func TestVerifyDisabledNoImpact(t *testing.T) {
	eng := newVerifyTestEngine(&mockDriver{})
	eng.VerifyOn = 0

	d := &mockDriver{}
	eng.Db = sql.OpenDB(mockConnector{d: d})

	row := TVerifyRow{WhoBuild: 1, UserIndx: 2, DataStat: 1, MastName: "mast"}
	if eror := eng.Insert(&row); eror != nil {
		t.Fatal(eror)
	}
	if mac, _ := d.insertValues[0][4].(string); mac != "" {
		t.Fatalf("disabled engine must not sign, got %q", mac)
	}

	//#读侧:垃圾 MAC 值也不校验
	d2 := &mockDriver{
		columns:   verifyRowColumns(),
		queryRows: [][]driver.Value{{int64(1), int64(2), int64(1), "mast", "MAC:garbage"}},
	}
	eng.Db = sql.OpenDB(mockConnector{d: d2})
	var rows []TVerifyRow
	if eror := eng.SelectL(&rows, "select "+verifyRowCols+" from tbl_verify_row"); eror != nil {
		t.Fatalf("disabled engine must not verify: %v", eror)
	}
}

// #测试11:VerifyHook 应用钩子接管算法(VerifyBy 为空也可用),载荷含表名域分隔
func TestVerifyCustomHook(t *testing.T) {
	var payloads [][]byte
	eng := newVerifyTestEngine(&mockDriver{})
	eng.VerifyBy = ""
	eng.VerifyHook = func(Payload []byte) ([]byte, error) {
		payloads = append(payloads, append([]byte{}, Payload...))
		return []byte("hooked-mac"), nil
	}

	row := TVerifyRow{WhoBuild: 1, UserIndx: 2, DataStat: 1, MastName: "mast"}
	d := &mockDriver{}
	eng.Db = sql.OpenDB(mockConnector{d: d})

	if eror := eng.Insert(&row); eror != nil {
		t.Fatal(eror)
	}
	if mac, _ := d.insertValues[0][4].(string); mac != UniVerifyTag+"aG9va2VkLW1hYw==" {
		t.Fatalf("hook mac must be used, got %q", mac)
	}
	if len(payloads) != 1 {
		t.Fatalf("hook should be called once, got %d", len(payloads))
	}
	//#载荷结构:表名 + 分隔符 + 登记序保护列值
	want := "tbl_verify_row" + verifySep + "1" + verifySep + "2" + verifySep + "1" + verifySep + "mast"
	if string(payloads[0]) != want {
		t.Fatalf("payload mismatch:\n got %q\nwant %q", payloads[0], want)
	}
}

// #测试12:同值跨表 MAC 不可移植(域分隔)
func TestVerifyPayloadDomainSeparated(t *testing.T) {
	type TOtherVerifyRow struct {
		WhoBuild int64  `db:"who_build"`
		UserIndx int64  `db:"user_indx"`
		DataStat int64  `db:"data_stat"`
		MastName string `db:"mast_name"`
		MacText  string `db:"mac_text"`
	}

	eng := newVerifyTestEngine(&mockDriver{})
	eng.RegisterClass(TOtherVerifyRow{}, "tbl_other_row").
		SetKeys("who_build", "user_indx").
		SetVerify("mac_text", "who_build", "user_indx", "data_stat", "mast_name")

	rowA := TVerifyRow{WhoBuild: 1, UserIndx: 2, DataStat: 1, MastName: "mast"}
	rowB := TOtherVerifyRow{WhoBuild: 1, UserIndx: 2, DataStat: 1, MastName: "mast"}

	if eror := eng.Insert(&rowA); eror != nil {
		t.Fatal(eror)
	}
	if eror := eng.Insert(&rowB); eror != nil {
		t.Fatal(eror)
	}

	if rowA.MacText == rowB.MacText {
		t.Fatal("same values in different tables must yield different macs")
	}
}

// #测试13:VerifyBy 为空且无钩子时 fast-fail
func TestVerifyMissingKeyFails(t *testing.T) {
	eng := newVerifyTestEngine(&mockDriver{})
	eng.VerifyBy = ""

	row := TVerifyRow{WhoBuild: 1, UserIndx: 2, DataStat: 1, MastName: "mast"}
	if eror := eng.Insert(&row); eror == nil {
		t.Fatal("missing key must fail fast")
	}
}

// #测试14:secreton 列与 HMAC 共存——载荷取明文,读侧先解密后验签
func TestVerifyWithSecretColumn(t *testing.T) {
	type TSecretVerifyRow struct {
		WhoBuild int64  `db:"who_build"`
		UserIndx int64  `db:"user_indx"`
		Password string `db:"password,secreton"`
		MacText  string `db:"mac_text"`
	}

	eng := newVerifyTestEngine(&mockDriver{})
	eng.SecretOn = 1
	eng.SecretBy = "test-secret-key"
	eng.RegisterClass(TSecretVerifyRow{}, "tbl_secret_verify").
		SetKeys("who_build", "user_indx").
		SetSecret("password").
		SetVerify("mac_text", "who_build", "user_indx", "password")

	//#写侧:载荷里的 password 是明文(hook 捕获断言)
	var payload string
	eng.VerifyHook = func(P []byte) ([]byte, error) {
		payload = string(P)
		return []byte("x"), nil
	}

	row := TSecretVerifyRow{WhoBuild: 1, UserIndx: 2, Password: "p@ss"}
	d := &mockDriver{}
	eng.Db = sql.OpenDB(mockConnector{d: d})

	if eror := eng.Insert(&row); eror != nil {
		t.Fatal(eror)
	}
	if !strings.HasSuffix(payload, "p@ss") {
		t.Fatalf("payload must carry plaintext password, got %q", payload)
	}
	//#落库的是密文(GCM 随机 nonce,只断言前缀与不同)
	if cipher, _ := d.insertValues[0][2].(string); !eng.IsEncrypted(cipher) {
		t.Fatalf("password column must be stored encrypted, got %q", cipher)
	}
}

// #测试15:行级绑定强制——保护列须含已登记主键,否则"整行搬运"无法检出(评估 §4.8)
func TestSetVerifyRequiresPkeyForRowBinding(t *testing.T) {
	//#① 只保护非键列:登记期即拒(fail-fast,而不是留到运行时才发现绑定失效)
	eng := &TUniEngine{ColLabel: "db", ColParam: "$", Provider: DtPOSTGR}
	eng.Initialize()
	tab := eng.RegisterClass(TVerifyRow{}, "tbl_bind_1").SetKeys("who_build", "user_indx")
	tab.SetVerify("mac_text", "data_stat", "mast_name")
	if tab.LastError == nil {
		t.Fatal("保护列不含主键时应拒绝登记")
	}
	if tab.VerifyOn {
		t.Fatal("被拒时不应置 VerifyOn")
	}

	//#② 保护列含主键:通过(载荷含行标识,搬移即可检出)
	tab2 := eng.RegisterClass(TVerifyRow{}, "tbl_bind_2").SetKeys("who_build", "user_indx")
	tab2.SetVerify("mac_text", "who_build", "data_stat")
	if tab2.LastError != nil {
		t.Fatalf("保护列含主键应通过, got %v", tab2.LastError)
	}
	if !tab2.VerifyOn {
		t.Fatal("登记成功应置 VerifyOn")
	}

	//#③ 逃生口:显式放弃行级绑定(确有理由时才用),登记前置于表上
	tab3 := eng.RegisterClass(TVerifyRow{}, "tbl_bind_3").SetKeys("who_build", "user_indx")
	tab3.VerifyUnkeyed = true
	tab3.SetVerify("mac_text", "data_stat", "mast_name")
	if tab3.LastError != nil {
		t.Fatalf("VerifyUnkeyed 应放行, got %v", tab3.LastError)
	}
}
