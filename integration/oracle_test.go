// oracle_test
// #Oracle 真驱动集成测试——验证 Oracle 协议族的占位符契约、CRUD/INSERT ALL/MERGE 与元数据探测
// #
// #驱动说明:本机无 Oracle Instant Client,故用**纯 Go** 驱动 github.com/sijms/go-ora/v2
// #(注册名 "oracle");README 记录的生产驱动 godror 需 Instant Client。引擎对 Oracle 族生成的是
// #":N" 占位符 + 裸标识符,两驱动一致,故本文件同时支持二者:
// #  UNIENGINE_ORACLE_DRIVER 默认 "oracle"(go-ora);用 godror 时设为 "godror" 并给整串 DSN。
// #
// #运行(需可达的 Oracle):
// #  UNIENGINE_ORACLE_HOST=127.0.0.1 UNIENGINE_ORACLE_PORT=1521 \
// #  UNIENGINE_ORACLE_USER=scott UNIENGINE_ORACLE_PASS='tiger' \
// #  UNIENGINE_ORACLE_SERVICE=ORCL go test ./... -run Oracle -v
// #  或整串: UNIENGINE_ORACLE_DSN="oracle://user:pass@host:1521/ORCL"
// #
// #不可达时默认跳过;设 UNIENGINE_ORACLE_REQUIRED=1 则失败(CI 用,与 pg/mssql 同口径)。
// #
// #安全:只操作自建对象 UNI_IT_ORA_PROBE(表) / UNI_IT_ORA_PROBE_V(视图),结束时 DROP。
package integration

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	UniEngine "github.com/kazarus/UniEngine"
	_ "github.com/sijms/go-ora/v2" //#纯 Go Oracle 驱动,注册名 "oracle"
)

const (
	oraProbeTable = "uni_it_ora_probe"
	oraProbeView  = "uni_it_ora_probe_v"
	oraProbePk    = "pk_uni_it_ora_probe"
)

// oracleDSN 组装连接串:优先整串 env;否则按分量拼 go-ora 风格 DSN
func oracleDSN() string {
	if v := os.Getenv("UNIENGINE_ORACLE_DSN"); v != "" {
		return v
	}

	host := os.Getenv("UNIENGINE_ORACLE_HOST")
	if host == "" {
		return ""
	}

	port := os.Getenv("UNIENGINE_ORACLE_PORT")
	if port == "" {
		port = "1521"
	}
	service := os.Getenv("UNIENGINE_ORACLE_SERVICE")
	if service == "" {
		service = "ORCL"
	}

	u := &url.URL{
		Scheme: os.Getenv("UNIENGINE_ORACLE_DRIVER"), //#仅占位;sijms/go-ora 的 scheme 需为 oracle
		User:   url.UserPassword(os.Getenv("UNIENGINE_ORACLE_USER"), os.Getenv("UNIENGINE_ORACLE_PASS")),
		Host:   host + ":" + port,
		Path:   "/" + service,
	}
	if u.Scheme == "" {
		u.Scheme = "oracle"
	}

	return u.String()
}

func oracleDriver() string {
	if v := os.Getenv("UNIENGINE_ORACLE_DRIVER"); v != "" {
		return v
	}
	return "oracle"
}

// openOracle 建立连接;不可达时跳过(REQUIRED=1 时失败)。连接由 t.Cleanup 关闭。
func openOracle(t *testing.T) *sql.DB {
	t.Helper()

	dsn := oracleDSN()
	if dsn == "" {
		t.Skip("未设置 UNIENGINE_ORACLE_DSN / UNIENGINE_ORACLE_HOST,跳过 Oracle 集成测试")
	}

	db, eror := sql.Open(oracleDriver(), dsn)
	if eror != nil {
		t.Fatalf("open oracle: %v", eror)
	}

	if eror = db.Ping(); eror != nil {
		if os.Getenv("UNIENGINE_ORACLE_REQUIRED") == "1" {
			t.Fatalf("Oracle 不可达(UNIENGINE_ORACLE_REQUIRED=1): %v", eror)
		}
		db.Close()
		t.Skipf("Oracle 不可达,跳过集成测试: %v", eror)
	}

	t.Cleanup(func() { db.Close() })

	return db
}

// mssqlEngineOn 的 Oracle 版:在既有连接上建引擎
func oracleEngineOn(db *sql.DB, colParam string) *UniEngine.TUniEngine {
	eng := &UniEngine.TUniEngine{Db: db, ColLabel: "db", ColParam: colParam, Provider: UniEngine.DtORACLE}
	eng.Initialize()

	return eng
}

// oraProbeRow 探测类:secret 标记 secreton(SecretOn=0 时直通,=1 时加解密)
type oraProbeRow struct {
	Id     int64  `db:"id"`
	Name   string `db:"name"`
	Secret string `db:"secret,secreton"`
}

// setupOracleProbe 重建自建表与视图并登记主键;Cleanup 时 DROP
func setupOracleProbe(t *testing.T, eng *UniEngine.TUniEngine) {
	t.Helper()

	cleanup := func() {
		_ = eng.Execute(fmt.Sprintf("drop view %s", oraProbeView))
		_ = eng.Execute(fmt.Sprintf("drop table %s", oraProbeTable))
	}
	cleanup()
	t.Cleanup(cleanup)

	ddl := fmt.Sprintf(`create table %s (
		id     number(19)    not null,
		name   varchar2(64),
		secret varchar2(1024),
		constraint %s primary key (id)
	)`, oraProbeTable, oraProbePk)
	if eror := eng.Execute(ddl); eror != nil {
		t.Fatalf("create table: %v", eror)
	}

	view := fmt.Sprintf("create view %s as select id, name from %s", oraProbeView, oraProbeTable)
	if eror := eng.Execute(view); eror != nil {
		t.Fatalf("create view: %v", eror)
	}

	if eror := eng.RegisterClass(oraProbeRow{}, oraProbeTable).SetKeys("id").LastError; eror != nil {
		t.Fatalf("SetKeys: %v", eror)
	}
}

// rawOraName 绕过引擎读库中真实存储值;不存在返回 ok=false
func rawOraName(t *testing.T, eng *UniEngine.TUniEngine, id int64) (string, bool) {
	t.Helper()

	var name sql.NullString
	eror := eng.Db.QueryRow(fmt.Sprintf("select name from %s where id=:1", oraProbeTable), id).Scan(&name)
	if eror == sql.ErrNoRows {
		return "", false
	}
	if eror != nil {
		t.Fatalf("raw select id=%d: %v", id, eror)
	}

	return name.String, true
}

// ---------------------------------------------------------------------------
// 1. 占位符契约:Oracle 用错 ColParam 是"硬报错",不存在静默写错
// ---------------------------------------------------------------------------

func TestOraclePlaceholderContract(t *testing.T) {
	db := openOracle(t)

	const probeValue = int64(7)

	//#:1:引擎为 Oracle 族生成的形式,必须真绑定
	var got sql.NullString
	if eror := db.QueryRow("select :1 from dual", probeValue).Scan(&got); eror != nil {
		t.Fatalf(":1 应可绑定实参,却失败: %v", eror)
	}
	if got.String != "7" {
		t.Fatalf(":1 绑定值 = %q, want \"7\"", got.String)
	}

	//#错误风格一律**报错**——这与 SQL Server 的 "$1 被当 money 字面量静默写错"形成对比
	for _, style := range []string{"select ? from dual", "select $1 from dual", "select @p1 from dual"} {
		var v sql.NullString
		eror := db.QueryRow(style, probeValue).Scan(&v)
		if eror == nil {
			t.Errorf("%s 竟然执行成功(值 %q);Oracle 上错误占位符应硬报错", style, v.String)
			continue
		}
		if !strings.Contains(eror.Error(), "ORA-") {
			t.Errorf("%s 报错内容非 ORA- 错误: %v", style, eror)
		}
		t.Logf("%s -> %v", style, eror)
	}
}

// ---------------------------------------------------------------------------
// 2. 引擎端到端:ColParam=":"(Oracle 族的唯一正确取值)
// ---------------------------------------------------------------------------

func TestOracleRoundTrip(t *testing.T) {
	db := openOracle(t)
	eng := oracleEngineOn(db, ":")
	setupOracleProbe(t, eng)

	//#Insert
	if eror := eng.Insert(&oraProbeRow{Id: 100, Name: "ALPHA", Secret: "s1"}, oraProbeTable); eror != nil {
		t.Fatalf("Insert: %v", eror)
	}
	if name, ok := rawOraName(t, eng, 100); !ok || name != "ALPHA" {
		t.Fatalf("Insert 后 id=100 -> (%q, ok=%v), want (\"ALPHA\", true)", name, ok)
	}

	//#Update(整行语义)
	if eror := eng.Update(&oraProbeRow{Id: 100, Name: "ALPHA-2", Secret: "s1"}, oraProbeTable); eror != nil {
		t.Fatalf("Update: %v", eror)
	}
	if name, _ := rawOraName(t, eng, 100); name != "ALPHA-2" {
		t.Fatalf("Update 后 id=100 -> %q, want \"ALPHA-2\"", name)
	}

	//#SaveIt → Oracle MERGE:not matched(插入)与 matched(更新)两条分支
	if eror := eng.SaveIt(&oraProbeRow{Id: 200, Name: "BETA", Secret: "s2"}, oraProbeTable); eror != nil {
		t.Fatalf("SaveIt(insert): %v", eror)
	}
	if name, ok := rawOraName(t, eng, 200); !ok || name != "BETA" {
		t.Fatalf("SaveIt 插入后 id=200 -> (%q, ok=%v), want (\"BETA\", true)", name, ok)
	}
	if eror := eng.SaveIt(&oraProbeRow{Id: 200, Name: "BETA-2", Secret: "s2"}, oraProbeTable); eror != nil {
		t.Fatalf("SaveIt(update): %v", eror)
	}
	if name, _ := rawOraName(t, eng, 200); name != "BETA-2" {
		t.Fatalf("SaveIt 更新后 id=200 -> %q, want \"BETA-2\"", name)
	}

	//#SaveItWhenNotExist → MERGE 仅 not matched 分支(已存在时应保持不变)
	if eror := eng.SaveItWhenNotExist(&oraProbeRow{Id: 200, Name: "SHOULD-NOT-CHANGE", Secret: "s2"}, oraProbeTable); eror != nil {
		t.Fatalf("SaveItWhenNotExist: %v", eror)
	}
	if name, _ := rawOraName(t, eng, 200); name != "BETA-2" {
		t.Fatalf("SaveItWhenNotExist 不应覆盖已存在行, got %q", name)
	}

	//#InsertL → Oracle 走 INSERT ALL 多行语法
	rows := []oraProbeRow{{Id: 300, Name: "GAMMA", Secret: "s3"}, {Id: 301, Name: "DELTA", Secret: "s4"}}
	if eror := eng.InsertL(&rows, oraProbeTable); eror != nil {
		t.Fatalf("InsertL(INSERT ALL): %v", eror)
	}
	for _, want := range rows {
		if name, ok := rawOraName(t, eng, want.Id); !ok || name != want.Name {
			t.Fatalf("InsertL 后 id=%d -> (%q, ok=%v), want (%q, true)", want.Id, name, ok, want.Name)
		}
	}

	//#SelectL:经引擎读回
	var loaded []oraProbeRow
	if eror := eng.SelectL(&loaded, fmt.Sprintf("select id,name,secret from %s order by id", oraProbeTable)); eror != nil {
		t.Fatalf("SelectL: %v", eror)
	}
	if len(loaded) != 4 {
		t.Fatalf("SelectL 读回 %d 行, want 4: %+v", len(loaded), loaded)
	}

	//#Delete:只删目标行
	if eror := eng.Delete(&oraProbeRow{Id: 100}, oraProbeTable); eror != nil {
		t.Fatalf("Delete: %v", eror)
	}
	if _, ok := rawOraName(t, eng, 100); ok {
		t.Fatalf("Delete 后 id=100 仍存在")
	}
	if _, ok := rawOraName(t, eng, 200); !ok {
		t.Fatalf("Delete 误删了非目标行 id=200")
	}
}

// TestOracleWrongColParamFailsLoudly 对照 SQL Server:Oracle 族配错 ColParam 只会硬报错,不损坏数据
func TestOracleWrongColParamFailsLoudly(t *testing.T) {
	db := openOracle(t)

	for _, colParam := range []string{"$", "?"} {
		eng := oracleEngineOn(db, colParam)
		setupOracleProbe(t, eng)

		eror := eng.Insert(&oraProbeRow{Id: 100, Name: "ALPHA"}, oraProbeTable)
		if eror == nil {
			t.Errorf("ColParam=%q 应报错(Oracle 不接受该占位符),却返回成功", colParam)
			continue
		}
		if !strings.Contains(eror.Error(), "ORA-") {
			t.Errorf("ColParam=%q 报错内容非 ORA-: %v", colParam, eror)
		}
		//#关键:必须**没有**行被写入(不存在 SQL Server 那种"报成功却写错"的形态)
		if name, ok := rawOraName(t, eng, 100); ok {
			t.Errorf("ColParam=%q 下竟然写入了 id=100(name=%q);Oracle 上不应出现静默写入", colParam, name)
		}
	}
}

// ---------------------------------------------------------------------------
// 3. 元数据探测:ExistTable / ExistViews / ExistField / ExistConst / AutoKeys
// ---------------------------------------------------------------------------

func TestOracleMetadata(t *testing.T) {
	db := openOracle(t)
	eng := oracleEngineOn(db, ":")
	setupOracleProbe(t, eng)

	//#表存在、视图判为视图(而非同名表)——老版本曾把同名普通表误判为视图
	if ok, eror := eng.ExistTable(oraProbeTable); eror != nil || !ok {
		t.Errorf("ExistTable(%s) = (%v, %v), want (true, nil)", oraProbeTable, ok, eror)
	}
	if ok, eror := eng.ExistViews(oraProbeView); eror != nil || !ok {
		t.Errorf("ExistViews(%s) = (%v, %v), want (true, nil)", oraProbeView, ok, eror)
	}
	if ok, _ := eng.ExistViews(oraProbeTable); ok {
		t.Errorf("普通表 %s 不应被判为视图", oraProbeTable)
	}
	if ok, _ := eng.ExistTable(oraProbeView); ok {
		t.Errorf("视图 %s 不应出现在 all_tables 判定中", oraProbeView)
	}

	//#字段
	if ok, eror := eng.ExistField(oraProbeTable, "secret"); eror != nil || !ok {
		t.Errorf("ExistField(secret) = (%v, %v), want (true, nil)", ok, eror)
	}
	if ok, _ := eng.ExistField(oraProbeTable, "no_such_col"); ok {
		t.Errorf("不存在的列不应被判定存在")
	}

	//#约束:建表时显式命名了主键
	if ok, eror := eng.ExistConst(UniEngine.CtPK, oraProbePk); eror != nil || !ok {
		t.Errorf("ExistConst(CtPK,%s) = (%v, %v), want (true, nil)", oraProbePk, ok, eror)
	}
	if ok, _ := eng.ExistConst(UniEngine.CtFK, oraProbePk); ok {
		t.Errorf("主键不应被判为外键")
	}

	//#AutoKeys:按数据库元数据自动登记主键
	auto := &UniEngine.TUniEngine{Db: db, ColLabel: "db", ColParam: ":", Provider: UniEngine.DtORACLE}
	_ = auto.Initialize()
	tbl := auto.RegisterClass(oraProbeRow{}, oraProbeTable)
	if eror := tbl.AutoKeys(auto, UniEngine.TAutoKeys4ORACLE{}); eror != nil {
		t.Fatalf("AutoKeys: %v", eror)
	}
	tbl = auto.GetTable(oraProbeTable)
	if _, ok := tbl.HashPkeys["id"]; !ok {
		t.Errorf("AutoKeys 应登记主键 id, got %v", tbl.ListPkeys)
	}
}

// ---------------------------------------------------------------------------
// 4. 应用加密在 Oracle 上的落库与还原(varchar2 承载 ENC: 密文)
// ---------------------------------------------------------------------------

func TestOracleSecretRoundTrip(t *testing.T) {
	db := openOracle(t)
	eng := oracleEngineOn(db, ":")
	eng.SecretOn = 1
	eng.SecretBy = "oracle-integration-secret"
	eng.SecretIter = 4096 //#测试用低迭代,避免拖慢
	setupOracleProbe(t, eng)

	if eror := eng.Insert(&oraProbeRow{Id: 1, Name: "n", Secret: "TOP-SECRET"}, oraProbeTable); eror != nil {
		t.Fatalf("Insert(secreton): %v", eror)
	}

	//#绕过引擎读存储原值:应为 ENC: 密文
	var raw string
	if eror := db.QueryRow(fmt.Sprintf("select secret from %s where id=:1", oraProbeTable), int64(1)).Scan(&raw); eror != nil {
		t.Fatalf("raw select: %v", eror)
	}
	if !strings.HasPrefix(raw, UniEngine.UniSecretTag) {
		t.Errorf("库中应存 ENC: 密文, got %q", raw)
	}

	//#经引擎读回应还原为明文
	var loaded oraProbeRow
	if eror := eng.Select(&loaded, fmt.Sprintf("select id,name,secret from %s where id=:1", oraProbeTable), int64(1)); eror != nil {
		t.Fatalf("Select: %v", eror)
	}
	if loaded.Secret != "TOP-SECRET" {
		t.Errorf("解密后 = %q, want \"TOP-SECRET\"", loaded.Secret)
	}
}
