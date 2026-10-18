// mysql_test
// #MySQL 真驱动集成测试——验证 MySQL 协议族的占位符契约、CRUD、元数据探测与加密往返
// #
// #驱动:github.com/go-sql-driver/mysql(库本体不依赖,仅 integration 模块引入)
// #
// #运行(需可达的 MySQL;连不上时自动跳过):
// #  UNIENGINE_MYSQL_DSN="user:pass@tcp(127.0.0.1:3306)/demo?charset=utf8mb4" go test ./... -run MySQL -v
// #  或分量方式:
// #  UNIENGINE_MYSQL_HOST=127.0.0.1 UNIENGINE_MYSQL_PORT=3306 \
// #  UNIENGINE_MYSQL_USER=demo UNIENGINE_MYSQL_PASS='...' \
// #  UNIENGINE_MYSQL_DB=demo go test ./... -run MySQL -v
// #
// #不可达时默认跳过;设 UNIENGINE_MYSQL_REQUIRED=1 则失败(CI 用,与 pg/mssql/oracle 同口径)。
// #
// #注意:MySQL 无法从 DSN 解析出库名时,Exist*/AutoKeys 需要引擎的 DataBase 字段有值,
// #本文件在分量模式下自动从 UNIENGINE_MYSQL_DB 填充。
// #
// #安全:只操作自建表 uni_it_mysql_probe,结束时 DROP。
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

const mysqlProbeTable = "uni_it_mysql_probe"

// mysqlEnv 返回 (dsn, database);database 供引擎的 DataBase 字段使用
func mysqlEnv() (string, string) {
	database := os.Getenv("UNIENGINE_MYSQL_DB")

	if v := os.Getenv("UNIENGINE_MYSQL_DSN"); v != "" {
		return v, database
	}

	host := os.Getenv("UNIENGINE_MYSQL_HOST")
	if host == "" {
		return "", ""
	}

	port := os.Getenv("UNIENGINE_MYSQL_PORT")
	if port == "" {
		port = "3306"
	}

	//#timeout 覆盖"DNS 解析 + TCP 连接":公网域名解析偶发偏慢,给足 15s
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&timeout=15s",
		os.Getenv("UNIENGINE_MYSQL_USER"), os.Getenv("UNIENGINE_MYSQL_PASS"), host, port, database)

	return dsn, database
}

// openMySQL 建立连接;不可达时跳过(REQUIRED=1 时失败)。连接由 t.Cleanup 关闭。
func openMySQL(t *testing.T) *sql.DB {
	t.Helper()

	dsn, _ := mysqlEnv()
	if dsn == "" {
		t.Skip("未设置 UNIENGINE_MYSQL_DSN / UNIENGINE_MYSQL_HOST,跳过 MySQL 集成测试")
	}

	db, eror := sql.Open("mysql", dsn)
	if eror != nil {
		t.Fatalf("open mysql: %v", eror)
	}

	if eror = db.Ping(); eror != nil {
		if os.Getenv("UNIENGINE_MYSQL_REQUIRED") == "1" {
			t.Fatalf("MySQL 不可达(UNIENGINE_MYSQL_REQUIRED=1): %v", eror)
		}
		db.Close()
		t.Skipf("MySQL 不可达,跳过集成测试: %v", eror)
	}

	t.Cleanup(func() { db.Close() })

	return db
}

// mysqlEngineOn 在既有连接上建引擎;DataBase 用于 information_schema 探测
func mysqlEngineOn(db *sql.DB, colParam string) *UniEngine.TUniEngine {
	_, database := mysqlEnv()

	eng := &UniEngine.TUniEngine{
		Db:       db,
		ColLabel: "db",
		ColParam: colParam,
		Provider: UniEngine.DtMYSQLN,
		DataBase: database,
	}
	eng.Initialize()

	return eng
}

type mysqlProbeRow struct {
	Id     int64  `db:"id"`
	Name   string `db:"name"`
	Secret string `db:"secret,secreton"`
}

// setupMySQLProbe 重建自建表并登记主键;Cleanup 时 DROP
func setupMySQLProbe(t *testing.T, eng *UniEngine.TUniEngine) {
	t.Helper()

	drop := fmt.Sprintf("drop table if exists %s", mysqlProbeTable)
	if eror := eng.Execute(drop); eror != nil {
		t.Fatalf("drop: %v", eror)
	}
	t.Cleanup(func() {
		if eror := eng.Execute(drop); eror != nil {
			t.Logf("cleanup: drop %s 失败: %v", mysqlProbeTable, eror)
		}
	})

	ddl := fmt.Sprintf(`create table %s (
		id     bigint       not null,
		name   varchar(64),
		secret varchar(1024),
		primary key (id)
	) engine=InnoDB default charset=utf8mb4`, mysqlProbeTable)
	if eror := eng.Execute(ddl); eror != nil {
		t.Fatalf("create table: %v", eror)
	}

	if eror := eng.RegisterClass(mysqlProbeRow{}, mysqlProbeTable).SetKeys("id").LastError; eror != nil {
		t.Fatalf("SetKeys: %v", eror)
	}
}

// rawMySQLName 绕过引擎读库中真实存储值;不存在返回 ok=false
func rawMySQLName(t *testing.T, eng *UniEngine.TUniEngine, id int64) (string, bool) {
	t.Helper()

	var name sql.NullString
	eror := eng.Db.QueryRow(fmt.Sprintf("select name from %s where id=?", mysqlProbeTable), id).Scan(&name)
	if eror == sql.ErrNoRows {
		return "", false
	}
	if eror != nil {
		t.Fatalf("raw select id=%d: %v", id, eror)
	}

	return name.String, true
}

// ---------------------------------------------------------------------------
// 1. 占位符契约:MySQL 只认 "?";错误风格必须"不绑定"(报错或落到别的语义)
// ---------------------------------------------------------------------------

func TestMySQLPlaceholderContract(t *testing.T) {
	db := openMySQL(t)

	const probeValue = int64(7)

	//#?:MySQL 的位置占位符,必须真绑定
	var got sql.NullString
	if eror := db.QueryRow("select ?", probeValue).Scan(&got); eror != nil {
		t.Fatalf("? 应可绑定实参,却失败: %v", eror)
	}
	if got.String != "7" {
		t.Fatalf("? 绑定值 = %q, want \"7\"", got.String)
	}

	//#错误风格:一律"不绑定"。
	//#注意 "@p1" 在 MySQL 里是**用户变量**,句法合法(值为 NULL)——故不能断言"必然报错",
	//#只能断言"绝不等于传入实参";这与 SQL Server 的 $N(=money 字面量)同类,值得固化。
	for _, style := range []string{"select :1", "select $1", "select @p1"} {
		var v sql.NullString
		eror := db.QueryRow(style, probeValue).Scan(&v)
		if eror == nil && v.String == "7" {
			t.Errorf("%s 竟然真绑定了实参;若驱动/服务端行为变化,本用例与 README 结论需重新评估", style)
			continue
		}
		if eror != nil {
			t.Logf("%s -> 报错: %v", style, firstLineOf(eror.Error()))
		} else {
			t.Logf("%s -> 未报错但实参被忽略,返回 %q(MySQL 用户变量语义)", style, v.String)
		}
	}
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

// ---------------------------------------------------------------------------
// 2. 引擎端到端:ColParam="?"(MySQL 族的唯一取值)
// ---------------------------------------------------------------------------

func TestMySQLRoundTrip(t *testing.T) {
	db := openMySQL(t)
	eng := mysqlEngineOn(db, "?")
	setupMySQLProbe(t, eng)

	//#Insert
	if eror := eng.Insert(&mysqlProbeRow{Id: 100, Name: "ALPHA", Secret: "s1"}, mysqlProbeTable); eror != nil {
		t.Fatalf("Insert: %v", eror)
	}
	if name, ok := rawMySQLName(t, eng, 100); !ok || name != "ALPHA" {
		t.Fatalf("Insert 后 id=100 -> (%q, ok=%v), want (\"ALPHA\", true)", name, ok)
	}

	//#Update(整行语义)
	if eror := eng.Update(&mysqlProbeRow{Id: 100, Name: "ALPHA-2", Secret: "s1"}, mysqlProbeTable); eror != nil {
		t.Fatalf("Update: %v", eror)
	}
	if name, _ := rawMySQLName(t, eng, 100); name != "ALPHA-2" {
		t.Fatalf("Update 后 id=100 -> %q, want \"ALPHA-2\"", name)
	}

	//#SaveIt:MySQL **不**走 on duplicate key(任一唯一键都会触发,与主键语义不等价),
	//#引擎按设计回退 count-then-dispatch——这里验证该回退路径在真库上成立
	if eror := eng.SaveIt(&mysqlProbeRow{Id: 200, Name: "BETA", Secret: "s2"}, mysqlProbeTable); eror != nil {
		t.Fatalf("SaveIt(insert 回退路径): %v", eror)
	}
	if name, ok := rawMySQLName(t, eng, 200); !ok || name != "BETA" {
		t.Fatalf("SaveIt 插入后 id=200 -> (%q, ok=%v), want (\"BETA\", true)", name, ok)
	}
	if eror := eng.SaveIt(&mysqlProbeRow{Id: 200, Name: "BETA-2", Secret: "s2"}, mysqlProbeTable); eror != nil {
		t.Fatalf("SaveIt(update 回退路径): %v", eror)
	}
	if name, _ := rawMySQLName(t, eng, 200); name != "BETA-2" {
		t.Fatalf("SaveIt 更新后 id=200 -> %q, want \"BETA-2\"", name)
	}

	//#SaveItWhenNotExist:已存在时不得覆盖
	if eror := eng.SaveItWhenNotExist(&mysqlProbeRow{Id: 200, Name: "SHOULD-NOT-CHANGE", Secret: "s2"}, mysqlProbeTable); eror != nil {
		t.Fatalf("SaveItWhenNotExist: %v", eror)
	}
	if name, _ := rawMySQLName(t, eng, 200); name != "BETA-2" {
		t.Fatalf("SaveItWhenNotExist 不应覆盖已存在行, got %q", name)
	}

	//#InsertL:多行 VALUES
	rows := []mysqlProbeRow{{Id: 300, Name: "GAMMA", Secret: "s3"}, {Id: 301, Name: "DELTA", Secret: "s4"}}
	if eror := eng.InsertL(&rows, mysqlProbeTable); eror != nil {
		t.Fatalf("InsertL: %v", eror)
	}
	for _, want := range rows {
		if name, ok := rawMySQLName(t, eng, want.Id); !ok || name != want.Name {
			t.Fatalf("InsertL 后 id=%d -> (%q, ok=%v), want (%q, true)", want.Id, name, ok, want.Name)
		}
	}

	//#SelectL:经引擎读回
	var loaded []mysqlProbeRow
	if eror := eng.SelectL(&loaded, fmt.Sprintf("select id,name,secret from %s order by id", mysqlProbeTable)); eror != nil {
		t.Fatalf("SelectL: %v", eror)
	}
	if len(loaded) != 4 {
		t.Fatalf("SelectL 读回 %d 行, want 4: %+v", len(loaded), loaded)
	}

	//#Delete:只删目标行
	if eror := eng.Delete(&mysqlProbeRow{Id: 100}, mysqlProbeTable); eror != nil {
		t.Fatalf("Delete: %v", eror)
	}
	if _, ok := rawMySQLName(t, eng, 100); ok {
		t.Fatalf("Delete 后 id=100 仍存在")
	}
	if _, ok := rawMySQLName(t, eng, 200); !ok {
		t.Fatalf("Delete 误删了非目标行 id=200")
	}
}

// ---------------------------------------------------------------------------
// 3. 元数据探测:Exist* 与 AutoKeys(MySQL 走 information_schema,依赖 DataBase)
// ---------------------------------------------------------------------------

func TestMySQLMetadata(t *testing.T) {
	db := openMySQL(t)
	eng := mysqlEngineOn(db, "?")
	setupMySQLProbe(t, eng)

	if eng.DataBase == "" {
		t.Skip("未设置 UNIENGINE_MYSQL_DB,MySQL 的 information_schema 探测需要库名")
	}

	if ok, eror := eng.ExistTable(mysqlProbeTable); eror != nil || !ok {
		t.Errorf("ExistTable(%s) = (%v, %v), want (true, nil)", mysqlProbeTable, ok, eror)
	}
	if ok, _ := eng.ExistTable("no_such_table_uni"); ok {
		t.Errorf("不存在的表不应被判定存在")
	}

	//#视图:走 information_schema.views(此前版本误用 tables,会把同名表判成视图)
	view := mysqlProbeTable + "_v"
	_ = eng.Execute(fmt.Sprintf("drop view if exists %s", view))
	t.Cleanup(func() { _ = eng.Execute(fmt.Sprintf("drop view if exists %s", view)) })
	if eror := eng.Execute(fmt.Sprintf("create view %s as select id, name from %s", view, mysqlProbeTable)); eror != nil {
		t.Fatalf("create view: %v", eror)
	}
	if ok, eror := eng.ExistViews(view); eror != nil || !ok {
		t.Errorf("ExistViews(%s) = (%v, %v), want (true, nil)", view, ok, eror)
	}
	if ok, _ := eng.ExistViews(mysqlProbeTable); ok {
		t.Errorf("普通表 %s 不应被判为视图", mysqlProbeTable)
	}

	if ok, eror := eng.ExistField(mysqlProbeTable, "secret"); eror != nil || !ok {
		t.Errorf("ExistField(secret) = (%v, %v), want (true, nil)", ok, eror)
	}
	if ok, _ := eng.ExistField(mysqlProbeTable, "no_such_col"); ok {
		t.Errorf("不存在的列不应被判定存在")
	}

	//#约束:主键名由 MySQL 固定为 PRIMARY(引擎按 constraint_name 匹配)
	if ok, eror := eng.ExistConst(UniEngine.CtPK, "PRIMARY"); eror != nil || !ok {
		t.Errorf("ExistConst(CtPK,PRIMARY) = (%v, %v), want (true, nil)", ok, eror)
	}

	//#AutoKeys:MySQL 变体需要库名
	auto := mysqlEngineOn(db, "?")
	tbl := auto.RegisterClass(mysqlProbeRow{}, mysqlProbeTable)
	if eror := tbl.AutoKeys(auto, UniEngine.TAutoKeys4MYSQLN{DataBase: auto.DataBase}); eror != nil {
		t.Fatalf("AutoKeys: %v", eror)
	}
	tbl = auto.GetTable(mysqlProbeTable)
	if _, ok := tbl.HashPkeys["id"]; !ok {
		t.Errorf("AutoKeys 应登记主键 id, got %v", tbl.ListPkeys)
	}
}

// ---------------------------------------------------------------------------
// 4. 应用加密在 MySQL 上的落库与还原
// ---------------------------------------------------------------------------

func TestMySQLSecretRoundTrip(t *testing.T) {
	db := openMySQL(t)
	eng := mysqlEngineOn(db, "?")
	eng.SecretOn = 1
	eng.SecretBy = "mysql-integration-secret"
	eng.SecretIter = 4096 //#测试用低迭代
	setupMySQLProbe(t, eng)

	if eror := eng.Insert(&mysqlProbeRow{Id: 1, Name: "n", Secret: "TOP-SECRET"}, mysqlProbeTable); eror != nil {
		t.Fatalf("Insert(secreton): %v", eror)
	}

	var raw string
	if eror := db.QueryRow(fmt.Sprintf("select secret from %s where id=?", mysqlProbeTable), int64(1)).Scan(&raw); eror != nil {
		t.Fatalf("raw select: %v", eror)
	}
	if !strings.HasPrefix(raw, UniEngine.UniSecretTag) {
		t.Errorf("库中应存 ENC: 密文, got %q", raw)
	}

	var loaded mysqlProbeRow
	if eror := eng.Select(&loaded, fmt.Sprintf("select id,name,secret from %s where id=?", mysqlProbeTable), int64(1)); eror != nil {
		t.Fatalf("Select: %v", eror)
	}
	if loaded.Secret != "TOP-SECRET" {
		t.Errorf("解密后 = %q, want \"TOP-SECRET\"", loaded.Secret)
	}
}

// ---------------------------------------------------------------------------
// 5. 行级绑定(评估 §4.8):保护列含主键时,"整行搬运"必须被检出
// ---------------------------------------------------------------------------

const mysqlVerifyTable = "uni_it_mysql_verify"

type mysqlVerifyRow struct {
	Id      int64  `db:"id"`
	Name    string `db:"name"`
	MacText string `db:"mac_text"`
}

// newMySQLVerifyProbe 建自建表(含 MAC 伴随列)并登记类;返回表对象与引擎
func newMySQLVerifyProbe(t *testing.T, db *sql.DB) (*UniEngine.TUniTable, *UniEngine.TUniEngine) {
	t.Helper()

	eng := mysqlEngineOn(db, "?")
	eng.VerifyOn = 1
	eng.VerifyBy = "mysql-verify-key"
	eng.VerifyMode = 1

	drop := fmt.Sprintf("drop table if exists %s", mysqlVerifyTable)
	if eror := eng.Execute(drop); eror != nil {
		t.Fatalf("drop: %v", eror)
	}
	t.Cleanup(func() { _ = eng.Execute(drop) })

	ddl := fmt.Sprintf(`create table %s (
		id       bigint       not null,
		name     varchar(64),
		mac_text varchar(200) not null default '',
		primary key (id)
	) engine=InnoDB default charset=utf8mb4`, mysqlVerifyTable)
	if eror := eng.Execute(ddl); eror != nil {
		t.Fatalf("create table: %v", eror)
	}

	tbl := eng.RegisterClass(mysqlVerifyRow{}, mysqlVerifyTable).SetKeys("id")
	if tbl.LastError != nil {
		t.Fatalf("SetKeys: %v", tbl.LastError)
	}

	return tbl, eng
}

func TestMySQLVerifyRowBinding(t *testing.T) {
	db := openMySQL(t)

	//#① 登记期强制:保护列不含主键 → 拒绝(否则"行级绑定"只是纸面承诺)
	tblReject, _ := newMySQLVerifyProbe(t, db)
	tblReject = tblReject.SetVerify("mac_text", "name")
	if tblReject.LastError == nil {
		t.Fatal("保护列不含主键应被登记期拒绝")
	}

	//#② 含主键:整行搬运(数据连同 MAC 一起搬)必须被检出
	tbl, eng := newMySQLVerifyProbe(t, db)
	tbl = tbl.SetVerify("mac_text", "id", "name")
	if eror := tbl.LastError; eror != nil {
		t.Fatalf("SetVerify: %v", eror)
	}

	for _, Row := range []mysqlVerifyRow{{Id: 1, Name: "A"}, {Id: 2, Name: "B"}} {
		Item := Row
		if eror := eng.Insert(&Item, mysqlVerifyTable); eror != nil {
			t.Fatalf("Insert id=%d: %v", Item.Id, eror)
		}
	}

	//#把 2 号的 name 与 mac 一起贴到 1 号(id 是行标识,搬不动)
	move := fmt.Sprintf("update %s r1, %s r2 set r1.name=r2.name, r1.mac_text=r2.mac_text where r1.id=1 and r2.id=2",
		mysqlVerifyTable, mysqlVerifyTable)
	if eror := eng.Execute(move); eror != nil {
		t.Fatalf("搬运: %v", eror)
	}

	var got mysqlVerifyRow
	eror := eng.Select(&got, fmt.Sprintf("select id,name,mac_text from %s where id=?", mysqlVerifyTable), int64(1))
	if eror == nil {
		t.Fatalf("保护列含主键时整行搬运应被检出,却通过: %+v", got)
	}
	t.Logf("整行搬运被检出: %v", eror)

	//#③ 篡改被保护列(MAC 不动)同样必须被检出
	_, _ = db.Exec(fmt.Sprintf("update %s set name='TAMPERED' where id=1", mysqlVerifyTable))
	if eror := eng.Select(&got, fmt.Sprintf("select id,name,mac_text from %s where id=?", mysqlVerifyTable), int64(1)); eror == nil {
		t.Fatal("篡改被保护列应被检出,却通过")
	}
}

// TestMySQLPlaceholderRewriteStoredValues 评审 Q5 真库验收:$N 方言转换经词法扫描后,
// 乱序/重复编号按出现序正确绑定、字符串常量与注释原样保留、非法编号在引擎层显式报错;
// 落库值以**绕过引擎的原生 database/sql 读写**核对,不依赖引擎自身读路径。
func TestMySQLPlaceholderRewriteStoredValues(t *testing.T) {
	db := openMySQL(t)
	eng := mysqlEngineOn(db, "?")

	drop := "drop table if exists uni_it_mysql_q5"
	if eror := eng.Execute(drop); eror != nil {
		t.Fatalf("drop: %v", eror)
	}
	t.Cleanup(func() {
		if eror := eng.Execute(drop); eror != nil {
			t.Logf("cleanup: drop 失败: %v", eror)
		}
	})

	if eror := eng.Execute(`create table uni_it_mysql_q5 (
		id     bigint       not null,
		amount bigint       not null default 0,
		note   varchar(100) not null default '',
		primary key (id)
	) engine=InnoDB default charset=utf8mb4`); eror != nil {
		t.Fatalf("create table: %v", eror)
	}

	//#① 乱序+常量+注释:amount=$2 绑 99,note='$1' 保持字面量,where id=$1 匹配 7;
	//#行尾块注释里的 $2 若被误改写,? 数将超过实参数,驱动直接报错
	if _, eror := db.Exec("insert into uni_it_mysql_q5 (id) values (7)"); eror != nil {
		t.Fatal(eror)
	}
	if eror := eng.Execute("update uni_it_mysql_q5 set amount=$2, note='$1' where id=$1 /* $2 */", 7, 99); eror != nil {
		t.Fatalf("乱序改写执行失败: %v", eror)
	}
	var amount int64
	var note string
	if eror := db.QueryRow("select amount, note from uni_it_mysql_q5 where id=7").Scan(&amount, &note); eror != nil {
		t.Fatal(eror)
	}
	if amount != 99 || note != "$1" {
		t.Fatalf("乱序改写落库值错误: amount=%d(应为 99), note=%q(应为 \"$1\")", amount, note)
	}

	//#② 重复编号:$1+$1 -> 5+5=10 落到 id=$2(=8) 的行
	if _, eror := db.Exec("insert into uni_it_mysql_q5 (id) values (8)"); eror != nil {
		t.Fatal(eror)
	}
	if eror := eng.Execute("update uni_it_mysql_q5 set amount=$1+$1 where id=$2", 5, 8); eror != nil {
		t.Fatalf("重复编号执行失败: %v", eror)
	}
	if eror := db.QueryRow("select amount from uni_it_mysql_q5 where id=8").Scan(&amount); eror != nil {
		t.Fatal(eror)
	}
	if amount != 10 {
		t.Fatalf("重复编号落库值错误: amount=%d(应为 10)", amount)
	}

	//#③ 非法编号/实参多余:引擎层显式报错,语句不触库
	if eror := eng.Execute("select $3 from uni_it_mysql_q5", 1, 2); eror == nil {
		t.Fatal("编号超实参数应报错")
	}
	if eror := eng.Execute("update uni_it_mysql_q5 set amount=$1 where id=$1", 7, 99); eror == nil {
		t.Fatal("实参多余应报错")
	}
}

const mysqlPageTable = "uni_it_mysql_page"

type mysqlPageRow struct {
	Id   int64  `db:"id"`
	Name string `db:"name"`
}

// #InsertP 分页 clamp 的 MySQL 真机验证:负值归一化为 999 行,2000 行分 3 页落库。
func TestMySQLInsertPPageSizeClamp(t *testing.T) {
	db := openMySQL(t)
	eng := mysqlEngineOn(db, "?")

	drop := "drop table if exists " + mysqlPageTable
	if eror := eng.Execute(drop); eror != nil {
		t.Fatalf("drop: %v", eror)
	}
	ddl := "create table " + mysqlPageTable + "(id bigint primary key, name varchar(64)) engine=InnoDB default charset=utf8mb4"
	if eror := eng.Execute(ddl); eror != nil {
		t.Fatalf("create: %v", eror)
	}
	t.Cleanup(func() {
		if eror := eng.Execute(drop); eror != nil {
			t.Logf("cleanup: drop %s failed: %v", mysqlPageTable, eror)
		}
	})

	tbl := eng.RegisterClass(mysqlPageRow{}, mysqlPageTable).SetKeys("id")
	if tbl.LastError != nil {
		t.Fatalf("register: %v", tbl.LastError)
	}

	rows := make([]mysqlPageRow, 2000)
	for i := range rows {
		rows[i] = mysqlPageRow{Id: int64(i + 1), Name: "n"}
	}
	if eror := eng.InsertP(&rows, -5, mysqlPageTable); eror != nil {
		t.Fatalf("InsertP: %v", eror)
	}
	n, eror := eng.SelectD("select count(1) from " + mysqlPageTable)
	if eror != nil || n != 2000 {
		t.Fatalf("count=(%d,%v), want 2000", n, eror)
	}
}
