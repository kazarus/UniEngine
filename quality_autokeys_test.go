// quality_autokeys_test
// #AutoKeys 不传生成器时按协议族选择内置 SQL,不能继续写死 PostgreSQL。
package UniEngine

import (
	"strings"
	"testing"
)

func TestAutoKeysDefaultSQLByProvider(t *testing.T) {
	cases := []struct {
		provider TDriveType
		want     string
	}{
		{DtPOSTGR, "pg_attribute"},
		{DtKINGES, "pg_attribute"},
		{DtOPENGS, "pg_attribute"},
		{DtPOLODB, "pg_attribute"},
		{DtSQLSRV, "sys.indexes"},
		{DtORACLE, "user_cons_columns"},
		{DtDAMENG, "user_cons_columns"},
		{DtMYSQLN, "information_schema.columns"},
		{DtTAURUS, "information_schema.columns"},
	}

	for _, C := range cases {
		eng := newTestEngine()
		eng.Provider = C.provider
		eng.DataBase = "demo"
		SQL, eror := eng.defaultAutoKeysSQL("t_probe")
		if eror != nil {
			t.Errorf("provider=%v: %v", C.provider, eror)
			continue
		}
		if !strings.Contains(SQL, C.want) {
			t.Errorf("provider=%v SQL=%q want substring %q", C.provider, SQL, C.want)
		}
	}
}

func TestAutoKeysDefaultSQLRequiresMySQLDatabase(t *testing.T) {
	eng := newTestEngine()
	eng.Provider = DtMYSQLN
	eng.DataBase = ""
	if _, eror := eng.defaultAutoKeysSQL("t_probe"); eror == nil {
		t.Fatal("MySQL AutoKeys without DataBase must fail")
	}
}

func TestAutoKeysDefaultSQLRejectsUnknownProvider(t *testing.T) {
	eng := newTestEngine()
	eng.Provider = TDriveType(999)
	if _, eror := eng.defaultAutoKeysSQL("t_probe"); eror == nil {
		t.Fatal("unknown provider must fail instead of silently using PostgreSQL")
	}
}
