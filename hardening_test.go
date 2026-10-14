package UniEngine

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// hardeningUser 用于注册测试：email 标记 readonly
type hardeningUser struct {
	ID    int64  `db:"id" json:"id"`
	Name  string `db:"name" json:"name"`
	Email string `db:"email,readonly" json:"email"`
}

func newHardeningEngine(provider TDriveType) *TUniEngine {
	return &TUniEngine{ColLabel: "db", ColParam: "$", Provider: provider}
}

func TestValidIdent(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"test_user", true},
		{"schema.table", true},
		{"a_b123", true},
		{"x;drop table t", false},
		{"a'b", false},
		{"a--b", false},
		{"", false},
	}
	for _, c := range cases {
		if got := validIdent(c.name); got != c.want {
			t.Errorf("validIdent(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestExistTableRejectsInjection(t *testing.T) {
	engine := newHardeningEngine(DtPOSTGR)
	if _, err := engine.ExistTable("t;drop table x"); err == nil {
		t.Error("ExistTable with malicious name should return error")
	}
}

func TestRegisterClassFieldOrder(t *testing.T) {
	engine := newHardeningEngine(DtPOSTGR)
	engine.RegisterClass(hardeningUser{}, "test_user")

	uniTable, ok := engine.HashTabl["test_user"]
	if !ok {
		t.Fatal("registered table not found by lowercase name key")
	}
	if len(uniTable.ListField) != 3 {
		t.Fatalf("ListField = %d, want 3 (including readonly)", len(uniTable.ListField))
	}
	if uniTable.ListField[0].FieldName != "id" || uniTable.ListField[1].FieldName != "name" {
		t.Errorf("field order = %s,%s; want id,name", uniTable.ListField[0].FieldName, uniTable.ListField[1].FieldName)
	}
}

func TestSetKeysDedupAndOrder(t *testing.T) {
	engine := newHardeningEngine(DtPOSTGR)
	tb := engine.RegisterClass(hardeningUser{}, "test_user")

	if err := tb.SetKeys("name", "id").LastError; err != nil {
		t.Fatalf("SetKeys: %v", err)
	}
	if err := tb.SetKeys("id").LastError; err != nil {
		t.Fatalf("SetKeys dup: %v", err)
	}
	if len(tb.ListPkeys) != 2 {
		t.Fatalf("ListPkeys = %d, want 2 (dedup)", len(tb.ListPkeys))
	}
	if tb.ListPkeys[0].FieldName != "name" {
		t.Errorf("pkey order[0] = %q, want name", tb.ListPkeys[0].FieldName)
	}

	if err := tb.SetKeys("not_exist").LastError; err == nil {
		t.Error("SetKeys on unregistered field should return error")
	}
}

func TestAutoKeysMySQLMissingDatabase(t *testing.T) {
	engine := newHardeningEngine(DtMYSQLN)
	tb := engine.RegisterClass(hardeningUser{}, "test_user")

	// 未指定 DataBase 时，GetSqlAutoKeys 返回错误，AutoKeys 应返回错误而非 panic
	if err := tb.AutoKeys(engine, TAutoKeys4MYSQLN{}); err == nil {
		t.Error("AutoKeys with empty database should return error")
	}
}

// 并发注册:不经过 Initialize 的引擎并发 RegisterClass,验证锁的懒初始化与注册互斥
// (在 -race 下运行才能捕获旧实现的懒初始化数据竞争)
func TestConcurrentRegisterClass(t *testing.T) {
	engine := &TUniEngine{ColLabel: "db", ColParam: "$", Provider: DtPOSTGR}

	const N = 16
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			engine.RegisterClass(hardeningUser{}, fmt.Sprintf("t_conc_%d", n))
		}(i)
	}
	wg.Wait()

	for i := 0; i < N; i++ {
		name := fmt.Sprintf("t_conc_%d", i)
		if engine.GetTable(name) == nil {
			t.Fatalf("table %s not registered", name)
		}
	}
}

// ---------------------------------------------------------------------------
// 标识符引用:schema.table 需逐段加引号,不能被当成单个标识符
// ---------------------------------------------------------------------------

func TestQuoteIdentDotted(t *testing.T) {
	cases := []struct{ in, want string }{
		{"t", `"t"`},
		{"schema.table", `"schema"."table"`},
		{`a"b`, `"a""b"`},
	}
	for _, c := range cases {
		if got := quoteIdent(c.in); got != c.want {
			t.Errorf("quoteIdent(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestCopyInStmtDottedTable(t *testing.T) {
	got := copyInStmt("schema.t", []string{"a", "b"})
	want := `COPY "schema"."t" ("a", "b") FROM STDIN`
	if got != want {
		t.Fatalf("copyInStmt = %s, want %s", got, want)
	}
}

// ---------------------------------------------------------------------------
// 注册:跳过无 db tag 与未导出字段(前者污染 "" 键,后者反射 panic)
// ---------------------------------------------------------------------------

// regSkipUser 含 正常字段 / 无 tag 字段 / 未导出字段
type regSkipUser struct {
	ID     int64  `db:"id"`
	Name   string `db:"name"`
	NoTag  string // 无 db tag,应被跳过
	hidden string // 未导出,应被跳过
}

func TestRegisterClassSkipsUntaggedAndUnexported(t *testing.T) {
	engine := newHardeningEngine(DtPOSTGR)
	tb := engine.RegisterClass(regSkipUser{}, "reg_skip_user")

	if len(tb.ListField) != 2 {
		t.Fatalf("ListField = %d, want 2 (only tagged exported fields)", len(tb.ListField))
	}
	if _, ok := tb.HashField[""]; ok {
		t.Error(`registry must not contain empty "" field key`)
	}
	if _, ok := tb.HashField["notag"]; ok {
		t.Error("untagged field must be skipped")
	}
	if _, ok := tb.HashField["hidden"]; ok {
		t.Error("unexported field must be skipped")
	}
}

// ---------------------------------------------------------------------------
// 事务:重复 Begin 必须显式拒绝,而非覆盖并泄漏上一个事务
// ---------------------------------------------------------------------------

func TestBeginTwiceRejected(t *testing.T) {
	engine := &TUniEngine{}
	engine.inTx = true // 模拟已开启事务;守卫应在触碰 Db(此处为 nil)前返回

	if err := engine.Begin(); !errors.Is(err, ErrAlreadyInTransaction) {
		t.Fatalf("expected ErrAlreadyInTransaction, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// 写方法传入 typed nil 指针:返回明确错误,不再反射 panic
// ---------------------------------------------------------------------------

func TestWriteMethodsRejectNilPointer(t *testing.T) {
	d := &mockDriver{}
	eng := &TUniEngine{Db: sql.OpenDB(mockConnector{d: d}), ColLabel: "db", ColParam: "$", Provider: DtPOSTGR}
	eng.Initialize()
	tbl := eng.RegisterClass(hardeningUser{}, "hardening_user")
	if err := tbl.SetKeys("id").LastError; err != nil {
		t.Fatalf("SetKeys: %v", err)
	}

	cases := map[string]func() error{
		"SaveIt": func() error { return eng.SaveIt((*hardeningUser)(nil)) },
		"Update": func() error { return eng.Update((*hardeningUser)(nil)) },
		"Insert": func() error { return eng.Insert((*hardeningUser)(nil)) },
		"Delete": func() error { return eng.Delete((*hardeningUser)(nil)) },
		"SaveItWhenNotExist": func() error {
			return eng.SaveItWhenNotExist((*hardeningUser)(nil))
		},
	}
	for name, call := range cases {
		err := call()
		if err == nil {
			t.Errorf("%s with nil pointer should return error", name)
			continue
		}
		if !strings.Contains(err.Error(), "nil pointer") {
			t.Errorf("%s error should mention nil pointer, got: %v", name, err)
		}
	}
}

// ---------------------------------------------------------------------------
// 链式语法:RegisterClass().SetKeys().SetSecret()
// ---------------------------------------------------------------------------

type chainUser struct {
	WhoBuild string `db:"who_build"`
	UserIndx int64  `db:"user_indx"`
	SysPathc string `db:"sys_pathc"`
	MastName string `db:"mast_name"`
}

func TestChainRegisterKeysSecret(t *testing.T) {
	d := &mockDriver{}
	eng := &TUniEngine{Db: sql.OpenDB(mockConnector{d: d}), ColLabel: "db", ColParam: "$", Provider: DtPOSTGR}
	eng.Initialize()

	//#链式:与用户实际调用形态一致(大写字段名,链尾不再接收返回值)
	tbl := eng.RegisterClass(chainUser{}, "sys_path").
		SetKeys("WHO_BUILD", "USER_INDX", "SYS_PATHC").
		SetSecret("MAST_NAME")

	//#主键三列齐(大小写不敏感),加密列标记,表级 SecretOn 置位
	if len(tbl.HashPkeys) != 3 {
		t.Fatalf("expect 3 pkeys, got %d", len(tbl.HashPkeys))
	}
	for _, k := range []string{"who_build", "user_indx", "sys_pathc"} {
		if _, Valid := tbl.HashPkeys[k]; !Valid {
			t.Fatalf("pkey %s not registered", k)
		}
	}
	if !tbl.HashField["mast_name"].SecretOn {
		t.Fatal("mast_name should be marked secreton")
	}
	if !tbl.SecretOn {
		t.Fatal("table SecretOn should be set")
	}

	//#链中出错记入 LastError 并短路:MAST_NAME 先成为主键,再 SetSecret 被互斥守卫拒绝
	tbl2 := eng.RegisterClass(chainUser{}, "sys_path2").
		SetKeys("WHO_BUILD", "USER_INDX", "SYS_PATHC").
		SetKeys("MAST_NAME"). //#非加密列,可作主键
		SetSecret("MAST_NAME")
	if tbl2.LastError == nil {
		t.Fatal("SetSecret on a pkey inside chain should record Err")
	}
}

// ---------------------------------------------------------------------------
// 评审 P0:裸 nil/typed nil/非指针/非法类型——全部公共入口必须返回错误而非 panic
// ---------------------------------------------------------------------------

type p0Row struct {
	Id   int64  `db:"id"`
	Name string `db:"name"`
}

func (r p0Row) GetMapUnique() string { return fmt.Sprintf("%d", r.Id) }

func newP0Engine() *TUniEngine {
	d := &mockDriver{}
	eng := &TUniEngine{Db: sql.OpenDB(mockConnector{d: d}), ColLabel: "db", ColParam: "$", Provider: DtPOSTGR}
	eng.Initialize()
	return eng
}

// #全部 13 个公共 CRUD 入口:裸 nil 必须返回 ErrNilParam,绝不允许 panic
func TestAllEntriesRejectBareNil(t *testing.T) {
	eng := newP0Engine()
	_ = eng.RegisterClass(p0Row{}, "t_p0")

	cases := map[string]func() error{
		"SaveIt":             func() error { return eng.SaveIt(nil) },
		"SaveItWhenNotExist": func() error { return eng.SaveItWhenNotExist(nil) },
		"Update":             func() error { return eng.Update(nil) },
		"Insert":             func() error { return eng.Insert(nil) },
		"InsertL":            func() error { return eng.InsertL(nil) },
		"Delete":             func() error { return eng.Delete(nil) },
		"CopyInL":            func() error { return eng.CopyInL(nil) },
		"InsertP":            func() error { return eng.InsertP(nil, 10) },
		"CopyInP":            func() error { return eng.CopyInP(nil, 10) },
		"Select":             func() error { return eng.Select(nil, "select * from t_p0") },
		"SelectL":            func() error { return eng.SelectL(nil, "select * from t_p0") },
		"SelectM":            func() error { return eng.SelectM(nil, "select * from t_p0") },
		"SelectH":            func() error { return eng.SelectH(nil, nil, "select * from t_p0") },
	}
	for name, call := range cases {
		//#recover 兜底断言:修复回归时先暴露 panic 而非误报错误文本
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s(nil) PANICKED: %v", name, r)
				}
			}()
			err := call()
			if !errors.Is(err, ErrNilParam) {
				t.Errorf("%s(nil) should return ErrNilParam, got: %v", name, err)
			}
		}()
	}
}

// #typed nil(带类型的空指针)同样统一拦截
func TestEntriesRejectTypedNil(t *testing.T) {
	eng := newP0Engine()

	if eror := eng.SaveIt((*p0Row)(nil)); !errors.Is(eror, ErrNilParam) {
		t.Errorf("SaveIt typed nil: %v", eror)
	}
	if eror := eng.SelectL((*[]p0Row)(nil), "select * from t_p0"); !errors.Is(eror, ErrNilParam) {
		t.Errorf("SelectL typed nil slice: %v", eror)
	}
}

// #Select 单 struct 目标必须传指针(否则逐列回写不可寻址)
func TestSelectRequiresPointer(t *testing.T) {
	eng := newP0Engine()
	_ = eng.RegisterClass(p0Row{}, "t_p0")

	if eror := eng.Select(p0Row{}, "select * from t_p0"); eror == nil || !strings.Contains(eror.Error(), "needs a pointer param") {
		t.Errorf("Select with value should demand pointer, got: %v", eror)
	}
}

// #RegisterClass:裸 nil 与非结构体返回带 LastError 的未注册表(链式约定),不 panic
func TestRegisterClassRejectsNonStruct(t *testing.T) {
	eng := newP0Engine()

	tblNil := eng.RegisterClass(nil, "t_nil")
	if tblNil == nil || !errors.Is(tblNil.LastError, ErrNilParam) {
		t.Errorf("RegisterClass(nil) should carry ErrNilParam in LastError, got: %+v", tblNil)
	}

	tblMap := eng.RegisterClass(map[string]int{}, "t_map")
	if tblMap == nil || tblMap.LastError == nil {
		t.Error("RegisterClass(map) should carry LastError")
	}

	tblPtr := eng.RegisterClass(&p0Row{}, "t_ptr")
	if tblPtr == nil || tblPtr.LastError == nil {
		t.Error("RegisterClass(pointer) should carry LastError")
	}

	//#非法类型不得进入注册表
	if tab := eng.GetTable("t_nil"); tab != nil {
		t.Error("failed registration must not enter registry")
	}
	//#合法注册不受影响
	if tbl := eng.RegisterClass(p0Row{}, "t_p0"); tbl.LastError != nil {
		t.Errorf("valid registration broken: %v", tbl.LastError)
	}
}

// #评审 P0 收尾:SelectL 非指针 / SelectM,H nil map 与非 string 键——7 条剩余 panic 路径
func TestSelectLRequiresPointer(t *testing.T) {
	eng := newP0Engine()
	_ = eng.RegisterClass(p0Row{}, "t_p0")

	if eror := eng.SelectL([]p0Row{}, "select * from t_p0"); eror == nil || !strings.Contains(eror.Error(), "needs a pointer param") {
		t.Errorf("SelectL with value slice should demand pointer, got: %v", eror)
	}
}

func TestSelectMHRejectsNilMap(t *testing.T) {
	eng := newP0Engine()
	_ = eng.RegisterClass(p0Row{}, "t_p0")

	//#直接 nil map
	var Direct map[string]p0Row
	if eror := eng.SelectM(Direct, "select * from t_p0"); eror == nil || !strings.Contains(eror.Error(), "nil map") {
		t.Errorf("SelectM(nil map) should error, got: %v", eror)
	}
	if eror := eng.SelectH(Direct, nil, "select * from t_p0"); eror == nil || !strings.Contains(eror.Error(), "nil map") {
		t.Errorf("SelectH(nil map) should error, got: %v", eror)
	}

	//#经指针间接的 nil map
	VarViaPtr := &Direct
	if eror := eng.SelectM(VarViaPtr, "select * from t_p0"); eror == nil || !strings.Contains(eror.Error(), "nil map") {
		t.Errorf("SelectM(ptr->nil map) should error, got: %v", eror)
	}
	if eror := eng.SelectH(VarViaPtr, nil, "select * from t_p0"); eror == nil || !strings.Contains(eror.Error(), "nil map") {
		t.Errorf("SelectH(ptr->nil map) should error, got: %v", eror)
	}
}

func TestSelectMHRequiresStringKey(t *testing.T) {
	eng := newP0Engine()
	_ = eng.RegisterClass(p0Row{}, "t_p0")

	if eror := eng.SelectM(map[int]p0Row{}, "select * from t_p0"); eror == nil || !strings.Contains(eror.Error(), "string key") {
		t.Errorf("SelectM(int-key map) should error, got: %v", eror)
	}
	if eror := eng.SelectH(map[int64]p0Row{}, nil, "select * from t_p0"); eror == nil || !strings.Contains(eror.Error(), "string key") {
		t.Errorf("SelectH(int64-key map) should error, got: %v", eror)
	}
}

// #正向对照:string 键 + 已初始化 map(直接与指针两种形态)正常工作
func TestSelectMHValidMapsStillWork(t *testing.T) {
	d := &mockDriver{columns: []string{"id", "name"}, queryRows: [][]driver.Value{{int64(1), "A"}}}
	eng := newP0Engine()
	eng.Db = sql.OpenDB(mockConnector{d: d})
	_ = eng.RegisterClass(p0Row{}, "t_p0")

	Direct := map[string]p0Row{}
	if eror := eng.SelectM(Direct, "select * from t_p0"); eror != nil || len(Direct) != 1 {
		t.Errorf("SelectM direct map: %v len=%d", eror, len(Direct))
	}

	VarViaPtr := map[string]p0Row{}
	if eror := eng.SelectH(&VarViaPtr, nil, "select * from t_p0"); eror != nil || len(VarViaPtr) != 1 {
		t.Errorf("SelectH via pointer: %v len=%d", eror, len(VarViaPtr))
	}
}

// #对抗性复查:Kind==String 只是种类近似——命名字符串键此前会 panic,map[any]T 此前被误杀
type MyKey string

func TestSelectMHNamedStringKeyWorks(t *testing.T) {
	d := &mockDriver{columns: []string{"id", "name"}, queryRows: [][]driver.Value{{int64(1), "A"}}}
	eng := newP0Engine()
	eng.Db = sql.OpenDB(mockConnector{d: d})
	_ = eng.RegisterClass(p0Row{}, "t_p0")

	Named := map[MyKey]p0Row{}
	if eror := eng.SelectM(Named, "select * from t_p0"); eror != nil {
		t.Fatalf("SelectM named-string key: %v", eror)
	}
	if _, Valid := Named["1"]; !Valid {
		t.Fatalf("named-string key should be written, got %+v", Named)
	}

	NamedH := map[MyKey]p0Row{}
	if eror := eng.SelectH(NamedH, nil, "select * from t_p0"); eror != nil {
		t.Fatalf("SelectH named-string key: %v", eror)
	}
	if _, Valid := NamedH[""]; !Valid {
		t.Fatalf("SelectH named-string key should be written (nil f → empty key), got %+v", NamedH)
	}
}

func TestSelectMHAnyKeyWorks(t *testing.T) {
	d := &mockDriver{columns: []string{"id", "name"}, queryRows: [][]driver.Value{{int64(2), "B"}}}
	eng := newP0Engine()
	eng.Db = sql.OpenDB(mockConnector{d: d})
	_ = eng.RegisterClass(p0Row{}, "t_p0")

	AnyKey := map[any]p0Row{}
	if eror := eng.SelectM(AnyKey, "select * from t_p0"); eror != nil {
		t.Fatalf("SelectM any key (string is assignable to any): %v", eror)
	}
	if _, Valid := AnyKey["2"]; !Valid {
		t.Fatalf("any key should be written, got %+v", AnyKey)
	}
}

func TestSelectMHRejectsNonStringAssignableKey(t *testing.T) {
	eng := newP0Engine()
	_ = eng.RegisterClass(p0Row{}, "t_p0")

	//#非 string 且非命名 string(如 int)仍报错,消息含 assignable 语义
	if eror := eng.SelectM(map[int]p0Row{}, "select * from t_p0"); eror == nil || !strings.Contains(eror.Error(), "assignable from string") {
		t.Errorf("SelectM(int key) should error, got: %v", eror)
	}
	if eror := eng.SelectH(map[float64]p0Row{}, nil, "select * from t_p0"); eror == nil || !strings.Contains(eror.Error(), "assignable from string") {
		t.Errorf("SelectH(float64 key) should error, got: %v", eror)
	}
}

// ---------------------------------------------------------------------------
// 评审加固:注册 API 标识符白名单(表名/字段名/tag 列名)
// 运行时传入的名字会被直接拼进生成的 SQL(CRUD 表名不引号、MySQL/Oracle 族列名亦不引号),
// 故与 Write/Exist* 路径同样做 validIdent 校验:非法输入不进注册表、不 panic。
// ---------------------------------------------------------------------------

func TestRegisterRejectsInvalidIdentifiers(t *testing.T) {
	type regBadTag struct {
		Id  int64 `db:"id"`
		Bad int64 `db:"a;drop table w"`
	}
	type regOk struct {
		Id   int64  `db:"id"`
		Name string `db:"name"`
	}

	engine := &TUniEngine{ColLabel: "db", ColParam: "?", Provider: DtMYSQLN}
	engine.Initialize()

	cases := []struct {
		name     string
		sentinel error
		call     func() error
	}{
		{"RegisterTable", ErrInvalidTableName, func() error { return engine.RegisterTable("t;drop table x", 0).LastError }},
		{"RegisterField(table)", ErrInvalidTableName, func() error { return engine.RegisterField("t;drop table x", "id").LastError }},
		{"RegisterField(field)", ErrInvalidFieldName, func() error { return engine.RegisterField("reg_ok", "a;drop table y").LastError }},
		{"RegisterPkeys(field)", ErrInvalidFieldName, func() error { return engine.RegisterPkeys("reg_ok", "a--b").LastError }},
		{"RegisterClass(table)", ErrInvalidTableName, func() error { return engine.RegisterClass(regOk{}, "t;drop table z").LastError }},
		{"RegisterClass(tag)", ErrInvalidFieldName, func() error { return engine.RegisterClass(regBadTag{}, "reg_tag").LastError }},
	}
	for _, c := range cases {
		if eror := c.call(); !errors.Is(eror, c.sentinel) {
			t.Errorf("%s 应返回 %v, got: %v", c.name, c.sentinel, eror)
		}
	}

	//#非法注册不得进入注册表,PrepareRunSQL 亦不应产出可注入 SQL
	if tbl := engine.GetTable("t;drop table x"); tbl != nil {
		t.Error("非法表名不得进入注册表")
	}
	if sql, _, _, _ := engine.PrepareRunSQL("t;drop table x", EtInsert); sql != "" {
		t.Errorf("非法表名的 PrepareRunSQL 应为空, got: %q", sql)
	}

	//#只读标记 db:"-" / db:"_" 是标记而非列名,不得被白名单误杀
	type regReadonly struct {
		Id   int64  `db:"id"`
		Code string `db:"-"`
		Alt  string `db:"_"`
	}
	if eror := engine.RegisterClass(regReadonly{}, "reg_ro").LastError; eror != nil {
		t.Errorf(`db:"-"/db:"_" 不应被标识符校验拒绝, got: %v`, eror)
	}

	//#正向对照:合法注册与既有链式行为不受影响
	engine.RegisterTable("reg_ok", 0)
	engine.RegisterField("reg_ok", "id")
	engine.RegisterField("reg_ok", "name")
	engine.RegisterPkeys("reg_ok", "id")
	engine.PrepareTables("reg_ok")
	sql, _, _, eror := engine.PrepareRunSQL("reg_ok", EtInsert)
	if eror != nil || !strings.Contains(sql, "insert into reg_ok") {
		t.Fatalf("合法注册受影响: sql=%q err=%v", sql, eror)
	}
}

// #RegisterPkeys 在表未登记时曾返回 nil,链式 .SetKeys() 会 nil 解引用 panic
func TestRegisterPkeysMissingTableNoPanic(t *testing.T) {
	engine := &TUniEngine{ColLabel: "db", ColParam: "$", Provider: DtPOSTGR}
	engine.Initialize()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RegisterPkeys 链式调用不得 panic, got: %v", r)
		}
	}()

	tbl := engine.RegisterPkeys("no_such_table", "id")
	if tbl == nil {
		t.Fatal("RegisterPkeys 不得返回 nil(链式约定要求非 nil 载体)")
	}
	if !errors.Is(tbl.LastError, ErrUnregisteredClass) {
		t.Errorf("期望 ErrUnregisteredClass, got: %v", tbl.LastError)
	}
	if eror := tbl.SetKeys("id").LastError; eror == nil {
		t.Error("链式 SetKeys 应在 LastError 上短路,而非 panic 或静默通过")
	}
}
