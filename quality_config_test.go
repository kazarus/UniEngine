// quality_config_test
// #Initialize 在使用真实 Db 时 fail-fast 校验基础配置;零值/仅注册引擎保持兼容。
package UniEngine

import (
	"database/sql"
	"errors"
	"testing"
)

func TestInitializeValidatesBasicConfigWithDB(t *testing.T) {
	newDB := func() *sql.DB { return sql.OpenDB(mockConnector{d: &mockDriver{}}) }

	cases := []struct {
		name   string
		engine *TUniEngine
	}{
		{"empty ColLabel", &TUniEngine{Db: newDB(), ColLabel: "", ColParam: "$", Provider: DtPOSTGR}},
		{"empty ColParam", &TUniEngine{Db: newDB(), ColLabel: "db", ColParam: "", Provider: DtPOSTGR}},
		{"invalid ColParam", &TUniEngine{Db: newDB(), ColLabel: "db", ColParam: "!!", Provider: DtPOSTGR}},
		{"zero Provider", &TUniEngine{Db: newDB(), ColLabel: "db", ColParam: "$", Provider: 0}},
		{"unknown Provider", &TUniEngine{Db: newDB(), ColLabel: "db", ColParam: "$", Provider: TDriveType(999)}},
	}
	for _, C := range cases {
		if eror := C.engine.Initialize(); !errors.Is(eror, ErrInvalidConfig) {
			t.Errorf("%s: got %v, want ErrInvalidConfig", C.name, eror)
		}
	}
}

func TestInitializeAllowsRegistrationOnlyZeroEngine(t *testing.T) {
	eng := &TUniEngine{}
	if eror := eng.Initialize(); eror != nil {
		t.Fatalf("zero engine registration-only Initialize must stay valid: %v", eror)
	}
	if eng.GetTable("unitable") == nil || eng.GetTable("unifield") == nil {
		t.Fatal("built-in metadata classes should be registered")
	}
}

func TestInitializeAcceptsValidBasicConfig(t *testing.T) {
	eng := &TUniEngine{
		Db:       sql.OpenDB(mockConnector{d: &mockDriver{}}),
		ColLabel: "db",
		ColParam: "$",
		Provider: DtPOSTGR,
	}
	if eror := eng.Initialize(); eror != nil {
		t.Fatalf("valid config: %v", eror)
	}
}
