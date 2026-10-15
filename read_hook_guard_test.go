package UniEngine

// 读钩子守卫回归:SetSqlResult 结果钩子接管整行填充,激活的验签/解密对钩子路径
// 不生效——必须 fail-fast,不得静默免验签/静默交密文(读侧绕过缺陷的封堵)。
// 与写侧守卫(write_hook_guard_test.go)同一哲学。

import (
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
)

// #宽容模式(VerifyMode=1)下结果钩子 + 激活验签同样必须拒绝:
// #宽容跳过验签是给"无标签存量行"的回填期语义,不是给接管整行的钩子的
func TestSelectLHookRejectedWhenVerifyLenient(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.VerifyOn = 1
	eng.VerifyMode = 1 //#宽容模式,缺省值,显式写出以标明被测对象
	eng.VerifyBy = "test-verify-key"
	eng.RegisterClass(setSqlResultRow{}, "hook_verify").SetKeys("user_name").
		SetVerify("password", "user_name")

	var rows []setSqlResultRow
	eror := eng.SelectL(&rows, "select user_name,password from hook_verify")
	if !errors.Is(eror, ErrIncompleteVerification) {
		t.Fatalf("宽容模式下结果钩子 + 激活验签应报 ErrIncompleteVerification: %v", eror)
	}
	if len(d.queries) != 0 {
		t.Fatalf("被拒后不应有任何 SQL 执行: %v", d.queries)
	}
}

// #结果钩子 + 激活的加密列:钩子拿到的是原始密文,必须报错
func TestSelectLHookRejectedWhenSecretActive(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.SecretOn = 1
	eng.RegisterClass(setSqlResultRow{}, "hook_secret").SetSecret("password")

	var rows []setSqlResultRow
	eror := eng.SelectL(&rows, "select user_name,password from hook_secret")
	if eror == nil {
		t.Fatal("结果钩子 + 激活加密列应报错")
	}
	if !strings.Contains(eror.Error(), "result hook") || !strings.Contains(eror.Error(), "secreton") {
		t.Fatalf("错误应指明钩子绕过 secreton 解密: %v", eror)
	}
	if len(d.queries) != 0 {
		t.Fatalf("被拒后不应有任何 SQL 执行: %v", d.queries)
	}
}

// #负例:SecretOn=1 但表无加密列(且未验签)时结果钩子放行
func TestSelectLHookAllowedWhenNoSecretCols(t *testing.T) {
	d := &mockDriver{
		columns:   []string{"user_name", "password"},
		queryRows: [][]driver.Value{{"kazarus", "p@ss"}},
	}
	eng := newMockEngine(d)
	eng.SecretOn = 1
	eng.RegisterClass(setSqlResultRow{}, "hook_row")

	var rows []setSqlResultRow
	if eror := eng.SelectL(&rows, "select * from hook_row"); eror != nil {
		t.Fatalf("无加密列时结果钩子应放行: %v", eror)
	}
	if len(rows) != 1 || rows[0].UserName != "kazarus" {
		t.Fatalf("钩子应正常回填: %+v", rows)
	}
}
