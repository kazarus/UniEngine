// quality_fix6_test
// #2026-10-18 注册表双键冲突:不静默覆盖,显式 Force 才能替换手工表

package UniEngine

import (
	"errors"
	"testing"
)

type regConflictA struct {
	ID int64 `db:"id"`
}

type regConflictB struct {
	ID int64 `db:"id"`
}

func TestRegisterClassRejectsManualTableConflict(t *testing.T) {
	eng := newTestEngine()
	eng.RegisterTable("t_manual", 7)
	eng.RegisterField("t_manual", "id")
	eng.RegisterPkeys("t_manual", "id")

	Before := eng.GetTable("t_manual")
	if Before == nil || len(Before.HashPkeys) != 1 {
		t.Fatal("precondition: manual table should have one pkey")
	}

	tbl := eng.RegisterClass(regConflictA{}, "t_manual")
	if !errors.Is(tbl.LastError, ErrTableConflict) {
		t.Fatalf("manual table conflict must be ErrTableConflict, got %v", tbl.LastError)
	}
	After := eng.GetTable("t_manual")
	if After != Before {
		t.Fatal("conflict must not replace the published manual table")
	}
	if _, ok := After.HashPkeys["id"]; !ok {
		t.Fatal("conflict must not drop manual pkeys")
	}
	if _, ok := eng.HashTabl["UniEngine.regConflictA"]; ok {
		t.Fatal("conflict must not publish the class key")
	}
}

func TestRegisterClassForceReplacesManualTable(t *testing.T) {
	eng := newTestEngine()
	eng.RegisterTable("t_manual", 7)
	eng.RegisterPkeys("t_manual", "id")

	tbl := eng.RegisterClassForce(regConflictA{}, "t_manual")
	if tbl.LastError != nil {
		t.Fatalf("RegisterClassForce should replace a manual table, got %v", tbl.LastError)
	}
	if tbl.ClassType == nil || tbl.ClassType.String() != "UniEngine.regConflictA" {
		t.Fatalf("forced table should be class-owned, got %+v", tbl.ClassType)
	}
	if eng.GetTable("t_manual") != tbl {
		t.Fatal("registry should point at the forced snapshot")
	}

	//#被替换的手工 pkey 不会自动迁移;显式重新登记后才可写
	tbl = tbl.SetKeys("id")
	if tbl.LastError != nil {
		t.Fatalf("SetKeys after force: %v", tbl.LastError)
	}
}

func TestRegisterClassAllowsDifferentClassSameTable(t *testing.T) {
	eng := newTestEngine()
	First := eng.RegisterClass(regConflictA{}, "t_shared")
	if First.LastError != nil {
		t.Fatal(First.LastError)
	}

	Second := eng.RegisterClass(regConflictB{}, "t_shared")
	if Second.LastError != nil {
		t.Fatalf("different class on same table must be allowed, got %v", Second.LastError)
	}
	if got := eng.GetTable("t_shared"); got != Second {
		t.Fatal("table key must point at the latest registration")
	}
	if got := eng.HashTabl["UniEngine.regConflictA"]; got != First {
		t.Fatal("first class key must stay intact")
	}
	if got := eng.HashTabl["UniEngine.regConflictB"]; got != Second {
		t.Fatal("second class key must be published")
	}
}

func TestRegisterClassForceAllowsDifferentClassSameTable(t *testing.T) {
	eng := newTestEngine()
	First := eng.RegisterClass(regConflictA{}, "t_owned")
	if First.LastError != nil {
		t.Fatal(First.LastError)
	}

	tbl := eng.RegisterClassForce(regConflictB{}, "t_owned")
	if tbl.LastError != nil {
		t.Fatalf("force should allow another class on the same table, got %v", tbl.LastError)
	}
	if eng.GetTable("t_owned") != tbl {
		t.Fatal("table key should point at the latest class")
	}
	if eng.HashTabl["UniEngine.regConflictA"] != First {
		t.Fatal("first class key must stay intact")
	}
}

func TestRegisterClassRejectsClassRebind(t *testing.T) {
	eng := newTestEngine()
	First := eng.RegisterClass(regConflictA{}, "t_first")
	if First.LastError != nil {
		t.Fatal(First.LastError)
	}

	Second := eng.RegisterClass(regConflictA{}, "t_second")
	if !errors.Is(Second.LastError, ErrClassConflict) {
		t.Fatalf("same class to another table must be ErrClassConflict, got %v", Second.LastError)
	}
	if got := eng.HashTabl["UniEngine.regConflictA"]; got != First {
		t.Fatal("class key must not be rebound")
	}
	if eng.GetTable("t_second") != nil {
		t.Fatal("second table must not be published")
	}
}

func TestRegisterClassForceCannotRebindClass(t *testing.T) {
	eng := newTestEngine()
	First := eng.RegisterClass(regConflictA{}, "t_first")
	if First.LastError != nil {
		t.Fatal(First.LastError)
	}

	Second := eng.RegisterClassForce(regConflictA{}, "t_second")
	if !errors.Is(Second.LastError, ErrClassConflict) {
		t.Fatalf("force must not rebind a class, got %v", Second.LastError)
	}
}

func TestRegisterClassAllowsSameClassSameTable(t *testing.T) {
	eng := newTestEngine()
	First := eng.RegisterClass(regConflictA{}, "t_same")
	if First.LastError != nil {
		t.Fatal(First.LastError)
	}
	First = First.SetKeys("id")
	if First.LastError != nil {
		t.Fatal(First.LastError)
	}

	Second := eng.RegisterClass(regConflictA{}, "t_same")
	if Second.LastError != nil {
		t.Fatalf("same class/table re-register must be allowed: %v", Second.LastError)
	}
	if eng.GetTable("t_same") != Second || eng.HashTabl["UniEngine.regConflictA"] != Second {
		t.Fatal("both keys must point at the new snapshot")
	}
	//#重新注册按重建语义处理,旧 pkey 不自动保留
	if len(Second.HashPkeys) != 0 {
		t.Fatalf("re-register resets class metadata, got pkeys %+v", Second.HashPkeys)
	}
}

func TestInitializeTwiceStableWithConflictChecks(t *testing.T) {
	eng := &TUniEngine{ColLabel: "db", ColParam: "$", Provider: DtPOSTGR}
	if eror := eng.Initialize(); eror != nil {
		t.Fatal(eror)
	}
	if eror := eng.Initialize(); eror != nil {
		t.Fatalf("second Initialize should be idempotent, got %v", eror)
	}
}
