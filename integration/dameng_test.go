// dameng_test
// #达梦真机测试:Oracle 兼容协议(SQL 生成走 FmORACLE),驱动 gitee.com/chunanyong/dm。
// #
// #运行:
// #  UNIENGINE_DM_HOST=127.0.0.1 UNIENGINE_DM_PORT=5236 \
// #  UNIENGINE_DM_USER=SYSDBA UNIENGINE_DM_PASS='...' \
// #  UNIENGINE_DM_DB=UNIENGINE_TESTDB go test ./integration/ -run Dameng -v
// #  或整串: UNIENGINE_DM_DSN='dm://SYSDBA:pass@127.0.0.1:5236?schema=DB'
// #
// #不可达时跳过;UNIENGINE_DM_REQUIRED=1 时失败。只操作自建 uni_it_dm_* 表并清理。
package integration

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	_ "gitee.com/chunanyong/dm"
	UniEngine "github.com/kazarus/UniEngine"
)

const (
	dmProbeTable = "uni_it_dm_probe"
	dmPageTable  = "uni_it_dm_page"
	dmWideTable  = "uni_it_dm_wide"
)

func dmEnv() (dsn, schema string) {
	schema = os.Getenv("UNIENGINE_DM_SCHEMA")
	if schema == "" {
		schema = os.Getenv("UNIENGINE_DM_DB")
	}
	if v := os.Getenv("UNIENGINE_DM_DSN"); v != "" {
		return v, schema
	}
	host := os.Getenv("UNIENGINE_DM_HOST")
	if host == "" {
		return "", ""
	}
	port := os.Getenv("UNIENGINE_DM_PORT")
	if port == "" {
		port = "5236"
	}
	user := os.Getenv("UNIENGINE_DM_USER")
	pass := os.Getenv("UNIENGINE_DM_PASS")
	dsn = fmt.Sprintf("dm://%s:%s@%s:%s?schema=%s",
		url.PathEscape(user), url.PathEscape(pass), host, port, url.QueryEscape(schema))
	return dsn, schema
}

func openDameng(t *testing.T) *sql.DB {
	t.Helper()

	dsn, _ := dmEnv()
	if dsn == "" {
		t.Skip("未设置 UNIENGINE_DM_DSN / UNIENGINE_DM_HOST,跳过达梦集成测试")
	}
	db, eror := sql.Open("dm", dsn)
	if eror != nil {
		t.Fatalf("open dm: %v", eror)
	}
	if eror = db.Ping(); eror != nil {
		if os.Getenv("UNIENGINE_DM_REQUIRED") == "1" {
			t.Fatalf("达梦不可达(UNIENGINE_DM_REQUIRED=1): %v", eror)
		}
		db.Close()
		t.Skipf("达梦不可达,跳过集成测试: %v", eror)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func damengEngineOn(t *testing.T, db *sql.DB) *UniEngine.TUniEngine {
	t.Helper()
	eng := &UniEngine.TUniEngine{
		Db:       db,
		ColLabel: "db",
		ColParam: ":",
		Provider: UniEngine.DtDAMENG,
	}
	if eror := eng.Initialize(); eror != nil {
		t.Fatalf("Initialize: %v", eror)
	}
	return eng
}

func damengDrop(t *testing.T, eng *UniEngine.TUniEngine, table string) {
	t.Helper()
	//#达梦/Oracle 不支持 drop table if exists;不存在时的错误忽略。
	_ = eng.Execute("drop table " + table)
}

type dmProbeRow struct {
	Id     int64  `db:"id"`
	Name   string `db:"name"`
	Secret string `db:"secret,secreton"`
}

func setupDamengProbe(t *testing.T, eng *UniEngine.TUniEngine) {
	t.Helper()
	damengDrop(t, eng, dmProbeTable)
	if eror := eng.Execute("create table " + dmProbeTable + "(id bigint primary key, name varchar(64), secret varchar(1024))"); eror != nil {
		t.Fatalf("create: %v", eror)
	}
	t.Cleanup(func() { damengDrop(t, eng, dmProbeTable) })

	tbl := eng.RegisterClass(dmProbeRow{}, dmProbeTable).SetKeys("id")
	if tbl.LastError != nil {
		t.Fatalf("register: %v", tbl.LastError)
	}
}

func TestDamengRoundTrip(t *testing.T) {
	db := openDameng(t)
	eng := damengEngineOn(t, db)
	setupDamengProbe(t, eng)

	if eror := eng.Insert(&dmProbeRow{Id: 100, Name: "ALPHA", Secret: "s1"}); eror != nil {
		t.Fatalf("Insert: %v", eror)
	}
	var got dmProbeRow
	if eror := eng.Select(&got, "select id,name,secret from "+dmProbeTable+" where id=?", int64(100)); eror != nil {
		t.Fatalf("Select: %v", eror)
	}
	if got.Name != "ALPHA" || got.Secret != "s1" {
		t.Fatalf("Select got %+v", got)
	}

	got.Name = "ALPHA-2"
	if eror := eng.Update(&got); eror != nil {
		t.Fatalf("Update: %v", eror)
	}
	var name string
	if eror := db.QueryRow("select name from "+dmProbeTable+" where id=?", int64(100)).Scan(&name); eror != nil || name != "ALPHA-2" {
		t.Fatalf("raw after update=(%q,%v)", name, eror)
	}

	if eror := eng.SaveIt(&dmProbeRow{Id: 200, Name: "BETA"}); eror != nil {
		t.Fatalf("SaveIt insert: %v", eror)
	}
	if eror := eng.SaveIt(&dmProbeRow{Id: 200, Name: "BETA-2"}); eror != nil {
		t.Fatalf("SaveIt update: %v", eror)
	}
	if eror := eng.SaveItWhenNotExist(&dmProbeRow{Id: 200, Name: "SHOULD-NOT-CHANGE"}); eror != nil {
		t.Fatalf("SaveItWhenNotExist: %v", eror)
	}
	if eror := db.QueryRow("select name from "+dmProbeTable+" where id=?", int64(200)).Scan(&name); eror != nil || name != "BETA-2" {
		t.Fatalf("SaveItWhenNotExist overwrote row: (%q,%v)", name, eror)
	}

	if eror := eng.InsertL(&[]dmProbeRow{{Id: 300, Name: "GAMMA"}, {Id: 301, Name: "DELTA"}}); eror != nil {
		t.Fatalf("InsertL(INSERT ALL): %v", eror)
	}
	if eror := eng.Delete(&dmProbeRow{Id: 100}); eror != nil {
		t.Fatalf("Delete: %v", eror)
	}
	n, eror := eng.SelectD("select count(1) from " + dmProbeTable)
	if eror != nil || n != 3 {
		t.Fatalf("count=(%d,%v), want 3", n, eror)
	}

	if ok, eror := eng.ExistTable(dmProbeTable); eror != nil || !ok {
		t.Fatalf("ExistTable=(%v,%v)", ok, eror)
	}
	if ok, eror := eng.ExistField(dmProbeTable, "name"); eror != nil || !ok {
		t.Fatalf("ExistField=(%v,%v)", ok, eror)
	}
	tbl := eng.GetTable(dmProbeTable)
	if err := tbl.AutoKeys(eng); err != nil {
		t.Fatalf("AutoKeys(default): %v", err)
	}
	if _, ok := eng.GetTable(dmProbeTable).HashPkeys["id"]; !ok {
		t.Fatal("AutoKeys should register id")
	}
}

func TestDamengSecretRoundTrip(t *testing.T) {
	db := openDameng(t)
	eng := damengEngineOn(t, db)
	eng.SecretOn = 1
	eng.SecretBy = "dameng-integration-key"
	eng.SecretIter = 2048
	setupDamengProbe(t, eng)

	if eror := eng.Insert(&dmProbeRow{Id: 1000, Name: "SEC", Secret: "s3cret中文"}); eror != nil {
		t.Fatalf("Insert: %v", eror)
	}
	var raw string
	if eror := db.QueryRow("select secret from "+dmProbeTable+" where id=?", int64(1000)).Scan(&raw); eror != nil {
		t.Fatalf("raw select: %v", eror)
	}
	if !strings.HasPrefix(raw, UniEngine.UniSecretTag) || raw == "s3cret中文" {
		t.Fatalf("stored secret should be ENC cipher, got %.40s", raw)
	}

	var got dmProbeRow
	if eror := eng.Select(&got, "select id,name,secret from "+dmProbeTable+" where id=?", int64(1000)); eror != nil {
		t.Fatalf("Select: %v", eror)
	}
	if got.Secret != "s3cret中文" {
		t.Fatalf("decrypt failed: %q", got.Secret)
	}
	if v, eror := eng.SelectS("select secret from "+dmProbeTable+" where id=?", int64(1000)); eror != nil || v != "s3cret中文" {
		t.Fatalf("SelectS=(%q,%v)", v, eror)
	}
}

type dmPageRow struct {
	Id   int64  `db:"id"`
	Name string `db:"name"`
}

func TestDamengInsertPPageSizeClamp(t *testing.T) {
	db := openDameng(t)
	eng := damengEngineOn(t, db)
	damengDrop(t, eng, dmPageTable)
	if eror := eng.Execute("create table " + dmPageTable + "(id bigint primary key, name varchar(64))"); eror != nil {
		t.Fatalf("create: %v", eror)
	}
	t.Cleanup(func() { damengDrop(t, eng, dmPageTable) })
	tbl := eng.RegisterClass(dmPageRow{}, dmPageTable).SetKeys("id")
	if tbl.LastError != nil {
		t.Fatalf("register: %v", tbl.LastError)
	}

	rows := make([]dmPageRow, 1000)
	for i := range rows {
		rows[i] = dmPageRow{Id: int64(i + 1), Name: "n"}
	}
	if eror := eng.InsertP(&rows, -5, dmPageTable); eror != nil {
		t.Fatalf("InsertP: %v", eror)
	}
	n, eror := eng.SelectD("select count(1) from " + dmPageTable)
	if eror != nil || n != 1000 {
		t.Fatalf("count=(%d,%v), want 1000", n, eror)
	}
}

type dmWideRow struct {
	Id  int64 `db:"id"`
	C01 int64 `db:"c01"`
	C02 int64 `db:"c02"`
	C03 int64 `db:"c03"`
	C04 int64 `db:"c04"`
	C05 int64 `db:"c05"`
	C06 int64 `db:"c06"`
	C07 int64 `db:"c07"`
	C08 int64 `db:"c08"`
	C09 int64 `db:"c09"`
	C10 int64 `db:"c10"`
}

// #生产兼容配置:Provider=DtORACLE + Supplier=DtDAMENG。
// #SQL 仍走 Oracle 分支,批量预算走达梦 2000/列数(10 列 → 200 行/页)。
func TestDamengOracleCompatInsertP(t *testing.T) {
	db := openDameng(t)
	eng := &UniEngine.TUniEngine{
		Db:       db,
		ColLabel: "db",
		ColParam: ":",
		Provider: UniEngine.DtORACLE,
		Supplier: UniEngine.DtDAMENG,
	}
	if eror := eng.Initialize(); eror != nil {
		t.Fatalf("Initialize: %v", eror)
	}

	damengDrop(t, eng, dmWideTable)
	if eror := eng.Execute("create table " + dmWideTable + "(id bigint primary key, c01 bigint, c02 bigint, c03 bigint, c04 bigint, c05 bigint, c06 bigint, c07 bigint, c08 bigint, c09 bigint, c10 bigint)"); eror != nil {
		t.Fatalf("create: %v", eror)
	}
	t.Cleanup(func() { damengDrop(t, eng, dmWideTable) })
	tbl := eng.RegisterClass(dmWideRow{}, dmWideTable).SetKeys("id")
	if tbl.LastError != nil {
		t.Fatalf("register: %v", tbl.LastError)
	}

	rows := make([]dmWideRow, 500)
	for i := range rows {
		rows[i] = dmWideRow{Id: int64(i + 1), C01: 1, C02: 2, C03: 3, C04: 4, C05: 5, C06: 6, C07: 7, C08: 8, C09: 9, C10: 10}
	}
	if eror := eng.InsertP(&rows, 999, dmWideTable); eror != nil {
		t.Fatalf("InsertP: %v", eror)
	}
	n, eror := eng.SelectD("select count(1) from " + dmWideTable)
	if eror != nil || n != 500 {
		t.Fatalf("count=(%d,%v), want 500", n, eror)
	}
}

func TestDamengTransaction(t *testing.T) {
	db := openDameng(t)
	eng := damengEngineOn(t, db)
	setupDamengProbe(t, eng)

	if eror := eng.Begin(); eror != nil {
		t.Fatalf("Begin: %v", eror)
	}
	if eror := eng.Insert(&dmProbeRow{Id: 500, Name: "ROLLBACK"}); eror != nil {
		t.Fatalf("Insert tx: %v", eror)
	}
	if eror := eng.Cancel(); eror != nil {
		t.Fatalf("Cancel: %v", eror)
	}
	if n, eror := eng.SelectD("select count(1) from " + dmProbeTable + " where id=500"); eror != nil || n != 0 {
		t.Fatalf("rollback count=(%d,%v), want 0", n, eror)
	}

	if eror := eng.Begin(); eror != nil {
		t.Fatalf("Begin 2: %v", eror)
	}
	if eror := eng.Insert(&dmProbeRow{Id: 501, Name: "COMMIT"}); eror != nil {
		t.Fatalf("Insert 2: %v", eror)
	}
	if eror := eng.Commit(); eror != nil {
		t.Fatalf("Commit: %v", eror)
	}
	if n, eror := eng.SelectD("select count(1) from " + dmProbeTable + " where id=501"); eror != nil || n != 1 {
		t.Fatalf("commit count=(%d,%v), want 1", n, eror)
	}
}
