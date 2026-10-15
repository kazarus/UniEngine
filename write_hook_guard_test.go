package UniEngine

// 写钩子守卫回归:自定义 SQL 写钩子(GetSqlUpdate/GetSqlInsert/GetSqlInsertL/
// SetSqlValues*)的语句与实参由钩子方生成,激活的 secreton 加密与验签对钩子路径
// 不生效——必须 fail-fast 报错,不得静默写明文/丢签名(绕过缺陷的封堵)。

import (
	"database/sql/driver"
	"reflect"
	"strings"
	"testing"
)

// hookGuardUser 同时实现单笔更新/插入钩子
type hookGuardUser struct {
	UserName string `db:"user_name"`
	Password string `db:"password"`
	MacText  string `db:"mac_text"`
}

func (u hookGuardUser) GetSqlUpdate(e *TUniEngine, TableName string) string {
	return "update hook_guard_user set password=? where user_name=?"
}

func (u hookGuardUser) SetSqlValues(e *TUniEngine, qt TQueryType, Out *[]interface{}) {
	*Out = append(*Out, u.Password, u.UserName)
}

func (u hookGuardUser) GetSqlInsert(e *TUniEngine, TableName string) string {
	return "insert into hook_guard_user (user_name,password) values (?,?)"
}

// hookGuardBatch 实现批量插入钩子
type hookGuardBatch struct {
	Name string `db:"name"`
	Code string `db:"code"`
}

func (b hookGuardBatch) GetSqlInsertL(e *TUniEngine, TableName string, Batch int64) string {
	return "insert into hook_guard_batch (name,code) values (?,?)"
}

func (b hookGuardBatch) SetSqlValuesL(e *TUniEngine, qt TQueryType, Row reflect.Value, Out *[]interface{}) {
	*Out = append(*Out, Row.FieldByName("Name").Interface(), Row.FieldByName("Code").Interface())
}

// #Update 走钩子且表有激活的加密列:必须报错且不执行任何 SQL
func TestUpdateHookRejectedWhenSecretActive(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.SecretOn = 1
	eng.RegisterClass(hookGuardUser{}, "hook_guard_user").SetKeys("user_name").SetSecret("password")

	row := hookGuardUser{UserName: "u", Password: "p"}
	eror := eng.Update(&row)
	if eror == nil {
		t.Fatal("钩子路径 + 激活加密列应报错")
	}
	if !strings.Contains(eror.Error(), "write hook") || !strings.Contains(eror.Error(), "secreton") {
		t.Fatalf("错误应指明钩子绕过 secreton 加密: %v", eror)
	}
	if len(d.queries) != 0 {
		t.Fatalf("被拒后不应有任何 SQL 落库: %v", d.queries)
	}
}

// #Insert 走钩子且表启用验签:必须报错且不执行任何 SQL
func TestInsertHookRejectedWhenVerifyActive(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.VerifyOn = 1
	eng.VerifyMode = 1
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

// #InsertL 走批量钩子且表有激活的加密列:必须报错
func TestInsertLHookRejectedWhenSecretActive(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.SecretOn = 1
	eng.RegisterClass(hookGuardBatch{}, "hook_guard_batch").SetSecret("code")

	rows := []hookGuardBatch{{Name: "a", Code: "c"}}
	eror := eng.InsertL(&rows)
	if eror == nil {
		t.Fatal("批量钩子路径 + 激活加密列应报错")
	}
	if !strings.Contains(eror.Error(), "InsertL") || !strings.Contains(eror.Error(), "secreton") {
		t.Fatalf("错误应指明方法与绕过对象: %v", eror)
	}
	if len(d.queries) != 0 {
		t.Fatalf("被拒后不应有任何 SQL 落库: %v", d.queries)
	}
}

// #SaveIt 钩子类回退 count-then-dispatch,最终经 Update/Insert 同样被守卫拦截
func TestSaveItHookRejectedWhenSecretActive(t *testing.T) {
	d := &mockDriver{
		columns:   []string{"value"},
		queryRows: [][]driver.Value{{int64(0)}},
	}
	eng := newMockEngine(d)
	eng.SecretOn = 1
	eng.RegisterClass(hookGuardUser{}, "hook_guard_user").SetKeys("user_name").SetSecret("password")

	row := hookGuardUser{UserName: "u", Password: "p"}
	eror := eng.SaveIt(&row)
	if eror == nil || !strings.Contains(eror.Error(), "write hook") {
		t.Fatalf("SaveIt 钩子类 + 激活加密列应报错: %v", eror)
	}
	//#只允许 count 探测语句,写入语句不得执行
	for _, q := range d.queries {
		if !strings.Contains(q, "select count(1)") {
			t.Fatalf("写入语句不应执行: %v", d.queries)
		}
	}
}

// #负例1:SecretOn=0(功能未激活)时钩子路径放行,与默认路径行为一致
func TestHookPathAllowedWhenSecretOff(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.RegisterClass(hookGuardUser{}, "hook_guard_user").SetKeys("user_name").SetSecret("password")

	row := hookGuardUser{UserName: "u", Password: "p"}
	if eror := eng.Update(&row); eror != nil {
		t.Fatalf("SecretOn=0 时钩子路径应放行: %v", eror)
	}
	if len(d.queries) != 1 || !strings.Contains(d.queries[0], "update hook_guard_user") {
		t.Fatalf("钩子 SQL 应原样执行: %v", d.queries)
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
	if len(d.queries) != 1 || !strings.Contains(d.queries[0], "update hook_guard_user") {
		t.Fatalf("钩子 SQL 应原样执行: %v", d.queries)
	}
}
