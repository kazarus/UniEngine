package UniEngine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
)

/*
  when write data to struct, should be ptr;
  when read data from struct, whoever, ptr or struct;
*/

// TUniEngine is a multi-database ORM-like engine built on database/sql.
//
// Concurrency: TUniEngine is safe for concurrent use. Query/write paths hold
// the registry RWMutex only for the duration of a lookup (class registry /
// current transaction), and Register* may run alongside queries. Two
// exceptions:
//   - while a transaction is active (between Begin and Commit/Cancel), calls
//     must be serialized — database/sql's *sql.Tx is not safe for concurrent
//     use, and every statement is routed to that transaction;
//   - config fields (ColLabel/ColParam/Provider/SecretOn/SecretBy/...) and
//     table-structure edits (SetKeys/SetSecret/AutoKeys/PrepareTables/
//     PrepareRunSQL) should complete before concurrent queries begin.
type TUniEngine struct {
	Db *sql.DB

	mu       *sync.RWMutex //#保护 HashTabl 与事务状态（指针：按值传递引擎时共享同一把锁）
	ColLabel string        //#字段字号
	ColParam string        //#参数符号
	HashTabl map[string]*TUniTable

	SecretOn int64  //#开启敏感信息加密
	SecretBy string //#敏感信息加密密钥

	SecretHook TSecretHook //#应用加密钩子#为空时使用内置AES-256-GCM

	Instance string     //#数据库实例
	DataBase string     //#数据库名称
	DataUser string     //#数据库用户
	Provider TDriveType //#数据库驱动
	Supplier TDriveType //#数据库驱动(区分 DtORACLE VS DtDAMENG)

	tx       *sql.Tx //#当前事务(Begin 之后非 nil)
	inTx     bool    //#事务进行中(期间调用须串行)
	runDebug int32   //#SQL调试输出开关#原子读写,可运行中切换
}

// muLazy 保护各引擎 mu 的懒初始化（仅覆盖建锁窗口，不护业务临界区）
var muLazy sync.Mutex

// initLock 保证 self.mu 已创建（并发安全；供 Initialize 与 lockTables 调用）
func (self *TUniEngine) initLock() {

	muLazy.Lock()
	defer muLazy.Unlock()

	if self.mu == nil {
		self.mu = &sync.RWMutex{}
	}
}

// lockTables 保证锁可用并加写锁（注册/表结构变更/事务状态变更）
func (self *TUniEngine) lockTables() {

	self.initLock()
	self.mu.Lock()
}

// rlockTables 保证锁可用并加读锁（注册表/事务状态查询）
func (self *TUniEngine) rlockTables() {

	self.initLock()
	self.mu.RLock()
}

// debugging 返回调试输出开关状态（原子读，可运行中切换）
func (self *TUniEngine) debugging() bool {
	return atomic.LoadInt32(&self.runDebug) != 0
}

// tableByType 按类全名取注册表条目（读锁；未注册返回 nil）
func (self *TUniEngine) tableByType(TypeName string) *TUniTable {

	self.rlockTables()
	defer self.mu.RUnlock()

	return self.HashTabl[TypeName]
}

// tableByName 按表名取注册表条目（读锁；未注册返回 nil）
func (self *TUniEngine) tableByName(TableName string) *TUniTable {

	self.rlockTables()
	defer self.mu.RUnlock()

	return self.HashTabl[strings.ToLower(TableName)]
}

// currentTx 返回当前事务（无事务时 nil）；读锁保护，与 Begin/Commit/Cancel 互斥
func (self *TUniEngine) currentTx() *sql.Tx {

	self.rlockTables()
	defer self.mu.RUnlock()

	if self.inTx {
		return self.tx
	}

	return nil
}

// validIdent 校验数据库标识符仅含安全字符（字母/数字/下划线/点），用于表名/字段名拼接前防注入
func validIdent(name string) bool {

	if name == "" {
		return false
	}

	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '.') {
			return false
		}
	}

	return true
}

func (self *TUniEngine) getValParam(aIndex int) string {

	if self.Provider == DtMYSQLN {
		return fmt.Sprintf("%s", self.ColParam)
	}

	return fmt.Sprintf("%s%d", self.ColParam, aIndex)
}

func (self *TUniEngine) getColParam(FieldName string) string {

	if self.Provider == DtMYSQLN {
		return fmt.Sprintf("%s", FieldName)
	}

	if self.Provider == DtORACLE {
		return fmt.Sprintf("%s", FieldName)
	}

	return "\"" + FieldName + "\""
}

// 参数占位符模式：$1 / $2 ...（Oracle 转 :1，MySQL 转 ?）
var reParamPlaceholder = regexp.MustCompile(`\$\d+`)

func (self *TUniEngine) getSqlQuery(SqlQuery string, args ...interface{}) string {

	if self.Provider == DtORACLE && len(args) > 0 {

		if self.debugging() {
			fmt.Println(`UniEngine: Oracle驱动时,替换"$"到":"`)
		}

		// 仅替换参数占位符（$1 -> :1），避免误改 SQL 文本中其它 $ 字符
		SqlQuery = reParamPlaceholder.ReplaceAllStringFunc(SqlQuery, func(m string) string {
			return ":" + m[1:]
		})
	}

	if self.Provider == DtMYSQLN && len(args) > 0 {

		if self.debugging() {
			fmt.Println(`UniEngine: MySQL驱动时,替换"$"到"?"`)
		}

		// 包级预编译正则，消除每次调用 Compile 的开销与静默失败返回空串的路径
		SqlQuery = reParamPlaceholder.ReplaceAllString(SqlQuery, "?")
	}

	return SqlQuery
}

// debugSQL 统一的 SQL 调试输出（runDebug 开启时）
func (self *TUniEngine) debugSQL(Kind string, SqlQuery interface{}, args interface{}) {

	if !self.debugging() {
		return
	}

	fmt.Printf("UniEngine: %s.sql: %v\n", Kind, SqlQuery)
	fmt.Printf("UniEngine: %s.val: %v\n", Kind, args)
}

func (self *TUniEngine) ProviderName() string {

	var result string

	switch self.Provider {
	case DtORACLE:
		{
			result = "oracle"
		}
	case DtSQLSRV:
		{
			result = "sqlserver"
		}
	case DtPOSTGR:
		{
			result = "postgresql"
		}
	case DtMYSQLN:
		{
			result = "mysql"
		}
	}

	return result
}

// SpecialPageSize 返回调用方请求的分页大小；非正值（0/负数）回退 DefaultPageSize。
func (self *TUniEngine) SpecialPageSize(aPageSize int64) int64 {

	if aPageSize <= 0 {
		return self.DefaultPageSize()
	}

	return aPageSize
}

func (self *TUniEngine) DefaultPageSize() int64 {

	switch self.Provider {
	case DtORACLE, DtPOSTGR:
		{
			return 99
		}
	case DtSQLSRV, DtMYSQLN:
		{
			return 10
		}
	}

	return 0
}

func (self *TUniEngine) RegisterClass(aClass interface{}, TableName string) *TUniTable {

	self.lockTables()
	defer self.mu.Unlock()

	if self.HashTabl == nil {
		self.HashTabl = make(map[string]*TUniTable, 0)
	}

	t := reflect.TypeOf(aClass)
	n := t.NumField()

	var UniTable = &TUniTable{}
	UniTable.HashField = make(map[string]TUniField, 0)
	UniTable.HashPkeys = make(map[string]TUniField, 0)
	UniTable.TableName = TableName

	for i := 0; i < n; i++ {

		f := t.Field(i)

		var UniField = TUniField{}
		UniField.AttriName = f.Name

		UniField.initialize(f.Tag.Get(self.ColLabel))

		UniTable.HashField[strings.ToLower(UniField.FieldName)] = UniField
		// 保留 struct 声明顺序，供 INSERT/UPDATE 列序生成
		UniTable.ListField = append(UniTable.ListField, UniField)
	}

	// 同时以小写表名（PrepareTables/PrepareRunSQL 路径）与类全名（SaveIt/Insert/Delete/Select* 路径）注册，
	// 统一两套 key 的可见性；两者指向同一表
	self.HashTabl[strings.ToLower(TableName)] = UniTable
	self.HashTabl[t.String()] = UniTable

	return UniTable
}

func (self *TUniEngine) RegisterTable(TableName string, IPriority int64) *TUniTable {

	self.lockTables()
	defer self.mu.Unlock()

	if self.HashTabl == nil {
		self.HashTabl = make(map[string]*TUniTable, 0)
	}

	UniTable, Valid := self.HashTabl[strings.ToLower(TableName)]
	if !Valid {
		UniTable = &TUniTable{}
		UniTable.HashField = make(map[string]TUniField, 0)
		UniTable.HashPkeys = make(map[string]TUniField, 0)
		UniTable.TableName = strings.ToLower(TableName)
	}
	UniTable.IPriority = IPriority

	self.HashTabl[strings.ToLower(TableName)] = UniTable

	return UniTable
}

func (self *TUniEngine) RegisterField(TableName string, FieldName string) *TUniTable {

	self.lockTables()
	defer self.mu.Unlock()

	if self.HashTabl == nil {
		self.HashTabl = make(map[string]*TUniTable, 0)
	}

	UniTable, Valid := self.HashTabl[strings.ToLower(TableName)]
	if !Valid {
		UniTable = &TUniTable{}
		UniTable.HashField = make(map[string]TUniField, 0)
		UniTable.HashPkeys = make(map[string]TUniField, 0)
		UniTable.TableName = strings.ToLower(TableName)
	}

	var UniField = TUniField{}
	UniField.AttriName = ""
	UniField.FieldName = strings.ToLower(FieldName)
	UniField.TableName = strings.ToLower(TableName)

	UniTable.HashField[strings.ToLower(UniField.FieldName)] = UniField

	self.HashTabl[strings.ToLower(TableName)] = UniTable

	return UniTable
}

func (self *TUniEngine) RegisterPkeys(TableName string, FieldName string) *TUniTable {

	self.lockTables()
	defer self.mu.Unlock()

	if self.HashTabl == nil {
		self.HashTabl = make(map[string]*TUniTable, 0)
	}

	UniTable, Valid := self.HashTabl[strings.ToLower(TableName)]
	if Valid {
		var UniField = TUniField{}
		UniField.AttriName = ""
		UniField.FieldName = strings.ToLower(FieldName)
		UniField.TableName = strings.ToLower(TableName)

		UniTable.HashPkeys[strings.ToLower(UniField.FieldName)] = UniField

		self.HashTabl[strings.ToLower(TableName)] = UniTable
	}

	return UniTable
}

func (self *TUniEngine) GetTable(TableName string) *TUniTable {

	return self.tableByName(TableName)
}

func (self *TUniEngine) PrepareTables(TableName string) error {

	self.lockTables()
	defer self.mu.Unlock()

	UniTable, Valid := self.HashTabl[strings.ToLower(TableName)]
	if !Valid {
		//#未注册的表静默跳过,保持旧行为
		return nil
	}

	//#按确定序重建(ListField 既有序优先,其余按字段名排序),避免 map 迭代序导致 SQL 文本漂移
	ListField := make([]TUniField, 0, len(UniTable.HashField))
	for _, ItemPara := range UniTable.orderedFields() {
		if ItemPara.ReadOnly {
			continue
		}
		ListField = append(ListField, ItemPara)
	}
	UniTable.ListField = ListField

	ListPkeys := make([]TUniField, 0, len(UniTable.HashPkeys))
	for _, ItemPara := range UniTable.orderedPkeys() {
		if ItemPara.ReadOnly {
			continue
		}
		ListPkeys = append(ListPkeys, ItemPara)
	}
	UniTable.ListPkeys = ListPkeys

	return nil
}

func (self *TUniEngine) PrepareRunSQL(TableName string, QueryType TQueryType) (string, []TUniField, []TUniField, error) {

	self.lockTables()
	defer self.mu.Unlock()

	var SqlResult string
	var ListField = make([]TUniField, 0)

	UniTable, Valid := self.HashTabl[strings.ToLower(TableName)]
	if !Valid {
		//#未注册的表:返回空结果(旧代码在此对 nil 指针取 ListPkeys 会 panic)
		return SqlResult, ListField, nil, nil
	}

	switch QueryType {

	case EtSelect:
		{
			var SqlWhere []string
			ColIndex := 1
			for _, ItemPara := range UniTable.orderedPkeys() {
				if ItemPara.ReadOnly {
					continue
				}
				SqlWhere = append(SqlWhere, fmt.Sprintf("    and %s=%s", self.getColParam(ItemPara.FieldName), self.getValParam(ColIndex)))
				ColIndex = ColIndex + 1
			}

			if len(UniTable.HashPkeys) > 0 {
				SqlResult = fmt.Sprintf("where 1=1 %s", strings.Join(SqlWhere, ""))
				UniTable.SqlSelect = SqlResult
			}
		}
	case EtInsert:
		{
			var colList []string
			var paramList []string
			ColIndex := 1
			for _, ItemPara := range UniTable.orderedFields() {
				if ItemPara.ReadOnly {
					continue
				}
				colList = append(colList, self.getColParam(ItemPara.FieldName))
				paramList = append(paramList, self.getValParam(ColIndex))
				ColIndex = ColIndex + 1

				ListField = append(ListField, ItemPara)
			}

			if len(colList) > 0 {
				SqlResult = fmt.Sprintf("insert into %s ( %s ) values ( %s ) ", UniTable.TableName, strings.Join(colList, ","), strings.Join(paramList, ","))
				UniTable.SqlInsert = SqlResult
			}
		}
	case EtUpdate:
		{
			var setList []string
			var keyList []string
			ColIndex := 1
			for _, ItemPara := range UniTable.orderedFields() {
				if ItemPara.ReadOnly {
					continue
				}

				if _, valid := UniTable.HashPkeys[strings.ToLower(ItemPara.FieldName)]; valid {
					ItemPara.PkeyOnly = true
					ListField = append(ListField, ItemPara)
					continue
				}

				setList = append(setList, self.getColParam(ItemPara.FieldName)+"="+self.getValParam(ColIndex))
				ColIndex = ColIndex + 1

				ListField = append(ListField, ItemPara)
			}

			for _, ItemPara := range UniTable.orderedPkeys() {
				keyList = append(keyList, self.getColParam(ItemPara.FieldName)+"="+self.getValParam(ColIndex))
				ColIndex = ColIndex + 1
			}

			if len(setList) == 0 {
				return "", ListField, UniTable.ListPkeys, fmt.Errorf("UniEngine: no updatable column for table:%s", UniTable.TableName)
			}

			SqlResult = fmt.Sprintf("update %s set %s where 1=1 and %s", UniTable.TableName, strings.Join(setList, ","), strings.Join(keyList, " and "))
			UniTable.SqlUpdate = SqlResult
		}
	}

	return SqlResult, ListField, UniTable.ListPkeys, nil
}

// ---------------------------------------------------------------------------
// 查询公共核心
// ---------------------------------------------------------------------------

// queryScalarCtx 标量查询公共核心：执行并取最后一行的单列值（SelectD/F/S 共用）。
func (self *TUniEngine) queryScalarCtx(ctx context.Context, SqlQuery string, dst sql.Scanner, args []interface{}) error {

	SqlQuery = self.getSqlQuery(SqlQuery, args...)

	self.debugSQL("select", SqlQuery, args)

	st, eror := self.prepareCtx(ctx, SqlQuery)
	if eror != nil {
		return eror
	}
	defer st.Close()

	rows, eror := st.QueryContext(ctx, args...)
	if eror != nil {
		return eror
	}
	defer rows.Close()

	for rows.Next() {
		if eror = rows.Scan(dst); eror != nil {
			return eror
		}
	}

	//#rows.Next() 因错误提前结束(而非读完)时必须上抛,否则错误被静默吞掉、数据表现为截断
	return rows.Err()
}

// queryRowsCtx 行查询公共核心：逐行解码后交给 sink 写入目标容器
// （单个 struct / 切片 / map 由调用方决定）。HasSetSqlResult 钩子类的探测结果由
// 调用方传入,保持各方法原有的钩子识别语义。
func (self *TUniEngine) queryRowsCtx(ctx context.Context, elemType reflect.Type, hasHook bool, SqlQuery string, sink func(reflect.Value) error, args ...interface{}) error {

	SqlQuery = self.getSqlQuery(SqlQuery, args...)

	TablName := elemType.String()
	UniTable := self.tableByType(TablName)
	if UniTable == nil {
		return fmt.Errorf("UniEngine: no such class registered: %s", TablName)
	}

	self.debugSQL("select", SqlQuery, args)

	st, eror := self.prepareCtx(ctx, SqlQuery)
	if eror != nil {
		return eror
	}
	defer st.Close()

	rows, eror := st.QueryContext(ctx, args...)
	if eror != nil {
		return eror
	}
	defer rows.Close()

	column, eror := rows.Columns()
	if eror != nil {
		return eror
	}
	cCount := len(column)
	fields := make([]interface{}, cCount)
	values := make([]interface{}, cCount)

	for rows.Next() {

		u := reflect.New(elemType)

		if hasHook {
			for i := 0; i < cCount; i++ {
				values[i] = &fields[i]
			}

			if eror = rows.Scan(values...); eror != nil {
				return eror
			}

			x := u.Interface().(HasSetSqlResult)
			x.SetSqlResult(self, u.Interface(), column, fields)
		} else {
			elem := u.Elem()
			for ColIndex, ItemPara := range column {
				UniField, Valid := UniTable.HashField[strings.ToLower(ItemPara)]
				if !Valid {
					return fmt.Errorf("UniEngine: database have field[%s], but not in class[%s]", ItemPara, elemType.String())
				}
				values[ColIndex] = elem.FieldByName(UniField.AttriName).Addr().Interface()
			}

			if eror = rows.Scan(values...); eror != nil {
				return eror
			}

			if eror = self.DecryptResult(&elem, UniTable, column); eror != nil {
				return eror
			}
		}

		if eror = sink(u.Elem()); eror != nil {
			return eror
		}
	}

	//#rows.Next() 因错误提前结束(而非读完)时必须上抛,否则错误被静默吞掉、数据表现为截断
	return rows.Err()
}

// return int64;
func (self *TUniEngine) SelectD(SqlQuery string, args ...interface{}) (int64, error) {
	return self.SelectDCtx(context.Background(), SqlQuery, args...)
}

func (self *TUniEngine) SelectDCtx(ctx context.Context, SqlQuery string, args ...interface{}) (int64, error) {

	var size sql.NullInt64
	if eror := self.queryScalarCtx(ctx, SqlQuery, &size, args); eror != nil {
		return 0, eror
	}

	return size.Int64, nil
}

// return float64;
func (self *TUniEngine) SelectF(SqlQuery string, args ...interface{}) (float64, error) {
	return self.SelectFCtx(context.Background(), SqlQuery, args...)
}

func (self *TUniEngine) SelectFCtx(ctx context.Context, SqlQuery string, args ...interface{}) (float64, error) {

	var size sql.NullFloat64
	if eror := self.queryScalarCtx(ctx, SqlQuery, &size, args); eror != nil {
		return 0, eror
	}

	return size.Float64, nil
}

// return string;
func (self *TUniEngine) SelectS(SqlQuery string, args ...interface{}) (string, error) {
	return self.SelectSCtx(context.Background(), SqlQuery, args...)
}

func (self *TUniEngine) SelectSCtx(ctx context.Context, SqlQuery string, args ...interface{}) (string, error) {

	var text sql.NullString
	if eror := self.queryScalarCtx(ctx, SqlQuery, &text, args); eror != nil {
		return "", eror
	}

	return text.String, nil
}

// return struct;查询返回多行时报错(旧版静默保留最后一行),需要多行请用 SelectL。
func (self *TUniEngine) Select(i interface{}, SqlQuery string, args ...interface{}) error {
	return self.SelectCtx(context.Background(), i, SqlQuery, args...)
}

func (self *TUniEngine) SelectCtx(ctx context.Context, i interface{}, SqlQuery string, args ...interface{}) error {

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Struct {
		return errors.New("UniEngine: method [Select] only returns a struct; may be you should try [SelectL]")
	}

	var Result = reflect.Indirect(reflect.ValueOf(i))

	//#钩子探测保持旧语义:以传入值本身是否实现 HasSetSqlResult 为准
	_, hasHook := i.(HasSetSqlResult)

	rowCount := 0
	return self.queryRowsCtx(ctx, t, hasHook, SqlQuery, func(row reflect.Value) error {
		rowCount++
		if rowCount > 1 {
			return errors.New("UniEngine: method [Select] expects a single row, but the query returned more; use [SelectL]")
		}
		Result.Set(row)
		return nil
	}, args...)
}

// return slice of struct;
func (self *TUniEngine) SelectL(i interface{}, SqlQuery string, args ...interface{}) error {
	return self.SelectLCtx(context.Background(), i, SqlQuery, args...)
}

func (self *TUniEngine) SelectLCtx(ctx context.Context, i interface{}, SqlQuery string, args ...interface{}) error {

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Slice {
		return errors.New("UniEngine: method [SelectL] needs a slice param; may be you should try [Select]")
	}
	t = t.Elem()

	var Result = reflect.Indirect(reflect.ValueOf(i))

	return self.queryRowsCtx(ctx, t, t.Implements(THasSetSqlResult), SqlQuery, func(row reflect.Value) error {
		Result.Set(reflect.Append(Result, row))
		return nil
	}, args...)
}

// return map of struct;user;GetMapUnique;
func (self *TUniEngine) SelectM(i interface{}, SqlQuery string, args ...interface{}) error {
	return self.SelectMCtx(context.Background(), i, SqlQuery, args...)
}

func (self *TUniEngine) SelectMCtx(ctx context.Context, i interface{}, SqlQuery string, args ...interface{}) error {

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Map {
		return errors.New("UniEngine: method [SelectM] needs a map param; may be you should try [SelectL]")
	}
	t = t.Elem()

	if !t.Implements(THasGetMapUnique) {
		return fmt.Errorf("UniEngine: the class registered:[%s] does not Implemented [HasGetMapUnique]", t.String())
	}

	var Result = reflect.Indirect(reflect.ValueOf(i))

	return self.queryRowsCtx(ctx, t, t.Implements(THasSetSqlResult), SqlQuery, func(row reflect.Value) error {
		MapUnique := row.Interface().(HasGetMapUnique).GetMapUnique()
		Result.SetMapIndex(reflect.ValueOf(MapUnique), row)
		return nil
	}, args...)
}

// return map;use custom function;
func (self *TUniEngine) SelectH(i interface{}, f GetMapUnique, SqlQuery string, args ...interface{}) error {
	return self.SelectHCtx(context.Background(), i, f, SqlQuery, args...)
}

func (self *TUniEngine) SelectHCtx(ctx context.Context, i interface{}, f GetMapUnique, SqlQuery string, args ...interface{}) error {

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Map {
		return errors.New("UniEngine: method [SelectH] needs a map param; may be you should try [SelectL]")
	}
	t = t.Elem()

	var Result = reflect.Indirect(reflect.ValueOf(i))

	return self.queryRowsCtx(ctx, t, t.Implements(THasSetSqlResult), SqlQuery, func(row reflect.Value) error {
		var MapUnique string
		if f != nil {
			MapUnique = f(row.Interface())
		}
		Result.SetMapIndex(reflect.ValueOf(MapUnique), row)
		return nil
	}, args...)
}

// ---------------------------------------------------------------------------
// 写入公共前置
// ---------------------------------------------------------------------------

// resolveTarget 统一写方法的公共前置：解析可选的表名参数(TableName[0],空切片时用
// 注册表中的默认表名)、反射取元素类型并查注册表、标识符校验。
// wantKind 指定期望的容器种类(Insert 传 Struct,InsertL/CopyInL 传 Slice,
// 无需检查传 reflect.Invalid);needPkeys 要求类已注册主键。
func (self *TUniEngine) resolveTarget(Method string, i interface{}, TableName []string, wantKind reflect.Kind, needPkeys bool) (*TUniTable, reflect.Value, string, error) {

	var TablName string
	if len(TableName) > 0 {
		TablName = TableName[0]
	}

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if wantKind == reflect.Slice {
		if t.Kind() != reflect.Slice {
			return nil, reflect.Value{}, "", fmt.Errorf("UniEngine: method [%s] needs a slice param; check your code;", Method)
		}
		t = t.Elem()
	} else if wantKind == reflect.Struct && t.Kind() != reflect.Struct {
		return nil, reflect.Value{}, "", fmt.Errorf("UniEngine: method [%s] only returns a struct; may be you should try [InsertL]", Method)
	}

	UniTable := self.tableByType(t.String())
	if UniTable == nil {
		return nil, reflect.Value{}, "", fmt.Errorf("UniEngine: no such class registered: %s", t.String())
	}
	if needPkeys && len(UniTable.HashPkeys) == 0 {
		return nil, reflect.Value{}, "", fmt.Errorf("UniEngine: no pkeys column in class registered: %s", t.String())
	}

	if TablName == "" {
		TablName = UniTable.TableName
	}

	if !validIdent(TablName) {
		return nil, reflect.Value{}, "", errors.New("UniEngine: invalid table name: " + TablName)
	}

	return UniTable, reflect.Indirect(reflect.ValueOf(i)), TablName, nil
}

// ---------------------------------------------------------------------------
// SaveIt / SaveItWhenNotExist:方言原生 UPSERT + count-then-dispatch 回退
// ---------------------------------------------------------------------------

const (
	upsertModeUpdate = iota //#存在则更新,否则插入(SaveIt)
	upsertModeSkip          //#存在则跳过,否则插入(SaveItWhenNotExist)
)

// upsertStmt 按方言生成原生 UPSERT 语句,消除 SaveIt 两步(count 后写入)之间的并发窗口。
// 参数只传一遍(列序:keys 在前,cols 在后)。返回 ok=false 表示当前方言不支持,
// 调用方回退 count-then-dispatch。
//
// 注意:MySQL 不在此列——ON DUPLICATE KEY UPDATE 由任一唯一键触发而非仅主键,
// 与 SaveIt 的主键语义不等价,故 MySQL 一律回退旧路径。
func (self *TUniEngine) upsertStmt(Mode int, TablName string, keys, cols []TUniField) (string, bool) {

	if len(keys) == 0 {
		return "", false
	}

	var colList []string
	var paramList []string
	ColIndex := 1
	for _, list := range [][]TUniField{keys, cols} {
		for _, ItemPara := range list {
			colList = append(colList, self.getColParam(ItemPara.FieldName))
			paramList = append(paramList, self.getValParam(ColIndex))
			ColIndex++
		}
	}

	var keyList []string
	for _, ItemPara := range keys {
		keyList = append(keyList, self.getColParam(ItemPara.FieldName))
	}

	updateMode := Mode == upsertModeUpdate && len(cols) > 0

	switch self.Provider {

	case DtPOSTGR:
		{
			//#PG:excluded 伪表复用本次插入值,无需重复传参
			if !updateMode {
				return fmt.Sprintf("insert into %s ( %s ) values ( %s ) on conflict ( %s ) do nothing",
					TablName, strings.Join(colList, ","), strings.Join(paramList, ","), strings.Join(keyList, ",")), true
			}

			var setList []string
			for _, ItemPara := range cols {
				SqlCol := self.getColParam(ItemPara.FieldName)
				setList = append(setList, SqlCol+"=excluded."+SqlCol)
			}

			return fmt.Sprintf("insert into %s ( %s ) values ( %s ) on conflict ( %s ) do update set %s",
				TablName, strings.Join(colList, ","), strings.Join(paramList, ","), strings.Join(keyList, ","), strings.Join(setList, ",")), true
		}

	case DtORACLE:
		{
			//#Oracle:MERGE,源数据放 dual 子查询,参数只传一遍
			var selectList []string
			ColIndex = 1
			for _, list := range [][]TUniField{keys, cols} {
				for _, ItemPara := range list {
					selectList = append(selectList, fmt.Sprintf("%s %s", self.getValParam(ColIndex), ItemPara.FieldName))
					ColIndex++
				}
			}

			var onList []string
			for _, ItemPara := range keys {
				onList = append(onList, "t."+ItemPara.FieldName+"=src."+ItemPara.FieldName)
			}

			var insertVals []string
			for _, list := range [][]TUniField{keys, cols} {
				for _, ItemPara := range list {
					insertVals = append(insertVals, "src."+ItemPara.FieldName)
				}
			}

			cSQL := fmt.Sprintf("merge into %s t using (select %s from dual) src on (%s)",
				TablName, strings.Join(selectList, ","), strings.Join(onList, " and "))

			if updateMode {
				var setList []string
				for _, ItemPara := range cols {
					setList = append(setList, "t."+ItemPara.FieldName+"=src."+ItemPara.FieldName)
				}
				cSQL = cSQL + " when matched then update set " + strings.Join(setList, ",")
			}

			return cSQL + " when not matched then insert ( " + strings.Join(colList, ",") + " ) values ( " + strings.Join(insertVals, ",") + " )", true
		}

	case DtSQLSRV:
		{
			//#SQLServer:MERGE + HOLDLOCK,表值构造器传参一遍
			var onList []string
			for _, ItemPara := range keys {
				SqlCol := self.getColParam(ItemPara.FieldName)
				onList = append(onList, "t."+SqlCol+"=src."+SqlCol)
			}

			var insertVals []string
			for _, list := range [][]TUniField{keys, cols} {
				for _, ItemPara := range list {
					insertVals = append(insertVals, "src."+self.getColParam(ItemPara.FieldName))
				}
			}

			cSQL := fmt.Sprintf("merge into %s with (holdlock) as t using (values ( %s )) as src ( %s ) on (%s)",
				TablName, strings.Join(paramList, ","), strings.Join(colList, ","), strings.Join(onList, " and "))

			if updateMode {
				var setList []string
				for _, ItemPara := range cols {
					SqlCol := self.getColParam(ItemPara.FieldName)
					setList = append(setList, "t."+SqlCol+"=src."+SqlCol)
				}
				cSQL = cSQL + " when matched then update set " + strings.Join(setList, ",")
			}

			return cSQL + " when not matched then insert ( " + strings.Join(colList, ",") + " ) values ( " + strings.Join(insertVals, ",") + " );", true
		}
	}

	return "", false
}

// canNativeSave 判断该类是否可走方言原生 UPSERT:任一自定义 SQL 钩子
// (GetSqlUpdate/GetSqlInsert/SetSqlValues)存在时,自定义语句无法转为 UPSERT,回退旧路径。
func canNativeSave(v reflect.Value) bool {

	if _, ok := v.Interface().(HasGetSqlUpdate); ok {
		return false
	}
	if _, ok := v.Interface().(HasGetSqlInsert); ok {
		return false
	}
	if _, ok := v.Interface().(HasSetSqlValues); ok {
		return false
	}

	return true
}

// saveUpsert 方言原生 UPSERT 执行(keys/cols 按确定序收集,参数只传一遍)。
// 返回 done=false 表示方言不支持,调用方回退 count-then-dispatch。
func (self *TUniEngine) saveUpsert(ctx context.Context, UniTable *TUniTable, v reflect.Value, TablName string, Mode int) (bool, error) {

	keys := UniTable.pkeyFields()

	var cols []TUniField
	for _, ItemPara := range UniTable.writableFields() {
		if _, valid := UniTable.HashPkeys[strings.ToLower(ItemPara.FieldName)]; valid {
			continue
		}
		cols = append(cols, ItemPara)
	}

	if len(keys) == 0 {
		return false, nil
	}

	cSQL, ok := self.upsertStmt(Mode, TablName, keys, cols)
	if !ok {
		return false, nil
	}

	values := make([]interface{}, 0, len(keys)+len(cols))
	for _, list := range [][]TUniField{keys, cols} {
		for _, ItemPara := range list {
			if ItemPara.Encrypt {
				Value, eror := self.secretEncrypt(v.FieldByName(ItemPara.AttriName))
				if eror != nil {
					return false, eror
				}
				values = append(values, Value)
				continue
			}
			values = append(values, v.FieldByName(ItemPara.AttriName).Interface())
		}
	}

	self.debugSQL("upsert", cSQL, values)

	st, eror := self.prepareCtx(ctx, cSQL)
	if eror != nil {
		return false, eror
	}
	defer st.Close()

	if _, eror := st.ExecContext(ctx, values...); eror != nil {
		return false, fmt.Errorf("UniEngine: upsert fail: %w", eror)
	}

	return true, nil
}

// countByPkeys 按主键统计存在行数(count-then-dispatch 回退路径的探测语句)。
func (self *TUniEngine) countByPkeys(ctx context.Context, UniTable *TUniTable, v reflect.Value, TablName string) (int64, error) {

	var keyCols []string
	SqlValue := make([]interface{}, 0)

	ColIndex := 1
	for _, ItemPara := range UniTable.pkeyFields() {
		keyCols = append(keyCols, self.getColParam(ItemPara.FieldName)+"="+self.getValParam(ColIndex))
		SqlValue = append(SqlValue, v.FieldByName(ItemPara.AttriName).Interface())
		ColIndex = ColIndex + 1
	}

	if len(keyCols) == 0 {
		return 0, fmt.Errorf("UniEngine: no pkeys column in class registered: %s", UniTable.TableName)
	}

	SqlQuery := fmt.Sprintf("select count(1) from %s where %s", TablName, strings.Join(keyCols, " and "))

	return self.SelectDCtx(ctx, SqlQuery, SqlValue...)
}

// saveByCount 旧的 count-then-dispatch 路径:钩子类与不支持原生 UPSERT 的方言(MySQL)回退使用。
func (self *TUniEngine) saveByCount(ctx context.Context, i interface{}, UniTable *TUniTable, v reflect.Value, TablName string, TableName []string, updateWhenExist bool) error {

	cCount, eror := self.countByPkeys(ctx, UniTable, v, TablName)
	if eror != nil {
		return eror
	}

	if self.debugging() {
		fmt.Println("UniEngine: select.cnt", cCount)
	}

	if updateWhenExist {
		if cCount == 1 {
			return self.UpdateCtx(ctx, i, TableName...)
		}
		return self.InsertCtx(ctx, i, TableName...)
	}

	if cCount == 0 {
		return self.InsertCtx(ctx, i, TableName...)
	}

	return nil
}

func (self *TUniEngine) SaveIt(i interface{}, TableName ...string) error {
	return self.SaveItCtx(context.Background(), i, TableName...)
}

func (self *TUniEngine) SaveItCtx(ctx context.Context, i interface{}, TableName ...string) error {

	UniTable, v, TablName, eror := self.resolveTarget("SaveIt", i, TableName, reflect.Invalid, true)
	if eror != nil {
		return eror
	}

	//#默认路径走方言原生 UPSERT,消除 count 与写入之间的并发窗口;
	//#钩子类/不支持的方言回退旧的 count-then-dispatch
	if canNativeSave(v) {
		done, eror := self.saveUpsert(ctx, UniTable, v, TablName, upsertModeUpdate)
		if eror != nil {
			return eror
		}
		if done {
			return nil
		}
	}

	return self.saveByCount(ctx, i, UniTable, v, TablName, TableName, true)
}

func (self *TUniEngine) SaveItWhenNotExist(i interface{}, TableName ...string) error {
	return self.SaveItWhenNotExistCtx(context.Background(), i, TableName...)
}

func (self *TUniEngine) SaveItWhenNotExistCtx(ctx context.Context, i interface{}, TableName ...string) error {

	UniTable, v, TablName, eror := self.resolveTarget("SaveItWhenNotExist", i, TableName, reflect.Invalid, true)
	if eror != nil {
		return eror
	}

	if canNativeSave(v) {
		done, eror := self.saveUpsert(ctx, UniTable, v, TablName, upsertModeSkip)
		if eror != nil {
			return eror
		}
		if done {
			return nil
		}
	}

	return self.saveByCount(ctx, i, UniTable, v, TablName, TableName, false)
}

// ---------------------------------------------------------------------------
// Update / Insert / InsertL / CopyInL / Delete
// ---------------------------------------------------------------------------

func (self *TUniEngine) Update(i interface{}, TableName ...string) error {
	return self.UpdateCtx(context.Background(), i, TableName...)
}

func (self *TUniEngine) UpdateCtx(ctx context.Context, i interface{}, TableName ...string) error {

	UniTable, v, TablName, eror := self.resolveTarget("Update", i, TableName, reflect.Invalid, true)
	if eror != nil {
		return eror
	}

	SqlQuery := ""
	SqlValue := make([]interface{}, 0)

	if x, ok := v.Interface().(HasGetSqlUpdate); ok {
		SqlQuery = x.GetSqlUpdate(self, TablName)
	}
	if x, ok := v.Interface().(HasSetSqlValues); ok {
		x.SetSqlValues(self, EtUpdate, &SqlValue)
	}

	if SqlQuery == "" && len(SqlValue) == 0 {

		var setCols []string
		var keyCols []string
		xValue := make([]interface{}, 0)
		zValue := make([]interface{}, 0)

		ColIndex := 1
		for _, ItemPara := range UniTable.writableFields() {

			if _, valid := UniTable.HashPkeys[strings.ToLower(ItemPara.FieldName)]; valid {
				continue
			}

			setCols = append(setCols, self.getColParam(ItemPara.FieldName)+"="+self.getValParam(ColIndex))

			if ItemPara.Encrypt {
				Value, eror := self.secretEncrypt(v.FieldByName(ItemPara.AttriName))
				if eror != nil {
					return eror
				}
				xValue = append(xValue, Value)
			} else {
				xValue = append(xValue, v.FieldByName(ItemPara.AttriName).Interface())
			}
			ColIndex = ColIndex + 1
		}

		for _, ItemPara := range UniTable.pkeyFields() {
			keyCols = append(keyCols, self.getColParam(ItemPara.FieldName)+"="+self.getValParam(ColIndex))
			zValue = append(zValue, v.FieldByName(ItemPara.AttriName).Interface())
			ColIndex = ColIndex + 1
		}

		if len(setCols) == 0 {
			return fmt.Errorf("UniEngine: no updatable column in class registered: %s", UniTable.TableName)
		}
		if len(keyCols) == 0 {
			return fmt.Errorf("UniEngine: no pkeys column in class registered: %s", UniTable.TableName)
		}

		SqlQuery = fmt.Sprintf("update %s set %s where 1=1 and %s", TablName, strings.Join(setCols, ","), strings.Join(keyCols, " and "))
		SqlValue = append(xValue, zValue...)
	}

	self.debugSQL("update", SqlQuery, SqlValue)

	st, eror := self.prepareCtx(ctx, SqlQuery)
	if eror != nil {
		return eror
	}
	defer st.Close()

	if _, eror = st.ExecContext(ctx, SqlValue...); eror != nil {
		return fmt.Errorf("UniEngine: update fail: %w", eror)
	}

	return nil
}

func (self *TUniEngine) Insert(i interface{}, TableName ...string) error {
	return self.InsertCtx(context.Background(), i, TableName...)
}

func (self *TUniEngine) InsertCtx(ctx context.Context, i interface{}, TableName ...string) error {

	UniTable, v, TablName, eror := self.resolveTarget("Insert", i, TableName, reflect.Struct, false)
	if eror != nil {
		return eror
	}

	SqlQuery := ""
	SqlValue := make([]interface{}, 0)

	if x, ok := v.Interface().(HasGetSqlInsert); ok {
		SqlQuery = x.GetSqlInsert(self, TablName)
	}
	if x, ok := v.Interface().(HasSetSqlValues); ok {
		x.SetSqlValues(self, EtInsert, &SqlValue)
	}

	if SqlQuery == "" && len(SqlValue) == 0 {

		fields := UniTable.writableFields()
		if len(fields) == 0 {
			return fmt.Errorf("UniEngine: no insertable column in class registered: %s", UniTable.TableName)
		}

		var colList []string
		var paramList []string
		ColIndex := 1
		for _, ItemPara := range fields {

			colList = append(colList, self.getColParam(ItemPara.FieldName))
			paramList = append(paramList, self.getValParam(ColIndex))
			ColIndex = ColIndex + 1

			if ItemPara.Encrypt {
				Value, eror := self.secretEncrypt(v.FieldByName(ItemPara.AttriName))
				if eror != nil {
					return eror
				}
				SqlValue = append(SqlValue, Value)
				continue
			}
			SqlValue = append(SqlValue, v.FieldByName(ItemPara.AttriName).Interface())
		}

		SqlQuery = fmt.Sprintf("insert into %s ( %s ) values ( %s ) ", TablName, strings.Join(colList, ","), strings.Join(paramList, ","))
	}

	self.debugSQL("insert", SqlQuery, SqlValue)

	st, eror := self.prepareCtx(ctx, SqlQuery)
	if eror != nil {
		return eror
	}
	defer st.Close()

	if _, eror = st.ExecContext(ctx, SqlValue...); eror != nil {
		return fmt.Errorf("UniEngine: insert fail: %w", eror)
	}

	return nil
}

func (self *TUniEngine) InsertL(i interface{}, TableName ...string) error {
	return self.InsertLCtx(context.Background(), i, TableName...)
}

func (self *TUniEngine) InsertLCtx(ctx context.Context, i interface{}, TableName ...string) error {

	UniTable, v, TablName, eror := self.resolveTarget("InsertL", i, TableName, reflect.Slice, false)
	if eror != nil {
		return eror
	}

	if v.Len() == 0 {
		return nil
	}

	SqlQuery := ""
	SqlValue := make([]interface{}, 0)

	if x, ok := v.Index(0).Interface().(HasGetSqlInsertL); ok {
		SqlQuery = x.GetSqlInsertL(self, TablName, int64(v.Len()))
	}

	if x, ok := v.Index(0).Interface().(HasSetSqlValuesL); ok {
		for m := 0; m < v.Len(); m++ {
			f := v.Index(m)
			x.SetSqlValuesL(self, EtInsert, f, &SqlValue)
		}
	}

	if SqlQuery == "" && len(SqlValue) == 0 {

		fields := UniTable.writableFields()
		if len(fields) == 0 {
			return fmt.Errorf("UniEngine: no insertable column in class registered: %s", UniTable.TableName)
		}

		var colList []string
		for _, ItemPara := range fields {
			colList = append(colList, self.getColParam(ItemPara.FieldName))
		}

		var rowList []string
		ColIndex := 1
		for m := 0; m < v.Len(); m++ {

			f := v.Index(m)

			var paramList []string
			for _, ItemPara := range fields {

				paramList = append(paramList, self.getValParam(ColIndex))
				ColIndex = ColIndex + 1

				if ItemPara.Encrypt {
					Value, eror := self.secretEncrypt(f.FieldByName(ItemPara.AttriName))
					if eror != nil {
						return eror
					}
					SqlValue = append(SqlValue, Value)
					continue
				}
				SqlValue = append(SqlValue, f.FieldByName(ItemPara.AttriName).Interface())
			}

			if self.Provider == DtORACLE {
				//#Oracle:INSERT ALL 多行语法
				rowList = append(rowList, fmt.Sprintf("into %s ( %s ) values ( %s )", TablName, strings.Join(colList, ","), strings.Join(paramList, ",")))
			} else {
				rowList = append(rowList, fmt.Sprintf("( %s )", strings.Join(paramList, ",")))
			}
		}

		if self.Provider == DtORACLE {
			SqlQuery = fmt.Sprintf("insert all %s select 1 from dual", strings.Join(rowList, " "))
		} else {
			SqlQuery = fmt.Sprintf("insert into %s ( %s ) values %s", TablName, strings.Join(colList, ","), strings.Join(rowList, ","))
		}
	}

	self.debugSQL("insert", SqlQuery, SqlValue)

	st, eror := self.prepareCtx(ctx, SqlQuery)
	if eror != nil {
		return eror
	}
	defer st.Close()

	if _, eror = st.ExecContext(ctx, SqlValue...); eror != nil {
		return fmt.Errorf("UniEngine: insert fail: %w (hint: too many parameters? try [InsertP])", eror)
	}

	return nil
}

func (self *TUniEngine) CopyInL(i interface{}, TableName ...string) error {
	return self.CopyInLCtx(context.Background(), i, TableName...)
}

// #已废弃:旧版命名,新代码请用 CopyInL;仅为源码兼容保留
func (self *TUniEngine) SpecialInsertL(i interface{}, TableName ...string) error {
	return self.CopyInLCtx(context.Background(), i, TableName...)
}

// #已废弃:旧版命名,新代码请用 CopyInLCtx;仅为源码兼容保留
func (self *TUniEngine) SpecialInsertLCtx(ctx context.Context, i interface{}, TableName ...string) error {
	return self.CopyInLCtx(ctx, i, TableName...)
}

func (self *TUniEngine) CopyInLCtx(ctx context.Context, i interface{}, TableName ...string) error {

	UniTable, v, TablName, eror := self.resolveTarget("CopyInL", i, TableName, reflect.Slice, false)
	if eror != nil {
		return eror
	}

	if v.Len() == 0 {
		return nil
	}

	SqlQuery := make([]string, 0)
	SqlValue := make([][]interface{}, 0)

	if x, ok := v.Index(0).Interface().(HasCopyInGetSqlInsertL); ok {
		SqlQuery = x.CopyInGetSqlInsertL(self, TablName, int64(v.Len()))
	} else if x, ok := v.Index(0).Interface().(HasSpecialGetSqlInsertL); ok {
		//#兼容旧版接口
		SqlQuery = x.SpecialGetSqlInsertL(self, TablName, int64(v.Len()))
	}

	if x, ok := v.Index(0).Interface().(HasCopyInSetSqlValuesL); ok {
		for m := 0; m < v.Len(); m++ {
			f := v.Index(m)
			x.CopyInSetSqlValuesL(self, EtInsert, f, &SqlValue)
		}
	} else if x, ok := v.Index(0).Interface().(HasSpecialSetSqlValuesL); ok {
		//#兼容旧版接口
		for m := 0; m < v.Len(); m++ {
			f := v.Index(m)
			x.SpecialSetSqlValuesL(self, EtInsert, f, &SqlValue)
		}
	}

	if len(SqlQuery) == 0 && len(SqlValue) == 0 {

		fields := UniTable.writableFields()
		if len(fields) == 0 {
			return fmt.Errorf("UniEngine: no insertable column in class registered: %s", UniTable.TableName)
		}

		for _, ItemPara := range fields {
			SqlQuery = append(SqlQuery, ItemPara.FieldName)
		}

		for m := 0; m < v.Len(); m++ {

			f := v.Index(m)

			var row = make([]interface{}, 0)

			for _, ItemPara := range fields {

				if ItemPara.Encrypt {
					Value, eror := self.secretEncrypt(f.FieldByName(ItemPara.AttriName))
					if eror != nil {
						return eror
					}
					row = append(row, Value)
					continue
				}
				row = append(row, f.FieldByName(ItemPara.AttriName).Interface())
			}

			SqlValue = append(SqlValue, row)
		}
	}

	self.debugSQL("copyin", SqlQuery, SqlValue)

	//#COPY 仅 PostgreSQL 协议族可用;其余方言显式报错,不再发送必然失败的语句
	switch self.Provider {
	case DtPOSTGR, DtKINGES, DtOPENGS, DtPOLODB:
	default:
		return fmt.Errorf("UniEngine: CopyInL requires a PostgreSQL-protocol provider (DtPOSTGR/DtKINGES/DtOPENGS/DtPOLODB), got [%d]", self.Provider)
	}

	Sql4Text := copyInStmt(TablName, SqlQuery)

	st, eror := self.prepareCtx(ctx, Sql4Text)
	if eror != nil {
		return fmt.Errorf("UniEngine: copyin fail: %w (hint: too many parameters? try [CopyInP])", eror)
	}
	defer st.Close()

	// 执行所有行
	for _, row := range SqlValue {
		_, eror = st.ExecContext(ctx, row...)
		if eror != nil {
			return eror
		}
	}

	// 完成 COPY 的收尾调用(空参数)
	_, eror = st.ExecContext(ctx)
	if eror != nil {
		return fmt.Errorf("UniEngine: copyin fail: %w (hint: too many parameters? try [CopyInP])", eror)
	}

	return nil
}

func (self *TUniEngine) InsertP(i interface{}, PageSize int64, TableName ...string) error {
	return self.InsertPCtx(context.Background(), i, PageSize, TableName...)
}

func (self *TUniEngine) InsertPCtx(ctx context.Context, i interface{}, PageSize int64, TableName ...string) error {

	if PageSize == 0 || PageSize == -1 {
		PageSize = 999
	}

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Slice {
		return errors.New("UniEngine: method [InsertP] needs a slice param; check your code;")
	}

	var All4Data = reflect.Indirect(reflect.ValueOf(i))
	var ListData = reflect.MakeSlice(t, 0, 0)

	for I := 0; I < All4Data.Len(); I++ {

		ListData = reflect.Append(ListData, All4Data.Index(I))

		if ListData.Len() == int(PageSize) {
			if eror := self.insertPage(ctx, ListData, TableName...); eror != nil {
				return eror
			}
			ListData = reflect.MakeSlice(t, 0, 0)
		}
	}

	return self.insertPage(ctx, ListData, TableName...)
}

// insertPage 按方言路由单页批量写入(PolarDB 走 COPY 协议,其余走 INSERT)。
func (self *TUniEngine) insertPage(ctx context.Context, ListData reflect.Value, TableName ...string) error {

	if self.Supplier == DtPOLODB {
		return self.CopyInLCtx(ctx, ListData.Interface(), TableName...)
	}

	return self.InsertLCtx(ctx, ListData.Interface(), TableName...)
}

func (self *TUniEngine) CopyInP(i interface{}, PageSize int64, TableName ...string) error {
	return self.CopyInPCtx(context.Background(), i, PageSize, TableName...)
}

// #已废弃:旧版命名,新代码请用 CopyInP;仅为源码兼容保留
func (self *TUniEngine) SpecialInsertP(i interface{}, PageSize int64, TableName ...string) error {
	return self.CopyInPCtx(context.Background(), i, PageSize, TableName...)
}

// #已废弃:旧版命名,新代码请用 CopyInPCtx;仅为源码兼容保留
func (self *TUniEngine) SpecialInsertPCtx(ctx context.Context, i interface{}, PageSize int64, TableName ...string) error {
	return self.CopyInPCtx(ctx, i, PageSize, TableName...)
}

func (self *TUniEngine) CopyInPCtx(ctx context.Context, i interface{}, PageSize int64, TableName ...string) error {

	if PageSize == 0 || PageSize == -1 {
		PageSize = 999
	}

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Slice {
		return errors.New("UniEngine: method [CopyInP] needs a slice param; check your code;")
	}

	var All4Data = reflect.Indirect(reflect.ValueOf(i))
	var ListData = reflect.MakeSlice(t, 0, 0)

	for I := 0; I < All4Data.Len(); I++ {

		ListData = reflect.Append(ListData, All4Data.Index(I))

		if ListData.Len() == int(PageSize) {
			if eror := self.CopyInLCtx(ctx, ListData.Interface(), TableName...); eror != nil {
				return eror
			}
			ListData = reflect.MakeSlice(t, 0, 0)
		}
	}

	return self.CopyInLCtx(ctx, ListData.Interface(), TableName...)
}

func (self *TUniEngine) Delete(i interface{}, TableName ...string) error {
	return self.DeleteCtx(context.Background(), i, TableName...)
}

func (self *TUniEngine) DeleteCtx(ctx context.Context, i interface{}, TableName ...string) error {

	UniTable, v, TablName, eror := self.resolveTarget("Delete", i, TableName, reflect.Invalid, true)
	if eror != nil {
		return eror
	}

	var keyCols []string
	SqlValue := make([]interface{}, 0)

	ColIndex := 1
	for _, ItemPara := range UniTable.pkeyFields() {
		keyCols = append(keyCols, self.getColParam(ItemPara.FieldName)+"="+self.getValParam(ColIndex))
		SqlValue = append(SqlValue, v.FieldByName(ItemPara.AttriName).Interface())
		ColIndex = ColIndex + 1
	}

	if len(keyCols) == 0 {
		return fmt.Errorf("UniEngine: no pkeys column in class registered: %s", UniTable.TableName)
	}

	SqlQuery := fmt.Sprintf("delete from %s where %s", TablName, strings.Join(keyCols, " and "))

	self.debugSQL("delete", SqlQuery, SqlValue)

	st, eror := self.prepareCtx(ctx, SqlQuery)
	if eror != nil {
		return eror
	}
	defer st.Close()

	if _, eror = st.ExecContext(ctx, SqlValue...); eror != nil {
		return fmt.Errorf("UniEngine: delete fail: %w", eror)
	}

	return nil
}

// ---------------------------------------------------------------------------
// Execute / 元数据探测
// ---------------------------------------------------------------------------

func (self *TUniEngine) Execute(SqlQuery string, args ...interface{}) error {
	return self.ExecuteCtx(context.Background(), SqlQuery, args...)
}

func (self *TUniEngine) ExecuteCtx(ctx context.Context, SqlQuery string, args ...interface{}) error {

	SqlQuery = self.getSqlQuery(SqlQuery, args...)

	self.debugSQL("execute", SqlQuery, args)

	st, eror := self.prepareCtx(ctx, SqlQuery)
	if eror != nil {
		return eror
	}
	defer st.Close()

	if _, eror := st.ExecContext(ctx, args...); eror != nil {
		return fmt.Errorf("UniEngine: execute fail: %w", eror)
	}

	return nil
}

func (self *TUniEngine) ExecuteMust(SqlQuery string, args ...interface{}) error {
	return self.ExecuteMustCtx(context.Background(), SqlQuery, args...)
}

func (self *TUniEngine) ExecuteMustCtx(ctx context.Context, SqlQuery string, args ...interface{}) error {

	SqlQuery = self.getSqlQuery(SqlQuery, args...)

	self.debugSQL("execute", SqlQuery, args)

	st, eror := self.prepareCtx(ctx, SqlQuery)
	if eror != nil {
		return eror
	}
	defer st.Close()

	result, eror := st.ExecContext(ctx, args...)
	if eror != nil {
		return fmt.Errorf("UniEngine: execute fail: %w", eror)
	}

	size, eror := result.RowsAffected()
	if eror != nil {
		return eror
	}

	if size == 0 {
		return errors.New("UniEngine: the row count of affected is zero")
	}

	return nil
}

func (self *TUniEngine) IfDropView(TableName string) (bool, error) {
	return self.IfDropViewCtx(context.Background(), TableName)
}

func (self *TUniEngine) IfDropViewCtx(ctx context.Context, TableName string) (bool, error) {

	if !validIdent(TableName) {
		return false, errors.New("UniEngine: invalid table name: " + TableName)
	}

	mrok, eror := self.ExistViewsCtx(ctx, TableName)
	if eror != nil {
		return false, eror
	}

	if mrok {
		cSQL := fmt.Sprintf("DROP VIEW %s", TableName)
		if eror = self.ExecuteCtx(ctx, cSQL); eror != nil {
			return false, eror
		}
	}

	return true, nil
}

// existCount 四个 Exist* 探测的公共收尾:执行计数 SQL 并转换为布尔结果。
func (self *TUniEngine) existCount(ctx context.Context, Kind string, cSQL string) (bool, error) {

	self.debugSQL(Kind, cSQL, nil)

	if cSQL == "" {
		return false, fmt.Errorf("UniEngine: no sql for %s", Kind)
	}

	Size, eror := self.SelectDCtx(ctx, cSQL)
	if eror != nil {
		return false, eror
	}

	return Size > 0, nil
}

func (self *TUniEngine) ExistTable(TableName string) (bool, error) {
	return self.ExistTableCtx(context.Background(), TableName)
}

func (self *TUniEngine) ExistTableCtx(ctx context.Context, TableName string) (bool, error) {

	if !validIdent(TableName) {
		return false, errors.New("UniEngine: invalid table name: " + TableName)
	}

	var cSQL string

	switch self.Provider {
	case DtPOSTGR:
		cSQL = TExistTable4POSTGR{}.GetSqlExistTable(self, TableName)
	case DtSQLSRV:
		cSQL = TExistTable4SQLSRV{}.GetSqlExistTable(self, TableName)
	case DtORACLE:
		cSQL = TExistTable4ORACLE{}.GetSqlExistTable(self, TableName)
	case DtMYSQLN:
		if self.DataBase == "" {
			return false, errors.New("UniEngine: database is not specified")
		}
		cSQL = TExistTable4MYSQLN{}.GetSqlExistTable(self, TableName, self.DataBase)
	}

	return self.existCount(ctx, "existtable", cSQL)
}

func (self *TUniEngine) ExistViews(TableName string) (bool, error) {
	return self.ExistViewsCtx(context.Background(), TableName)
}

func (self *TUniEngine) ExistViewsCtx(ctx context.Context, TableName string) (bool, error) {

	if !validIdent(TableName) {
		return false, errors.New("UniEngine: invalid table name: " + TableName)
	}

	var cSQL string

	switch self.Provider {
	case DtPOSTGR:
		cSQL = TExistTable4POSTGR{}.GetSqlExistViews(self, TableName)
	case DtSQLSRV:
		cSQL = TExistTable4SQLSRV{}.GetSqlExistViews(self, TableName)
	case DtORACLE:
		cSQL = TExistTable4ORACLE{}.GetSqlExistViews(self, TableName)
	case DtMYSQLN:
		if self.DataBase == "" {
			return false, errors.New("UniEngine: database is not specified")
		}
		cSQL = TExistTable4MYSQLN{}.GetSqlExistViews(self, TableName, self.DataBase)
	}

	return self.existCount(ctx, "existviews", cSQL)
}

func (self *TUniEngine) ExistField(TableName, FieldName string) (bool, error) {
	return self.ExistFieldCtx(context.Background(), TableName, FieldName)
}

func (self *TUniEngine) ExistFieldCtx(ctx context.Context, TableName, FieldName string) (bool, error) {

	if !validIdent(TableName) || !validIdent(FieldName) {
		return false, errors.New("UniEngine: invalid table/field name: " + TableName + "." + FieldName)
	}

	var cSQL string

	switch self.Provider {
	case DtPOSTGR:
		cSQL = TExistField4POSTGR{}.GetSqlExistField(self, TableName, FieldName)
	case DtSQLSRV:
		cSQL = TExistField4SQLSRV{}.GetSqlExistField(self, TableName, FieldName)
	case DtORACLE:
		cSQL = TExistField4ORACLE{}.GetSqlExistField(self, TableName, FieldName)
	case DtMYSQLN:
		if self.DataBase == "" {
			return false, errors.New("UniEngine: database is not specified")
		}
		cSQL = TExistField4MYSQLN{}.GetSqlExistField(self, TableName, FieldName, self.DataBase)
	}

	return self.existCount(ctx, "existfield", cSQL)
}

// #ExistConst 判断指定类型的约束是否存在(按约束名/列名匹配)
func (self *TUniEngine) ExistConst(aConstType TConstType, aConstName string) (bool, error) {
	return self.ExistConstCtx(context.Background(), aConstType, aConstName)
}

func (self *TUniEngine) ExistConstCtx(ctx context.Context, aConstType TConstType, aConstName string) (bool, error) {

	if !validIdent(aConstName) {
		return false, errors.New("UniEngine: invalid constraint name: " + aConstName)
	}

	var cSQL string

	switch self.Provider {
	case DtPOSTGR:
		cSQL = TExistConst4POSTGR{}.GetSqlExistConst(self, aConstType, aConstName)
	case DtSQLSRV:
		cSQL = TExistConst4SQLSRV{}.GetSqlExistConst(self, aConstType, aConstName)
	case DtORACLE:
		cSQL = TExistConst4ORACLE{}.GetSqlExistConst(self, aConstType, aConstName)
	case DtMYSQLN:
		if self.DataBase == "" {
			return false, errors.New("UniEngine: database is not specified")
		}
		cSQL = TExistConst4MYSQLN{}.GetSqlExistConst(self, aConstType, aConstName, self.DataBase)
	}

	return self.existCount(ctx, "existconst", cSQL)
}

// ---------------------------------------------------------------------------
// 语句/事务管理
// ---------------------------------------------------------------------------

// prepareCtx 在当前事务(若有)或连接池上预备语句。
// 语句是局部变量,不再保存到引擎——这是并发安全的关键:查询路径不共享可变状态。
// 返回的语句由调用方负责 Close。
func (self *TUniEngine) prepareCtx(ctx context.Context, SqlQuery string) (*sql.Stmt, error) {

	if tx := self.currentTx(); tx != nil {
		return tx.PrepareContext(ctx, SqlQuery)
	}

	return self.Db.PrepareContext(ctx, SqlQuery)
}

func (self *TUniEngine) Begin() error {
	return self.BeginCtx(context.Background())
}

func (self *TUniEngine) BeginCtx(ctx context.Context) error {

	tx, eror := self.Db.BeginTx(ctx, nil)
	if eror != nil {
		return eror
	}

	self.lockTables()
	defer self.mu.Unlock()

	self.tx = tx
	self.inTx = true

	return nil
}

func (self *TUniEngine) Cancel() error {

	self.lockTables()
	defer self.mu.Unlock()

	if !self.inTx || self.tx == nil {
		return errors.New("UniEngine: no transaction")
	}

	if eror := self.tx.Rollback(); eror != nil {
		return eror
	}

	self.tx = nil
	self.inTx = false

	return nil
}

func (self *TUniEngine) Commit() error {

	self.lockTables()
	defer self.mu.Unlock()

	if !self.inTx || self.tx == nil {
		return errors.New("UniEngine: no transaction")
	}

	if eror := self.tx.Commit(); eror != nil {
		return eror
	}

	self.tx = nil
	self.inTx = false

	return nil
}

func (self *TUniEngine) RunDebug(Value bool) error {

	if Value {
		atomic.StoreInt32(&self.runDebug, 1)
	} else {
		atomic.StoreInt32(&self.runDebug, 0)
	}

	return nil
}

func (self *TUniEngine) Initialize() error {

	//#并发契约:表结构变更(SetKeys/SetSecret/AutoKeys/PrepareTables/PrepareRunSQL)
	//#与配置字段(ColLabel/ColParam/Provider/SecretOn 等)须在并发查询开始前完成;
	//#注册(RegisterClass/RegisterTable/...)与查询可并发
	self.initLock()

	self.RegisterClass(TUniTable{}, "github.com/kazarus/uniengine/unitable")
	self.RegisterClass(TUniField{}, "github.com/kazarus/uniengine/unifield")

	self.inTx = false

	return nil
}
