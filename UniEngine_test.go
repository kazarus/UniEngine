package UniEngine

import (
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
	if e.DefaultPageSize() != 99 || e.SpecialPageSize(50) != 99 {
		t.Fatal("oracle page size should be 99")
	}
	e.Provider = DtSQLSRV
	if e.DefaultPageSize() != 10 || e.SpecialPageSize(50) != 50 {
		t.Fatal("sqlserver page size mismatch")
	}
	e.Provider = DtPOSTGR
	if e.DefaultPageSize() != 99 || e.SpecialPageSize(50) != 99 {
		t.Fatal("postgres page size should be 99")
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
	_, err := ak.GetSqlAutoKeys(e, "mock_row")
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
// ExistConst: 应返回明确的 not-implemented 错误(旧代码查询空串)。
// ---------------------------------------------------------------------------

func TestExistConstNotImplemented(t *testing.T) {
	e := newTestEngine()
	ok, err := e.ExistConst(CtPK, "any")
	if ok {
		t.Fatal("expected false for unimplemented ExistConst")
	}
	if err == nil || !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("expected not-implemented error, got: %v", err)
	}
}

// (ctx 变体方法的存在性由 go build 保证;运行时需真实 DB,此处不覆盖。)
