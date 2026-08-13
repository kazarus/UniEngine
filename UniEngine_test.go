package UniEngine

import (
	"strings"
	"testing"
)

// testUser 用于注册测试：email 标记 readonly，验证生成 SQL 跳过只读列
type testUser struct {
	ID    int64  `db:"id" json:"id"`
	Name  string `db:"name" json:"name"`
	Email string `db:"email,readonly" json:"email"`
}

func newTestEngine(provider TDriveType) *TUniEngine {
	return &TUniEngine{ColLabel: "db", ColParam: "$", Provider: provider}
}

func TestGetValParam(t *testing.T) {
	pg := newTestEngine(DtPOSTGR)
	if got := pg.getValParam(1); got != "$1" {
		t.Errorf("Postgres getValParam(1) = %q, want $1", got)
	}

	my := newTestEngine(DtMYSQLN)
	my.ColParam = "?"
	if got := my.getValParam(1); got != "?" {
		t.Errorf("MySQL getValParam(1) = %q, want ?", got)
	}
}

func TestGetColParam(t *testing.T) {
	pg := newTestEngine(DtPOSTGR)
	if got := pg.getColParam("id"); got != `"id"` {
		t.Errorf("Postgres getColParam(id) = %q, want \"id\"", got)
	}

	my := newTestEngine(DtMYSQLN)
	if got := my.getColParam("id"); got != "id" {
		t.Errorf("MySQL getColParam(id) = %q, want id", got)
	}
}

func TestGetSqlQuery(t *testing.T) {
	// Postgres：占位符保持原样
	pg := newTestEngine(DtPOSTGR)
	if got := pg.getSqlQuery("select * from t where id=$1 and x=$2", 1, 2); got != "select * from t where id=$1 and x=$2" {
		t.Errorf("Postgres placeholder = %q", got)
	}

	// Oracle：$N -> :N，且字符串字面量中的 $ 不被误改
	or := newTestEngine(DtORACLE)
	or.ColParam = ":"
	if got := or.getSqlQuery("select '$abc' as x from t where id=$1", 1); got != "select '$abc' as x from t where id=:1" {
		t.Errorf("Oracle placeholder = %q", got)
	}

	// MySQL：$N -> ?
	my := newTestEngine(DtMYSQLN)
	my.ColParam = "?"
	if got := my.getSqlQuery("select * from t where id=$1", 1); got != "select * from t where id=?" {
		t.Errorf("MySQL placeholder = %q", got)
	}

	// 无参数时不执行任何替换
	if got := pg.getSqlQuery("select * from t where id=$1"); got != "select * from t where id=$1" {
		t.Errorf("no-arg query = %q", got)
	}
}

func TestPrepareRunSQLInsert(t *testing.T) {
	engine := newTestEngine(DtPOSTGR)
	engine.RegisterClass(testUser{}, "test_user")
	if err := engine.PrepareTables("test_user"); err != nil {
		t.Fatalf("PrepareTables: %v", err)
	}

	sql, fields, _, err := engine.PrepareRunSQL("test_user", EtInsert)
	if err != nil {
		t.Fatalf("PrepareRunSQL insert: %v", err)
	}
	// readonly 的 email 不应出现在列清单中；字段按 struct 声明顺序
	want := `insert into test_user ( "id","name" ) values ( $1,$2 ) `
	if sql != want {
		t.Errorf("insert sql = %q, want %q", sql, want)
	}
	if len(fields) != 2 {
		t.Errorf("insert fields = %d, want 2 (readonly excluded)", len(fields))
	}
}

func TestPrepareRunSQLUpdate(t *testing.T) {
	engine := newTestEngine(DtPOSTGR)
	tb := engine.RegisterClass(testUser{}, "test_user")
	if err := tb.SetKeys("id"); err != nil {
		t.Fatalf("SetKeys: %v", err)
	}
	if err := engine.PrepareTables("test_user"); err != nil {
		t.Fatalf("PrepareTables: %v", err)
	}

	sql, _, _, err := engine.PrepareRunSQL("test_user", EtUpdate)
	if err != nil {
		t.Fatalf("PrepareRunSQL update: %v", err)
	}
	// pkey id 进 WHERE，name 进 SET，email(readonly) 忽略
	want := `update test_user set "name"=$1 where 1=1 and "id"=$2`
	if sql != want {
		t.Errorf("update sql = %q, want %q", sql, want)
	}
}

func TestPrepareRunSQLSelect(t *testing.T) {
	engine := newTestEngine(DtPOSTGR)
	tb := engine.RegisterClass(testUser{}, "test_user")
	if err := tb.SetKeys("id"); err != nil {
		t.Fatalf("SetKeys: %v", err)
	}

	sql, _, _, err := engine.PrepareRunSQL("test_user", EtSelect)
	if err != nil {
		t.Fatalf("PrepareRunSQL select: %v", err)
	}
	if !strings.Contains(sql, `"id"=$1`) {
		t.Errorf("select sql = %q, want pkey where clause", sql)
	}
}

// 全只读字段：不应越界 panic，而应返回明确错误
func TestPrepareRunSQLAllReadonly(t *testing.T) {
	type allRo struct {
		A string `db:"a,readonly"`
	}
	engine := newTestEngine(DtPOSTGR)
	engine.RegisterClass(allRo{}, "all_ro")
	if err := engine.PrepareTables("all_ro"); err != nil {
		t.Fatalf("PrepareTables: %v", err)
	}

	if _, _, _, err := engine.PrepareRunSQL("all_ro", EtInsert); err == nil {
		t.Error("insert with all-readonly fields should return error, got nil")
	}
	if _, _, _, err := engine.PrepareRunSQL("all_ro", EtUpdate); err == nil {
		t.Error("update with all-readonly fields should return error, got nil")
	}
}

func TestRegisterClassFieldOrder(t *testing.T) {
	engine := newTestEngine(DtPOSTGR)
	engine.RegisterClass(testUser{}, "test_user")
	// RegisterClass 直接注册后（未 PrepareTables），PrepareRunSQL 依赖 ListField
	// 通过 RegisterClass 按声明顺序填充的有序列表
	uniTable, ok := engine.HashTabl["test_user"]
	if !ok {
		t.Fatal("registered table not found by lowercase name key")
	}
	if len(uniTable.ListField) != 3 {
		t.Fatalf("ListField = %d, want 3 (including readonly)", len(uniTable.ListField))
	}
	if uniTable.ListField[0].FieldName != "id" || uniTable.ListField[1].FieldName != "name" {
		t.Errorf("field order = %s,%s; want id,name", uniTable.ListField[0].FieldName, uniTable.ListField[1].FieldName)
	}
}

func TestSetKeysDedupAndOrder(t *testing.T) {
	engine := newTestEngine(DtPOSTGR)
	tb := engine.RegisterClass(testUser{}, "test_user")

	if err := tb.SetKeys("name", "id"); err != nil {
		t.Fatalf("SetKeys: %v", err)
	}
	if err := tb.SetKeys("id"); err != nil {
		t.Fatalf("SetKeys dup: %v", err)
	}
	if len(tb.ListPkeys) != 2 {
		t.Fatalf("ListPkeys = %d, want 2 (dedup)", len(tb.ListPkeys))
	}
	if tb.ListPkeys[0].FieldName != "name" {
		t.Errorf("pkey order[0] = %q, want name", tb.ListPkeys[0].FieldName)
	}

	if err := tb.SetKeys("not_exist"); err == nil {
		t.Error("SetKeys on unregistered field should return error")
	}
}

func TestAutoKeysMySQLMissingDatabase(t *testing.T) {
	engine := newTestEngine(DtMYSQLN)
	tb := engine.RegisterClass(testUser{}, "test_user")

	// 未指定 DataBase 时，GetSqlAutoKeys 返回空 SQL，AutoKeys 应返回错误而非 panic
	if err := tb.AutoKeys(*engine, TAutoKeys4MYSQLN{}); err == nil {
		t.Error("AutoKeys with empty database should return error")
	}
}

func TestValidIdent(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"test_user", true},
		{"schema.table", true},
		{"a_b123", true},
		{"x;drop table t", false},
		{"a'b", false},
		{"a--b", false},
		{"", false},
	}
	for _, c := range cases {
		if got := validIdent(c.name); got != c.want {
			t.Errorf("validIdent(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestExistTableRejectsInjection(t *testing.T) {
	engine := newTestEngine(DtPOSTGR)
	if _, err := engine.ExistTable("t;drop table x"); err == nil {
		t.Error("ExistTable with malicious name should return error")
	}
}
