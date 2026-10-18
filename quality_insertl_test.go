// quality_insertl_test
// #InsertL 是单条 SQL 语义;超过 999 行必须提示改用 InsertP,而不是生成超大 SQL。
package UniEngine

import (
	"errors"
	"testing"
)

func TestInsertLRejectsMoreThanMaxBatchRows(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.RegisterClass(pageRow{}, "t_large")

	rows := make([]pageRow, int(maxBatchRows)+1)
	for i := range rows {
		rows[i] = pageRow{Id: int64(i), Name: "n"}
	}
	if eror := eng.InsertL(&rows); !errors.Is(eror, ErrBatchTooLarge) {
		t.Fatalf("InsertL over max rows: got %v, want ErrBatchTooLarge", eror)
	}
	if len(d.queries) != 0 {
		t.Fatalf("oversized InsertL must not run SQL: %v", d.queries)
	}
}

func TestInsertLAllowsMaxBatchRows(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.RegisterClass(pageRow{}, "t_limit")

	rows := make([]pageRow, int(maxBatchRows))
	for i := range rows {
		rows[i] = pageRow{Id: int64(i), Name: "n"}
	}
	if eror := eng.InsertL(&rows); eror != nil {
		t.Fatalf("InsertL at max rows: %v", eror)
	}
}
