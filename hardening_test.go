package UniEngine

import (
	"fmt"
	"sync"
	"testing"
)

// hardeningUser 用于注册测试：email 标记 readonly
type hardeningUser struct {
	ID    int64  `db:"id" json:"id"`
	Name  string `db:"name" json:"name"`
	Email string `db:"email,readonly" json:"email"`
}

func newHardeningEngine(provider TDriveType) *TUniEngine {
	return &TUniEngine{ColLabel: "db", ColParam: "$", Provider: provider}
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
	engine := newHardeningEngine(DtPOSTGR)
	if _, err := engine.ExistTable("t;drop table x"); err == nil {
		t.Error("ExistTable with malicious name should return error")
	}
}

func TestRegisterClassFieldOrder(t *testing.T) {
	engine := newHardeningEngine(DtPOSTGR)
	engine.RegisterClass(hardeningUser{}, "test_user")

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
	engine := newHardeningEngine(DtPOSTGR)
	tb := engine.RegisterClass(hardeningUser{}, "test_user")

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
	engine := newHardeningEngine(DtMYSQLN)
	tb := engine.RegisterClass(hardeningUser{}, "test_user")

	// 未指定 DataBase 时，GetSqlAutoKeys 返回错误，AutoKeys 应返回错误而非 panic
	if err := tb.AutoKeys(engine, TAutoKeys4MYSQLN{}); err == nil {
		t.Error("AutoKeys with empty database should return error")
	}
}

// 并发注册:不经过 Initialize 的引擎并发 RegisterClass,验证锁的懒初始化与注册互斥
// (在 -race 下运行才能捕获旧实现的懒初始化数据竞争)
func TestConcurrentRegisterClass(t *testing.T) {
	engine := &TUniEngine{ColLabel: "db", ColParam: "$", Provider: DtPOSTGR}

	const N = 16
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			engine.RegisterClass(hardeningUser{}, fmt.Sprintf("t_conc_%d", n))
		}(i)
	}
	wg.Wait()

	for i := 0; i < N; i++ {
		name := fmt.Sprintf("t_conc_%d", i)
		if engine.GetTable(name) == nil {
			t.Fatalf("table %s not registered", name)
		}
	}
}
