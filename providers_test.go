package UniEngine

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// 协议族归一化:金仓/openGauss/PolarDB 走 PG 族,达梦走 Oracle 族,Taurus 走 MySQL 族
// ---------------------------------------------------------------------------

func TestDbFamilyMapping(t *testing.T) {
	cases := map[TDriveType]TDbFamily{
		DtPOSTGR:      FmPOSTGR,
		DtKINGES:      FmPOSTGR,
		DtOPENGS:      FmPOSTGR,
		DtPOLODB:      FmPOSTGR,
		DtSQLSRV:      FmSQLSRV,
		DtORACLE:      FmORACLE,
		DtDAMENG:      FmORACLE,
		DtMYSQLN:      FmMYSQLN,
		DtTAURUS:      FmMYSQLN,
		DtACCESS:      FmUNKNOWN,
		DtSQLITE:      FmUNKNOWN,
		TDriveType(0): FmUNKNOWN,
	}
	for provider, want := range cases {
		if got := dbFamilyOf(provider); got != want {
			t.Errorf("provider %d: family=%d want %d", provider, got, want)
		}
	}
}

func newFamilyEngine(d *mockDriver, provider TDriveType, colParam string) *TUniEngine {
	eng := &TUniEngine{Db: sql.OpenDB(mockConnector{d: d}), ColLabel: "db", ColParam: colParam, Provider: provider}
	eng.Initialize()
	eng.RegisterClass(TTestUser{}, "test_user")
	return eng
}

// KINGES(PG 族):PG 风格占位符/引号,元数据探测走 pg_class,SaveIt 走 on conflict,CopyIn 可用。
func TestKingESPGFamily(t *testing.T) {
	d := &mockDriver{
		columns:   []string{"count"},
		queryRows: [][]driver.Value{{int64(1)}},
	}
	eng := newFamilyEngine(d, DtKINGES, "$")

	if got := eng.getValParam(1); got != "$1" {
		t.Fatalf("kinges param=%q want $1", got)
	}
	if got := eng.getColParam("id"); got != `"id"` {
		t.Fatalf("kinges col=%q want quoted", got)
	}
	if eng.DefaultPageSize() != 99 {
		t.Fatal("kinges default page size should be 99 (pg family)")
	}
	if eng.ProviderName() != "kingbase" {
		t.Fatalf("provider name=%q", eng.ProviderName())
	}

	//#元数据探测按族路由到 PG 目录
	eng.DataBase = ""
	if ok, eror := eng.ExistTable("test_user"); eror != nil || !ok {
		t.Fatalf("kinges ExistTable should route to pg sql: %v %v", ok, eror)
	}
	queries := d.snapshotQueries()
	if len(queries) == 0 || !strings.Contains(queries[0], "pg_class") {
		t.Fatalf("kinges exist sql should use pg_class: %v", queries)
	}

	//#SaveIt 原生 UPSERT
	eng.RegisterClass(TTestUser{}, "test_user").SetKeys("user_name")
	row := TTestUser{UserName: "k", Password: "p"}
	if eror := eng.SaveIt(&row, "test_user"); eror != nil {
		t.Fatal(eror)
	}
	queries = d.snapshotQueries()
	if !strings.Contains(queries[len(queries)-1], "on conflict ( \"user_name\" ) do update") {
		t.Fatalf("kinges upsert sql wrong: %s", queries[len(queries)-1])
	}

	//#COPY 可用
	users := []TTestUser{{UserName: "a", Password: "b"}}
	if eror := eng.CopyInL(&users, "test_user"); eror != nil {
		t.Fatal(eror)
	}
	queries = d.snapshotQueries()
	if !strings.Contains(queries[len(queries)-1], "COPY") {
		t.Fatalf("kinges copy sql missing: %v", queries)
	}
}

// TAURUS(MySQL 族):? 占位符/裸列名,元数据探测走 information_schema,SaveIt 回退 count 路径。
func TestTaurusMySQLFamily(t *testing.T) {
	d := &mockDriver{
		columns:   []string{"value"},
		queryRows: [][]driver.Value{{int64(1)}},
	}
	eng := newFamilyEngine(d, DtTAURUS, "?")
	eng.DataBase = "taurus_db"

	if got := eng.getValParam(1); got != "?" {
		t.Fatalf("taurus param=%q want ?", got)
	}
	if got := eng.getColParam("id"); got != "id" {
		t.Fatalf("taurus col=%q want bare", got)
	}
	if eng.ProviderName() != "taurus" {
		t.Fatalf("provider name=%q", eng.ProviderName())
	}

	if ok, eror := eng.ExistTable("test_user"); eror != nil || !ok {
		t.Fatalf("taurus ExistTable should route to information_schema: %v %v", ok, eror)
	}
	queries := d.snapshotQueries()
	if len(queries) == 0 || !strings.Contains(queries[0], "information_schema.tables") {
		t.Fatalf("taurus exist sql wrong: %v", queries)
	}

	//#SaveIt 走 count-then-dispatch(MySQL 族唯一键语义不等价)
	eng.RegisterClass(TTestUser{}, "test_user").SetKeys("user_name")
	row := TTestUser{UserName: "k", Password: "p"}
	if eror := eng.SaveIt(&row, "test_user"); eror != nil {
		t.Fatal(eror)
	}
	queries = d.snapshotQueries()
	last := queries[len(queries)-1]
	if !strings.Contains(last, "update test_user set password=?") {
		t.Fatalf("taurus should fall back to count-then-update, got: %s", last)
	}
}

// DAMENG(Oracle 族)::N 占位符/裸列名,多行插入走 INSERT ALL,元数据探测走 Oracle 目录。
func TestDamengOracleFamily(t *testing.T) {
	d := &mockDriver{
		columns:   []string{"count"},
		queryRows: [][]driver.Value{{int64(1)}},
	}
	eng := newFamilyEngine(d, DtDAMENG, ":")

	if got := eng.getValParam(2); got != ":2" {
		t.Fatalf("dameng param=%q want :2", got)
	}
	if got := eng.getColParam("id"); got != "id" {
		t.Fatalf("dameng col=%q want bare", got)
	}
	if eng.ProviderName() != "dameng" {
		t.Fatalf("provider name=%q", eng.ProviderName())
	}

	eng.DataBase = ""
	if ok, eror := eng.ExistTable("test_user"); eror != nil || !ok {
		t.Fatalf("dameng ExistTable should route to oracle catalog: %v %v", ok, eror)
	}
	queries := d.snapshotQueries()
	if len(queries) == 0 || !strings.Contains(queries[0], "all_tables") {
		t.Fatalf("dameng exist sql wrong: %v", queries)
	}

	//#多行插入走 INSERT ALL(旧版仅 DtORACLE 有此分支,达梦会生成不兼容的多 values 语法)
	rows := []TTestUser{{UserName: "a", Password: "x"}, {UserName: "b", Password: "y"}}
	if eror := eng.InsertL(&rows, "test_user"); eror != nil {
		t.Fatal(eror)
	}
	queries = d.snapshotQueries()
	last := queries[len(queries)-1]
	if !strings.Contains(last, "insert all") || !strings.Contains(last, "select 1 from dual") {
		t.Fatalf("dameng insertL should use insert all: %s", last)
	}
}

// 哨兵错误:errors.Is 判别可用,且消息文本与旧版一致。
func TestSentinelErrors(t *testing.T) {
	d := &mockDriver{}
	eng := newFamilyEngine(d, DtPOSTGR, "$")

	type unknownRow struct {
		A string `db:"a"`
	}
	row := unknownRow{A: "x"}
	eror := eng.Insert(&row)
	if !errors.Is(eror, ErrUnregisteredClass) {
		t.Fatalf("expect ErrUnregisteredClass, got %v", eror)
	}
	if eror == nil || !strings.Contains(eror.Error(), "UniEngine: no such class registered: ") {
		t.Fatalf("message text should be preserved: %v", eror)
	}

	type pkeylessRow struct {
		A string `db:"a"`
	}
	eng.RegisterClass(pkeylessRow{}, "t_pkeyless")
	row2 := pkeylessRow{A: "x"}
	eror = eng.Update(&row2)
	if !errors.Is(eror, ErrNoPkeys) {
		t.Fatalf("expect ErrNoPkeys, got %v", eror)
	}

	if eror := eng.Cancel(); !errors.Is(eror, ErrNoTransaction) {
		t.Fatalf("expect ErrNoTransaction, got %v", eror)
	}
}

// 内嵌提升字段防御:AttriName 解析到提升字段(reflect Index 长度>1)时报错,
// 而不是按 [0] 错绑到内嵌结构体本身(旧 FieldByName 路径能处理提升字段,预计算后不再支持)。
// 引擎只注册顶层字段,正常注册不可达此路径;此处直写 HashField 模拟(注册表为导出 map)。
func TestSelectPromotedFieldGuard(t *testing.T) {
	type embeddedBase struct {
		ID int64
	}
	type outerRow struct {
		embeddedBase
		Name string `db:"name"`
	}

	d := &mockDriver{
		columns:   []string{"id"},
		queryRows: [][]driver.Value{{int64(1)}},
	}
	eng := newMockEngine(d)
	eng.RegisterClass(outerRow{}, "t_outer")

	//#手工注入提升字段映射:FieldByName("ID") 对 outerRow 返回 Index=[0,0]
	tbl := eng.GetTable("t_outer")
	if tbl == nil {
		t.Fatal("t_outer not registered")
	}
	tbl.HashField["id"] = TUniField{AttriName: "ID", FieldName: "id"}

	var rows []outerRow
	eror := eng.SelectL(&rows, "select id from t_outer")
	if eror == nil || !strings.Contains(eror.Error(), "embedded struct") {
		t.Fatalf("expected promoted-field guard error, got %v", eror)
	}
}
