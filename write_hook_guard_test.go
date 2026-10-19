package UniEngine

// 写钩子守卫回归:自定义 SQL 写钩子(GetSqlUpdate/GetSqlInsert/GetSqlInsertL/
// SetSqlValues*)——验签激活时 fail-fast(钩子语句无法附带 MAC 伴随列);加密激活时
// 引擎解析钩子 SQL 的列→实参映射并就地加密 secreton 列实参(与读钩子交接前解密
// 对称),形态不可信解析(字面量/表达式/实参数不匹配)维持 fail-fast。

import (
	"database/sql/driver"
	"reflect"
	"strings"
	"testing"
)

// hookGuardUser 实现单笔更新/插入钩子;SetSqlValues 按查询类型切换实参序,
// 与各自 SQL 的列序一致(投产钩子的实际形态,见 load/tabl/gzzd 的 helpr_*.db.go)
type hookGuardUser struct {
	UserName string `db:"user_name"`
	Password string `db:"password"`
	MacText  string `db:"mac_text"`
}

func (u hookGuardUser) GetSqlUpdate(e *TUniEngine, TableName string) string {
	return "update hook_guard_user set password=? where user_name=?"
}

func (u hookGuardUser) GetSqlInsert(e *TUniEngine, TableName string) string {
	return "insert into hook_guard_user (user_name,password) values (?,?)"
}

func (u hookGuardUser) SetSqlValues(e *TUniEngine, qt TQueryType, Out *[]interface{}) {
	if qt == EtUpdate {
		*Out = append(*Out, u.Password, u.UserName) //#set password=?, where user_name=?
		return
	}
	*Out = append(*Out, u.UserName, u.Password) //#(user_name,password)
}

// hookGuardBatch 实现批量插入钩子
type hookGuardBatch struct {
	Name string `db:"name"`
	Code string `db:"code"`
}

func (b hookGuardBatch) GetSqlInsertL(e *TUniEngine, TableName string, Batch int64) string {
	//#真实批量钩子按 Batch 生成对应组数的 VALUES(引擎以实参总数校验防错位)
	var Rows []string
	for i := int64(0); i < Batch; i++ {
		Rows = append(Rows, "(?,?)")
	}
	return "insert into hook_guard_batch (name,code) values " + strings.Join(Rows, ",")
}

func (b hookGuardBatch) SetSqlValuesL(e *TUniEngine, qt TQueryType, Row reflect.Value, Out *[]interface{}) {
	*Out = append(*Out, Row.FieldByName("Name").Interface(), Row.FieldByName("Code").Interface())
}

// hookNumberedUser 编号式占位符钩子(投产 load.TDATA 的 $N 形态)
type hookNumberedUser struct {
	UserName string `db:"user_name"`
	Password string `db:"password"`
}

func (u hookNumberedUser) GetSqlInsert(e *TUniEngine, TableName string) string {
	return "insert into hook_num_user (user_name,password) values ($1,$2)"
}

func (u hookNumberedUser) SetSqlValues(e *TUniEngine, qt TQueryType, Out *[]interface{}) {
	*Out = append(*Out, u.UserName, u.Password)
}

// hookLiteralUser 更新语句为纯字面量,secreton 列值在 SQL 文本内,无法加密
type hookLiteralUser struct {
	UserName string `db:"user_name"`
	Password string `db:"password"`
}

func (u hookLiteralUser) GetSqlUpdate(e *TUniEngine, TableName string) string {
	return "update hook_literal_user set password='x' where user_name='kazarus'"
}

// #Update 钩子 + 激活加密列:secreton 列实参被自动加密,WHERE 主键实参保持明文
func TestUpdateHookEncryptsSecretArg(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.SecretOn = 1
	eng.SecretBy = "hook-encrypt-test-key"
	eng.RegisterClass(hookGuardUser{}, "hook_guard_user").SetKeys("user_name").SetSecret("password")

	row := hookGuardUser{UserName: "u", Password: "p"}
	if eror := eng.Update(&row); eror != nil {
		t.Fatalf("可解析形态的钩子路径应自动加密后放行: %v", eror)
	}
	if len(d.insertValues) != 1 || len(d.insertValues[0]) != 2 {
		t.Fatalf("应有 1 次执行 2 个实参: %v", d.insertValues)
	}
	Cipher, _ := d.insertValues[0][0].(string)
	if !strings.HasPrefix(Cipher, "ENC:") || Cipher == "p" {
		t.Fatalf("SET 段 secreton 实参应已加密: %v", d.insertValues[0][0])
	}
	if d.insertValues[0][1] != "u" {
		t.Fatalf("WHERE 主键实参应保持明文: %v", d.insertValues[0][1])
	}
}

// #Insert 钩子 + 验签激活:维持 fail-fast(与加密侧不同,签名无法附带进钩子语句)
func TestInsertHookRejectedWhenVerifyActive(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.VerifyOn = 1
	eng.VerificationMode = 1
	eng.VerifyBy = "test-verify-key"
	eng.RegisterClass(hookGuardUser{}, "hook_guard_user").SetKeys("user_name").
		SetVerify("mac_text", "user_name", "password")

	row := hookGuardUser{UserName: "u", Password: "p"}
	eror := eng.Insert(&row)
	if eror == nil {
		t.Fatal("钩子路径 + 激活验签应报错")
	}
	if !strings.Contains(eror.Error(), "write hook") || !strings.Contains(eror.Error(), "verify") {
		t.Fatalf("错误应指明钩子绕过验签: %v", eror)
	}
	if len(d.queries) != 0 {
		t.Fatalf("被拒后不应有任何 SQL 落库: %v", d.queries)
	}
}

// #InsertL 批量钩子 + 激活加密列:逐行实参中 secreton 列位置被加密
func TestInsertLHookEncryptsSecretArgs(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.SecretOn = 1
	eng.SecretBy = "hook-encrypt-test-key"
	eng.RegisterClass(hookGuardBatch{}, "hook_guard_batch").SetSecret("code")

	rows := []hookGuardBatch{{Name: "a", Code: "c1"}, {Name: "b", Code: "c2"}}
	if eror := eng.InsertL(&rows); eror != nil {
		t.Fatalf("批量钩子路径应自动加密后放行: %v", eror)
	}
	if len(d.insertValues) != 1 || len(d.insertValues[0]) != 4 {
		t.Fatalf("应有 1 次执行 4 个实参: %v", d.insertValues)
	}
	//#列序 (name,code):每行 arg1 为 code
	for _, row := range []int{0, 1} {
		Got, _ := d.insertValues[0][row*2+1].(string)
		if !strings.HasPrefix(Got, "ENC:") {
			t.Fatalf("第 %d 行 code 实参应已加密: %v", row, d.insertValues[0][row*2+1])
		}
		if d.insertValues[0][row*2] != []string{"a", "b"}[row] {
			t.Fatalf("第 %d 行 name 实参应保持明文: %v", row, d.insertValues[0][row*2])
		}
	}
}

// #SaveIt 钩子类回退 count-then-dispatch,Insert 钩子实参同样被自动加密
func TestSaveItHookEncryptsSecretArg(t *testing.T) {
	d := &mockDriver{
		columns:   []string{"value"},
		queryRows: [][]driver.Value{{int64(0)}},
	}
	eng := newMockEngine(d)
	eng.SecretOn = 1
	eng.SecretBy = "hook-encrypt-test-key"
	eng.RegisterClass(hookGuardUser{}, "hook_guard_user").SetKeys("user_name").SetSecret("password")

	row := hookGuardUser{UserName: "u", Password: "p"}
	if eror := eng.SaveIt(&row); eror != nil {
		t.Fatalf("SaveIt 钩子类应自动加密后放行: %v", eror)
	}
	//#count + insert 两次语句;insert 的 (user_name,password) 列序下 password=arg1
	if len(d.insertValues) != 1 || len(d.insertValues[0]) != 2 {
		t.Fatalf("应有 1 次插入执行 2 个实参: %v", d.insertValues)
	}
	Got, _ := d.insertValues[0][1].(string)
	if !strings.HasPrefix(Got, "ENC:") {
		t.Fatalf("password 实参应已加密: %v", d.insertValues[0][1])
	}
}

// #编号式占位符($N,投产 load.TDATA 形态):按编号映射实参并加密
func TestInsertHookEncryptsNumberedPlaceholders(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.SecretOn = 1
	eng.SecretBy = "hook-encrypt-test-key"
	eng.RegisterClass(hookNumberedUser{}, "hook_num_user").SetSecret("password")

	row := hookNumberedUser{UserName: "u", Password: "p"}
	if eror := eng.Insert(&row); eror != nil {
		t.Fatalf("编号式占位符钩子应自动加密后放行: %v", eror)
	}
	if len(d.insertValues) != 1 || len(d.insertValues[0]) != 2 {
		t.Fatalf("应有 1 次执行 2 个实参: %v", d.insertValues)
	}
	Got, _ := d.insertValues[0][1].(string)
	if !strings.HasPrefix(Got, "ENC:") || d.insertValues[0][0] != "u" {
		t.Fatalf("$2(password)应加密、$1(user_name)应明文: %v", d.insertValues[0])
	}
}

// #不可信形态:secreton 列值是 SQL 字面量(无法加密)→ 维持 fail-fast
func TestHookSecretLiteralFailsFast(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.SecretOn = 1
	eng.SecretBy = "hook-encrypt-test-key"
	eng.RegisterClass(hookLiteralUser{}, "hook_literal_user").SetKeys("user_name").SetSecret("password")

	row := hookLiteralUser{UserName: "u", Password: "p"}
	eror := eng.Update(&row)
	if eror == nil || !strings.Contains(eror.Error(), "cannot map") {
		t.Fatalf("字面量 secreton 值应维持 fail-fast: %v", eror)
	}
	if len(d.queries) != 0 {
		t.Fatalf("被拒后不应有任何 SQL 落库: %v", d.queries)
	}
}

// #实参数与 SQL 声明不匹配 → 维持 fail-fast(防"一组 VALUES 配多行实参"错位):
// #错误钩子不按 Batch 扩展 VALUES 组,实参数(2行×2列)与 SQL 占位符数(2)不一致
type hookMismatchBatch struct {
	Name string `db:"name"`
	Code string `db:"code"`
}

func (b hookMismatchBatch) GetSqlInsertL(e *TUniEngine, TableName string, Batch int64) string {
	return "insert into hook_mismatch_batch (name,code) values (?,?)"
}

func (b hookMismatchBatch) SetSqlValuesL(e *TUniEngine, qt TQueryType, Row reflect.Value, Out *[]interface{}) {
	*Out = append(*Out, Row.FieldByName("Name").Interface(), Row.FieldByName("Code").Interface())
}

func TestHookSecretArgCountMismatchFailsFast(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.SecretOn = 1
	eng.SecretBy = "hook-encrypt-test-key"
	eng.RegisterClass(hookMismatchBatch{}, "hook_mismatch_batch").SetSecret("code")

	rows := []hookMismatchBatch{{Name: "a", Code: "c1"}, {Name: "b", Code: "c2"}}
	if eror := eng.InsertL(&rows); eror == nil || !strings.Contains(eror.Error(), "cannot map secreton columns safely") {
		t.Fatalf("实参数不匹配应维持 fail-fast: %v", eror)
	}
	if len(d.queries) != 0 {
		t.Fatalf("被拒后不应有任何 SQL 落库: %v", d.queries)
	}
}

// #负例1:SecretOn=0(功能未激活)时钩子路径放行且不加密
func TestHookPathAllowedWhenSecretOff(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.RegisterClass(hookGuardUser{}, "hook_guard_user").SetKeys("user_name").SetSecret("password")

	row := hookGuardUser{UserName: "u", Password: "p"}
	if eror := eng.Update(&row); eror != nil {
		t.Fatalf("SecretOn=0 时钩子路径应放行: %v", eror)
	}
	if len(d.insertValues) != 1 || d.insertValues[0][0] != "p" {
		t.Fatalf("未激活时实参应原样: %v", d.insertValues)
	}
}

// #负例2:SecretOn=1 但表无加密列(且未验签)时钩子路径放行
func TestHookPathAllowedWhenNoSecretCols(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.SecretOn = 1
	eng.RegisterClass(hookGuardUser{}, "hook_guard_user").SetKeys("user_name")

	row := hookGuardUser{UserName: "u", Password: "p"}
	if eror := eng.Update(&row); eror != nil {
		t.Fatalf("无加密列时钩子路径应放行: %v", eror)
	}
	if len(d.insertValues) != 1 || len(d.insertValues[0]) != 2 {
		t.Fatalf("钩子 SQL 应原样执行: %v", d.insertValues)
	}
}

// #①编号式占位符被多个 secreton 列位复用($1,$1 / 多行 $1,$2,$1,$2):
// #同一实参只加密一次——重复加密会产出双层密文,读取侧解一次得到密文文本
type hookDupSecret struct {
	Code  string `db:"code"`
	Code2 string `db:"code2"`
}

func TestHookDuplicateNumberedArgEncryptedOnce(t *testing.T) {
	eng := newMockEngine(&mockDriver{})
	eng.SecretOn = 1
	eng.SecretBy = "hook-encrypt-test-key"
	eng.RegisterClass(hookDupSecret{}, "hook_dup_t").SetSecret("code", "code2")

	//#单行复用:insert into t (code,code2) values ($1,$1),钩子只给 1 个实参
	SQL1 := "insert into hook_dup_t (code,code2) values ($1,$1)"
	Plan, Ok := resolveHookSecretArgs(eng.tableByType("UniEngine.hookDupSecret"), SQL1)
	if !Ok || len(Plan.Idx) != 1 || Plan.Idx[0] != 0 || Plan.ArgCount != 1 {
		t.Fatalf("复用编号应去重且实参布局=1: ok=%v plan=%+v", Ok, Plan)
	}

	//#多行复用:values ($1,$2),($1,$2),code 列的 arg1 只加密一次
	SQL2 := "insert into hook_dup_t (code,code2) values ($1,$2),($1,$2)"
	Plan2, Ok2 := resolveHookSecretArgs(eng.tableByType("UniEngine.hookDupSecret"), SQL2)
	if !Ok2 || len(Plan2.Idx) != 2 || Plan2.Idx[0] != 0 || Plan2.Idx[1] != 1 || Plan2.ArgCount != 2 {
		t.Fatalf("多行复用应去重: ok=%v plan=%+v", Ok2, Plan2)
	}

	//#端到端:单层加解密往返应还原(双层加密会解出 ENC: 文本而非明文)
	eng3 := newMockEngine(&mockDriver{})
	eng3.SecretOn = 1
	eng3.SecretBy = "hook-encrypt-test-key"
	Value, eror := eng3.Secret("p", true)
	if eror != nil {
		t.Fatal(eror)
	}
	Value2, eror := eng3.Secret(Value, false)
	if eror != nil || Value2 != "p" {
		t.Fatalf("单层加解密往返应还原: %v", eror)
	}
}

// #②列清单/SET 段内的注释不应导致整个写入被拒
type hookCmtUser struct {
	UserName string `db:"user_name"`
	Password string `db:"password"`
}

func TestHookColumnListCommentAccepted(t *testing.T) {
	eng := newMockEngine(&mockDriver{})
	eng.SecretOn = 1
	eng.SecretBy = "hook-encrypt-test-key"
	eng.RegisterClass(hookCmtUser{}, "hook_cmt_t").SetKeys("user_name").SetSecret("password")

	//#INSERT 列清单内注释
	SQL1 := "insert into hook_cmt_t (user_name, /* secret col */ password) values (?,?)"
	Plan, Ok := resolveHookSecretArgs(eng.tableByType("UniEngine.hookCmtUser"), SQL1)
	if !Ok || len(Plan.Idx) != 1 || Plan.Idx[0] != 1 {
		t.Fatalf("列清单含注释应可解析且定位 password: ok=%v plan=%+v", Ok, Plan)
	}

	//#UPDATE SET 段内注释
	SQL2 := "update hook_cmt_t set /* audit */ password=? where user_name=?"
	Plan2, Ok2 := resolveHookSecretArgs(eng.tableByType("UniEngine.hookCmtUser"), SQL2)
	if !Ok2 || len(Plan2.Idx) != 1 || Plan2.Idx[0] != 0 {
		t.Fatalf("SET 段含注释应可解析: ok=%v plan=%+v", Ok2, Plan2)
	}
}

// #③未闭合字面量的掩码不得残留末字符(否则末尾 ? 被误检为占位符)
func TestMaskUnclosedLiteralNoPhantomPlaceholder(t *testing.T) {
	sql := "insert into t (code) values ('unclosed?"
	Masked := maskSQLLiterals(sql)
	if last := Masked[len(Masked)-1]; last != ' ' {
		t.Fatalf("未闭合字面量末字符应被掩码: tail=%q", Masked[len(Masked)-3:])
	}
	if Plcs := scanPlaceholders(Masked); len(Plcs) != 0 {
		t.Fatalf("字面量内 ? 不应被误检为占位符: %+v", Plcs)
	}

	//#闭合字面量行为不变:闭定界符保留,内部掩码
	closed := maskSQLLiterals("values ('a?b','x')")
	if strings.Count(closed, "?") != 0 {
		t.Fatalf("闭合字面量内部不应残留: %q", closed)
	}
}
