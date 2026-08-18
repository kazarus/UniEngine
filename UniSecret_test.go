// UniSecret_test
// #应用加密测试
package UniEngine

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"testing"
)

// ----------------------------------------
// #mock driver,记录写入参数,返回预置查询结果
// ----------------------------------------

type mockDriver struct {
	insertValues [][]driver.Value
	queryRows    [][]driver.Value
	columns      []string
}

func (d *mockDriver) Open(name string) (driver.Conn, error) {
	return &mockConn{d: d}, nil
}

type mockConnector struct{ d *mockDriver }

func (c mockConnector) Connect(ctx context.Context) (driver.Conn, error) {
	return &mockConn{d: c.d}, nil
}

func (c mockConnector) Driver() driver.Driver { return c.d }

type mockConn struct{ d *mockDriver }

func (c *mockConn) Prepare(query string) (driver.Stmt, error) { return &mockStmt{d: c.d}, nil }
func (c *mockConn) Close() error                              { return nil }
func (c *mockConn) Begin() (driver.Tx, error)                 { return nil, errors.New("no tx") }

type mockStmt struct{ d *mockDriver }

func (s *mockStmt) Close() error   { return nil }
func (s *mockStmt) NumInput() int  { return -1 }
func (s *mockStmt) Exec(args []driver.Value) (driver.Result, error) {
	s.d.insertValues = append(s.d.insertValues, args)
	return driver.RowsAffected(1), nil
}
func (s *mockStmt) Query(args []driver.Value) (driver.Rows, error) {
	return &mockRows{d: s.d}, nil
}

type mockRows struct {
	d   *mockDriver
	idx int
}

func (r *mockRows) Columns() []string { return r.d.columns }
func (r *mockRows) Close() error      { return nil }
func (r *mockRows) Next(dest []driver.Value) error {
	if r.idx >= len(r.d.queryRows) {
		return io.EOF
	}
	for i := range dest {
		dest[i] = r.d.queryRows[r.idx][i]
	}
	r.idx++
	return nil
}

// ----------------------------------------
// #测试类:password 标记了 encrypt
// ----------------------------------------

type TTestUser struct {
	UserName string `db:"user_name"`
	Password string `db:"password,encrypt"`
}

func newTestEngine(d *mockDriver) *TUniEngine {
	db := sql.OpenDB(mockConnector{d: d})
	eng := &TUniEngine{Db: db, ColLabel: "db", ColParam: "$", Provider: DtPOSTGR,
		SecretOn: 1, SecretBy: "test-secret-key"}
	eng.Initialize()
	eng.RegisterClass(TTestUser{}, "test_user")
	return eng
}

// #测试1:encrypt tag 被解析
func TestEncryptTagParse(t *testing.T) {
	eng := newTestEngine(&mockDriver{})
	tab, ok := eng.HashTabl["UniEngine.TTestUser"]
	if !ok {
		t.Fatal("class not registered")
	}
	field, ok := tab.HashField["password"]
	if !ok {
		t.Fatal("field password not registered")
	}
	if !field.Encrypt {
		t.Fatal("field password should be marked Encrypt")
	}
	if tab.HashField["user_name"].Encrypt {
		t.Fatal("field user_name should NOT be marked Encrypt")
	}
}

// #测试2:SecretOn=0 时直通
func TestSecretOffPassthrough(t *testing.T) {
	eng := &TUniEngine{SecretOn: 0}
	got, eror := eng.Secret("hello", true)
	if eror != nil || got != "hello" {
		t.Fatalf("SecretOn=0 should passthrough, got=%s eror=%v", got, eror)
	}
}

// #测试3:内置AES-256-GCM 加密/解密 往返
func TestSecretDefaultRoundTrip(t *testing.T) {
	eng := &TUniEngine{SecretOn: 1, SecretBy: "test-secret-key"}

	cipherText, eror := eng.Secret("p@ssw0rd", true)
	if eror != nil {
		t.Fatal(eror)
	}
	if cipherText == "p@ssw0rd" {
		t.Fatal("cipher text equals plain text")
	}

	plainText, eror := eng.Secret(cipherText, false)
	if eror != nil {
		t.Fatal(eror)
	}
	if plainText != "p@ssw0rd" {
		t.Fatalf("round trip fail: %s", plainText)
	}

	// #同一明文两次加密结果不同(随机nonce)
	cipherText2, _ := eng.Secret("p@ssw0rd", true)
	if cipherText == cipherText2 {
		t.Fatal("same plain text should produce different cipher text")
	}

	// #密钥为空时报错
	eng2 := &TUniEngine{SecretOn: 1}
	if _, eror = eng2.Secret("x", true); eror == nil {
		t.Fatal("empty SecretBy should fail")
	}
}

// #测试4:应用自定义加密钩子(接管加解密)
func TestSecretHook(t *testing.T) {
	eng := &TUniEngine{SecretOn: 1}
	eng.SecretHook = func(Value string, Encrypt bool) (string, error) {
		if Encrypt {
			return "ENC(" + Value + ")", nil
		}
		return "DEC(" + Value + ")", nil
	}

	got, _ := eng.Secret("abc", true)
	if got != "ENC(abc)" {
		t.Fatalf("hook encrypt fail: %s", got)
	}
	got, _ = eng.Secret("xyz", false)
	if got != "DEC(xyz)" {
		t.Fatalf("hook decrypt fail: %s", got)
	}
}

// #测试5:Insert 时加密敏感字段
func TestInsertEncrypt(t *testing.T) {
	d := &mockDriver{}
	eng := newTestEngine(d)

	user := TTestUser{UserName: "kazarus", Password: "p@ssw0rd"}
	if eror := eng.Insert(&user, "test_user"); eror != nil {
		t.Fatal(eror)
	}

	if len(d.insertValues) != 1 {
		t.Fatalf("expect 1 insert, got %d", len(d.insertValues))
	}

	var plainUserName, plainPassword string
	for _, v := range d.insertValues[0] {
		s := fmt.Sprintf("%v", v)
		if dec, eror := eng.Secret(s, false); eror == nil {
			plainPassword = dec
		} else {
			plainUserName = s
		}
	}

	if plainUserName != "kazarus" {
		t.Fatalf("user_name should stay plain, got %s", plainUserName)
	}
	if plainPassword != "p@ssw0rd" {
		t.Fatalf("password should be encrypted, decrypted=%s", plainPassword)
	}
}

// #测试6:Update 时加密set值,主键值不加密
func TestUpdateEncrypt(t *testing.T) {
	d := &mockDriver{}
	eng := newTestEngine(d)
	eng.RegisterClass(TTestUser{}, "test_user").SetKeys("user_name")

	user := TTestUser{UserName: "kazarus", Password: "newpass"}
	if eror := eng.Update(&user, "test_user"); eror != nil {
		t.Fatal(eror)
	}

	if len(d.insertValues) != 1 {
		t.Fatalf("expect 1 update, got %d", len(d.insertValues))
	}

	args := d.insertValues[0]
	// #xValue(密码,已加密) + zValue(主键,明文)
	if len(args) != 2 {
		t.Fatalf("expect 2 args, got %d", len(args))
	}

	dec, eror := eng.Secret(fmt.Sprintf("%v", args[0]), false)
	if eror != nil || dec != "newpass" {
		t.Fatalf("password should be encrypted: dec=%s eror=%v", dec, eror)
	}
	if fmt.Sprintf("%v", args[1]) != "kazarus" {
		t.Fatalf("pkey should stay plain, got %v", args[1])
	}
}

// #测试7:Select 时自动解密敏感字段
func TestSelectDecrypt(t *testing.T) {
	eng := newTestEngine(&mockDriver{})

	cipherText, _ := eng.Secret("p@ssw0rd", true)
	d := &mockDriver{
		columns:   []string{"user_name", "password"},
		queryRows: [][]driver.Value{{"kazarus", cipherText}},
	}
	eng.Db = sql.OpenDB(mockConnector{d: d})

	var user TTestUser
	if eror := eng.Select(&user, "select * from test_user"); eror != nil {
		t.Fatal(eror)
	}
	if user.UserName != "kazarus" || user.Password != "p@ssw0rd" {
		t.Fatalf("select decrypt fail: %+v", user)
	}
}

// #测试8:SelectL 时自动解密敏感字段
func TestSelectLDecrypt(t *testing.T) {
	eng := newTestEngine(&mockDriver{})

	cipherText, _ := eng.Secret("p@ssw0rd", true)
	d := &mockDriver{
		columns:   []string{"user_name", "password"},
		queryRows: [][]driver.Value{{"kazarus", cipherText}},
	}
	eng.Db = sql.OpenDB(mockConnector{d: d})

	var users []TTestUser
	if eror := eng.SelectL(&users, "select * from test_user"); eror != nil {
		t.Fatal(eror)
	}
	if len(users) != 1 || users[0].Password != "p@ssw0rd" {
		t.Fatalf("selectL decrypt fail: %+v", users)
	}
}

// #测试9:SetSecret 手工标记加密字段
func TestSetSecret(t *testing.T) {
	eng := newTestEngine(&mockDriver{})
	eng.RegisterClass(TTestUser{}, "test_user").SetSecret("password")

	tab, _ := eng.HashTabl["UniEngine.TTestUser"]
	if !tab.HashField["password"].Encrypt {
		t.Fatal("SetSecret should mark password Encrypt")
	}
}
