// tidb_test
// #TiDB 真机测试——MySQL 协议兼容验证:CRUD/UPSERT 回退/批量/加密/元数据/事务
// #
// #TiDB 监听 4000,驱动与 MySQL 相同(go-sql-driver/mysql),引擎使用 DtMYSQLN。
// #
// #运行:
// #  UNIENGINE_TIDB_HOST=127.0.0.1 UNIENGINE_TIDB_PORT=4000 \
// #  UNIENGINE_TIDB_USER=root UNIENGINE_TIDB_PASS='...' \
// #  UNIENGINE_TIDB_DB=UNIENGINE_TESTDB go test ./integration/ -run TiDB -v
// #  或整串:
// #  UNIENGINE_TIDB_DSN='root:pass@tcp(127.0.0.1:4000)/UNIENGINE_TESTDB?charset=utf8mb4' \
// #  go test ./integration/ -run TiDB -v
// #
// #不可达时跳过;UNIENGINE_TIDB_REQUIRED=1 时失败。只操作自建 uni_it_tidb_* 表并清理。
package integration

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	UniEngine "github.com/kazarus/UniEngine"
)

const (
	tidbProbeTable = "uni_it_tidb_probe"
	tidbPageTable  = "uni_it_tidb_page"
)

func tidbEnv() (dsn, database string) {
	database = os.Getenv("UNIENGINE_TIDB_DB")
	if database == "" {
		database = os.Getenv("UNIENGINE_MYSQL_DB")
	}

	if v := os.Getenv("UNIENGINE_TIDB_DSN"); v != "" {
		return v, database
	}

	host := os.Getenv("UNIENGINE_TIDB_HOST")
	if host == "" {
		return "", ""
	}
	port := os.Getenv("UNIENGINE_TIDB_PORT")
	if port == "" {
		port = "4000"
	}
	return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&timeout=15s",
		os.Getenv("UNIENGINE_TIDB_USER"),
		os.Getenv("UNIENGINE_TIDB_PASS"),
		host, port, database), database
}

func openTiDB(t *testing.T) *sql.DB {
	t.Helper()

	dsn, _ := tidbEnv()
	if dsn == "" {
		t.Skip("未设置 UNIENGINE_TIDB_DSN / UNIENGINE_TIDB_HOST,跳过 TiDB 集成测试")
	}

	db, eror := sql.Open("mysql", dsn)
	if eror != nil {
		t.Fatalf("open tidb: %v", eror)
	}
	if eror = db.Ping(); eror != nil {
		if os.Getenv("UNIENGINE_TIDB_REQUIRED") == "1" {
			t.Fatalf("TiDB 不可达(UNIENGINE_TIDB_REQUIRED=1): %v", eror)
		}
		db.Close()
		t.Skipf("TiDB 不可达,跳过集成测试: %v", eror)
	}
	t.Cleanup(func() { db.Close() })

	return db
}

func tidbEngineOn(t *testing.T, db *sql.DB) *UniEngine.TUniEngine {
	t.Helper()
	_, database := tidbEnv()
	eng := &UniEngine.TUniEngine{
		Db:       db,
		ColLabel: "db",
		ColParam: "?",
		Provider: UniEngine.DtMYSQLN,
		DataBase: database,
	}
	eng.Initialize()
	return eng
}

type tidbProbeRow struct {
	Id     int64  `db:"id"`
	Name   string `db:"name"`
	Secret string `db:"secret,secreton"`
}

func setupTiDBProbe(t *testing.T, eng *UniEngine.TUniEngine) {
	t.Helper()

	drop := "drop table if exists " + tidbProbeTable
	if eror := eng.Execute(drop); eror != nil {
		t.Fatalf("drop: %v", eror)
	}
	create := "create table " + tidbProbeTable + "(id bigint primary key, name varchar(64), secret varchar(1024))"
	if eror := eng.Execute(create); eror != nil {
		t.Fatalf("create: %v", eror)
	}
	t.Cleanup(func() {
		if eror := eng.Execute(drop); eror != nil {
			t.Logf("cleanup: drop %s failed: %v", tidbProbeTable, eror)
		}
	})

	tbl := eng.RegisterClass(tidbProbeRow{}, tidbProbeTable).SetKeys("id")
	if tbl.LastError != nil {
		t.Fatalf("register: %v", tbl.LastError)
	}
}

func TestTiDBRoundTrip(t *testing.T) {
	db := openTiDB(t)
	eng := tidbEngineOn(t, db)
	setupTiDBProbe(t, eng)

	if eror := eng.Insert(&tidbProbeRow{Id: 100, Name: "ALPHA", Secret: "s1"}); eror != nil {
		t.Fatalf("Insert: %v", eror)
	}
	var got tidbProbeRow
	if eror := eng.Select(&got, "select id,name,secret from "+tidbProbeTable+" where id=?", int64(100)); eror != nil {
		t.Fatalf("Select: %v", eror)
	}
	if got.Name != "ALPHA" || got.Secret != "s1" {
		t.Fatalf("Select got %+v", got)
	}

	got.Name = "ALPHA-2"
	if eror := eng.Update(&got); eror != nil {
		t.Fatalf("Update: %v", eror)
	}

	//#MySQL 族 SaveIt 走 count-then-dispatch(不是原生 UPSERT)
	if eror := eng.SaveIt(&tidbProbeRow{Id: 200, Name: "BETA", Secret: "s2"}); eror != nil {
		t.Fatalf("SaveIt insert: %v", eror)
	}
	if eror := eng.SaveIt(&tidbProbeRow{Id: 200, Name: "BETA-2", Secret: "s2"}); eror != nil {
		t.Fatalf("SaveIt update: %v", eror)
	}
	if eror := eng.SaveItWhenNotExist(&tidbProbeRow{Id: 200, Name: "SHOULD-NOT-CHANGE", Secret: "s2"}); eror != nil {
		t.Fatalf("SaveItWhenNotExist: %v", eror)
	}
	var beta tidbProbeRow
	if eror := eng.Select(&beta, "select id,name,secret from "+tidbProbeTable+" where id=?", int64(200)); eror != nil {
		t.Fatalf("Select beta: %v", eror)
	}
	if beta.Name != "BETA-2" {
		t.Fatalf("SaveItWhenNotExist overwrote existing row: %+v", beta)
	}

	if eror := eng.InsertL(&[]tidbProbeRow{{Id: 300, Name: "GAMMA"}, {Id: 301, Name: "DELTA"}}); eror != nil {
		t.Fatalf("InsertL: %v", eror)
	}
	if eror := eng.Delete(&tidbProbeRow{Id: 100}); eror != nil {
		t.Fatalf("Delete: %v", eror)
	}

	n, eror := eng.SelectD("select count(1) from " + tidbProbeTable)
	if eror != nil || n != 3 {
		t.Fatalf("count=(%d,%v), want 3", n, eror)
	}

	//#元数据:Exist*/AutoKeys 走 information_schema
	if ok, eror := eng.ExistTable(tidbProbeTable); eror != nil || !ok {
		t.Fatalf("ExistTable=(%v,%v)", ok, eror)
	}
	if ok, eror := eng.ExistField(tidbProbeTable, "name"); eror != nil || !ok {
		t.Fatalf("ExistField=(%v,%v)", ok, eror)
	}
	tbl := eng.GetTable(tidbProbeTable)
	if err := tbl.AutoKeys(eng, UniEngine.TAutoKeys4MYSQLN{DataBase: eng.DataBase}); err != nil {
		t.Fatalf("AutoKeys: %v", err)
	}
	if _, ok := eng.GetTable(tidbProbeTable).HashPkeys["id"]; !ok {
		t.Fatal("AutoKeys should register primary key id")
	}
}

func TestTiDBSecretRoundTrip(t *testing.T) {
	db := openTiDB(t)
	eng := tidbEngineOn(t, db)
	eng.SecretOn = 1
	eng.SecretBy = "tidb-integration-key"
	eng.SecretIter = 2048
	setupTiDBProbe(t, eng)

	if eror := eng.Insert(&tidbProbeRow{Id: 1000, Name: "SEC", Secret: "s3cret中文"}); eror != nil {
		t.Fatalf("Insert: %v", eror)
	}
	var raw string
	if eror := db.QueryRow("select secret from "+tidbProbeTable+" where id=?", int64(1000)).Scan(&raw); eror != nil {
		t.Fatalf("raw select: %v", eror)
	}
	if !strings.HasPrefix(raw, UniEngine.UniSecretTag) || raw == "s3cret中文" {
		t.Fatalf("stored secret should be ENC cipher, got %.40s", raw)
	}

	var got tidbProbeRow
	if eror := eng.Select(&got, "select id,name,secret from "+tidbProbeTable+" where id=?", int64(1000)); eror != nil {
		t.Fatalf("Select: %v", eror)
	}
	if got.Secret != "s3cret中文" {
		t.Fatalf("decrypt failed: %q", got.Secret)
	}
	if v, eror := eng.SelectS("select secret from "+tidbProbeTable+" where id=?", int64(1000)); eror != nil || v != "s3cret中文" {
		t.Fatalf("SelectS decrypt=(%q,%v)", v, eror)
	}
}

type tidbPageRow struct {
	Id   int64  `db:"id"`
	Name string `db:"name"`
}

func TestTiDBInsertPPageSizeClamp(t *testing.T) {
	db := openTiDB(t)
	eng := tidbEngineOn(t, db)

	drop := "drop table if exists " + tidbPageTable
	if eror := eng.Execute(drop); eror != nil {
		t.Fatalf("drop: %v", eror)
	}
	create := "create table " + tidbPageTable + "(id bigint primary key, name varchar(64))"
	if eror := eng.Execute(create); eror != nil {
		t.Fatalf("create: %v", eror)
	}
	t.Cleanup(func() {
		if eror := eng.Execute(drop); eror != nil {
			t.Logf("cleanup: drop %s failed: %v", tidbPageTable, eror)
		}
	})

	tbl := eng.RegisterClass(tidbPageRow{}, tidbPageTable).SetKeys("id")
	if tbl.LastError != nil {
		t.Fatalf("register: %v", tbl.LastError)
	}

	rows := make([]tidbPageRow, 2000)
	for i := range rows {
		rows[i] = tidbPageRow{Id: int64(i + 1), Name: "n"}
	}
	//#MySQL 族按 60000/2 计算后仍被 999 行上限收敛;负值也应走同一归一化。
	if eror := eng.InsertP(&rows, -5, tidbPageTable); eror != nil {
		t.Fatalf("InsertP: %v", eror)
	}
	n, eror := eng.SelectD("select count(1) from " + tidbPageTable)
	if eror != nil || n != 2000 {
		t.Fatalf("count=(%d,%v), want 2000", n, eror)
	}
}

func TestTiDBTransaction(t *testing.T) {
	db := openTiDB(t)
	eng := tidbEngineOn(t, db)
	setupTiDBProbe(t, eng)

	if eror := eng.Begin(); eror != nil {
		t.Fatalf("Begin: %v", eror)
	}
	if eror := eng.Insert(&tidbProbeRow{Id: 500, Name: "ROLLBACK"}); eror != nil {
		t.Fatalf("Insert in tx: %v", eror)
	}
	if eror := eng.Cancel(); eror != nil {
		t.Fatalf("Cancel: %v", eror)
	}
	if n, eror := eng.SelectD("select count(1) from " + tidbProbeTable + " where id=500"); eror != nil || n != 0 {
		t.Fatalf("rollback count=(%d,%v), want 0", n, eror)
	}

	if eror := eng.Begin(); eror != nil {
		t.Fatalf("Begin 2: %v", eror)
	}
	if eror := eng.Insert(&tidbProbeRow{Id: 501, Name: "COMMIT"}); eror != nil {
		t.Fatalf("Insert 2: %v", eror)
	}
	if eror := eng.Commit(); eror != nil {
		t.Fatalf("Commit: %v", eror)
	}
	if n, eror := eng.SelectD("select count(1) from " + tidbProbeTable + " where id=501"); eror != nil || n != 1 {
		t.Fatalf("commit count=(%d,%v), want 1", n, eror)
	}
}
