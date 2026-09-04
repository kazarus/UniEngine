package UniEngine

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// mockRow is a sample struct used to exercise RegisterClass / SetKeys.
type mockRow struct {
	ID   int    `db:"id"`
	Name string `db:"name"`
	Code string `db:"-"` //readonly marker
}

func newTestEngine() TUniEngine {
	var e TUniEngine
	e.Initialize()
	e.ColLabel = "db"
	e.ColParam = "$"
	e.Provider = DtPOSTGR
	return e
}

// ---------------------------------------------------------------------------
// 常量与重命名
// ---------------------------------------------------------------------------

func TestConstValues(t *testing.T) {
	if EtSelect != 1 || EtInsert != 2 || EtUpdate != 3 || EtDelete != 4 {
		t.Fatalf("TQueryType const values changed: %d %d %d %d", EtSelect, EtInsert, EtUpdate, EtDelete)
	}
	if DtPOSTGR != 1 || DtSQLSRV != 2 || DtORACLE != 3 || DtMYSQLN != 6 {
		t.Fatalf("TDriveType const values changed")
	}
}

// Compile-time check that DataLeng was renamed to DataLen.
func TestDataLenFieldRenamed(t *testing.T) {
	f := TUniField{DataLen: "10"}
	if f.DataLen != "10" {
		t.Fatal("DataLen field not settable")
	}
}

// ---------------------------------------------------------------------------
// UniField.initialize
// ---------------------------------------------------------------------------

func TestUniFieldInitialize(t *testing.T) {
	cases := []struct {
		tag      string
		field    string
		readonly bool
	}{
		{"id", "id", false},
		{"-", "-", true},
		{"_", "_", true},
		{"id,readonly", "id", true},
	}
	for _, c := range cases {
		var f TUniField
		f.initialize(c.tag)
		if f.FieldName != c.field {
			t.Errorf("tag %q: FieldName=%q want %q", c.tag, f.FieldName, c.field)
		}
		if f.ReadOnly != c.readonly {
			t.Errorf("tag %q: ReadOnly=%v want %v", c.tag, f.ReadOnly, c.readonly)
		}
	}
}

// ---------------------------------------------------------------------------
// getSqlQuery / getValParam / getColParam
// ---------------------------------------------------------------------------

func TestGetSqlQueryOracle(t *testing.T) {
	e := newTestEngine()
	e.Provider = DtORACLE
	e.ColParam = ":"
	in := "select $1 from t where x=$2"
	out := e.getSqlQuery(in, 1, 2)
	if out != "select :1 from t where x=:2" {
		t.Fatalf("oracle replace: %q", out)
	}
}

func TestGetSqlQueryMySQL(t *testing.T) {
	e := newTestEngine()
	e.Provider = DtMYSQLN
	e.ColParam = "?"
	in := "select $1 from t where x=$2"
	out := e.getSqlQuery(in, 1, 2)
	if out != "select ? from t where x=?" {
		t.Fatalf("mysql replace: %q", out)
	}
}

func TestGetSqlQueryNoArgsUnchanged(t *testing.T) {
	e := newTestEngine()
	e.Provider = DtMYSQLN
	in := "select $1 from t"
	out := e.getSqlQuery(in)
	if out != in {
		t.Fatalf("no-args should be unchanged: %q", out)
	}
}

func TestGetValParam(t *testing.T) {
	e := newTestEngine()
	e.Provider = DtMYSQLN
	e.ColParam = "?"
	if got := e.getValParam(1); got != "?" {
		t.Fatalf("mysql getValParam=%q want ?", got)
	}
	e.Provider = DtPOSTGR
	e.ColParam = "$"
	if got := e.getValParam(2); got != "$2" {
		t.Fatalf("postgres getValParam=%q want $2", got)
	}
}

func TestGetColParam(t *testing.T) {
	e := newTestEngine()
	e.Provider = DtMYSQLN
	if got := e.getColParam("id"); got != "id" {
		t.Fatalf("mysql getColParam=%q want id", got)
	}
	e.Provider = DtORACLE
	if got := e.getColParam("id"); got != "id" {
		t.Fatalf("oracle getColParam=%q want id", got)
	}
	e.Provider = DtPOSTGR
	if got := e.getColParam("id"); got != `"id"` {
		t.Fatalf("postgres getColParam=%q want \"id\"", got)
	}
}

// ---------------------------------------------------------------------------
// ProviderName / page sizes
// ---------------------------------------------------------------------------

func TestProviderName(t *testing.T) {
	e := newTestEngine()
	cases := map[TDriveType]string{
		DtORACLE: "oracle",
		DtSQLSRV: "sqlserver",
		DtPOSTGR: "postgresql",
		DtMYSQLN: "mysql",
	}
	for p, want := range cases {
		e.Provider = p
		if got := e.ProviderName(); got != want {
			t.Errorf("provider %v: name=%q want %q", p, got, want)
		}
	}
}

func TestPageSizes(t *testing.T) {
	e := newTestEngine()
	e.Provider = DtORACLE
	if e.DefaultPageSize() != 99 || e.SpecialPageSize(0) != 99 {
		t.Fatal("oracle default page size should be 99")
	}
	if e.SpecialPageSize(50) != 50 {
		t.Fatal("SpecialPageSize should respect the requested size")
	}
	e.Provider = DtSQLSRV
	if e.DefaultPageSize() != 10 || e.SpecialPageSize(50) != 50 {
		t.Fatal("sqlserver page size mismatch")
	}
	e.Provider = DtPOSTGR
	if e.DefaultPageSize() != 99 || e.SpecialPageSize(50) != 50 {
		t.Fatal("postgres page size mismatch")
	}
	e.Provider = DtMYSQLN
	if e.DefaultPageSize() != 10 || e.SpecialPageSize(0) != 10 {
		t.Fatal("mysql page size should default to 10")
	}
}

// ---------------------------------------------------------------------------
// RegisterClass + SetKeys: 指针修复的核心验证
// ---------------------------------------------------------------------------

// RegisterClass 必须把 *TUniTable 存入 map,SetKeys 通过返回指针修改后,
// map 中的条目也必须同步更新(旧代码存值拷贝、返回 &局部,SetKeys 的修改会丢失)。
func TestRegisterClassSetKeysPersists(t *testing.T) {
	e := newTestEngine()
	tbl := e.RegisterClass(mockRow{}, "mock_row")
	if tbl == nil {
		t.Fatal("RegisterClass returned nil")
	}
	if err := tbl.SetKeys("id"); err != nil {
		t.Fatalf("SetKeys error: %v", err)
	}
	if _, ok := tbl.HashPkeys["id"]; !ok {
		t.Fatal("returned *TUniTable missing pkey 'id'")
	}
	key := reflect.TypeOf(mockRow{}).String()
	entry, ok := e.HashTabl[key]
	if !ok || entry == nil {
		t.Fatalf("map has no entry for %s", key)
	}
	if _, ok := entry.HashPkeys["id"]; !ok {
		t.Fatal("map entry missing pkey 'id' (pointer-to-local bug not fixed)")
	}
}

// SetKeys 对未注册字段应返回 error(旧代码会 panic)。
func TestSetKeysUnregisteredFieldReturnsError(t *testing.T) {
	e := newTestEngine()
	tbl := e.RegisterClass(mockRow{}, "mock_row")
	err := tbl.SetKeys("no_such_field")
	if err == nil {
		t.Fatal("expected error for unregistered field, got nil")
	}
}

// AutoKeys 实现的 MySQL 变体在未指定 DataBase 时应返回 error(旧代码会 panic)。
func TestAutoKeysMYSQLNMissingDatabase(t *testing.T) {
	e := newTestEngine()
	ak := TAutoKeys4MYSQLN{}
	_, err := ak.GetSqlAutoKeys(&e, "mock_row")
	if err == nil {
		t.Fatal("expected error when DataBase is empty")
	}
}

// ---------------------------------------------------------------------------
// PrepareRunSQL: SQL 生成与 bug 修复验证
// ---------------------------------------------------------------------------

// EtSelect 在无主键时不应 panic(旧代码对空串 SqlField 做 [1:] 会 panic)。
func TestPrepareRunSQLSelectNoPkeysNoPanic(t *testing.T) {
	e := newTestEngine()
	e.RegisterTable("t_nopkey", 0)
	e.RegisterField("t_nopkey", "a")
	e.PrepareTables("t_nopkey")
	sql, _, _, err := e.PrepareRunSQL("t_nopkey", EtSelect)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sql != "" {
		t.Fatalf("expected empty sql for no-pkey select, got %q", sql)
	}
}

func TestPrepareRunSQLSelectWithPkey(t *testing.T) {
	e := newTestEngine()
	e.RegisterTable("t_sel", 0)
	e.RegisterField("t_sel", "id")
	e.RegisterPkeys("t_sel", "id")
	e.PrepareTables("t_sel")
	sql, _, _, err := e.PrepareRunSQL("t_sel", EtSelect)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(sql, "where 1=1") {
		t.Fatalf("select sql missing where prefix: %q", sql)
	}
	if !strings.Contains(sql, `"id"=$1`) {
		t.Fatalf("select sql missing pkey clause: %q", sql)
	}
}

func TestPrepareRunSQLInsert(t *testing.T) {
	e := newTestEngine()
	e.RegisterTable("t_ins", 0)
	e.RegisterField("t_ins", "id")
	e.RegisterField("t_ins", "name")
	e.PrepareTables("t_ins")
	sql, fields, _, err := e.PrepareRunSQL("t_ins", EtInsert)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(sql, "insert into t_ins") {
		t.Fatalf("insert sql missing insert into: %q", sql)
	}
	if !strings.Contains(sql, `"id"`) || !strings.Contains(sql, `"name"`) {
		t.Fatalf("insert sql missing field names: %q", sql)
	}
	if !strings.Contains(sql, "$1") || !strings.Contains(sql, "$2") {
		t.Fatalf("insert sql missing params: %q", sql)
	}
	if len(fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(fields))
	}
}

// EtUpdate: 非主键字段进 set,主键字段进 where。
// 这里 1 主键 + 1 非主键,顺序确定:name=$1, id=$2。
func TestPrepareRunSQLUpdate(t *testing.T) {
	e := newTestEngine()
	e.RegisterTable("t_upd", 0)
	e.RegisterField("t_upd", "id")
	e.RegisterField("t_upd", "name")
	e.RegisterPkeys("t_upd", "id")
	e.PrepareTables("t_upd")
	sql, _, _, err := e.PrepareRunSQL("t_upd", EtUpdate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(sql, "update t_upd set") {
		t.Fatalf("update sql missing update set: %q", sql)
	}
	if !strings.Contains(sql, `"name"=$1`) {
		t.Fatalf("update sql missing set clause: %q", sql)
	}
	if !strings.Contains(sql, `"id"=$2`) {
		t.Fatalf("update sql missing where pkey clause: %q", sql)
	}
	if !strings.Contains(sql, "where 1=1") {
		t.Fatalf("update sql missing where 1=1: %q", sql)
	}
}

// ---------------------------------------------------------------------------
// ExistConst: 未设置 Provider 时应返回明确的 no-sql 错误(而非执行空串)。
// ---------------------------------------------------------------------------

func TestExistConstUnsupportedProvider(t *testing.T) {
	e := TUniEngine{} // Provider 未设置(0),无对应 SQL
	ok, err := e.ExistConst(CtPK, "any")
	if ok {
		t.Fatal("expected false for unsupported provider")
	}
	if err == nil || !strings.Contains(err.Error(), "no sql for existconst") {
		t.Fatalf("expected no-sql error, got: %v", err)
	}
}

func TestExistConstRejectsInjection(t *testing.T) {
	e := newTestEngine()
	if _, err := e.ExistConst(CtPK, "x;drop table t"); err == nil {
		t.Error("ExistConst with malicious name should return error")
	}
}

func TestExistConstSQLGenerators(t *testing.T) {
	eng := TUniEngine{}
	allTypes := []TConstType{CtPK, CtFK, CtUK, CtDF}

	pg := TExistConst4POSTGR{}
	for _, c := range allTypes {
		if sql := pg.GetSqlExistConst(&eng, c, "nm"); sql == "" {
			t.Errorf("pg GetSqlExistConst(%v) should not be empty", c)
		}
	}
	if sql := pg.GetSqlExistConst(&eng, CtPK, "pk_user"); !strings.Contains(sql, "contype='p'") || !strings.Contains(sql, "pk_user") {
		t.Errorf("pg pk sql wrong: %s", sql)
	}

	ss := TExistConst4SQLSRV{}
	for _, c := range allTypes {
		if sql := ss.GetSqlExistConst(&eng, c, "nm"); sql == "" {
			t.Errorf("sqlsrv GetSqlExistConst(%v) should not be empty", c)
		}
	}
	if sql := ss.GetSqlExistConst(&eng, CtUK, "uq_email"); !strings.Contains(sql, "type='UQ'") {
		t.Errorf("sqlsrv uk sql wrong: %s", sql)
	}

	ora := TExistConst4ORACLE{}
	for _, c := range allTypes {
		if sql := ora.GetSqlExistConst(&eng, c, "nm"); sql == "" {
			t.Errorf("oracle GetSqlExistConst(%v) should not be empty", c)
		}
	}
	if sql := ora.GetSqlExistConst(&eng, CtFK, "fk_order"); !strings.Contains(sql, "constraint_type='R'") {
		t.Errorf("oracle fk sql wrong: %s", sql)
	}

	my := TExistConst4MYSQLN{}
	for _, c := range allTypes {
		if sql := my.GetSqlExistConst(&eng, c, "nm", "testdb"); sql == "" {
			t.Errorf("mysql GetSqlExistConst(%v) should not be empty", c)
		}
	}
	if sql := my.GetSqlExistConst(&eng, CtPK, "PRIMARY", "testdb"); !strings.Contains(sql, "PRIMARY KEY") || !strings.Contains(sql, "testdb") {
		t.Errorf("mysql pk sql wrong: %s", sql)
	}
}

func TestExistConstFoundViaMock(t *testing.T) {
	d := &mockDriver{
		columns:   []string{"count"},
		queryRows: [][]driver.Value{{int64(1)}},
	}
	eng := &TUniEngine{Db: sql.OpenDB(mockConnector{d: d}), ColLabel: "db", ColParam: "$", Provider: DtPOSTGR}
	eng.Initialize()
	ok, eror := eng.ExistConst(CtPK, "pk_user")
	if eror != nil {
		t.Fatal(eror)
	}
	if !ok {
		t.Fatal("expected true when count > 0")
	}
}

func TestExistConstNotFoundViaMock(t *testing.T) {
	d := &mockDriver{
		columns:   []string{"count"},
		queryRows: [][]driver.Value{{int64(0)}},
	}
	eng := &TUniEngine{Db: sql.OpenDB(mockConnector{d: d}), ColLabel: "db", ColParam: "$", Provider: DtPOSTGR}
	eng.Initialize()
	ok, eror := eng.ExistConst(CtFK, "fk_none")
	if eror != nil {
		t.Fatal(eror)
	}
	if ok {
		t.Fatal("expected false when count = 0")
	}
}

// ---------------------------------------------------------------------------
// 第一阶段加固:rows.Err 传播 / 确定性 SQL / 原生 UPSERT / CopyIn 大小写 / 空列防护
// ---------------------------------------------------------------------------

// newMockEngine 构造接 mock driver 的 PG 引擎(SQL 文本记录在 d.queries)。
func newMockEngine(d *mockDriver) *TUniEngine {
	eng := &TUniEngine{Db: sql.OpenDB(mockConnector{d: d}), ColLabel: "db", ColParam: "$", Provider: DtPOSTGR}
	eng.Initialize()
	return eng
}

// rows.Next() 因 IO 错误提前退出时,SelectL 必须把 rows.Err() 上抛,
// 而不是静默返回 nil 造成"看似正常实则截断"。
func TestRowsErrPropagated(t *testing.T) {
	d := &mockDriver{
		columns:   []string{"user_name", "password"},
		queryRows: [][]driver.Value{{"kazarus", "x"}},
		nextErr:   errors.New("mock io error"),
	}
	eng := newMockEngine(d)
	eng.RegisterClass(TTestUser{}, "test_user")

	var users []TTestUser
	eror := eng.SelectL(&users, "select * from test_user")
	if eror == nil {
		t.Fatal("rows.Next error should propagate, got nil")
	}
	if !strings.Contains(eror.Error(), "mock io error") {
		t.Fatalf("unexpected error: %v", eror)
	}
	if len(users) != 0 {
		t.Fatalf("no row should be delivered on error, got %d", len(users))
	}
}

// 同一输入多次 Insert,生成的 SQL 文本必须逐字相同(类声明序,不受 map 迭代序影响)。
func TestInsertDeterministicSQL(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.RegisterClass(TTestUser{}, "test_user")

	row := TTestUser{UserName: "kazarus", Password: "plain"}
	if eror := eng.Insert(&row, "test_user"); eror != nil {
		t.Fatal(eror)
	}
	if eror := eng.Insert(&row, "test_user"); eror != nil {
		t.Fatal(eror)
	}

	want := `insert into test_user ( "user_name","password" ) values ( $1,$2 ) `
	if len(d.queries) != 2 || d.queries[0] != want || d.queries[1] != want {
		t.Fatalf("insert sql should be deterministic:\n got  %q\n want %q", d.queries, want)
	}
}

// RegisterField 直写的手工注册路径同样保证确定序(按字段名排序补齐)。
func TestManualFieldOrderDeterministic(t *testing.T) {
	eng := newTestEngine()
	eng.RegisterTable("t_manual", 0)
	eng.RegisterField("t_manual", "zz")
	eng.RegisterField("t_manual", "aa")

	sql, _, _, eror := eng.PrepareRunSQL("t_manual", EtInsert)
	if eror != nil {
		t.Fatal(eror)
	}
	if !strings.Contains(sql, `( "aa","zz" )`) || !strings.Contains(sql, "values ( $1,$2 )") {
		t.Fatalf("manual fields should be sorted deterministically: %q", sql)
	}
}

// PG:SaveIt 默认走 on conflict do update,单条语句,不再先 count。
func TestSaveItNativeUpsertPG(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.RegisterClass(TTestUser{}, "test_user").SetKeys("user_name")

	row := TTestUser{UserName: "kazarus", Password: "p@ssw0rd"}
	if eror := eng.SaveIt(&row, "test_user"); eror != nil {
		t.Fatal(eror)
	}

	if len(d.queries) != 1 {
		t.Fatalf("native upsert should be a single statement, got %d: %v", len(d.queries), d.queries)
	}
	if !strings.Contains(d.queries[0], `on conflict ( "user_name" ) do update set "password"=excluded."password"`) {
		t.Fatalf("pg upsert sql wrong: %s", d.queries[0])
	}
}

// PG:SaveItWhenNotExist 默认走 on conflict do nothing。
func TestSaveItWhenNotExistNativeSkipPG(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.RegisterClass(TTestUser{}, "test_user").SetKeys("user_name")

	row := TTestUser{UserName: "kazarus", Password: "p@ssw0rd"}
	if eror := eng.SaveItWhenNotExist(&row, "test_user"); eror != nil {
		t.Fatal(eror)
	}

	if len(d.queries) != 1 {
		t.Fatalf("native skip-insert should be a single statement, got %d: %v", len(d.queries), d.queries)
	}
	if !strings.Contains(d.queries[0], `on conflict ( "user_name" ) do nothing`) {
		t.Fatalf("pg skip-insert sql wrong: %s", d.queries[0])
	}
}

// Oracle:MERGE 语法,参数只传一遍。
func TestSaveItUpsertOracle(t *testing.T) {
	d := &mockDriver{}
	eng := &TUniEngine{Db: sql.OpenDB(mockConnector{d: d}), ColLabel: "db", ColParam: ":", Provider: DtORACLE}
	eng.Initialize()
	eng.RegisterClass(TTestUser{}, "test_user").SetKeys("user_name")

	row := TTestUser{UserName: "kazarus", Password: "p"}
	if eror := eng.SaveIt(&row, "test_user"); eror != nil {
		t.Fatal(eror)
	}

	if len(d.queries) != 1 {
		t.Fatalf("oracle merge should be a single statement, got %d: %v", len(d.queries), d.queries)
	}
	q := d.queries[0]
	if !strings.Contains(q, "merge into test_user t using (select :1 user_name,:2 password from dual) src") ||
		!strings.Contains(q, "on (t.user_name=src.user_name)") ||
		!strings.Contains(q, "when matched then update set t.password=src.password") ||
		!strings.Contains(q, "when not matched then insert ( user_name,password ) values ( src.user_name,src.password )") {
		t.Fatalf("oracle merge sql wrong: %s", q)
	}
}

// SQLServer:MERGE + HOLDLOCK。
func TestSaveItUpsertSQLSRV(t *testing.T) {
	d := &mockDriver{}
	eng := &TUniEngine{Db: sql.OpenDB(mockConnector{d: d}), ColLabel: "db", ColParam: "$", Provider: DtSQLSRV}
	eng.Initialize()
	eng.RegisterClass(TTestUser{}, "test_user").SetKeys("user_name")

	row := TTestUser{UserName: "kazarus", Password: "p"}
	if eror := eng.SaveIt(&row, "test_user"); eror != nil {
		t.Fatal(eror)
	}

	if len(d.queries) != 1 {
		t.Fatalf("sqlserver merge should be a single statement, got %d: %v", len(d.queries), d.queries)
	}
	q := d.queries[0]
	if !strings.Contains(q, "merge into test_user with (holdlock) as t") ||
		!strings.Contains(q, `using (values ( $1,$2 )) as src ( "user_name","password" )`) ||
		!strings.Contains(q, "when matched then update set t.\"password\"=src.\"password\"") {
		t.Fatalf("sqlserver merge sql wrong: %s", q)
	}
}

// MySQL:ON DUPLICATE KEY 由任一唯一键触发,与主键语义不等价,应回退 count-then-dispatch。
func TestSaveItMySQLFallsBackToCount(t *testing.T) {
	d := &mockDriver{
		columns:   []string{"value"},
		queryRows: [][]driver.Value{{int64(1)}},
	}
	eng := &TUniEngine{Db: sql.OpenDB(mockConnector{d: d}), ColLabel: "db", ColParam: "?", Provider: DtMYSQLN}
	eng.Initialize()
	eng.RegisterClass(TTestUser{}, "test_user").SetKeys("user_name")

	row := TTestUser{UserName: "kazarus", Password: "p"}
	if eror := eng.SaveIt(&row, "test_user"); eror != nil {
		t.Fatal(eror)
	}

	if len(d.queries) != 2 {
		t.Fatalf("mysql should keep count-then-dispatch, got %d: %v", len(d.queries), d.queries)
	}
	if !strings.Contains(d.queries[0], "select count(1) from test_user where user_name=?") {
		t.Fatalf("count sql wrong: %s", d.queries[0])
	}
	if !strings.Contains(d.queries[1], "update test_user set password=? where 1=1 and user_name=?") {
		t.Fatalf("update sql wrong: %s", d.queries[1])
	}
}

// hookUpdateUser 实现了 GetSqlUpdate 钩子:SaveIt 必须回退 count-then-dispatch。
type hookUpdateUser struct {
	UserName string `db:"user_name"`
	Password string `db:"password"`
}

func (u hookUpdateUser) GetSqlUpdate(UniEngineEx *TUniEngine, TableName string) string {
	return "update hook_user set password='x' where user_name='kazarus'"
}

func TestSaveItHookFallsBackToCount(t *testing.T) {
	d := &mockDriver{
		columns:   []string{"value"},
		queryRows: [][]driver.Value{{int64(0)}},
	}
	eng := newMockEngine(d)
	eng.RegisterClass(hookUpdateUser{}, "hook_user").SetKeys("user_name")

	row := hookUpdateUser{UserName: "kazarus"}
	if eror := eng.SaveIt(&row, "hook_user"); eror != nil {
		t.Fatal(eror)
	}

	// count==0 → 走 Insert(该类无 Insert 钩子,故为普通 insert)
	if len(d.queries) != 2 {
		t.Fatalf("hook class should keep count-then-dispatch, got %d: %v", len(d.queries), d.queries)
	}
	if !strings.Contains(d.queries[0], "select count(1)") {
		t.Fatalf("count sql missing: %s", d.queries[0])
	}
	if !strings.Contains(d.queries[1], "insert into hook_user") {
		t.Fatalf("insert sql missing: %s", d.queries[1])
	}
}

// CopyInL 不再把 COPY 语句整体转小写:混合大小写表名保持原样。
func TestCopyInPreservesTableNameCase(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.RegisterClass(TTestUser{}, "MockUser")

	users := []TTestUser{{UserName: "a", Password: "b"}}
	if eror := eng.CopyInL(&users, "MockUser"); eror != nil {
		t.Fatal(eror)
	}

	if len(d.queries) == 0 || !strings.Contains(d.queries[0], `COPY "MockUser" ("user_name", "password") FROM STDIN`) {
		t.Fatalf("copy statement should preserve table name case: %v", d.queries)
	}
}

// allReadonlyRow 全部字段只读:Insert 应返回明确错误而非越界 panic。
type allReadonlyRow struct {
	Name string `db:"-"`
}

func TestInsertNoWritableColumnErrors(t *testing.T) {
	eng := newMockEngine(&mockDriver{})
	eng.RegisterClass(allReadonlyRow{}, "t_ro")

	row := allReadonlyRow{Name: "x"}
	if eror := eng.Insert(&row); eror == nil || !strings.Contains(eror.Error(), "no insertable column") {
		t.Fatalf("expected no-insertable-column error, got %v", eror)
	}
}

// 全部可写字段均为主键:Update 无可更新列,应返回明确错误而非 panic。
func TestUpdateAllPkeyErrors(t *testing.T) {
	eng := newMockEngine(&mockDriver{})
	eng.RegisterClass(mockRow{}, "mock_row").SetKeys("id", "name")

	row := mockRow{ID: 1, Name: "x"}
	if eror := eng.Update(&row); eror == nil || !strings.Contains(eror.Error(), "no updatable column") {
		t.Fatalf("expected no-updatable-column error, got %v", eror)
	}
}

// 确定性多行 InsertL:两行数据的占位符编号连续且列序稳定。
func TestInsertLDeterministicSQL(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.RegisterClass(TTestUser{}, "test_user")

	rows := []TTestUser{{UserName: "a", Password: "x"}, {UserName: "b", Password: "y"}}
	if eror := eng.InsertL(&rows, "test_user"); eror != nil {
		t.Fatal(eror)
	}

	want := `insert into test_user ( "user_name","password" ) values ( $1,$2 ),( $3,$4 )`
	if len(d.queries) != 1 || d.queries[0] != want {
		t.Fatalf("insertL sql wrong:\n got  %q\n want %q", d.queries, want)
	}
}
