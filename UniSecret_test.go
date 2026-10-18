// UniSecret_test
// #应用加密测试

package UniEngine

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// ----------------------------------------
// #mock driver,记录写入参数,返回预置查询结果
// ----------------------------------------

type mockDriver struct {
	mu           sync.Mutex //#保护 queries/insertValues(并发查询测试下多 goroutine 追加)
	queries      []string   //#记录 Prepare 的语句文本(供 SQL 文本断言)
	insertValues [][]driver.Value
	queryRows    [][]driver.Value
	columns      []string
	nextErr      error  //#非空时 rows.Next 返回该错误,模拟读取中断
	commitErr    error  //#非空时事务 Commit 返回该错误,模拟驱动层提交失败
	rollbackErr  error  //#非空时事务 Rollback 返回该错误,模拟驱动层回滚失败
	execErr      error  //#非空时 Exec 返回该错误,模拟写路径驱动失败
	affected     *int64 //#非空时 Exec 报告该影响行数(缺省 1),用于覆盖 ExecuteMust 的零行分支
	probeResult  string //#SQL Server 绑定探针的可选覆盖值;空串表示回显实参
	probeCount   int
}

func (d *mockDriver) recordQuery(query string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.queries = append(d.queries, query)
}

func (d *mockDriver) recordExec(args []driver.Value) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.insertValues = append(d.insertValues, args)
}

// snapshotQueries 返回已记录语句的副本(测试断言用,避免直接读并发切片)
func (d *mockDriver) snapshotQueries() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, len(d.queries))
	copy(out, d.queries)
	return out
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

func (c *mockConn) Prepare(query string) (driver.Stmt, error) {
	c.d.recordQuery(query)
	return &mockStmt{d: c.d}, nil
}
func (c *mockConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if !strings.Contains(query, "/* UniEngine bind check */") {
		return nil, driver.ErrSkip
	}
	if len(args) != 1 {
		return nil, fmt.Errorf("binding probe needs one argument, got %d", len(args))
	}
	c.d.mu.Lock()
	c.d.probeCount++
	result := c.d.probeResult
	c.d.mu.Unlock()
	if result == "" {
		result, _ = args[0].Value.(string)
	}
	return &mockProbeRows{value: result}, nil
}
func (c *mockConn) Close() error              { return nil }
func (c *mockConn) Begin() (driver.Tx, error) { return &mockTx{d: c.d}, nil }

// #mockTx 供事务路径测试(Begin/Commit/Cancel);经 mockDriver 注入驱动层失败
type mockTx struct{ d *mockDriver }

func (tx *mockTx) Commit() error {
	if tx.d != nil && tx.d.commitErr != nil {
		return tx.d.commitErr
	}
	return nil
}
func (tx *mockTx) Rollback() error {
	if tx.d != nil && tx.d.rollbackErr != nil {
		return tx.d.rollbackErr
	}
	return nil
}

type mockStmt struct{ d *mockDriver }

func (s *mockStmt) Close() error  { return nil }
func (s *mockStmt) NumInput() int { return -1 }
func (s *mockStmt) Exec(args []driver.Value) (driver.Result, error) {
	s.d.recordExec(args)

	if s.d.execErr != nil {
		return nil, s.d.execErr
	}
	if s.d.affected != nil {
		return driver.RowsAffected(*s.d.affected), nil
	}

	return driver.RowsAffected(1), nil
}
func (s *mockStmt) Query(args []driver.Value) (driver.Rows, error) {
	return &mockRows{d: s.d}, nil
}

type mockRows struct {
	d   *mockDriver
	idx int
}

type mockProbeRows struct {
	value string
	done  bool
}

func (r *mockProbeRows) Columns() []string { return []string{"value"} }
func (r *mockProbeRows) Close() error      { return nil }
func (r *mockProbeRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = r.value
	return nil
}

func (r *mockRows) Columns() []string { return r.d.columns }
func (r *mockRows) Close() error      { return nil }
func (r *mockRows) Next(dest []driver.Value) error {
	if r.d.nextErr != nil {
		return r.d.nextErr
	}
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
// #测试类:password 标记了 secreton
// ----------------------------------------

type TTestUser struct {
	UserName string `db:"user_name"`
	Password string `db:"password,secreton"`
}

func newSecretTestEngine(d *mockDriver) *TUniEngine {
	db := sql.OpenDB(mockConnector{d: d})
	eng := &TUniEngine{Db: db, ColLabel: "db", ColParam: "$", Provider: DtPOSTGR,
		SecretOn: 1, SecretBy: "test-secret-key", SecretIter: 4096} //#小迭代数加速测试;KDF行为另有专项测试
	eng.Initialize()
	eng.RegisterClass(TTestUser{}, "test_user")
	return eng
}

// #测试1:secreton tag 被解析
func TestEncryptTagParse(t *testing.T) {
	eng := newSecretTestEngine(&mockDriver{})
	tab, ok := eng.HashTabl["UniEngine.TTestUser"]
	if !ok {
		t.Fatal("class not registered")
	}
	field, ok := tab.HashField["password"]
	if !ok {
		t.Fatal("field password not registered")
	}
	if !field.SecretOn {
		t.Fatal("field password should be marked Encrypt")
	}
	if tab.HashField["user_name"].SecretOn {
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
	eng := &TUniEngine{SecretOn: 1, SecretBy: "test-secret-key", SecretIter: 2048}

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
	eng := newSecretTestEngine(d)

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
		if eng.IsEncrypted(s) {
			plainPassword, _ = eng.Secret(s, false)
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
	eng := newSecretTestEngine(d)
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
	eng := newSecretTestEngine(&mockDriver{})

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
	eng := newSecretTestEngine(&mockDriver{})

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
	eng := newSecretTestEngine(&mockDriver{})
	eng.RegisterClass(TTestUser{}, "test_user").SetSecret("password")

	tab, _ := eng.HashTabl["UniEngine.TTestUser"]
	if !tab.HashField["password"].SecretOn {
		t.Fatal("SetSecret should mark password Encrypt")
	}
}

// #测试10:加密结果带明文标签 ENC:
func TestSecretTagPrefix(t *testing.T) {
	eng := &TUniEngine{SecretOn: 1, SecretBy: "test-secret-key", SecretIter: 2048}

	cipherText, eror := eng.Secret("p@ssw0rd", true)
	if eror != nil {
		t.Fatal(eror)
	}
	if !strings.HasPrefix(cipherText, UniSecretTag) {
		t.Fatalf("cipher text should carry tag %s, got %s", UniSecretTag, cipherText)
	}
	if !eng.IsEncrypted(cipherText) {
		t.Fatal("IsEncrypted should be true for tagged cipher text")
	}
	if eng.IsEncrypted("legacy-plain") {
		t.Fatal("IsEncrypted should be false for legacy plain text")
	}
}

// #测试11:存量明文鉴别——解密时无标签的值直通返回,不报错
func TestSecretLegacyPassthrough(t *testing.T) {
	eng := &TUniEngine{SecretOn: 1, SecretBy: "test-secret-key"}

	// #存量明文(未加密时代写入的数据)
	for _, legacy := range []string{"p@ssw0rd", "123456", "abc-中文-123"} {
		got, eror := eng.Secret(legacy, false)
		if eror != nil {
			t.Fatalf("legacy plain text %q should not error: %v", legacy, eror)
		}
		if got != legacy {
			t.Fatalf("legacy plain text %q should pass through unchanged, got %q", legacy, got)
		}
	}
}

// #测试12:带标签但密钥错误的密文,解密报错(不静默)
func TestSecretWrongKey(t *testing.T) {
	eng := &TUniEngine{SecretOn: 1, SecretBy: "key-a", SecretIter: 2048}
	eng2 := &TUniEngine{SecretOn: 1, SecretBy: "key-b"}

	cipherText, eror := eng.Secret("secret", true)
	if eror != nil {
		t.Fatal(eror)
	}
	if _, eror = eng2.Secret(cipherText, false); eror == nil {
		t.Fatal("decrypt with wrong key should fail")
	}
}

// #测试13:混合数据读取——存量明文与新密文共存,Select 均能正确还原
func TestSelectMixedLegacyAndEncrypted(t *testing.T) {
	eng := newSecretTestEngine(&mockDriver{})

	cipherText, _ := eng.Secret("new-secret", true)
	d := &mockDriver{
		columns: []string{"user_name", "password"},
		queryRows: [][]driver.Value{
			{"legacy_user", "old-plain-pass"}, //#存量明文行
			{"new_user", cipherText},          //#加密后新行
		},
	}
	eng.Db = sql.OpenDB(mockConnector{d: d})

	var users []TTestUser
	if eror := eng.SelectL(&users, "select * from test_user"); eror != nil {
		t.Fatal(eror)
	}
	if len(users) != 2 {
		t.Fatalf("expect 2 rows, got %d", len(users))
	}
	if users[0].Password != "old-plain-pass" {
		t.Fatalf("legacy row should stay plain, got %q", users[0].Password)
	}
	if users[1].Password != "new-secret" {
		t.Fatalf("new row should be decrypted, got %q", users[1].Password)
	}
}

// #测试14:废弃包装 SpecialInsertL 仍可用,行为与 CopyInL 一致
func TestSpecialInsertLDeprecated(t *testing.T) {
	d := &mockDriver{}
	eng := newSecretTestEngine(d)

	users := []TTestUser{
		{UserName: "kazarus", Password: "p@ssw0rd"},
		{UserName: "alice", Password: "w0nder"},
	}
	if eror := eng.SpecialInsertL(&users, "test_user"); eror != nil {
		t.Fatal(eror)
	}

	//#COPY 完成时会额外 Exec 一次空参数,故 2 行数据 + 1 次收尾
	if len(d.insertValues) != 3 {
		t.Fatalf("expect 3 exec (2 rows + 1 finish), got %d", len(d.insertValues))
	}
	if len(d.insertValues[2]) != 0 {
		t.Fatalf("last exec should be empty finish call, got %v", d.insertValues[2])
	}

	for i, row := range d.insertValues[:2] {
		if len(row) != 2 {
			t.Fatalf("row %d: expect 2 values, got %d", i, len(row))
		}
		var plainUser, plainPass string
		for _, v := range row {
			s := fmt.Sprintf("%v", v)
			if eng.IsEncrypted(s) {
				plainPass, _ = eng.Secret(s, false)
			} else {
				plainUser = s
			}
		}
		if plainUser != users[i].UserName || plainPass != users[i].Password {
			t.Fatalf("row %d: user=%s pass=%s", i, plainUser, plainPass)
		}
	}
}

// #测试15:旧版接口 HasSpecialGetSqlInsertL/HasSpecialSetSqlValuesL 仍被 CopyInL 探测
type legacyCopyInUser struct {
	UserName string `db:"user_name"`
	Password string `db:"password"`
}

func (u legacyCopyInUser) SpecialGetSqlInsertL(UniEngineEx *TUniEngine, TableName string, Count int64) []string {
	return []string{"user_name", "password"}
}

func (u legacyCopyInUser) SpecialSetSqlValuesL(UniEngineEx *TUniEngine, QueryType TQueryType, f reflect.Value, out *[][]interface{}) {
	*out = append(*out, []interface{}{
		f.FieldByName("UserName").String(),
		f.FieldByName("Password").String() + "-legacy",
	})
}

func TestCopyInLegacyInterface(t *testing.T) {
	d := &mockDriver{}
	eng := newSecretTestEngine(d)
	eng.RegisterClass(legacyCopyInUser{}, "legacy_user")

	users := []legacyCopyInUser{{UserName: "bob", Password: "secret"}}
	if eror := eng.CopyInL(&users, "legacy_user"); eror != nil {
		t.Fatal(eror)
	}

	//#自定义钩子接管赋值:密码带 -legacy 后缀,且不走通用加密路径
	if len(d.insertValues) < 1 {
		t.Fatalf("expect at least 1 exec, got %d", len(d.insertValues))
	}
	row := d.insertValues[0]
	if len(row) != 2 || row[0] != "bob" || row[1] != "secret-legacy" {
		t.Fatalf("legacy interface values wrong: %v", row)
	}
}

// #测试16:非 string 字段标记 secreton,写入时报错(fast-fail,不产生读不回的密文)
type tIntSecret struct {
	Name string `db:"name"`
	Age  int64  `db:"age,secreton"`
}

func TestEncryptNonStringFieldErrors(t *testing.T) {
	eng := newSecretTestEngine(&mockDriver{})
	eng.RegisterClass(tIntSecret{}, "t_int_secret")

	row := tIntSecret{Name: "kazarus", Age: 18}
	eror := eng.Insert(&row)
	if eror == nil {
		t.Fatal("insert with non-string encrypt field should fail")
	}
	if !strings.Contains(eror.Error(), "secreton only supports string field") {
		t.Fatalf("unexpected error: %v", eror)
	}

	//#Update 路径同样报错
	eng.RegisterClass(tIntSecret{}, "t_int_secret").SetKeys("name")
	eror = eng.Update(&row)
	if eror == nil || !strings.Contains(eror.Error(), "secreton only supports string field") {
		t.Fatalf("update should fail with string-only error, got: %v", eror)
	}
}

// #测试17:SetSecret 同步 HashField 与 ListField 两份副本
func TestSetSecretSyncListField(t *testing.T) {
	eng := newSecretTestEngine(&mockDriver{})
	tb := eng.RegisterClass(TTestUser{}, "test_user")

	if eror := tb.SetSecret("password").LastError; eror != nil {
		t.Fatal(eror)
	}

	if !tb.HashField["password"].SecretOn {
		t.Fatal("HashField copy should be marked")
	}

	found := false
	for _, f := range tb.ListField {
		if f.FieldName == "password" {
			found = true
			if !f.SecretOn {
				t.Fatal("ListField copy should be marked too (PrepareRunSQL path)")
			}
		}
	}
	if !found {
		t.Fatal("password not found in ListField")
	}

	//#非字符串参数报错而非 panic
	if eror := tb.SetSecret(123).LastError; eror == nil {
		t.Fatal("non-string argument should return error")
	}
}

// ----------------------------------------
// #secreton×主键互斥与主键值明文(WHERE 定位语义)
// ----------------------------------------

// #测试18:SetSecret 拒绝已登记为主键的字段
func TestSetSecretRejectsPkey(t *testing.T) {
	d := &mockDriver{}
	eng := newSecretTestEngine(d)
	tbl := eng.GetTable("test_user")
	tbl = tbl.SetKeys("user_name")
	if eror := tbl.LastError; eror != nil {
		t.Fatalf("SetKeys: %v", eror)
	}
	if eror := tbl.SetSecret("user_name").LastError; eror == nil {
		t.Fatal("SetSecret on a pkey field should return error")
	}
}

// #测试19:SetKeys 拒绝已标记 secreton 的字段(与测试18构成双向守卫)
func TestSetKeysRejectsEncrypt(t *testing.T) {
	d := &mockDriver{}
	eng := newSecretTestEngine(d)
	tbl := eng.RegisterClass(TTestUser{}, "test_user3")
	if eror := tbl.SetKeys("password").LastError; eror == nil {
		t.Fatal("SetKeys on an encrypt-marked field should return error")
	}
}

// #测试20:AutoKeys 发现的主键若已标记 secreton,同样拒绝
func TestAutoKeysRejectsEncrypt(t *testing.T) {
	d := &mockDriver{columns: []string{"field_name"}, queryRows: [][]driver.Value{{"password"}}}
	eng := newSecretTestEngine(d)
	tbl := eng.RegisterClass(TTestUser{}, "test_user")
	eror := tbl.AutoKeys(eng)
	if eror == nil || !strings.Contains(eror.Error(), "secreton") {
		t.Fatalf("AutoKeys on encrypt-marked pkey should return error, got: %v", eror)
	}
}

// #tPkeySecretRow 两列均标记 secreton;user_name 经直接改注册表强制成为主键,
// #绕过 SetKeys/SetSecret 守卫,验证写入路径的兜底行为:主键值明文,数据列密文
type tPkeySecretRow struct {
	UserName string `db:"user_name,secreton"`
	Password string `db:"password,secreton"`
}

func newPkeySecretEngine(d *mockDriver) *TUniEngine {
	db := sql.OpenDB(mockConnector{d: d})
	eng := &TUniEngine{Db: db, ColLabel: "db", ColParam: "$", Provider: DtPOSTGR,
		SecretOn: 1, SecretBy: "test-secret-key", SecretIter: 4096} //#小迭代数加速测试;KDF行为另有专项测试
	eng.Initialize()
	tbl := eng.RegisterClass(tPkeySecretRow{}, "t_pkey_secret")
	fld := tbl.HashField["user_name"]
	tbl.HashPkeys["user_name"] = fld
	tbl.ListPkeys = append(tbl.ListPkeys, fld)
	return eng
}

// #测试21:Insert 路径主键值明文,数据列密文
func TestInsertPkeyPlaintext(t *testing.T) {
	d := &mockDriver{}
	eng := newPkeySecretEngine(d)

	row := tPkeySecretRow{UserName: "UID001", Password: "PW001"}
	if eror := eng.Insert(&row); eror != nil {
		t.Fatalf("insert: %v", eror)
	}

	if len(d.insertValues) != 1 {
		t.Fatalf("expected 1 exec, got %d", len(d.insertValues))
	}
	if got := fmt.Sprintf("%v", d.insertValues[0][0]); got != "UID001" {
		t.Fatalf("pkey value should stay plaintext, got %q", got)
	}
	if got := fmt.Sprintf("%v", d.insertValues[0][1]); !strings.HasPrefix(got, "ENC:") {
		t.Fatalf("data column should be encrypted, got %q", got)
	}
}

// #测试22:SaveIt 原生 UPSERT 路径主键值明文(on conflict 匹配即 WHERE 语义)
func TestSaveItPkeyPlaintext(t *testing.T) {
	d := &mockDriver{}
	eng := newPkeySecretEngine(d)

	row := tPkeySecretRow{UserName: "UID001", Password: "PW001"}
	if eror := eng.SaveIt(&row); eror != nil {
		t.Fatalf("saveit: %v", eror)
	}

	sqls := d.snapshotQueries()
	if len(sqls) == 0 || !strings.Contains(sqls[0], "on conflict") {
		t.Fatalf("expected native upsert sql, got %v", sqls)
	}
	if got := fmt.Sprintf("%v", d.insertValues[0][0]); got != "UID001" {
		t.Fatalf("upsert pkey value should stay plaintext, got %q", got)
	}
	if got := fmt.Sprintf("%v", d.insertValues[0][1]); !strings.HasPrefix(got, "ENC:") {
		t.Fatalf("upsert data column should be encrypted, got %q", got)
	}
}

// #测试23:Update 的 SET 数据列密文,WHERE 主键值明文
func TestUpdatePkeyPlaintext(t *testing.T) {
	d := &mockDriver{}
	eng := newPkeySecretEngine(d)

	row := tPkeySecretRow{UserName: "UID001", Password: "PW002"}
	if eror := eng.Update(&row); eror != nil {
		t.Fatalf("update: %v", eror)
	}

	if got := fmt.Sprintf("%v", d.insertValues[0][0]); !strings.HasPrefix(got, "ENC:") {
		t.Fatalf("update set column should be encrypted, got %q", got)
	}
	if got := fmt.Sprintf("%v", d.insertValues[0][1]); got != "UID001" {
		t.Fatalf("update where pkey should stay plaintext, got %q", got)
	}
}

// #测试24:Delete 的 WHERE 主键值明文
func TestDeletePkeyPlaintext(t *testing.T) {
	d := &mockDriver{}
	eng := newPkeySecretEngine(d)

	row := tPkeySecretRow{UserName: "UID001", Password: "PW002"}
	if eror := eng.Delete(&row); eror != nil {
		t.Fatalf("delete: %v", eror)
	}

	if got := fmt.Sprintf("%v", d.insertValues[0][0]); got != "UID001" {
		t.Fatalf("delete where pkey should stay plaintext, got %q", got)
	}
}

// ----------------------------------------
// #PBKDF2 派生与密文格式
// ----------------------------------------

// #parseEncPayload 解出密文的 盐/迭代数/剩余体,供断言与构造
func parseEncPayload(t *testing.T, Value string) (salt []byte, iter int, body []byte) {
	t.Helper()
	data, eror := base64.StdEncoding.DecodeString(strings.TrimPrefix(Value, UniSecretTag))
	if eror != nil {
		t.Fatalf("decode v2 payload: %v", eror)
	}
	if len(data) < secretHeadLen {
		t.Fatalf("payload too short: %d", len(data))
	}
	return data[:secretSaltLen], int(binary.BigEndian.Uint32(data[secretSaltLen : secretSaltLen+4])), data[secretSaltLen+4:]
}

// #测试25:盐内嵌密文,读取自包含——另一引擎实例(模拟另一进程)可直接解密
func TestSecretSaltSelfContained(t *testing.T) {
	engA := &TUniEngine{SecretOn: 1, SecretBy: "shared-key", SecretIter: 2048}
	engB := &TUniEngine{SecretOn: 1, SecretBy: "shared-key", SecretIter: 999999} //#迭代数不同也不影响:读取以密文内嵌值为准

	cipherText, eror := engA.Secret("cross-process", true)
	if eror != nil {
		t.Fatal(eror)
	}

	_, iter, _ := parseEncPayload(t, cipherText)
	if iter != 2048 {
		t.Fatalf("embedded iterations should honor writer's SecretIter, got %d", iter)
	}

	plainText, eror := engB.Secret(cipherText, false)
	if eror != nil {
		t.Fatal(eror)
	}
	if plainText != "cross-process" {
		t.Fatalf("self-contained decrypt fail: %s", plainText)
	}
}

// #测试26:SecretIter 未设置时使用缺省值(密文内嵌验证)
func TestSecretDefaultIterations(t *testing.T) {
	eng := &TUniEngine{SecretOn: 1, SecretBy: "k"} //#SecretIter=0 → 缺省

	cipherText, eror := eng.Secret("v", true)
	if eror != nil {
		t.Fatal(eror)
	}

	if _, iter, _ := parseEncPayload(t, cipherText); iter != UniSecretIter {
		t.Fatalf("default iterations should be %d, got %d", UniSecretIter, iter)
	}
}

// #测试27:密文内嵌迭代数异常(0/超上限)时拒绝派生,防损坏或恶意密文触发高开销
func TestSecretRejectsAbsurdIterations(t *testing.T) {
	eng := &TUniEngine{SecretOn: 1, SecretBy: "k", SecretIter: 2048}

	for _, iter := range []uint32{0, 99999999} {
		payload := make([]byte, secretHeadLen)
		binary.BigEndian.PutUint32(payload[secretSaltLen:secretSaltLen+4], iter)
		bad := UniSecretTag + base64.StdEncoding.EncodeToString(payload)
		if _, eror := eng.Secret(bad, false); eror == nil {
			t.Fatalf("iterations %d should be rejected", iter)
		}
	}
}

// #测试33:SecretIter 越界必须在**写**路径 fail-fast
// #(读路径已有上限校验;缺失写侧校验会产出引擎自己永远读不回的密文)
func TestSecretWriteRejectsOversizedIter(t *testing.T) {
	//#边界内:可加密且可原样读回
	ok := &TUniEngine{SecretOn: 1, SecretBy: "k", SecretIter: secretIterMax}
	cipherText, eror := ok.Secret("v", true)
	if eror != nil {
		t.Fatalf("SecretIter=secretIterMax 应可加密,却失败: %v", eror)
	}
	if plain, eror := ok.Secret(cipherText, false); eror != nil || plain != "v" {
		t.Fatalf("SecretIter=secretIterMax 的密文应可读回, got (%q, %v)", plain, eror)
	}

	//#越界:必须报错,且不得返回任何密文
	for _, iter := range []int{secretIterMax + 1, 99999999, 1 << 31} {
		eng := &TUniEngine{SecretOn: 1, SecretBy: "k", SecretIter: iter}
		cipherText, eror := eng.Secret("v", true)
		if eror == nil {
			//#若放行,读出必然失败——正是本测试要拦下的"写成功读不回"
			if _, derr := eng.Secret(cipherText, false); derr != nil {
				t.Fatalf("SecretIter=%d 写路径未拦截,产出读不回的密文(解密报错 %v)", iter, derr)
			}
			t.Fatalf("SecretIter=%d 越界却未报错", iter)
		}
		if cipherText != "" {
			t.Fatalf("SecretIter=%d 报错时不应返回密文, got %q", iter, cipherText)
		}
	}
}

// #测试34:派生密钥缓存的键必须覆盖 SecretBy——运行时轮换后新密文只应由新密钥解开
// #(曾漏掉 SecretBy:同进程轮换既不生效也不报错,新旧数据全线沿用旧密钥)
func TestSecretRotateKeyTakesEffect(t *testing.T) {
	eng := &TUniEngine{SecretOn: 1, SecretBy: "key-a", SecretIter: 2048}

	before, eror := eng.Secret("v", true)
	if eror != nil {
		t.Fatal(eror)
	}

	eng.SecretBy = "key-b" //#运行时轮换(同进程,缓存已热)

	after, eror := eng.Secret("v", true)
	if eror != nil {
		t.Fatal(eror)
	}

	//#轮换后新密文必须可由新密钥读回
	if plain, eror := eng.Secret(after, false); eror != nil || plain != "v" {
		t.Fatalf("轮换后新密文应可由新密钥读回, got (%q, %v)", plain, eror)
	}
	//#且**不得**被旧密钥解开——否则说明缓存键漏了 SecretBy
	keyA := &TUniEngine{SecretOn: 1, SecretBy: "key-a", SecretIter: 2048}
	if _, eror := keyA.Secret(after, false); eror == nil {
		t.Fatal("轮换后新密文竟能被旧密钥解开:缓存键未覆盖 SecretBy")
	}
	//#冷缓存的 key-b 引擎也应能读回,证明写侧确实用了新密钥
	keyB := &TUniEngine{SecretOn: 1, SecretBy: "key-b", SecretIter: 2048}
	if plain, eror := keyB.Secret(after, false); eror != nil || plain != "v" {
		t.Fatalf("轮换后新密文应可由 key-b 读回, got (%q, %v)", plain, eror)
	}
	//#轮换后旧密文在同进程内也应报错(与冷缓存行为一致,不得静默沿用旧派生密钥)
	if _, eror := eng.Secret(before, false); eror == nil {
		t.Fatal("轮换后旧密文不应再能解密(同进程亦不得静默沿用旧密钥)")
	}
}

// #测试29:并发加解密(冷缓存下多 goroutine 竞争首次派生;验证缓存与锁的正确性)
func TestSecretConcurrentAccess(t *testing.T) {
	eng := &TUniEngine{SecretOn: 1, SecretBy: "race-key", SecretIter: 8192}

	cipherText, eror := eng.Secret("hot-value", true)
	if eror != nil {
		t.Fatal(eror)
	}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			plain, eror := eng.Secret(cipherText, false)
			if eror != nil || plain != "hot-value" {
				t.Errorf("concurrent decrypt fail: %v %q", eror, plain)
			}
			if _, eror := eng.Secret("re-encrypt", true); eror != nil {
				t.Errorf("concurrent encrypt fail: %v", eror)
			}
		}()
	}
	wg.Wait()
}

// #测试30:表级 SecretOn 标记——SetSecret 与 tag 两条路径都要置位
func TestTableSecretOnFlag(t *testing.T) {
	d := &mockDriver{}
	eng := newSecretTestEngine(d)

	//#tag 路径:TTestUser.Password 带 secreton 标记,newSecretTestEngine 已按 test_user 注册
	tblTag := eng.GetTable("test_user")
	if !tblTag.SecretOn {
		t.Fatal("RegisterClass should set table SecretOn when a field carries the secreton tag")
	}

	//#SetSecret 路径:无 tag 的类,手工标记后置位
	type tPlainUser struct {
		UserName string `db:"user_name"`
		Password string `db:"password"`
	}
	tblPlain := eng.RegisterClass(tPlainUser{}, "t_plain_user")
	if tblPlain.SecretOn {
		t.Fatal("table without any secret field should not be marked")
	}
	tblPlain = tblPlain.SetSecret("password")
	if eror := tblPlain.LastError; eror != nil {
		t.Fatalf("SetSecret: %v", eror)
	}
	if !tblPlain.SecretOn {
		t.Fatal("SetSecret should set table SecretOn")
	}

	//#注册表内同一指针,GetTable 取到的也是置位后的状态
	if tab := eng.GetTable("t_plain_user"); tab == nil || !tab.SecretOn {
		t.Fatal("registry entry should reflect the SecretOn flag")
	}
}

// #测试N:ListSecretTable 注册表驱动遍历——仅含加密列的表,双键去重,按表名排序
func TestListSecretTable(t *testing.T) {
	eng := newSecretTestEngine(&mockDriver{})

	//#user_name 作定位键(password 为加密列,不能作键)
	eng.RegisterClass(TTestUser{}, "test_user").SetKeys("user_name").SetSecret("password")
	type tOther struct {
		ID   int64  `db:"id"`
		Name string `db:"name"`
	}
	eng.RegisterClass(tOther{}, "test_other").SetKeys("id") //#无加密列,不应出现

	ListTable := eng.ListSecretTable()
	if len(ListTable) != 1 {
		t.Fatalf("expect 1 secret table, got %d: %+v", len(ListTable), ListTable)
	}
	tab := ListTable[0]
	if tab.TableName != "test_user" {
		t.Fatalf("expect test_user, got %s", tab.TableName)
	}
	if tab.ClassType != reflect.TypeOf(TTestUser{}) {
		t.Fatalf("ClassType should be registered, got %v", tab.ClassType)
	}
	if len(tab.ListPkeys) != 1 || tab.ListPkeys[0].FieldName != "user_name" {
		t.Fatalf("pkeys should be [user_name], got %+v", tab.ListPkeys)
	}
}

// #测试N:按 ClassType 反射构造切片走 SelectL,密文读入即自动解密(注册表驱动读路径)
func TestSelectLByClassType(t *testing.T) {
	eng := newSecretTestEngine(&mockDriver{})

	cipherText, eror := eng.Secret("p@ssw0rd", true)
	if eror != nil {
		t.Fatal(eror)
	}

	eng.RegisterClass(TTestUser{}, "test_user").SetKeys("user_name").SetSecret("password")
	tab := eng.GetTable("test_user")

	eng.Db = sql.OpenDB(mockConnector{d: &mockDriver{
		columns:   []string{"user_name", "password"},
		queryRows: [][]driver.Value{{"kazarus", cipherText}},
	}})

	ListData := reflect.New(reflect.SliceOf(tab.ClassType))
	if eror := eng.SelectL(ListData.Interface(), "SELECT user_name,password FROM test_user"); eror != nil {
		t.Fatal(eror)
	}

	if ListData.Elem().Len() != 1 {
		t.Fatalf("expect 1 row, got %d", ListData.Elem().Len())
	}
	ItemData := ListData.Elem().Index(0)
	if ItemData.FieldByName("UserName").String() != "kazarus" {
		t.Fatalf("key field mismatch: %v", ItemData)
	}
	if ItemData.FieldByName("Password").String() != "p@ssw0rd" {
		t.Fatalf("secret field should auto-decrypt, got %q", ItemData.FieldByName("Password").String())
	}
}

// #测试31:SelectS 标量路径解密——带标签密文还原,存量明文直通
func TestSelectSDecrypt(t *testing.T) {
	eng := newSecretTestEngine(&mockDriver{})

	cipherText, eror := eng.Secret("p@ssw0rd", true)
	if eror != nil {
		t.Fatal(eror)
	}

	d := &mockDriver{
		columns:   []string{"password"},
		queryRows: [][]driver.Value{{cipherText}},
	}
	eng.Db = sql.OpenDB(mockConnector{d: d})

	//#密文 → 还原
	got, eror := eng.SelectS("select password from test_user")
	if eror != nil {
		t.Fatal(eror)
	}
	if got != "p@ssw0rd" {
		t.Fatalf("SelectS should decrypt cipher, got %q", got)
	}

	//#存量明文(无标签) → 直通
	d2 := &mockDriver{columns: []string{"password"}, queryRows: [][]driver.Value{{"old-plain"}}}
	eng.Db = sql.OpenDB(mockConnector{d: d2})
	got, eror = eng.SelectS("select password from test_user")
	if eror != nil {
		t.Fatal(eror)
	}
	if got != "old-plain" {
		t.Fatalf("legacy plaintext should pass through, got %q", got)
	}
}

// #测试32:ColSecret 加密列清单——tag 与 SetSecret 双路径登记,重复 SetSecret 去重
func TestListSecretColumns(t *testing.T) {
	eng := newSecretTestEngine(&mockDriver{})

	//#tag 路径:TTestUser.Password 带 secreton 标记,newSecretTestEngine 已按 test_user 注册
	tbl := eng.GetTable("test_user")
	if len(tbl.ColSecret) != 1 || tbl.ColSecret[0].FieldName != "password" {
		t.Fatalf("tag path should register [password], got %+v", tbl.ColSecret)
	}

	//#SetSecret 路径:手工登记另一列
	type tDualSecret struct {
		UserName string `db:"user_name"`
		Password string `db:"password"`
		Remark   string `db:"remark"`
	}
	tbl2 := eng.RegisterClass(tDualSecret{}, "t_dual_secret")
	tbl2 = tbl2.SetSecret("password", "remark")
	if eror := tbl2.LastError; eror != nil {
		t.Fatalf("SetSecret: %v", eror)
	}
	if len(tbl2.ColSecret) != 2 {
		t.Fatalf("expect 2 secret columns, got %+v", tbl2.ColSecret)
	}

	//#去重:重复 SetSecret 同一列,清单不增长
	tbl2 = tbl2.SetSecret("password")
	if eror := tbl2.LastError; eror != nil {
		t.Fatalf("SetSecret again: %v", eror)
	}
	if len(tbl2.ColSecret) != 2 {
		t.Fatalf("duplicate SetSecret should not grow ColSecret, got %+v", tbl2.ColSecret)
	}
}
