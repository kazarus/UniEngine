// integration
// #PostgreSQL 真实驱动(pgx)集成测试——独立模块,库本体保持零第三方依赖。
// #覆盖:CRUD / 原生UPSERT / COPY协议 / 应用加密 / 元数据探测 / 事务。
// #
// #运行(需可达的 PostgreSQL):
// #  UNIENGINE_PG_DSN="postgres://user:pass@127.0.0.1:5432/db?sslmode=disable" \
// #    go test ./integration/ -v
// #连不上时默认跳过;设 UNIENGINE_PG_REQUIRED=1 则失败(CI 用)。
package integration

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	UniEngine "github.com/kazarus/UniEngine"
	"github.com/kazarus/UniEngine/contrib/pgxcopy"

	_ "github.com/jackc/pgx/v5/stdlib" //#database/sql 驱动注册
)

// ---------------------------------------------------------------------------
// 环境与夹具
// ---------------------------------------------------------------------------

func pgDSN() string {
	if v := os.Getenv("UNIENGINE_PG_DSN"); v != "" {
		return v
	}
	return "postgres://uni:uni@127.0.0.1:5432/uni?sslmode=disable"
}

func newEngine(t *testing.T) *UniEngine.TUniEngine {

	t.Helper()

	db, eror := sql.Open("pgx", pgDSN())
	if eror != nil {
		t.Fatalf("open pg: %v", eror)
	}

	if eror = db.Ping(); eror != nil {
		if os.Getenv("UNIENGINE_PG_REQUIRED") == "1" {
			t.Fatalf("PostgreSQL 不可达(UNIENGINE_PG_REQUIRED=1): %v", eror)
		}
		db.Close()
		t.Skipf("PostgreSQL 不可达,跳过集成测试: %v", eror)
	}

	eng := &UniEngine.TUniEngine{Db: db, ColLabel: "db", ColParam: "$", Provider: UniEngine.DtPOSTGR}
	eng.Initialize()

	return eng
}

type itUser struct {
	UserName string `db:"user_name"`
	Password string `db:"password"`
}

// #加密列变体:走 ENC2 应用加密路径
type itSecretUser struct {
	UserName string `db:"user_name"`
	Password string `db:"password,encrypt"`
}

func newSecretEngine(t *testing.T) *UniEngine.TUniEngine {
	eng := newEngine(t)
	eng.SecretOn = 1
	eng.SecretBy = "integration-secret-key"
	return eng
}

func setupUserTable(t *testing.T, eng *UniEngine.TUniEngine) *UniEngine.TUniTable {

	t.Helper()

	if eror := eng.Execute("drop table if exists it_user"); eror != nil {
		t.Fatalf("drop it_user: %v", eror)
	}
	if eror := eng.Execute("create table it_user(user_name varchar(64) primary key, password varchar(1024))"); eror != nil {
		t.Fatalf("create it_user: %v", eror)
	}

	tbl := eng.RegisterClass(itUser{}, "it_user")
	if eror := tbl.SetKeys("user_name"); eror != nil {
		t.Fatalf("SetKeys: %v", eror)
	}

	return tbl
}

func countUser(t *testing.T, eng *UniEngine.TUniEngine) int64 {

	t.Helper()

	n, eror := eng.SelectD("select count(1) from it_user")
	if eror != nil {
		t.Fatalf("count: %v", eror)
	}
	return n
}

func passwordOf(t *testing.T, eng *UniEngine.TUniEngine, user string) string {

	t.Helper()

	v, eror := eng.SelectS("select password from it_user where user_name=$1", user)
	if eror != nil {
		t.Fatalf("select password(%s): %v", user, eror)
	}
	return v
}

// ---------------------------------------------------------------------------
// CRUD
// ---------------------------------------------------------------------------

func TestIntegrationCrud(t *testing.T) {

	eng := newEngine(t)
	defer eng.Db.Close()
	setupUserTable(t, eng)

	//#Insert + SelectL + Select 单行 + 标量
	if eror := eng.Insert(&itUser{"alice", "pw-a"}); eror != nil {
		t.Fatalf("insert: %v", eror)
	}
	if eror := eng.Insert(&itUser{"bob", "pw-b"}); eror != nil {
		t.Fatalf("insert: %v", eror)
	}

	var users []itUser
	if eror := eng.SelectL(&users, "select * from it_user order by user_name"); eror != nil {
		t.Fatalf("selectL: %v", eror)
	}
	if len(users) != 2 || users[0].UserName != "alice" || users[1].UserName != "bob" {
		t.Fatalf("selectL content: %+v", users)
	}

	var one itUser
	if eror := eng.Select(&one, "select * from it_user where user_name=$1", "bob"); eror != nil {
		t.Fatalf("select: %v", eror)
	}
	if one.Password != "pw-b" {
		t.Fatalf("select content: %+v", one)
	}

	if v, _ := eng.SelectS("select password from it_user where user_name=$1", "alice"); v != "pw-a" {
		t.Fatalf("selectS: %s", v)
	}
	if n := countUser(t, eng); n != 2 {
		t.Fatalf("count: %d", n)
	}

	//#Update
	if eror := eng.Update(&itUser{"alice", "pw-a2"}); eror != nil {
		t.Fatalf("update: %v", eror)
	}
	if v := passwordOf(t, eng, "alice"); v != "pw-a2" {
		t.Fatalf("after update: %s", v)
	}

	//#Delete
	if eror := eng.Delete(&itUser{"bob", "pw-b"}); eror != nil {
		t.Fatalf("delete: %v", eror)
	}
	if n := countUser(t, eng); n != 1 {
		t.Fatalf("after delete count: %d", n)
	}
}

// ---------------------------------------------------------------------------
// 原生 UPSERT(on conflict)
// ---------------------------------------------------------------------------

func TestIntegrationUpsert(t *testing.T) {

	eng := newEngine(t)
	defer eng.Db.Close()
	setupUserTable(t, eng)

	//#SaveIt:不存在 → 插入
	if eror := eng.SaveIt(&itUser{"bob", "v1"}); eror != nil {
		t.Fatalf("saveit insert: %v", eror)
	}
	if v := passwordOf(t, eng, "bob"); v != "v1" || countUser(t, eng) != 1 {
		t.Fatalf("saveit insert result: %s", v)
	}

	//#SaveIt:存在 → 更新(do update)
	if eror := eng.SaveIt(&itUser{"bob", "v2"}); eror != nil {
		t.Fatalf("saveit update: %v", eror)
	}
	if v := passwordOf(t, eng, "bob"); v != "v2" || countUser(t, eng) != 1 {
		t.Fatalf("saveit update result: %s count=%d", v, countUser(t, eng))
	}

	//#SaveItWhenNotExist:存在 → 跳过(do nothing)
	if eror := eng.SaveItWhenNotExist(&itUser{"bob", "v3"}); eror != nil {
		t.Fatalf("saveitwhennotexist skip: %v", eror)
	}
	if v := passwordOf(t, eng, "bob"); v != "v2" || countUser(t, eng) != 1 {
		t.Fatalf("saveitwhennotexist skip result: %s", v)
	}

	//#SaveItWhenNotExist:不存在 → 插入
	if eror := eng.SaveItWhenNotExist(&itUser{"carol", "c1"}); eror != nil {
		t.Fatalf("saveitwhennotexist insert: %v", eror)
	}
	if v := passwordOf(t, eng, "carol"); v != "c1" || countUser(t, eng) != 2 {
		t.Fatalf("saveitwhennotexist insert result: %s", v)
	}
}

// ---------------------------------------------------------------------------
// COPY 协议(CopyInL / CopyInP 分页)
// ---------------------------------------------------------------------------

func TestIntegrationCopyIn(t *testing.T) {

	eng := newEngine(t)
	defer eng.Db.Close()
	setupUserTable(t, eng)

	//#pgx 驱动下须接 CopyInHook 走原生 CopyFrom(见 TestIntegrationCopyInDefaultPathPinned)
	eng.CopyInHook = pgxcopy.Hook

	//#CopyInL 批量 120 行
	users := make([]itUser, 0, 120)
	for i := 0; i < 120; i++ {
		users = append(users, itUser{fmt.Sprintf("copy-%03d", i), fmt.Sprintf("pw-%03d", i)})
	}
	if eror := eng.CopyInL(&users); eror != nil {
		t.Fatalf("copyinl: %v", eror)
	}
	if n := countUser(t, eng); n != 120 {
		t.Fatalf("copyinl count: %d", n)
	}
	if v := passwordOf(t, eng, "copy-007"); v != "pw-007" {
		t.Fatalf("copyinl content: %s", v)
	}

	//#CopyInP 分页:30 行按 PageSize=7 分 5 页写入
	setupUserTable(t, eng)

	page := make([]itUser, 0, 30)
	for i := 0; i < 30; i++ {
		page = append(page, itUser{fmt.Sprintf("page-%02d", i), fmt.Sprintf("pp-%02d", i)})
	}
	if eror := eng.CopyInP(&page, 7); eror != nil {
		t.Fatalf("copyinp: %v", eror)
	}
	if n := countUser(t, eng); n != 30 {
		t.Fatalf("copyinp count: %d", n)
	}
}

// #内置 pq 风格逐行协议在 pgx stdlib 下的行为钉住:
// #pgx 不模拟 lib/pq 的 COPY 约定(NumInput=0,逐行传参被 database/sql 拒绝),
// #故不接 CopyInHook 时 CopyInL 报该错——这是文档化限制,非回归
func TestIntegrationCopyInDefaultPathPinned(t *testing.T) {

	eng := newEngine(t)
	defer eng.Db.Close()
	setupUserTable(t, eng)

	eror := eng.CopyInL(&[]itUser{{"pin-1", "pw"}})
	if eror == nil || !strings.Contains(eror.Error(), "expected 0 arguments") {
		t.Fatalf("pgx default copy path should fail with 'expected 0 arguments', got %v", eror)
	}
	if n := countUser(t, eng); n != 0 {
		t.Fatalf("failed copy should write nothing, count=%d", n)
	}
}

// ---------------------------------------------------------------------------
// 应用加密:存储层为 ENC2 密文,读取层自动还原
// ---------------------------------------------------------------------------

func TestIntegrationSecret(t *testing.T) {

	eng := newSecretEngine(t)
	defer eng.Db.Close()
	setupUserTable(t, eng)
	tbl := eng.RegisterClass(itSecretUser{}, "it_user")
	if eror := tbl.SetKeys("user_name"); eror != nil {
		t.Fatalf("SetKeys: %v", eror)
	}

	//#Insert:库中落为 ENC2 密文(SelectS 标量路径不解密,恰好用于断言存储形态)
	if eror := eng.Insert(&itSecretUser{"sec-1", "s3cret中文"}); eror != nil {
		t.Fatalf("insert: %v", eror)
	}
	raw1 := passwordOf(t, eng, "sec-1")
	if !strings.HasPrefix(raw1, "ENC2:") || raw1 == "s3cret中文" {
		t.Fatalf("stored value should be ENC2 cipher, got %.40s", raw1)
	}

	//#同一明文两次加密,密文不同(随机盐+nonce)
	if eror := eng.Insert(&itSecretUser{"sec-2", "s3cret中文"}); eror != nil {
		t.Fatalf("insert: %v", eror)
	}
	if raw2 := passwordOf(t, eng, "sec-2"); raw2 == raw1 {
		t.Fatal("same plain text should produce different cipher text")
	}

	//#SelectL:自动解密还原(含中文)
	var users []itSecretUser
	if eror := eng.SelectL(&users, "select * from it_user where user_name like 'sec-%' order by user_name"); eror != nil {
		t.Fatalf("selectL: %v", eror)
	}
	if len(users) != 2 || users[0].Password != "s3cret中文" || users[1].Password != "s3cret中文" {
		t.Fatalf("decrypt fail: %+v", users)
	}

	//#Update 加密列
	if eror := eng.Update(&itSecretUser{"sec-1", "rotated"}); eror != nil {
		t.Fatalf("update: %v", eror)
	}
	if raw := passwordOf(t, eng, "sec-1"); !strings.HasPrefix(raw, "ENC2:") {
		t.Fatalf("update should store cipher, got %.40s", raw)
	}
	var one itSecretUser
	if eror := eng.Select(&one, "select * from it_user where user_name=$1", "sec-1"); eror != nil {
		t.Fatalf("select: %v", eror)
	}
	if one.Password != "rotated" {
		t.Fatalf("update decrypt fail: %+v", one)
	}

	//#SaveIt 更新加密列(原生 UPSERT 路径)
	if eror := eng.SaveIt(&itSecretUser{"sec-2", "v9"}); eror != nil {
		t.Fatalf("saveit: %v", eror)
	}
	if v := passwordOf(t, eng, "sec-2"); !strings.HasPrefix(v, "ENC2:") {
		t.Fatalf("saveit should store cipher, got %.40s", v)
	}
	var two itSecretUser
	eng.Select(&two, "select * from it_user where user_name=$1", "sec-2")
	if two.Password != "v9" {
		t.Fatalf("saveit decrypt fail: %+v", two)
	}

	//#存量明文直通:绕过引擎写入的明文,读取原样返回
	if eror := eng.Execute("insert into it_user values($1,$2)", "legacy-1", "plain-old"); eror != nil {
		t.Fatalf("insert legacy: %v", eror)
	}
	var legacy itSecretUser
	if eror := eng.Select(&legacy, "select * from it_user where user_name=$1", "legacy-1"); eror != nil {
		t.Fatalf("select legacy: %v", eror)
	}
	if legacy.Password != "plain-old" {
		t.Fatalf("legacy plaintext should pass through, got %q", legacy.Password)
	}

	//#COPY + 加密:批量协议路径同样落密文、读还原
	setupUserTable(t, eng)
	eng.RegisterClass(itSecretUser{}, "it_user").SetKeys("user_name")
	eng.CopyInHook = pgxcopy.Hook

	batch := []itSecretUser{{"cp-1", "cp-secret"}, {"cp-2", "cp-secret"}, {"cp-3", "cp-secret"}}
	if eror := eng.CopyInL(&batch); eror != nil {
		t.Fatalf("copyinl secret: %v", eror)
	}
	if n := countUser(t, eng); n != 3 {
		t.Fatalf("copyinl secret count: %d", n)
	}
	if raw := passwordOf(t, eng, "cp-2"); !strings.HasPrefix(raw, "ENC2:") {
		t.Fatalf("copyin should store cipher, got %.40s", raw)
	}
	var cps []itSecretUser
	if eror := eng.SelectL(&cps, "select * from it_user order by user_name"); eror != nil {
		t.Fatalf("selectL: %v", eror)
	}
	for _, u := range cps {
		if u.Password != "cp-secret" {
			t.Fatalf("copyin decrypt fail: %+v", u)
		}
	}
}

// ---------------------------------------------------------------------------
// 元数据探测:AutoKeys / Exist* (pg_catalog 真实查询)
// ---------------------------------------------------------------------------

type itAuto struct {
	Id   int64  `db:"id"`
	Name string `db:"name"`
}

func TestIntegrationAutoKeysAndExist(t *testing.T) {

	eng := newEngine(t)
	defer eng.Db.Close()

	if eror := eng.Execute("drop table if exists it_auto"); eror != nil {
		t.Fatalf("drop: %v", eror)
	}
	if eror := eng.Execute("create table it_auto(id bigserial primary key, name varchar(64))"); eror != nil {
		t.Fatalf("create: %v", eror)
	}

	tbl := eng.RegisterClass(itAuto{}, "it_auto")

	//#AutoKeys:从 pg_attribute/pg_constraint 发现主键 id
	if eror := tbl.AutoKeys(eng); eror != nil {
		t.Fatalf("autokeys: %v", eror)
	}
	key, Valid := tbl.HashPkeys["id"]
	if !Valid || key.FieldName != "id" {
		t.Fatalf("autokeys should register id, got %+v", tbl.HashPkeys)
	}

	//#ExistTable / ExistField / ExistConst(PK 默认约束名 it_auto_pkey)
	if ok, eror := eng.ExistTable("it_auto"); eror != nil || !ok {
		t.Fatalf("existtable: %v %v", ok, eror)
	}
	if ok, _ := eng.ExistTable("it_no_such"); ok {
		t.Fatal("existtable should be false for missing table")
	}
	if ok, eror := eng.ExistField("it_auto", "name"); eror != nil || !ok {
		t.Fatalf("existfield: %v %v", ok, eror)
	}
	if ok, _ := eng.ExistField("it_auto", "no_such"); ok {
		t.Fatal("existfield should be false for missing column")
	}
	if ok, eror := eng.ExistConst(UniEngine.CtPK, "it_auto_pkey"); eror != nil || !ok {
		t.Fatalf("existconst pk: %v %v", ok, eror)
	}
	if ok, _ := eng.ExistConst(UniEngine.CtPK, "it_auto_pkey_x"); ok {
		t.Fatal("existconst should be false for missing constraint")
	}
}

// ---------------------------------------------------------------------------
// 事务:语句路由到 *sql.Tx;Cancel 回滚;重复 Begin 拒绝
// ---------------------------------------------------------------------------

func TestIntegrationTransaction(t *testing.T) {

	eng := newEngine(t)
	defer eng.Db.Close()
	setupUserTable(t, eng)

	//#Begin → Insert → Cancel(回滚)
	if eror := eng.Begin(); eror != nil {
		t.Fatalf("begin: %v", eror)
	}
	if eror := eng.Insert(&itUser{"tx-rollback", "pw"}); eror != nil {
		t.Fatalf("insert in tx: %v", eror)
	}
	if eror := eng.Cancel(); eror != nil {
		t.Fatalf("cancel: %v", eror)
	}
	if n := countUser(t, eng); n != 0 {
		t.Fatalf("rollback fail, count=%d", n)
	}

	//#Begin → Insert → Commit(提交)
	if eror := eng.Begin(); eror != nil {
		t.Fatalf("begin: %v", eror)
	}
	if eror := eng.Insert(&itUser{"tx-commit", "pw"}); eror != nil {
		t.Fatalf("insert in tx: %v", eror)
	}
	if eror := eng.Commit(); eror != nil {
		t.Fatalf("commit: %v", eror)
	}
	if n := countUser(t, eng); n != 1 {
		t.Fatalf("commit fail, count=%d", n)
	}

	//#事务期间重复 Begin 必须被拒绝
	if eror := eng.Begin(); eror != nil {
		t.Fatalf("begin: %v", eror)
	}
	if eror := eng.Begin(); !errors.Is(eror, UniEngine.ErrAlreadyInTransaction) {
		t.Fatalf("nested begin should fail with ErrAlreadyInTransaction, got %v", eror)
	}

	//#事务期间 COPY 钩子被拒绝(连接池连接无法路由到 *sql.Tx,不能静默落到事务外)
	eng.CopyInHook = pgxcopy.Hook
	if eror := eng.CopyInL(&[]itUser{{"tx-copy", "pw"}}); eror == nil || !strings.Contains(eror.Error(), "inside a transaction") {
		t.Fatalf("copyin hook in tx should be rejected, got %v", eror)
	}
	eng.Cancel()
}
