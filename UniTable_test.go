// UniTable_test
// #TUniTable 元数据链式设置:SetModule 赋值 ModuleBy

package UniEngine

import "testing"

// TModuleRow SetModule 测试类:mac_text 作为 SetVerify 的 MAC 伴随列
type TModuleRow struct {
	UserName string `db:"user_name"`
	MacText  string `db:"mac_text"`
}

// #测试1:SetModule 返回新快照、双键发布、旧快照不被改写
func TestSetModuleSnapshot(t *testing.T) {
	eng := newMockEngine(&mockDriver{})
	Base := eng.RegisterClass(TModuleRow{}, "module_row")
	if Base.LastError != nil {
		t.Fatal(Base.LastError)
	}

	Next := Base.SetModule("core")
	if Next == Base {
		t.Fatal("SetModule should return a new snapshot")
	}
	if Next.ModuleBy != "core" {
		t.Fatalf("ModuleBy not set: %q", Next.ModuleBy)
	}
	if Base.ModuleBy != "" {
		t.Fatalf("published snapshot must not be mutated in place: %q", Base.ModuleBy)
	}

	for _, Key := range []string{"UniEngine.TModuleRow", "module_row"} {
		Tbl, Ok := eng.HashTabl[Key]
		if !Ok || Tbl.ModuleBy != "core" {
			t.Fatalf("registry key[%s] should point to the new snapshot: %+v", Key, Tbl)
		}
	}
}

// #测试2:SetModule 可在 SetVerify 之后调用并可清空(纯元数据,不受主键冻结影响)
func TestSetModuleAfterVerify(t *testing.T) {
	eng := newMockEngine(&mockDriver{})
	Tbl := eng.RegisterClass(TModuleRow{}, "module_row").
		SetKeys("user_name").
		SetVerify("mac_text", "user_name").
		SetModule("risk")
	if Tbl.LastError != nil {
		t.Fatal(Tbl.LastError)
	}
	if Tbl.ModuleBy != "risk" {
		t.Fatalf("ModuleBy not set after verify: %q", Tbl.ModuleBy)
	}

	Cleared := Tbl.SetModule("")
	if Cleared.ModuleBy != "" {
		t.Fatalf("empty module should clear ModuleBy: %q", Cleared.ModuleBy)
	}
	if Published := eng.HashTabl["module_row"]; Published == nil || Published.ModuleBy != "" {
		t.Fatal("cleared snapshot should be published")
	}
}

// #测试3:失败链上 SetModule 短路,不覆盖错误、不改 ModuleBy
func TestSetModuleStopsOnLastError(t *testing.T) {
	eng := newMockEngine(&mockDriver{})
	Broken := eng.RegisterClass(TModuleRow{}, "module_row").SetKeys("no_such_column")
	if Broken.LastError == nil {
		t.Fatal("expected SetKeys to fail on unregistered field")
	}

	Same := Broken.SetModule("core")
	if Same != Broken {
		t.Fatal("SetModule should short-circuit on LastError")
	}
	if Same.ModuleBy != "" {
		t.Fatalf("ModuleBy must stay empty on failed chain: %q", Same.ModuleBy)
	}
	if Same.LastError == nil {
		t.Fatal("LastError must be preserved")
	}
}

// #测试4:未挂引擎的裸表(engine==nil)原地设置,便于单测/元数据构造
func TestSetModuleStandalone(t *testing.T) {
	Tbl := &TUniTable{TableName: "standalone"}
	if Got := Tbl.SetModule("core"); Got != Tbl || Tbl.ModuleBy != "core" {
		t.Fatalf("standalone table should be mutated in place: %+v", Got)
	}
}
