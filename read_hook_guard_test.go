package UniEngine

// 读钩子路径回归:SetSqlResult 结果钩子接管整行填充——激活的验签对钩子路径
// 不生效,必须 fail-fast(不得静默免验签);激活的加密列则在交接钩子前解密,
// 钩子与默认解码路径拿同一口径的明文数据。

import (
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
)

// #宽容模式(VerificationMode=1)下结果钩子 + 激活验签同样必须拒绝:
// #宽容跳过验签是给"无标签存量行"的回填期语义,不是给接管整行的钩子的
func TestSelectLHookRejectedWhenVerifyLenient(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.VerifyOn = 1
	eng.VerificationMode = 1 //#宽容模式,缺省值,显式写出以标明被测对象
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

// #结果钩子 + 激活的加密列:secreton 结果列在交接钩子前解密,钩子拿到明文
// #(与默认解码路径同一数据口径,而非原始密文)
func TestSelectLHookDecryptsSecretColumns(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.SecretOn = 1
	eng.SecretBy = "hook-decrypt-test-key"
	eng.RegisterClass(setSqlResultRow{}, "hook_secret").SetSecret("password")

	//#写入侧同引擎产出密文,作为库内数据喂给模拟驱动
	cipher, eror := eng.Secret("p@ss", true)
	if eror != nil {
		t.Fatal(eror)
	}
	d.columns = []string{"user_name", "password"}
	d.queryRows = [][]driver.Value{{"kazarus", cipher}}

	var rows []setSqlResultRow
	if eror := eng.SelectL(&rows, "select user_name,password from hook_secret"); eror != nil {
		t.Fatalf("钩子路径解密查询应成功: %v", eror)
	}
	if len(rows) != 1 || rows[0].UserName != "kazarus" || rows[0].Password != "p@ss" {
		t.Fatalf("钩子应拿到解密后的明文: %+v", rows)
	}
}

// #负例:SecretOn=0 时读路径不解密(与默认路径同口径),钩子拿到原始密文
func TestSelectLHookKeepsRawWhenSecretOff(t *testing.T) {
	eng := newMockEngine(&mockDriver{})
	eng.SecretOn = 1
	eng.SecretBy = "hook-decrypt-test-key"
	cipher, eror := eng.Secret("p@ss", true)
	if eror != nil {
		t.Fatal(eror)
	}

	engOff := newMockEngine(&mockDriver{
		columns:   []string{"user_name", "password"},
		queryRows: [][]driver.Value{{"kazarus", cipher}},
	})
	engOff.RegisterClass(setSqlResultRow{}, "hook_secret").SetSecret("password")

	var rows []setSqlResultRow
	if eror := engOff.SelectL(&rows, "select user_name,password from hook_secret"); eror != nil {
		t.Fatalf("SecretOn=0 时钩子查询应成功: %v", eror)
	}
	if len(rows) != 1 || !strings.HasPrefix(rows[0].Password, "ENC:") {
		t.Fatalf("SecretOn=0 时钩子应拿到原始密文: %+v", rows)
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
