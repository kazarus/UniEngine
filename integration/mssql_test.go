// mssql_test
// #SQL Server 真驱动集成测试(go-mssqldb)
// #
// #核心事实(本文件用真库固化):SQL Server 的占位符正确性取决于 **驱动名 × ColParam** 两维:
// #
// #  | 驱动名      | ":" | "$" | "?" | "@p" |
// #  | ----------- | --- | --- | --- | ---- |
// #  | "mssql"     | ✅  | ✅  | ✅  | ❌   |  已废弃但仍注册;预处理 ? / :N / $N → @pN
// #  | "sqlserver" | ❌  | ⚠️  | ❌  | ✅   |  现代名;不预处理,只认 @pN
// #
// #   - "mssql"     :内部走 internal/querytext.ParseParams,把 ?、$N、:N(及 $name/:name)统一改写为 @pN,
// #                  故三种风格都能真绑定;而 @pN 不被识别 → "expected 0 arguments"。
// #   - "sqlserver" :不预处理。":" 与 "?" 直接被服务端判语法错误;而 "$N" 是**合法 money 字面量**,
// #                  不报错但实参被忽略 —— 即"引擎返回 nil、数据却写错"(见缺陷用例)。
// #   - 现代写法等价于 "mssql" 语义:sql.OpenDB(mssql.NewConnectorWithProcessQueryText(cfg))。
// #
// #运行(需可达的 SQL Server):
// #  UNIENGINE_MSSQL_DSN="sqlserver://sa:pass@127.0.0.1:1433?database=uni&encrypt=disable" \
// #    go test ./... -run MSSQL -v
// #  或分量方式(口令含 @ # $ % 等字符时推荐,本文件会做 URL 转义):
// #  UNIENGINE_MSSQL_HOST=127.0.0.1 UNIENGINE_MSSQL_PORT=1433 \
// #  UNIENGINE_MSSQL_USER=sa UNIENGINE_MSSQL_PASS='...' \
// #  UNIENGINE_MSSQL_DB=UNIENGINE_TESTDB go test ./... -run MSSQL -v
// #
// #不可达时默认跳过;设 UNIENGINE_MSSQL_REQUIRED=1 则改为失败(CI 用,与 pg_test.go 同口径)。
// #
// #错误组合在 Initialize 时通过只读参数回显被拒绝;集成用例作为常规门禁。
// #
// #安全:读写用例生成独立探测表名,并在 t.Cleanup 中删除。

package integration

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"testing"

	UniEngine "github.com/kazarus/UniEngine"
	_ "github.com/microsoft/go-mssqldb" //#database/sql 驱动注册:mssql(预处理)+ sqlserver(不预处理)
)

// ---------------------------------------------------------------------------
// 驱动名与夹具
// ---------------------------------------------------------------------------

const (
	//#现代驱动名:不预处理占位符,只接受 @pN
	driverSQLServer = "sqlserver"
	//#已废弃但仍注册的驱动名:预处理 ? / :N / $N → @pN(生产环境所用)
	driverMSSQL = "mssql"
)

// newMSSQLProbeTable 每个子测试使用独立表名,避免误删数据库里已有的同名对象。
func newMSSQLProbeTable(t *testing.T) string {
	t.Helper()
	var suffix [8]byte
	if _, eror := rand.Read(suffix[:]); eror != nil {
		t.Fatal(eror)
	}
	return "uni_it_mssql_probe_" + hex.EncodeToString(suffix[:])
}

// mssqlDSN 组装连接串:优先整串 env,其次分量 env;两者都缺返回空(调用方据此跳过)
func mssqlDSN() string {
	if v := os.Getenv("UNIENGINE_MSSQL_DSN"); v != "" {
		return v
	}

	host := os.Getenv("UNIENGINE_MSSQL_HOST")
	if host == "" {
		return ""
	}

	port := os.Getenv("UNIENGINE_MSSQL_PORT")
	if port == "" {
		port = "1433"
	}

	//#用 url.URL 组装,自动处理口令中的 @ # $ % 等特殊字符
	u := &url.URL{
		Scheme: "sqlserver",
		User:   url.UserPassword(os.Getenv("UNIENGINE_MSSQL_USER"), os.Getenv("UNIENGINE_MSSQL_PASS")),
		Host:   net.JoinHostPort(host, port),
	}

	q := u.Query()
	q.Set("database", os.Getenv("UNIENGINE_MSSQL_DB"))
	q.Set("encrypt", "disable") //#内网实例常见无证书;需加密时用 DSN 显式覆盖
	q.Set("TrustServerCertificate", "true")
	u.RawQuery = q.Encode()

	return u.String()
}

// openMSSQL 按驱动名建立连接;不可达时跳过(REQUIRED=1 时失败)。
// 连接生命周期由本函数用 t.Cleanup 接管:统一登记在**最早**位置,故按 LIFO
// 晚于各用例后续登记的 t.Cleanup(如建表后的 DROP)执行——DROP 一定跑在连接关闭之前。
// 若改用 defer,defer 会先于所有 t.Cleanup 执行,建表清理将落在已关闭的连接上而静默失败(表泄漏)。
func openMSSQL(t *testing.T, driver string) *sql.DB {
	t.Helper()

	dsn := mssqlDSN()
	if dsn == "" {
		t.Skip("未设置 UNIENGINE_MSSQL_DSN / UNIENGINE_MSSQL_HOST,跳过 SQL Server 集成测试")
	}

	db, eror := sql.Open(driver, dsn)
	if eror != nil {
		t.Fatalf("open %s: %v", driver, eror)
	}

	if eror = db.Ping(); eror != nil {
		if os.Getenv("UNIENGINE_MSSQL_REQUIRED") == "1" {
			t.Fatalf("SQL Server 不可达(UNIENGINE_MSSQL_REQUIRED=1): %v", eror)
		}
		db.Close()
		t.Skipf("SQL Server 不可达,跳过集成测试: %v", eror)
	}

	t.Cleanup(func() { db.Close() })

	return db
}

// mssqlEngineOn 在既有连接上建引擎(同一驱动名下可能需要"正确/错误"两个引擎)
func mssqlEngineOn(t *testing.T, db *sql.DB, colParam string) *UniEngine.TUniEngine {
	t.Helper()
	eng := &UniEngine.TUniEngine{Db: db, ColLabel: "db", ColParam: colParam, Provider: UniEngine.DtSQLSRV}
	if eror := eng.Initialize(); eror != nil {
		t.Fatalf("Initialize(%q): %v", colParam, eror)
	}

	return eng
}

// nativeParam 返回该驱动名下"一定能真绑定"的占位符风格——读回等原生语句用它,
// #避免断言语句自身因驱动名不匹配而失败(曾因此误判引擎写失败)
func nativeParam(driver string) string {
	if driver == driverMSSQL {
		return "?"
	}

	return "@p1"
}

type mssqlProbeRow struct {
	Id   int64  `db:"id"`
	Name string `db:"name"`
}

// setupMSSQLProbeTable 建立独立探测表并登记主键;Cleanup 时删除
func setupMSSQLProbeTable(t *testing.T, eng *UniEngine.TUniEngine, tableName string) {
	t.Helper()

	drop := fmt.Sprintf("if object_id('%s','U') is not null drop table %s", tableName, tableName)
	create := fmt.Sprintf("create table %s (id bigint not null primary key, name nvarchar(64) null)", tableName)
	if eror := eng.Execute(create); eror != nil {
		t.Fatalf("create %s: %v", tableName, eror)
	}
	t.Cleanup(func() {
		if eror := eng.Execute(drop); eror != nil {
			t.Errorf("cleanup: drop %s 失败: %v", tableName, eror)
		}
	})

	if eror := eng.RegisterClass(mssqlProbeRow{}, tableName).SetKeys("id").LastError; eror != nil {
		t.Fatalf("SetKeys: %v", eror)
	}
}

// rawProbeName 绕过引擎读库中真实存储值;不存在返回 ok=false
func rawProbeName(t *testing.T, eng *UniEngine.TUniEngine, driver, tableName string, id int64) (string, bool) {
	t.Helper()

	var name sql.NullString
	sqlText := fmt.Sprintf("select name from %s where id=%s", tableName, nativeParam(driver))
	eror := eng.Db.QueryRow(sqlText, id).Scan(&name)
	if eror == sql.ErrNoRows {
		return "", false
	}
	if eror != nil {
		t.Fatalf("raw select id=%d (driver=%s): %v", id, driver, eror)
	}

	return name.String, true
}

// ---------------------------------------------------------------------------
// 1. 占位符契约:驱动名 × 风格(说明"为什么 @p 不是万能的")
// ---------------------------------------------------------------------------

// TestMSSQLPlaceholderContract 在真库上固化"驱动名 × 占位符风格"的绑定矩阵。
// 用例与库是否修复无关,它记录的是 SQL Server + go-mssqldb 的外部事实。
func TestMSSQLPlaceholderContract(t *testing.T) {
	const probeValue = int64(7)

	cases := []struct {
		driver string
		sql    string
		binds  bool //#是否应真绑定(bind=false 时要求"结果不是实参值")
	}{
		//#"sqlserver":只认 @pN
		{driverSQLServer, "select @p1", true},
		{driverSQLServer, "select :1", false},
		{driverSQLServer, "select ?", false},
		{driverSQLServer, "select $1", false}, //#money 字面量:不报错但不绑定
		//#"mssql":预处理 ? / :N / $N → @pN
		{driverMSSQL, "select :1", true},
		{driverMSSQL, "select $1", true},
		{driverMSSQL, "select ?", true},
		{driverMSSQL, "select @p1", false}, //#不被预处理识别 → expected 0 arguments
	}

	for _, c := range cases {
		t.Run(c.driver+"_"+c.sql, func(t *testing.T) {
			db := openMSSQL(t, c.driver)

			var got sql.NullString
			eror := db.QueryRow(c.sql, probeValue).Scan(&got)

			if c.binds {
				if eror != nil {
					t.Fatalf("%s 下 %s 应真绑定,却失败: %v", c.driver, c.sql, eror)
				}
				if got.String != "7" {
					t.Fatalf("%s 下 %s 绑定值 = %q, want \"7\"", c.driver, c.sql, got.String)
				}
				return
			}

			//#不绑定:可能是报错,也可能是被当成字面量(如 money)
			if eror == nil && got.String == "7" {
				t.Errorf("%s 下 %s 竟然真绑定了实参;若驱动行为变化,本用例与文档结论需重新评估", c.driver, c.sql)
			}
			if eror == nil {
				t.Logf("%s 下 %s 未报错但实参被忽略,返回字面量 %q", c.driver, c.sql, got.String)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 2. 引擎端到端:覆盖所有"驱动名 × ColParam"可用组合
// ---------------------------------------------------------------------------

// TestMSSQLRoundTripMatrix 对每个可用组合跑完整 CRUD,断言数据与意图一致。
// 关键:生产环境用的是 "mssql" + ":"(与 "sqlserver" + "@p" 等价可用)。
func TestMSSQLRoundTripMatrix(t *testing.T) {
	combos := []struct {
		driver   string
		colParam string
	}{
		{driverMSSQL, ":"},      //#生产环境配置
		{driverMSSQL, "$"},      //#原 README 配置(在 mssql 驱动名下同样可用)
		{driverMSSQL, "?"},      //#sqlx 风格
		{driverSQLServer, "@p"}, //#现代驱动名下的唯一可用配置
	}

	for _, combo := range combos {
		t.Run(combo.driver+"_ColParam"+combo.colParam, func(t *testing.T) {
			db := openMSSQL(t, combo.driver)

			eng := mssqlEngineOn(t, db, combo.colParam)
			tableName := newMSSQLProbeTable(t)
			setupMSSQLProbeTable(t, eng, tableName)

			//#Insert
			if eror := eng.Insert(&mssqlProbeRow{Id: 100, Name: "ALPHA"}, tableName); eror != nil {
				t.Fatalf("Insert: %v", eror)
			}
			if name, ok := rawProbeName(t, eng, combo.driver, tableName, 100); !ok || name != "ALPHA" {
				t.Fatalf("Insert 后 id=100 -> (%q, ok=%v), want (\"ALPHA\", true)", name, ok)
			}

			//#Update:只改目标行
			if eror := eng.Update(&mssqlProbeRow{Id: 100, Name: "ALPHA-2"}, tableName); eror != nil {
				t.Fatalf("Update: %v", eror)
			}
			if name, _ := rawProbeName(t, eng, combo.driver, tableName, 100); name != "ALPHA-2" {
				t.Fatalf("Update 后 id=100 -> %q, want \"ALPHA-2\"", name)
			}

			//#SaveIt:not matched(插入)+ matched(更新)
			if eror := eng.SaveIt(&mssqlProbeRow{Id: 200, Name: "BETA"}, tableName); eror != nil {
				t.Fatalf("SaveIt(insert): %v", eror)
			}
			if name, ok := rawProbeName(t, eng, combo.driver, tableName, 200); !ok || name != "BETA" {
				t.Fatalf("SaveIt 插入后 id=200 -> (%q, ok=%v), want (\"BETA\", true)", name, ok)
			}
			if eror := eng.SaveIt(&mssqlProbeRow{Id: 200, Name: "BETA-2"}, tableName); eror != nil {
				t.Fatalf("SaveIt(update): %v", eror)
			}
			if name, _ := rawProbeName(t, eng, combo.driver, tableName, 200); name != "BETA-2" {
				t.Fatalf("SaveIt 更新后 id=200 -> %q, want \"BETA-2\"", name)
			}

			//#InsertL:多行占位符序号必须连续正确
			rows := []mssqlProbeRow{{Id: 300, Name: "GAMMA"}, {Id: 301, Name: "DELTA"}}
			if eror := eng.InsertL(&rows, tableName); eror != nil {
				t.Fatalf("InsertL: %v", eror)
			}
			for _, want := range rows {
				if name, ok := rawProbeName(t, eng, combo.driver, tableName, want.Id); !ok || name != want.Name {
					t.Fatalf("InsertL 后 id=%d -> (%q, ok=%v), want (%q, true)", want.Id, name, ok, want.Name)
				}
			}

			//#SelectL:经引擎读回
			var loaded []mssqlProbeRow
			if eror := eng.SelectL(&loaded, fmt.Sprintf("select id,name from %s order by id", tableName)); eror != nil {
				t.Fatalf("SelectL: %v", eror)
			}
			if len(loaded) != 4 {
				t.Fatalf("SelectL 读回 %d 行, want 4: %+v", len(loaded), loaded)
			}

			//#Delete:只删目标行
			if eror := eng.Delete(&mssqlProbeRow{Id: 100}, tableName); eror != nil {
				t.Fatalf("Delete: %v", eror)
			}
			if _, ok := rawProbeName(t, eng, combo.driver, tableName, 100); ok {
				t.Fatalf("Delete 后 id=100 仍存在")
			}
			if _, ok := rawProbeName(t, eng, combo.driver, tableName, 200); !ok {
				t.Fatalf("Delete 误删了非目标行 id=200")
			}
		})
	}
}

// TestMSSQLInitializeRejectsMismatchedBinding 确认错误组合在初始化阶段失败,
// 因此业务 SQL 不会遇到 sqlserver + $N 被当作 money 字面量的静默写错。
func TestMSSQLInitializeRejectsMismatchedBinding(t *testing.T) {
	combos := []struct {
		driver   string
		colParam string
	}{
		{driverSQLServer, "$"},
		{driverSQLServer, ":"},
		{driverSQLServer, "?"},
		{driverMSSQL, "@p"},
	}
	for _, combo := range combos {
		t.Run(combo.driver+"_ColParam"+combo.colParam, func(t *testing.T) {
			db := openMSSQL(t, combo.driver)
			eng := &UniEngine.TUniEngine{Db: db, ColLabel: "db", ColParam: combo.colParam, Provider: UniEngine.DtSQLSRV}
			if eror := eng.Initialize(); !errors.Is(eror, UniEngine.ErrInvalidPlaceholderBinding) {
				t.Fatalf("Initialize should reject mismatched binding, got %v", eror)
			}
			if len(eng.HashTabl) != 0 {
				t.Fatal("failed Initialize must not publish registry entries")
			}
		})
	}
}
