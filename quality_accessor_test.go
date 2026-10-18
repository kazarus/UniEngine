// quality_accessor_test
// #GetTableByClass 提供持锁的类名键访问,替代调用方直接读 HashTabl 的并发风险。
package UniEngine

import "testing"

type accessorRow struct {
	ID int64 `db:"id"`
}

func TestGetTableByClass(t *testing.T) {
	eng := newTestEngine()
	tbl := eng.RegisterClass(accessorRow{}, "t_accessor")
	if tbl.LastError != nil {
		t.Fatal(tbl.LastError)
	}
	if got := eng.GetTableByClass("UniEngine.accessorRow"); got != tbl {
		t.Fatalf("GetTableByClass=%+v want %+v", got, tbl)
	}
	if got := eng.GetTableByClass(""); got != nil {
		t.Fatalf("empty class name must return nil, got %+v", got)
	}
	if got := eng.GetTableByClass("UniEngine.missing"); got != nil {
		t.Fatalf("missing class must return nil, got %+v", got)
	}
}
