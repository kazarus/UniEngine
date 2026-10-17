// quality_fix4_test
// #2026-10-17 独立评审修复回归:钩子探测/COPY 加密守卫/SelectL 整批语义/装配防御

package UniEngine

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// #指针接收者钩子:所有入口必须与值接收者同口径
// ---------------------------------------------------------------------------

type ptrHookRow struct {
	ID int `db:"id"`
}

// #GetSqlInsert 使用指针接收者——旧实现只断言结构体值,会静默忽略本钩子
func (p *ptrHookRow) GetSqlInsert(e *TUniEngine, TableName string) string {
	return "insert into ptr_hook (id) values ($1) /* ptr-hook */"
}

func (p *ptrHookRow) GetSqlUpdate(e *TUniEngine, TableName string) string {
	return "update ptr_hook set id=$1 where id=$2 /* ptr-hook-update */"
}

func (p *ptrHookRow) SetSqlValues(e *TUniEngine, qt TQueryType, Out *[]interface{}) {
	if qt == EtInsert {
		*Out = append(*Out, p.ID)
		return
	}
	*Out = append(*Out, p.ID, p.ID)
}

func TestPointerReceiverHookInsert(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.RegisterClass(ptrHookRow{}, "ptr_hook")

	if eror := eng.Insert(&ptrHookRow{ID: 7}); eror != nil {
		t.Fatal(eror)
	}
	Queries := d.snapshotQueries()
	if len(Queries) != 1 || !strings.Contains(Queries[0], "ptr-hook") {
		t.Fatalf("pointer-receiver GetSqlInsert must be used, got %v", Queries)
	}
}

type ptrResultRow struct {
	Name string `db:"name"`
}

// #SetSqlResult 使用指针接收者——SelectL 旧实现用元素类型 t.Implements 判定,会漏掉本钩子
func (p *ptrResultRow) SetSqlResult(e *TUniEngine, obj interface{}, Column []string, Fields []interface{}) {
	p.Name = "hooked"
}

func TestPointerReceiverResultHookSelectL(t *testing.T) {
	d := &mockDriver{columns: []string{"name"}, queryRows: [][]driver.Value{{"db-value"}}}
	eng := newMockEngine(d)
	eng.RegisterClass(ptrResultRow{}, "ptr_result")

	var Rows []ptrResultRow
	if eror := eng.SelectL(&Rows, "select name from ptr_result"); eror != nil {
		t.Fatal(eror)
	}
	if len(Rows) != 1 || Rows[0].Name != "hooked" {
		t.Fatalf("pointer-receiver SetSqlResult must run in SelectL, got %+v", Rows)
	}
}

func TestPointerReceiverHookUpdate(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.RegisterClass(ptrHookRow{}, "ptr_hook").SetKeys("id")

	if eror := eng.Update(&ptrHookRow{ID: 7}); eror != nil {
		t.Fatal(eror)
	}
	Queries := d.snapshotQueries()
	if len(Queries) != 1 || !strings.Contains(Queries[0], "ptr-hook-update") {
		t.Fatalf("pointer-receiver GetSqlUpdate must be used, got %v", Queries)
	}
}

type ptrBatchRow struct {
	ID int `db:"id"`
}

func (p *ptrBatchRow) GetSqlInsertL(e *TUniEngine, TableName string, Batch int64) string {
	return "insert into ptr_batch (id) values ($1),($2) /* ptr-batch */"
}

func (p *ptrBatchRow) SetSqlValuesL(e *TUniEngine, qt TQueryType, Row reflect.Value, Out *[]interface{}) {
	*Out = append(*Out, Row.FieldByName("ID").Int())
}

func TestPointerReceiverHookInsertL(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.RegisterClass(ptrBatchRow{}, "ptr_batch")

	Rows := []ptrBatchRow{{ID: 1}, {ID: 2}}
	if eror := eng.InsertL(&Rows, "ptr_batch"); eror != nil {
		t.Fatal(eror)
	}
	Queries := d.snapshotQueries()
	if len(Queries) != 1 || !strings.Contains(Queries[0], "ptr-batch") {
		t.Fatalf("pointer-receiver GetSqlInsertL must be used, got %v", Queries)
	}
	if len(d.insertValues) != 1 || len(d.insertValues[0]) != 2 {
		t.Fatalf("SetSqlValuesL values should be collected, got %v", d.insertValues)
	}
}

type ptrKeyRow struct {
	ID int `db:"id"`
}

func (p *ptrKeyRow) GetMapUnique() string { return "ptr-key" }

func TestPointerReceiverMapUniqueSelectM(t *testing.T) {
	d := &mockDriver{columns: []string{"id"}, queryRows: [][]driver.Value{{int64(9)}}}
	eng := newMockEngine(d)
	eng.RegisterClass(ptrKeyRow{}, "ptr_key")

	Result := map[string]ptrKeyRow{}
	if eror := eng.SelectM(Result, "select id from ptr_key"); eror != nil {
		t.Fatal(eror)
	}
	if _, Ok := Result["ptr-key"]; !Ok {
		t.Fatalf("pointer-receiver GetMapUnique must be used, got %+v", Result)
	}
}

func TestCanNativeSaveDetectsPointerHook(t *testing.T) {
	if canNativeSave(reflect.ValueOf(&ptrHookRow{})) {
		t.Fatal("canNativeSave must return false for a pointer-receiver write hook")
	}
}

// ---------------------------------------------------------------------------
// #CopyInL 自定义钩子:加密与验签不得被静默绕过
// ---------------------------------------------------------------------------

type hookCopyRow struct {
	UserName string `db:"user_name"`
	Password string `db:"password"`
}

func (r hookCopyRow) GetSqlCopyInL(e *TUniEngine, TableName string, Batch int64) []string {
	return []string{"user_name", "password"}
}

func (r hookCopyRow) CopyInSetSqlValuesL(e *TUniEngine, qt TQueryType, Row reflect.Value, Out *[][]interface{}) {
	*Out = append(*Out, []interface{}{
		Row.FieldByName("UserName").String(),
		Row.FieldByName("Password").String(),
	})
}

func TestCopyInHookEncryptsSecretColumns(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.RegisterClass(hookCopyRow{}, "hook_copy").SetSecret("password")
	eng.SecretOn = 1
	eng.SecretBy = "copy-hook-key"
	eng.SecretIter = 2048

	Rows := []hookCopyRow{{UserName: "alice", Password: "plain-password"}}
	if eror := eng.CopyInL(&Rows, "hook_copy"); eror != nil {
		t.Fatal(eror)
	}
	if len(d.insertValues) == 0 || len(d.insertValues[0]) != 2 {
		t.Fatalf("expected one COPY row, got %v", d.insertValues)
	}
	if d.insertValues[0][0] != "alice" {
		t.Fatalf("non-secret column must stay plaintext, got %v", d.insertValues[0][0])
	}
	Cipher, Ok := d.insertValues[0][1].(string)
	if !Ok || !strings.HasPrefix(Cipher, UniSecretTag) {
		t.Fatalf("secreton column must be encrypted, got %v", d.insertValues[0][1])
	}
	Plain, eror := eng.Secret(Cipher, false)
	if eror != nil || Plain != "plain-password" {
		t.Fatalf("ciphertext should decrypt back, got %q, %v", Plain, eror)
	}
}

type hookCopyVerifyRow struct {
	ID   int64  `db:"id"`
	Name string `db:"name"`
	Mac  string `db:"mac"`
}

func (r hookCopyVerifyRow) GetSqlCopyInL(e *TUniEngine, TableName string, Batch int64) []string {
	return []string{"id", "name"} //#故意不含 MAC 列
}

func (r hookCopyVerifyRow) CopyInSetSqlValuesL(e *TUniEngine, qt TQueryType, Row reflect.Value, Out *[][]interface{}) {
	*Out = append(*Out, []interface{}{
		Row.FieldByName("ID").Int(),
		Row.FieldByName("Name").String(),
	})
}

func TestCopyInHookRejectedWhenVerifyActive(t *testing.T) {
	d := &mockDriver{}
	eng := &TUniEngine{Db: sql.OpenDB(mockConnector{d: d}), ColLabel: "db", ColParam: "$", Provider: DtPOSTGR,
		VerifyOn: 1, VerifyMode: 1, VerifyBy: "verify-key"}
	eng.Initialize()
	eng.RegisterClass(hookCopyVerifyRow{}, "hook_copy_verify").SetKeys("id").SetVerify("mac", "id", "name")

	Rows := []hookCopyVerifyRow{{ID: 1, Name: "n"}}
	eror := eng.CopyInL(&Rows, "hook_copy_verify")
	if eror == nil || !strings.Contains(eror.Error(), "bypasses engine-side verify") {
		t.Fatalf("custom COPY with verify active must fail fast, got %v", eror)
	}
	if len(d.insertValues) != 0 {
		t.Fatalf("no row should be written, got %v", d.insertValues)
	}
}

type hookCopyShortRow struct {
	UserName string `db:"user_name"`
	Password string `db:"password"`
}

func (r hookCopyShortRow) GetSqlCopyInL(e *TUniEngine, TableName string, Batch int64) []string {
	return []string{"user_name", "password"}
}

func (r hookCopyShortRow) CopyInSetSqlValuesL(e *TUniEngine, qt TQueryType, Row reflect.Value, Out *[][]interface{}) {
	*Out = append(*Out, []interface{}{Row.FieldByName("UserName").String()}) //#少一列
}

func TestCopyInHookRejectsShortRow(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.RegisterClass(hookCopyShortRow{}, "hook_copy_short").SetSecret("password")
	eng.SecretOn = 1
	eng.SecretBy = "k"
	eng.SecretIter = 2048

	Rows := []hookCopyShortRow{{UserName: "u", Password: "p"}}
	eror := eng.CopyInL(&Rows, "hook_copy_short")
	if eror == nil || !strings.Contains(eror.Error(), "has 1 values for 2 columns") {
		t.Fatalf("short custom COPY row must fail fast, got %v", eror)
	}
	if len(d.insertValues) != 0 {
		t.Fatalf("no row should be written, got %v", d.insertValues)
	}
}

type hookCopyNoColsRow struct {
	UserName string `db:"user_name"`
	Password string `db:"password"`
}

func (r hookCopyNoColsRow) CopyInSetSqlValuesL(e *TUniEngine, qt TQueryType, Row reflect.Value, Out *[][]interface{}) {
	*Out = append(*Out, []interface{}{Row.FieldByName("UserName").String(), Row.FieldByName("Password").String()})
}

func TestCopyInHookRejectsValuesWithoutColumns(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.RegisterClass(hookCopyNoColsRow{}, "hook_copy_nocols").SetSecret("password")
	eng.SecretOn = 1
	eng.SecretBy = "k"
	eng.SecretIter = 2048

	Rows := []hookCopyNoColsRow{{UserName: "u", Password: "p"}}
	eror := eng.CopyInL(&Rows, "hook_copy_nocols")
	if eror == nil || !strings.Contains(eror.Error(), "without a column list") {
		t.Fatalf("values without columns must fail fast, got %v", eror)
	}
}

// ---------------------------------------------------------------------------
// #SelectL 整批语义与标量单行约束
// ---------------------------------------------------------------------------

type nullRow struct {
	V string `db:"v"`
}

func TestSelectLDiscardsPartialRowsOnError(t *testing.T) {
	d := &mockDriver{
		columns:   []string{"v"},
		queryRows: [][]driver.Value{{"a"}, {"b"}, {nil}},
	}
	eng := newMockEngine(d)
	eng.RegisterClass(nullRow{}, "null_row")

	Rows := []nullRow{{V: "keep"}}
	eror := eng.SelectL(&Rows, "select v from null_row")
	if eror == nil {
		t.Fatal("NULL scan must fail")
	}
	if len(Rows) != 1 || Rows[0].V != "keep" {
		t.Fatalf("SelectL must roll back appended rows on error, preserving pre-existing data: %+v", Rows)
	}
}

func TestSelectDRejectsMultipleRows(t *testing.T) {
	d := &mockDriver{
		columns:   []string{"v"},
		queryRows: [][]driver.Value{{int64(1)}, {int64(2)}},
	}
	eng := newMockEngine(d)

	_, eror := eng.SelectD("select v from anything")
	if !errors.Is(eror, ErrMultipleRows) {
		t.Fatalf("scalar query with multiple rows must return ErrMultipleRows, got %v", eror)
	}
}

func TestSelectHNilKeyFunctionRejected(t *testing.T) {
	d := &mockDriver{columns: []string{"v"}, queryRows: [][]driver.Value{{"a"}}}
	eng := newMockEngine(d)
	eng.RegisterClass(nullRow{}, "null_row")

	Result := map[string]nullRow{}
	eror := eng.SelectH(Result, nil, "select v from null_row")
	if eror == nil || !strings.Contains(eror.Error(), "nil key function") {
		t.Fatalf("nil key function must be rejected, got %v", eror)
	}
}

// ---------------------------------------------------------------------------
// #装配防御:Db=nil / Initialize 不破坏活动事务
// ---------------------------------------------------------------------------

func TestNilDbReturnsErrorNotPanic(t *testing.T) {
	eng := &TUniEngine{ColLabel: "db", ColParam: "$", Provider: DtPOSTGR}

	if _, eror := eng.SelectD("select 1"); !errors.Is(eror, ErrNoDatabase) {
		t.Fatalf("SelectD with nil Db must return ErrNoDatabase, got %v", eror)
	}
	if eror := eng.Begin(); !errors.Is(eror, ErrNoDatabase) {
		t.Fatalf("Begin with nil Db must return ErrNoDatabase, got %v", eror)
	}
}

func TestInitializeDoesNotBreakActiveTransaction(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	if eror := eng.Begin(); eror != nil {
		t.Fatal(eror)
	}
	if eror := eng.Initialize(); eror != nil {
		t.Fatal(eror)
	}
	if eng.currentTx() == nil {
		t.Fatal("Initialize must not clear the active transaction")
	}
	if eror := eng.Commit(); eror != nil {
		t.Fatalf("commit after Initialize failed: %v", eror)
	}
	if eror := eng.Begin(); eror != nil {
		t.Fatalf("engine should accept a new transaction after commit: %v", eror)
	}
	_ = eng.Cancel()
}

// ---------------------------------------------------------------------------
// #元数据注入面 / 列名点号 / 验签登记期类型校验
// ---------------------------------------------------------------------------

type badTagRow struct {
	A string `db:"a.b"`
}

func TestRegisterClassRejectsDottedColumn(t *testing.T) {
	eng := newTestEngine()
	tbl := eng.RegisterClass(badTagRow{}, "t_dot")
	if !errors.Is(tbl.LastError, ErrInvalidFieldName) {
		t.Fatalf("dotted column must be rejected with ErrInvalidFieldName, got %v", tbl.LastError)
	}
}

func TestAutoKeysShortCircuitsLastError(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	tbl := eng.RegisterClass(ptrHookRow{}, "bad name")

	if tbl.LastError == nil {
		t.Fatal("invalid table name should set LastError")
	}
	if eror := tbl.AutoKeys(eng); !errors.Is(eror, tbl.LastError) {
		t.Fatalf("AutoKeys must return the chained LastError, got %v", eror)
	}
	if len(d.snapshotQueries()) != 0 {
		t.Fatalf("AutoKeys must not touch the database after LastError, got %v", d.snapshotQueries())
	}
}

func TestMySQLDataBaseWhitelist(t *testing.T) {
	d := &mockDriver{}
	eng := &TUniEngine{Db: sql.OpenDB(mockConnector{d: d}), ColLabel: "db", ColParam: "?", Provider: DtMYSQLN,
		DataBase: "uni' or 1=1 --"}
	eng.Initialize()

	if _, eror := eng.ExistTable("t_user"); !errors.Is(eror, ErrInvalidDataBase) {
		t.Fatalf("invalid DataBase must be rejected, got %v", eror)
	}
	Auto := TAutoKeys4MYSQLN{DataBase: "uni' or 1=1 --"}
	if _, eror := Auto.GetSqlAutoKeys(eng, "t_user"); !errors.Is(eror, ErrInvalidDataBase) {
		t.Fatalf("AutoKeys MySQL generator must reject invalid DataBase, got %v", eror)
	}
	if len(d.snapshotQueries()) != 0 {
		t.Fatalf("invalid DataBase must not execute SQL, got %v", d.snapshotQueries())
	}
}

func TestExistFieldRejectsDottedColumn(t *testing.T) {
	eng := newTestEngine()
	if _, eror := eng.ExistField("t_user", "a.b"); !errors.Is(eror, ErrInvalidFieldName) {
		t.Fatalf("dotted field name must be rejected, got %v", eror)
	}
}

type verifyTimeRow struct {
	ID      int64     `db:"id"`
	Updated time.Time `db:"updated"`
	Mac     string    `db:"mac"`
}

func TestSetVerifyRejectsUnsupportedProtectedKind(t *testing.T) {
	eng := newTestEngine()
	tbl := eng.RegisterClass(verifyTimeRow{}, "t_verify_time").SetKeys("id")
	tbl.SetVerify("mac", "id", "updated")
	if tbl.LastError == nil || !strings.Contains(tbl.LastError.Error(), "verify does not support") {
		t.Fatalf("time.Time protected column must be rejected at registration, got %v", tbl.LastError)
	}
}

// ---------------------------------------------------------------------------
// #调试输出不得打印实参值
// ---------------------------------------------------------------------------

func TestSQLDebugRedactsArgumentValues(t *testing.T) {
	eng := newTestEngine()
	_ = eng.RunDebug(true)

	Old := os.Stdout
	Read, Write, eror := os.Pipe()
	if eror != nil {
		t.Fatal(eror)
	}
	os.Stdout = Write
	eng.sqlDebug("select", "select ?", []interface{}{"top-secret-password"})
	_ = Write.Close()
	os.Stdout = Old

	Output, _ := io.ReadAll(Read)
	_ = Read.Close()

	if strings.Contains(string(Output), "top-secret-password") {
		t.Fatalf("debug output leaked argument values: %s", Output)
	}
	if !strings.Contains(string(Output), "values redacted") {
		t.Fatalf("debug output should state that values are redacted: %s", Output)
	}
}
