// quality_conston_test
// #ExistConstOn(CtDF) 用表名+列名精确判定默认值,解决 ExistConst 只看列名的歧义。
package UniEngine

import (
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
)

func TestExistConstOnCtDFUsesTable(t *testing.T) {
	cases := []struct {
		provider TDriveType
		want     []string
	}{
		{DtPOSTGR, []string{"pg_attrdef", "c.relname='t_probe'", "a.attname='col_probe'"}},
		{DtSQLSRV, []string{"sys.columns", "t.name='t_probe'", "c.name='col_probe'"}},
		{DtORACLE, []string{"user_tab_cols", "table_name=upper('t_probe')", "column_name=upper('col_probe')"}},
		{DtMYSQLN, []string{"information_schema.columns", "table_name='t_probe'", "column_name='col_probe'"}},
	}
	for _, C := range cases {
		d := &mockDriver{columns: []string{"count"}, queryRows: [][]driver.Value{{int64(1)}}}
		eng := newMockEngine(d)
		eng.Provider = C.provider
		eng.DataBase = "demo"
		if C.provider == DtSQLSRV {
			eng.ProvideName = "mssql"
		}
		ok, eror := eng.ExistConstOn("t_probe", CtDF, "col_probe")
		if eror != nil || !ok {
			t.Fatalf("provider=%v ExistConstOn=(%v,%v)", C.provider, ok, eror)
		}
		q := strings.Join(d.snapshotQueries(), " ")
		for _, part := range C.want {
			if !strings.Contains(q, part) {
				t.Errorf("provider=%v SQL=%q missing %q", C.provider, q, part)
			}
		}
	}
}

func TestExistConstOnValidatesNames(t *testing.T) {
	eng := newMockEngine(&mockDriver{})
	if _, eror := eng.ExistConstOn("t;drop", CtDF, "col"); !errors.Is(eror, ErrInvalidTableName) {
		t.Fatalf("invalid table: %v", eror)
	}
	if _, eror := eng.ExistConstOn("t", CtDF, "col;drop"); !errors.Is(eror, ErrInvalidConstraintName) {
		t.Fatalf("invalid column: %v", eror)
	}
}
