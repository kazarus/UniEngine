// quality_fix2_test
// #第二批修复回归测试(Q1/Q2/Q5),对应设计文档 docs/reviews/FIX_DESIGN_BATCH2_2026-10-14.md。
// #断言修复后的目标行为;评审探针(复现缺陷视角)复跑见批量验证步骤。

package UniEngine

import (
	"database/sql/driver"
	"reflect"
	"strings"
	"testing"
)

// #MAC 专项测试行:主键 id + 两保护列 + MAC 伴随列
type reviewMACRow struct {
	ID  int64  `db:"id"`
	A   string `db:"a"`
	B   string `db:"b"`
	MAC string `db:"mac"`
}

// ----------------------------------------
// Q1:MAC 载荷 v2 无歧义编码
// ----------------------------------------

// 载荷层直接验证:v1 拼接对 0x1f 注入元组产出相同载荷(v2 存在的理由),
// v2 逐列长度前缀后两元组载荷必不同。
func TestQ1PayloadV2Disambiguates(t *testing.T) {
	type rowAB struct {
		A string `db:"a"`
		B string `db:"b"`
	}
	Left := reflect.ValueOf(rowAB{A: "admin\x1f0", B: "user"})
	Right := reflect.ValueOf(rowAB{A: "admin", B: "0\x1fuser"})

	PayloadLeft, eror := verifyPayload("t", []int{0, 1}, Left)
	if eror != nil {
		t.Fatal(eror)
	}
	PayloadRight, eror := verifyPayload("t", []int{0, 1}, Right)
	if eror != nil {
		t.Fatal(eror)
	}
	if string(PayloadLeft) != string(PayloadRight) {
		t.Fatalf("v1 payload should collide for the probe tuples (probe precondition): %q vs %q", PayloadLeft, PayloadRight)
	}

	V2Left, eror := verifyPayloadV2("t", []string{"a", "b"}, []int{0, 1}, Left)
	if eror != nil {
		t.Fatal(eror)
	}
	V2Right, eror := verifyPayloadV2("t", []string{"a", "b"}, []int{0, 1}, Right)
	if eror != nil {
		t.Fatal(eror)
	}
	if string(V2Left) == string(V2Right) {
		t.Fatal("v2 payload must differ for tuples that only differ by separator placement")
	}
}

// VerifyMacVer=2:写入落 MAC2: 标签;0x1f 注入篡改(保留原 MAC)被严格模式拒绝。
func TestQ1MacV2TamperRejected(t *testing.T) {
	d := &mockDriver{columns: []string{"id", "a", "b", "mac"}}
	e := newMockEngine(d)
	defer e.Db.Close()
	e.VerifyOn, e.VerifyMode, e.VerifyBy, e.VerifyMacVer = 1, 2, "q1-test-key", 2
	tbl := e.RegisterClass(reviewMACRow{}, "review_mac").SetKeys("id").SetVerify("mac", "id", "a", "b")
	if tbl.LastError != nil {
		t.Fatal(tbl.LastError)
	}

	r := reviewMACRow{ID: 1, A: "admin\x1f0", B: "user"}
	if eror := e.Insert(&r); eror != nil {
		t.Fatal(eror)
	}
	if !strings.HasPrefix(r.MAC, "MAC2:") {
		t.Fatalf("VerifyMacVer=2 should emit MAC2: tag, got %q", r.MAC)
	}

	//#篡改:值换成碰撞元组,MAC 保留原值——必须验签失败(评审 Q1 探针的反转)
	d.queryRows = [][]driver.Value{{int64(1), "admin", "0\x1fuser", r.MAC}}
	var got reviewMACRow
	eror := e.Select(&got, "select id,a,b,mac from review_mac")
	if eror == nil || !strings.Contains(eror.Error(), "integrity check failed") {
		t.Fatalf("strict mode must reject tampered values carrying the original MAC, got %v", eror)
	}
}

// VerifyMacVer=2:仅分隔符位置不同的两元组 MAC 必不同(v1 下相同——碰撞根源)。
func TestQ1MacV2CollisionMACsDiffer(t *testing.T) {
	d := &mockDriver{columns: []string{"id", "a", "b", "mac"}}
	e := newMockEngine(d)
	defer e.Db.Close()
	e.VerifyOn, e.VerifyMode, e.VerifyBy, e.VerifyMacVer = 1, 2, "q1-test-key", 2
	e.RegisterClass(reviewMACRow{}, "review_mac").SetKeys("id").SetVerify("mac", "id", "a", "b")

	r1 := reviewMACRow{ID: 1, A: "admin\x1f0", B: "user"}
	r2 := reviewMACRow{ID: 1, A: "admin", B: "0\x1fuser"}
	if eror := e.Insert(&r1); eror != nil {
		t.Fatal(eror)
	}
	if eror := e.Insert(&r2); eror != nil {
		t.Fatal(eror)
	}
	if r1.MAC == r2.MAC {
		t.Fatal("v2 MACs must differ for separator-ambiguous tuples")
	}
}

// v2 下含 0x1f/空串/Unicode 的值正常往返验签(无歧义编码不需要禁止任何字节)。
func TestQ1MacV2WeirdValuesRoundTrip(t *testing.T) {
	d := &mockDriver{columns: []string{"id", "a", "b", "mac"}}
	e := newMockEngine(d)
	defer e.Db.Close()
	e.VerifyOn, e.VerifyMode, e.VerifyBy, e.VerifyMacVer = 1, 2, "q1-test-key", 2
	e.RegisterClass(reviewMACRow{}, "review_mac").SetKeys("id").SetVerify("mac", "id", "a", "b")

	r := reviewMACRow{ID: 7, A: "分隔符\x1f测试", B: ""}
	if eror := e.Insert(&r); eror != nil {
		t.Fatal(eror)
	}

	d.queryRows = [][]driver.Value{{int64(7), r.A, r.B, r.MAC}}
	var got reviewMACRow
	if eror := e.Select(&got, "select id,a,b,mac from review_mac"); eror != nil {
		t.Fatalf("v2 round-trip with 0x1f/empty/unicode values should verify, got %v", eror)
	}
}

// 显式 VerifyMacVer=1 保留旧写入格式;存量 v1 行升级后仍可读取。
// 完整防碰撞保证仍需把存量数据重签为 v2,迁移步骤见 README。
func TestQ1MacV1ExplicitCompatible(t *testing.T) {
	d := &mockDriver{columns: []string{"id", "a", "b", "mac"}}
	e := newMockEngine(d)
	defer e.Db.Close()
	e.VerifyOn, e.VerifyMode, e.VerifyBy, e.VerifyMacVer = 1, 2, "q1-test-key", 1
	e.RegisterClass(reviewMACRow{}, "review_mac").SetKeys("id").SetVerify("mac", "id", "a", "b")

	r := reviewMACRow{ID: 1, A: "admin", B: "user"}
	if eror := e.Insert(&r); eror != nil {
		t.Fatal(eror)
	}
	if !strings.HasPrefix(r.MAC, "MAC:") {
		t.Fatalf("explicit legacy version should emit MAC: tag, got %q", r.MAC)
	}

	d.queryRows = [][]driver.Value{{int64(1), "admin", "user", r.MAC}}
	var got reviewMACRow
	if eror := e.Select(&got, "select id,a,b,mac from review_mac"); eror != nil {
		t.Fatalf("v1 rows must keep verifying after upgrade, got %v", eror)
	}
}

// ----------------------------------------
// Q2:复合主键必须全覆盖
// ----------------------------------------

// 两列复合主键只保护一列:登记失败,错误列出缺失列(评审 Q2 探针的反转)。
func TestQ2PartialPkeyCoverageRejected(t *testing.T) {
	e := newVerifyTestEngine(&mockDriver{})
	defer e.Db.Close()

	tbl := e.GetTable("tbl_verify_row").SetVerify("mac_text", "who_build", "data_stat", "mast_name")
	if tbl.LastError == nil {
		t.Fatal("partial pkey coverage must be rejected")
	}
	if !strings.Contains(tbl.LastError.Error(), "user_indx") {
		t.Fatalf("error should name the missing pkey column, got %v", tbl.LastError)
	}
}

// 三列复合主键:缺任一列登记失败;全覆盖成功。
func TestQ2ThreePkeyCoverage(t *testing.T) {
	type row3k struct {
		K1  int64  `db:"k1"`
		K2  int64  `db:"k2"`
		K3  int64  `db:"k3"`
		Val string `db:"val"`
		Mac string `db:"mac"`
	}
	e := newMockEngine(&mockDriver{})
	defer e.Db.Close()

	//#缺 k3:拒绝
	tbl := e.RegisterClass(row3k{}, "t_row3k").SetKeys("k1", "k2", "k3")
	if tbl.SetVerify("mac", "k1", "k2", "val").LastError == nil {
		t.Fatal("missing k3 must be rejected")
	}

	//#全覆盖:接受(另一个引擎,避免同类改绑)
	e2 := newMockEngine(&mockDriver{})
	defer e2.Db.Close()
	tbl2 := e2.RegisterClass(row3k{}, "t_row3k_full").SetKeys("k1", "k2", "k3")
	if eror := tbl2.SetVerify("mac", "k1", "k2", "k3", "val").LastError; eror != nil {
		t.Fatalf("full coverage should register, got %v", eror)
	}
}

// 未登记主键就 SetVerify:显式报错提示先 SetKeys(链式顺序要求显式化)。
func TestQ2NoPkeysRejected(t *testing.T) {
	e := newMockEngine(&mockDriver{})
	defer e.Db.Close()

	tbl := e.RegisterClass(reviewMACRow{}, "t_nokeys").SetVerify("mac", "id", "a", "b")
	if tbl.LastError == nil {
		t.Fatal("SetVerify without pkeys must be rejected")
	}
	if !strings.Contains(tbl.LastError.Error(), "SetKeys") {
		t.Fatalf("error should hint SetKeys/AutoKeys first, got %v", tbl.LastError)
	}
}

// VerifyUnkeyed 逃生门:显式退出绑定后允许部分覆盖(语义不变)。
func TestQ2UnkeyedEscapeHatch(t *testing.T) {
	type row2k struct {
		K1  int64  `db:"k1"`
		K2  int64  `db:"k2"`
		Val string `db:"val"`
		Mac string `db:"mac"`
	}
	e := newMockEngine(&mockDriver{})
	defer e.Db.Close()

	tbl := e.RegisterClass(row2k{}, "t_unkeyed").SetKeys("k1", "k2")
	tbl.VerifyUnkeyed = true
	if eror := tbl.SetVerify("mac", "k1", "val").LastError; eror != nil {
		t.Fatalf("VerifyUnkeyed should allow partial coverage, got %v", eror)
	}
}

// 全覆盖后,改任一主键列的篡改必被拒(行绑定成立)。
func TestQ2FullCoverageTamperRejected(t *testing.T) {
	d := &mockDriver{columns: verifyRowColumns()}
	e := newVerifyTestEngine(d)
	defer e.Db.Close()
	e.VerifyMode = 2

	r := TVerifyRow{WhoBuild: 1, UserIndx: 2, DataStat: 1, MastName: "allowed"}
	if eror := e.Insert(&r); eror != nil {
		t.Fatal(eror)
	}

	//#改 user_indx=999,其余与原 MAC 保持不变:必须验签失败(评审 Q2 探针的反转)
	d.queryRows = [][]driver.Value{{int64(1), int64(999), int64(1), "allowed", r.MacText}}
	var got TVerifyRow
	eror := e.Select(&got, "select * from tbl_verify_row")
	if eror == nil || !strings.Contains(eror.Error(), "integrity check failed") {
		t.Fatalf("changing an uncovered pkey column must fail verification, got %v", eror)
	}
}

// ----------------------------------------
// Q5:占位符词法扫描与参数重排
// ----------------------------------------

// MySQL:字符串常量里的 $1 不改写;乱序编号按出现序重排实参(评审 Q5 探针的反转)。
func TestQ5MySQLStringConstantUntouched(t *testing.T) {
	d := &mockDriver{}
	e := newMockEngine(d)
	defer e.Db.Close()
	e.Provider, e.ColParam = DtMYSQLN, "?"

	if eror := e.Execute("update t set amount=$2, note='$1' where id=$1", 7, 99); eror != nil {
		t.Fatal(eror)
	}

	queries := d.snapshotQueries()
	if len(queries) == 0 {
		t.Fatal("no query recorded")
	}
	want := "update t set amount=?, note='$1' where id=?"
	if queries[0] != want {
		t.Fatalf("sql mismatch:\n got  %q\n want %q", queries[0], want)
	}
	//#出现序 $2(amount)→99,$1(id)→7:amount 绑 99、id 绑 7,与编号意图一致
	//#(database/sql 默认转换器把 int 实参转为 int64 再到驱动)
	if len(d.insertValues[0]) != 2 || d.insertValues[0][0] != int64(99) || d.insertValues[0][1] != int64(7) {
		t.Fatalf("args must be reordered by appearance order, got %v", d.insertValues[0])
	}
}

// MySQL:行注释与块注释里的 $N 原样保留。
func TestQ5MySQLCommentsUntouched(t *testing.T) {
	d := &mockDriver{}
	e := newMockEngine(d)
	defer e.Db.Close()
	e.Provider, e.ColParam = DtMYSQLN, "?"

	if eror := e.Execute("select a from t where b=$1 -- note $2", 5); eror != nil {
		t.Fatal(eror)
	}
	if eror := e.Execute("select /* $2 */ a from t where b=$1", 5); eror != nil {
		t.Fatal(eror)
	}

	queries := d.snapshotQueries()
	if len(queries) != 2 {
		t.Fatalf("expected 2 queries, got %v", queries)
	}
	if !strings.Contains(queries[0], "-- note $2") {
		t.Fatalf("line comment must keep $2, got %q", queries[0])
	}
	if !strings.Contains(queries[1], "/* $2 */") {
		t.Fatalf("block comment must keep $2, got %q", queries[1])
	}
	if d.insertValues[0][0] != int64(5) || d.insertValues[1][0] != int64(5) {
		t.Fatalf("unexpected args: %v / %v", d.insertValues[0], d.insertValues[1])
	}
}

// MySQL:重复编号按出现序复制实参($1,$1,$2 -> ?),? + [a1,a1,a2]。
func TestQ5MySQLDuplicateCopied(t *testing.T) {
	d := &mockDriver{}
	e := newMockEngine(d)
	defer e.Db.Close()
	e.Provider, e.ColParam = DtMYSQLN, "?"

	if eror := e.Execute("select $1,$1,$2", "a", "b"); eror != nil {
		t.Fatal(eror)
	}

	queries := d.snapshotQueries()
	if queries[0] != "select ?,?,?" {
		t.Fatalf("sql mismatch: %q", queries[0])
	}
	got := d.insertValues[0]
	if len(got) != 3 || got[0] != "a" || got[1] != "a" || got[2] != "b" {
		t.Fatalf("args must be [a,a,b], got %v", got)
	}
}

// MySQL:编号越界、$0、实参多余均显式报错。
func TestQ5MySQLInvalidNumberingRejected(t *testing.T) {
	e := newMockEngine(&mockDriver{})
	defer e.Db.Close()
	e.Provider, e.ColParam = DtMYSQLN, "?"

	if eror := e.Execute("select $3", 1, 2); eror == nil {
		t.Fatal("placeholder beyond args must be rejected")
	}
	if eror := e.Execute("select $0", 1); eror == nil {
		t.Fatal("$0 must be rejected")
	}
	if eror := e.Execute("select $1", 1, 2); eror == nil {
		t.Fatal("extra args must be rejected")
	}
}

// MySQL:原生 ? 占位符(无 $N)不改写不校验,行为与旧版一致。
func TestQ5MySQLNativePassThrough(t *testing.T) {
	d := &mockDriver{}
	e := newMockEngine(d)
	defer e.Db.Close()
	e.Provider, e.ColParam = DtMYSQLN, "?"
	in := "update t set a=? where id=?"

	if eror := e.Execute(in, 7, 9); eror != nil {
		t.Fatal(eror)
	}

	queries := d.snapshotQueries()
	if queries[0] != in {
		t.Fatalf("native ? sql should pass through, got %q", queries[0])
	}
	if len(d.insertValues[0]) != 2 || d.insertValues[0][0] != int64(7) || d.insertValues[0][1] != int64(9) {
		t.Fatalf("native ? args unchanged, got %v", d.insertValues[0])
	}
}

// Oracle:代码区 $1 -> :1;字符串常量里的 $1 不改写,实参原样。
func TestQ5OracleConstantUntouched(t *testing.T) {
	d := &mockDriver{}
	e := newMockEngine(d)
	defer e.Db.Close()
	e.Provider, e.ColParam = DtORACLE, ":"

	if eror := e.Execute("select '$1' from t where x=$1", "v"); eror != nil {
		t.Fatal(eror)
	}

	queries := d.snapshotQueries()
	want := "select '$1' from t where x=:1"
	if queries[0] != want {
		t.Fatalf("sql mismatch:\n got  %q\n want %q", queries[0], want)
	}
	if len(d.insertValues[0]) != 1 || d.insertValues[0][0] != "v" {
		t.Fatalf("oracle args unchanged, got %v", d.insertValues[0])
	}
}

// PG(缺省):$1 原样,不做任何改写。
func TestQ5PGUntouched(t *testing.T) {
	d := &mockDriver{}
	e := newMockEngine(d)
	defer e.Db.Close()

	if eror := e.Execute("select $1 from t", 1); eror != nil {
		t.Fatal(eror)
	}

	queries := d.snapshotQueries()
	if queries[0] != "select $1 from t" {
		t.Fatalf("pg should keep $1, got %q", queries[0])
	}
}
