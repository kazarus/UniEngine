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

	"github.com/lib/pq"
)

/*
  when write data to struct, should be ptr;
  when read data from struct, whoever, ptr or struct;
*/

// TUniEngine is a multi-database ORM-like engine built on database/sql.
//
// Concurrency: TUniEngine is NOT safe for concurrent use. It keeps mutable
// per-operation state (the prepared statement `st` and the transaction `tx`)
// on the struct itself, so two goroutines driving the same instance will
// clobber each other. Use one engine per goroutine, or serialize calls.
// The underlying *sql.DB, however, is safe for concurrent use.
type TUniEngine struct {
	Db *sql.DB
	tx *sql.Tx
	st *sql.Stmt

	mu       *sync.Mutex //#保护 HashTabl 初始化/注册（指针：按值传递引擎时共享同一把锁）
	ColLabel string      //#字段字号
	ColParam string      //#参数符号
	HashTabl map[string]*TUniTable

	SecretOn int64  //#开启敏感信息加密
	SecretBy string //#敏感信息加密密钥

	SecretHook TSecretHook //#应用加密钩子#为空时使用内置AES-256-GCM

	Instance string     //#数据库实例
	DataBase string     //#数据库名称
	DataUser string     //#数据库用户
	Provider TDriveType //#数据库驱动
	Supplier TDriveType //#数据库驱动(区分 DtORACLE VS DtDAMENG)

	canClose bool //default is true;if is transaction,canClose = false
	runDebug bool //default is false;print some sql;
}

// muLazy 保护各引擎 mu 的懒初始化（仅覆盖建锁窗口，不护业务临界区）
var muLazy sync.Mutex

// initLock 保证 self.mu 已创建（并发安全；供 Initialize 与 lockTables 调用）
func (self *TUniEngine) initLock() {

	muLazy.Lock()
	defer muLazy.Unlock()

	if self.mu == nil {
		self.mu = &sync.Mutex{}
	}
}

// lockTables 保证锁可用并加锁（懒初始化并发安全）
func (self *TUniEngine) lockTables() {

	self.initLock()
	self.mu.Lock()
}

// ensureTables 保证 HashTabl 已初始化（加锁，供只读方法调用）
func (self *TUniEngine) ensureTables() {

	self.lockTables()
	defer self.mu.Unlock()

	if self.HashTabl == nil {
		self.HashTabl = make(map[string]*TUniTable, 0)
	}
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

		if self.runDebug {
			fmt.Println(`UniEngine: Oracle驱动时,替换"$"到":"`)
		}

		// 仅替换参数占位符（$1 -> :1），避免误改 SQL 文本中其它 $ 字符
		SqlQuery = reParamPlaceholder.ReplaceAllStringFunc(SqlQuery, func(m string) string {
			return ":" + m[1:]
		})
	}

	if self.Provider == DtMYSQLN && len(args) > 0 {

		if self.runDebug {
			fmt.Println(`UniEngine: MySQL驱动时,替换"$"到"?"`)
		}

		// 包级预编译正则，消除每次调用 Compile 的开销与静默失败返回空串的路径
		SqlQuery = reParamPlaceholder.ReplaceAllString(SqlQuery, "?")
	}

	return SqlQuery
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

// #只是用函数包一下
func (self *TUniEngine) SpecialPageSize(aPageSize int64) int64 {

	var PageSize int64

	switch self.Provider {
	case DtORACLE:
		{
			PageSize = 99
		}
	case DtSQLSRV:
		{
			PageSize = aPageSize
		}
	case DtPOSTGR:
		{
			PageSize = 99
		}
	}

	return PageSize
}

func (self *TUniEngine) DefaultPageSize() int64 {

	var PageSize int64

	switch self.Provider {
	case DtORACLE:
		{
			PageSize = 99
		}
	case DtSQLSRV:
		{
			PageSize = 10
		}
	case DtPOSTGR:
		{
			PageSize = 99
		}
	}

	return PageSize
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
	switch Valid {
	case true:
		{
			UniTable.IPriority = IPriority
		}
	default:
		{
			UniTable = &TUniTable{}
			UniTable.HashField = make(map[string]TUniField, 0)
			UniTable.HashPkeys = make(map[string]TUniField, 0)
			UniTable.TableName = strings.ToLower(TableName)
			UniTable.IPriority = IPriority
		}
	}

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
	switch Valid {
	case true:
		{
		}
	default:
		{
			UniTable = &TUniTable{}
			UniTable.HashField = make(map[string]TUniField, 0)
			UniTable.HashPkeys = make(map[string]TUniField, 0)
			UniTable.TableName = strings.ToLower(TableName)
		}
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
	switch Valid {
	case true:
		{
			var UniField = TUniField{}
			UniField.AttriName = ""
			UniField.FieldName = strings.ToLower(FieldName)
			UniField.TableName = strings.ToLower(TableName)

			UniTable.HashPkeys[strings.ToLower(UniField.FieldName)] = UniField

			self.HashTabl[strings.ToLower(TableName)] = UniTable
		}
	default:
		{
			//table not registered; nothing to do
		}
	}

	return UniTable
}

func (self *TUniEngine) GetTable(TableName string) *TUniTable {

	self.ensureTables()

	UniTable, _ := self.HashTabl[strings.ToLower(TableName)]

	return UniTable
}

func (self *TUniEngine) PrepareTables(TableName string) error {

	self.ensureTables()

	UniTable, Valid := self.HashTabl[strings.ToLower(TableName)]
	switch Valid {
	case true:
		{
			if UniTable.ListField == nil {
				UniTable.ListField = make([]TUniField, 0)
			}
			UniTable.ListField = UniTable.ListField[0:0]

			if UniTable.ListPkeys == nil {
				UniTable.ListPkeys = make([]TUniField, 0)
			}
			UniTable.ListPkeys = UniTable.ListPkeys[0:0]

			if len(UniTable.HashField) > 0 {

				for _, ItemPara := range UniTable.HashField {

					if ItemPara.ReadOnly {
						continue
					}

					UniTable.ListField = append(UniTable.ListField, ItemPara)
				}
			}

			if len(UniTable.HashPkeys) > 0 {

				for _, ItemPara := range UniTable.HashPkeys {

					if ItemPara.ReadOnly {
						continue
					}

					UniTable.ListPkeys = append(UniTable.ListPkeys, ItemPara)
				}
			}

			self.HashTabl[strings.ToLower(TableName)] = UniTable
		}
	default:
		{
		}
	}

	return nil
}

func (self *TUniEngine) PrepareRunSQL(TableName string, QueryType TQueryType) (string, []TUniField, []TUniField, error) {

	self.ensureTables()

	var SqlResult string
	var ListField = make([]TUniField, 0)

	UniTable, Valid := self.HashTabl[strings.ToLower(TableName)]
	switch Valid {
	case true:
		{

			switch QueryType {
			case EtSelect:
				{
					var ColIndex int = 1
					var SqlWhere string = ""

					if len(UniTable.HashPkeys) > 0 {

						for _, ItemPara := range UniTable.ListPkeys {

							if ItemPara.ReadOnly {
								continue
							}

							SqlField := self.getColParam(ItemPara.FieldName)
							SqlParam := self.getValParam(ColIndex)
							SqlWhere = SqlWhere + fmt.Sprintf("    and %s=%s", SqlField, SqlParam)
							ColIndex = ColIndex + 1
						}

						SqlResult = fmt.Sprintf("where 1=1 %s", SqlWhere)
						UniTable.SqlSelect = SqlResult
					}
				}
			case EtInsert:
				{
					var ColIndex int = 1
					var SqlField string = ""
					var SqlParam string = ""

					if len(UniTable.HashField) > 0 {

						for _, ItemPara := range UniTable.ListField {

							if ItemPara.ReadOnly {
								continue
							}

							SqlField = SqlField + "," + self.getColParam(ItemPara.FieldName)
							SqlParam = SqlParam + "," + self.getValParam(ColIndex)
							ColIndex = ColIndex + 1

							ListField = append(ListField, ItemPara)
						}

						SqlField = string(SqlField[1:])
						SqlParam = string(SqlParam[1:])

						SqlResult = fmt.Sprintf("insert into %s ( %s ) values ( %s ) ", UniTable.TableName, SqlField, SqlParam)
						UniTable.SqlInsert = SqlResult
					}
				}
			case EtUpdate:
				{
					var ColIndex int = 1
					var SqlField string = ""
					var SqlWhere string = ""

					if len(UniTable.HashField) > 0 {

						for _, ItemPara := range UniTable.ListField {

							if ItemPara.ReadOnly {
								continue
							}

							if _, valid := UniTable.HashPkeys[strings.ToLower(ItemPara.FieldName)]; valid {
								ItemPara.PkeyOnly = true
								ListField = append(ListField, ItemPara)
								continue
							}

							SqlField = SqlField + "," + self.getColParam(ItemPara.FieldName) + "=" + self.getValParam(ColIndex)

							ColIndex = ColIndex + 1

							ListField = append(ListField, ItemPara)
						}
					}

					if len(UniTable.HashPkeys) > 0 {

						for _, item := range UniTable.ListPkeys {

							SqlWhere = SqlWhere + " and " + self.getColParam(item.FieldName) + "=" + self.getValParam(ColIndex)
							ColIndex = ColIndex + 1
						}
					}

					SqlField = string(SqlField[1:])
					SqlWhere = string(SqlWhere[1:])

					SqlResult = fmt.Sprintf("update %s set %s where 1=1 %s", UniTable.TableName, SqlField, SqlWhere)
					UniTable.SqlUpdate = SqlResult
				}
			}

			self.HashTabl[strings.ToLower(TableName)] = UniTable
		}
	default:
		{
		}
	}

	return SqlResult, ListField, UniTable.ListPkeys, nil
}

// return int64;
func (self *TUniEngine) SelectD(SqlQuery string, args ...interface{}) (int64, error) {
	return self.SelectDCtx(context.Background(), SqlQuery, args...)
}

func (self *TUniEngine) SelectDCtx(ctx context.Context, SqlQuery string, args ...interface{}) (int64, error) {

	var eror error
	var size sql.NullInt64

	SqlQuery = self.getSqlQuery(SqlQuery, args)

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: select.sql", SqlQuery)
		fmt.Println("UniEngine: select.val", args)
	}

	eror = self.prepareCtx(ctx, SqlQuery)
	if eror != nil {
		return 0, eror
	}
	defer self.release()

	rows, eror := self.st.QueryContext(ctx, args...)
	if eror != nil {
		return 0, eror
	}
	defer rows.Close()

	for rows.Next() {
		eror = rows.Scan(&size)
		if eror != nil {
			return 0, eror
		}
	}

	return size.Int64, nil
}

// return float64;
func (self *TUniEngine) SelectF(SqlQuery string, args ...interface{}) (float64, error) {
	return self.SelectFCtx(context.Background(), SqlQuery, args...)
}

func (self *TUniEngine) SelectFCtx(ctx context.Context, SqlQuery string, args ...interface{}) (float64, error) {

	var eror error
	var size sql.NullFloat64

	SqlQuery = self.getSqlQuery(SqlQuery, args)

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: select.sql", SqlQuery)
		fmt.Println("UniEngine: select.val", args)
	}

	eror = self.prepareCtx(ctx, SqlQuery)
	if eror != nil {
		return 0, eror
	}
	defer self.release()

	rows, eror := self.st.QueryContext(ctx, args...)

	if eror != nil {
		return 0, eror
	}
	defer rows.Close()

	for rows.Next() {
		eror = rows.Scan(&size)
		if eror != nil {
			return 0, eror
		}
	}
	return size.Float64, nil
}

// return string;
func (self *TUniEngine) SelectS(SqlQuery string, args ...interface{}) (string, error) {
	return self.SelectSCtx(context.Background(), SqlQuery, args...)
}

func (self *TUniEngine) SelectSCtx(ctx context.Context, SqlQuery string, args ...interface{}) (string, error) {

	var eror error
	var text sql.NullString

	SqlQuery = self.getSqlQuery(SqlQuery, args)

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: select.sql", SqlQuery)
		fmt.Println("UniEngine: select.val", args)
	}

	eror = self.prepareCtx(ctx, SqlQuery)
	if eror != nil {
		return "", eror
	}
	defer self.release()

	rows, eror := self.st.QueryContext(ctx, args...)
	if eror != nil {
		return "", eror
	}
	defer rows.Close()

	for rows.Next() {
		eror = rows.Scan(&text)
		if eror != nil {
			return "", eror
		}
	}

	return text.String, nil
}

// return struct;
func (self *TUniEngine) Select(i interface{}, SqlQuery string, args ...interface{}) error {
	return self.SelectCtx(context.Background(), i, SqlQuery, args...)
}

func (self *TUniEngine) SelectCtx(ctx context.Context, i interface{}, SqlQuery string, args ...interface{}) error {

	var eror error

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Struct {
		return errors.New("UniEngine: method [Select] only retun a struct; may be you should try [SelectL]")
	}

	SqlQuery = self.getSqlQuery(SqlQuery, args)

	TablName := t.String()
	UniTable, Valid := self.HashTabl[TablName]
	if !Valid {
		return fmt.Errorf("UniEngine: no such class registered: %s", TablName)
	}

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: select.sql", SqlQuery)
		fmt.Println("UniEngine: select.val", args)
	}

	eror = self.prepareCtx(ctx, SqlQuery)
	if eror != nil {
		return eror
	}
	defer self.release()

	rows, eror := self.st.QueryContext(ctx, args...)
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

		x, ok := i.(HasSetSqlResult)
		switch ok {
		case true:
			{
				for j := 0; j < cCount; j++ {
					values[j] = &fields[j]
				}

				eror = rows.Scan(values...)
				if eror != nil {
					return eror
				}

				x.SetSqlResult(*self, i, column, fields)
			}
		default:
			{
				var Result = reflect.Indirect(reflect.ValueOf(i))

				for ItemIndx, ItemPara := range column {
					UniField, Valid := UniTable.HashField[strings.ToLower(ItemPara)]
					if !Valid {
						return fmt.Errorf("UniEngine: database have field[%s], but not in class[%s]", ItemPara, t.String())
					}
					values[ItemIndx] = Result.FieldByName(UniField.AttriName).Addr().Interface()
				}

				eror = rows.Scan(values...)
				if eror != nil {
					return eror
				}

				if eror = self.DecryptResult(&Result, UniTable, column); eror != nil {
					return eror
				}
			}
		}
	}

	return nil
}

// return slice of struct;
func (self *TUniEngine) SelectL(i interface{}, SqlQuery string, args ...interface{}) error {
	return self.SelectLCtx(context.Background(), i, SqlQuery, args...)
}

func (self *TUniEngine) SelectLCtx(ctx context.Context, i interface{}, SqlQuery string, args ...interface{}) error {

	var eror error

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Slice {
		return errors.New("UniEngine: method [Select] only retun a struct; may be you should try [SelectL]")
	}

	if t.Kind() == reflect.Slice {
		t = t.Elem()
	}

	SqlQuery = self.getSqlQuery(SqlQuery, args)

	TablName := t.String()
	UniTable, Valid := self.HashTabl[TablName]
	if !Valid {
		return fmt.Errorf("UniEngine: no such class registered: %s", TablName)
	}

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: select.sql", SqlQuery)
		fmt.Println("UniEngine: select.val", args)
	}

	eror = self.prepareCtx(ctx, SqlQuery)
	if eror != nil {
		return eror
	}
	defer self.release()

	rows, eror := self.st.QueryContext(ctx, args...)
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

	var Result = reflect.Indirect(reflect.ValueOf(i))

	for rows.Next() {

		u := reflect.New(t)
		switch t.Implements(THasSetSqlResult) {
		case true:
			{
				for i := 0; i < cCount; i++ {
					values[i] = &fields[i]
				}

				eror = rows.Scan(values...)
				if eror != nil {
					return eror
				}

				x := u.Interface().(HasSetSqlResult)
				x.SetSqlResult(*self, u.Interface(), column, fields)

				Result.Set(reflect.Append(Result, u.Elem()))
			}
		default:
			{
				for ColIndex, ItemPara := range column {
					UniField, Valid := UniTable.HashField[strings.ToLower(ItemPara)]
					if !Valid {
						return fmt.Errorf("UniEngine: database have field[%s], but not in class[%s]", ItemPara, t.String())
					}
					values[ColIndex] = u.Elem().FieldByName(UniField.AttriName).Addr().Interface()
				}

				eror = rows.Scan(values...)
				if eror != nil {
					return eror
				}

				elem := u.Elem()
				if eror = self.DecryptResult(&elem, UniTable, column); eror != nil {
					return eror
				}

				Result.Set(reflect.Append(Result, u.Elem()))
			}
		}
	}

	return nil
}

// return map of struct;user;GetMapUnique;
func (self *TUniEngine) SelectM(i interface{}, SqlQuery string, args ...interface{}) error {
	return self.SelectMCtx(context.Background(), i, SqlQuery, args...)
}

func (self *TUniEngine) SelectMCtx(ctx context.Context, i interface{}, SqlQuery string, args ...interface{}) error {

	var eror error

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Map {
		return errors.New("UniEngine: method [Select] only retun a struct; may be you should try [SelectL]")
	}

	if t.Kind() == reflect.Map {
		t = t.Elem()
	}

	SqlQuery = self.getSqlQuery(SqlQuery, args)

	TablName := t.String()
	UniTable, Valid := self.HashTabl[TablName]
	if !Valid {
		return fmt.Errorf("UniEngine: no such class registered: %s", TablName)
	}

	if !t.Implements(THasGetMapUnique) {
		return fmt.Errorf("UniEngine: the class registered:[%s] does not Implemented [HasGetMapUnique]", TablName)
	}

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: select.sql", SqlQuery)
		fmt.Println("UniEngine: select.val", args)
	}

	eror = self.prepareCtx(ctx, SqlQuery)
	if eror != nil {
		return eror
	}
	defer self.release()

	rows, eror := self.st.QueryContext(ctx, args...)
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

	var Result = reflect.Indirect(reflect.ValueOf(i))

	for rows.Next() {

		u := reflect.New(t)

		switch t.Implements(THasSetSqlResult) {
		case true:
			{
				for i := 0; i < cCount; i++ {
					values[i] = &fields[i]
				}

				eror = rows.Scan(values...)
				if eror != nil {
					return eror
				}

				x := u.Interface().(HasSetSqlResult)
				x.SetSqlResult(*self, u.Interface(), column, fields)

				var MapUnique string
				if x, ok := u.Interface().(HasGetMapUnique); ok {
					MapUnique = x.GetMapUnique()
				}
				Result.SetMapIndex(reflect.ValueOf(MapUnique), u.Elem())
			}
		default:
			{
				for ColIndex, ItemPara := range column {
					UniField, Valid := UniTable.HashField[strings.ToLower(ItemPara)]
					if !Valid {
						return fmt.Errorf("UniEngine: database have field[%s], but not in class[%s]", ItemPara, t.String())
					}
					values[ColIndex] = u.Elem().FieldByName(UniField.AttriName).Addr().Interface()
				}

				eror = rows.Scan(values...)
				if eror != nil {
					return eror
				}

				elem := u.Elem()
				if eror = self.DecryptResult(&elem, UniTable, column); eror != nil {
					return eror
				}

				var MapUnique string
				if x, ok := u.Interface().(HasGetMapUnique); ok {
					MapUnique = x.GetMapUnique()
				}
				Result.SetMapIndex(reflect.ValueOf(MapUnique), u.Elem())
			}
		}
	}

	return nil
}

// return map;use custom function;
func (self *TUniEngine) SelectH(i interface{}, f GetMapUnique, SqlQuery string, args ...interface{}) error {
	return self.SelectHCtx(context.Background(), i, f, SqlQuery, args...)
}

func (self *TUniEngine) SelectHCtx(ctx context.Context, i interface{}, f GetMapUnique, SqlQuery string, args ...interface{}) error {

	var eror error

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Map {
		return errors.New("UniEngine: method [select] only retun a struct; may be you should try [SelectL]")
	}

	if t.Kind() == reflect.Map {
		t = t.Elem()
	}

	SqlQuery = self.getSqlQuery(SqlQuery, args)

	TablName := t.String()
	UniTable, Valid := self.HashTabl[TablName]
	if !Valid {
		return fmt.Errorf("UniEngine: no such class registered: %s", TablName)
	}

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: select.sql", SqlQuery)
		fmt.Println("UniEngine: select.val", args)
	}

	eror = self.prepareCtx(ctx, SqlQuery)
	if eror != nil {
		return eror
	}
	defer self.release()

	rows, eror := self.st.QueryContext(ctx, args...)
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

	var Result = reflect.Indirect(reflect.ValueOf(i))

	for rows.Next() {

		u := reflect.New(t)

		switch t.Implements(THasSetSqlResult) {
		case true:
			{
				for i := 0; i < cCount; i++ {
					values[i] = &fields[i]
				}

				eror = rows.Scan(values...)
				if eror != nil {
					return eror
				}

				x := u.Interface().(HasSetSqlResult)
				x.SetSqlResult(*self, u.Interface(), column, fields)

				var MapUnique string
				if f != nil {
					MapUnique = f(u.Elem().Interface())
				}
				Result.SetMapIndex(reflect.ValueOf(MapUnique), u.Elem())
			}
		default:
			{
				for ColIndex, ItemPara := range column {
					UniField, Valid := UniTable.HashField[strings.ToLower(ItemPara)]
					if !Valid {
						return fmt.Errorf("UniEngine: database have field[%s], but not in class[%s]", ItemPara, t.String())
					}
					values[ColIndex] = u.Elem().FieldByName(UniField.AttriName).Addr().Interface()
				}

				eror = rows.Scan(values...)
				if eror != nil {
					return eror
				}

				elem := u.Elem()
				if eror = self.DecryptResult(&elem, UniTable, column); eror != nil {
					return eror
				}

				var MapUnique string
				if f != nil {
					MapUnique = f(u.Elem().Interface())
				}
				Result.SetMapIndex(reflect.ValueOf(MapUnique), u.Elem())
			}
		}
	}

	return nil
}

func (self *TUniEngine) SaveIt(i interface{}, args ...interface{}) error {
	return self.SaveItCtx(context.Background(), i, args...)
}

func (self *TUniEngine) SaveItCtx(ctx context.Context, i interface{}, args ...interface{}) error {

	var eror error
	var mrok bool
	var TablName string

	if len(args) > 0 {
		TablName, mrok = args[0].(string)
		if !mrok {
			return errors.New("UniEngine: method [SaveIt] the second paramter need a string value.")
		}
	}

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	UniTable, Valid := self.HashTabl[t.String()]
	if !Valid {
		return fmt.Errorf("UniEngine: no such class registered: %s", t.String())
	}
	if len(UniTable.HashPkeys) == 0 {
		return fmt.Errorf("UniEngine: no pkeys column in class registered: %s", t.String())
	}

	if TablName == "" {
		TablName = UniTable.TableName
	}

	if !validIdent(TablName) {
		return errors.New("UniEngine: invalid table name: " + TablName)
	}

	v := reflect.Indirect(reflect.ValueOf(i))

	ColIndex := 1
	SqlWhere := ""

	SqlQuery := ""
	SqlValue := make([]interface{}, 0)

	if SqlQuery == "" && len(SqlValue) == 0 {

		for _, ItemPara := range UniTable.HashField {

			if ItemPara.ReadOnly {
				continue
			}

			if _, valid := UniTable.HashPkeys[ItemPara.FieldName]; valid {
				SqlWhere = SqlWhere + " and " + self.getColParam(ItemPara.FieldName) + "=" + self.getValParam(ColIndex)
				SqlValue = append(SqlValue, v.FieldByName(ItemPara.AttriName).Interface())
				ColIndex = ColIndex + 1
			}
		}

		SqlWhere = string(SqlWhere[4:])

		SqlQuery = fmt.Sprintf("select count(1) from %s where %s", TablName, SqlWhere)
	}

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: select.sql", SqlQuery)
		fmt.Println("UniEngine: select.val", SqlValue)
	}

	cCount, eror := self.SelectDCtx(ctx, SqlQuery, SqlValue...)
	if eror != nil {
		return eror
	}

	if self.runDebug {
		fmt.Println("UniEngine: select.cnt", cCount)
	}

	if cCount == 1 {
		return self.UpdateCtx(ctx, i, args...)
	}
	return self.InsertCtx(ctx, i, args...)
}

func (self *TUniEngine) SaveItWhenNotExist(i interface{}, args ...interface{}) error {
	return self.SaveItWhenNotExistCtx(context.Background(), i, args...)
}

func (self *TUniEngine) SaveItWhenNotExistCtx(ctx context.Context, i interface{}, args ...interface{}) error {

	var eror error
	var mrok bool
	var TablName string

	if len(args) > 0 {
		TablName, mrok = args[0].(string)
		if !mrok {
			return errors.New("UniEngine: method [SaveItWhenNotExist] the second paramter need a string value.")
		}
	}

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	UniTable, Valid := self.HashTabl[t.String()]
	if !Valid {
		return fmt.Errorf("UniEngine: no such class registered: %s", t.String())
	}
	if len(UniTable.HashPkeys) == 0 {
		return fmt.Errorf("UniEngine: no pkeys column in class registered: %s", t.String())
	}

	if TablName == "" {
		TablName = UniTable.TableName
	}

	if !validIdent(TablName) {
		return errors.New("UniEngine: invalid table name: " + TablName)
	}

	v := reflect.Indirect(reflect.ValueOf(i))

	ColIndex := 1
	SqlWhere := ""

	SqlQuery := ""
	SqlValue := make([]interface{}, 0)

	if SqlQuery == "" && len(SqlValue) == 0 {
		for _, item := range UniTable.HashField {

			if item.ReadOnly {
				continue
			}

			if _, valid := UniTable.HashPkeys[item.FieldName]; valid {
				SqlWhere = SqlWhere + " and " + self.getColParam(item.FieldName) + "=" + self.getValParam(ColIndex)
				SqlValue = append(SqlValue, v.FieldByName(item.AttriName).Interface())
				ColIndex = ColIndex + 1
			}
		}

		SqlWhere = string(SqlWhere[4:])

		SqlQuery = fmt.Sprintf("select count(1) from %s where %s", TablName, SqlWhere)
	}

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: select.sql", SqlQuery)
		fmt.Println("UniEngine: select.val", SqlValue)
	}

	cCount, eror := self.SelectDCtx(ctx, SqlQuery, SqlValue...)
	if eror != nil {
		return eror
	}

	if self.runDebug {
		fmt.Println("UniEngine: select.cnt", cCount)
	}

	if cCount == 0 {
		return self.InsertCtx(ctx, i, args...)
	}

	return nil
}

func (self *TUniEngine) Update(i interface{}, args ...interface{}) error {
	return self.UpdateCtx(context.Background(), i, args...)
}

func (self *TUniEngine) UpdateCtx(ctx context.Context, i interface{}, args ...interface{}) error {

	var eror error
	var mrok bool
	var TablName string

	if len(args) > 0 {
		TablName, mrok = args[0].(string)
		if !mrok {
			return errors.New("UniEngine: method [Update] the second paramter need a string value.")
		}
	}

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	UniTable, Valid := self.HashTabl[t.String()]
	if !Valid {
		return fmt.Errorf("UniEngine: no such class registered: %s", t.String())
	}
	if len(UniTable.HashPkeys) == 0 {
		return fmt.Errorf("UniEngine: no pkeys column in class registered: %s", t.String())
	}
	if TablName == "" {
		TablName = UniTable.TableName
	}

	if !validIdent(TablName) {
		return errors.New("UniEngine: invalid table name: " + TablName)
	}

	//#打印语句
	if self.runDebug {
		fmt.Printf("UniEngine: try update table:%s\n", UniTable.TableName)
	}

	v := reflect.Indirect(reflect.ValueOf(i))

	ColIndex := 1
	UniField := ""
	SqlWhere := ""

	SqlQuery := ""
	SqlValue := make([]interface{}, 0)
	xValue := make([]interface{}, 0)
	zValue := make([]interface{}, 0)

	if x, ok := v.Interface().(HasGetSqlUpdate); ok {
		SqlQuery = x.GetSqlUpdate(*self, TablName)
	}
	if x, ok := v.Interface().(HasSetSqlValues); ok {
		x.SetSqlValues(*self, EtUpdate, &SqlValue)
	}

	if SqlQuery == "" && len(SqlValue) == 0 {
		for _, ItemPara := range UniTable.HashField {

			if ItemPara.ReadOnly {
				continue
			}

			if _, valid := UniTable.HashPkeys[strings.ToLower(ItemPara.FieldName)]; valid {
				continue
			}

			UniField = UniField + "," + self.getColParam(ItemPara.FieldName) + "=" + self.getValParam(ColIndex)

			if ItemPara.Encrypt {
				Value, eror := self.secretEncrypt(v.FieldByName(ItemPara.AttriName))
				if eror != nil {
					return eror
				}
				xValue = append(xValue, Value)
				ColIndex = ColIndex + 1
				continue
			}
			xValue = append(xValue, v.FieldByName(ItemPara.AttriName).Interface())

			ColIndex = ColIndex + 1
		}

		for _, ItemPara := range UniTable.HashPkeys {

			SqlWhere = SqlWhere + " and " + self.getColParam(ItemPara.FieldName) + "=" + self.getValParam(ColIndex)
			zValue = append(zValue, v.FieldByName(ItemPara.AttriName).Interface())
			ColIndex = ColIndex + 1
		}

		UniField = string(UniField[1:])
		SqlWhere = string(SqlWhere[1:])

		SqlQuery = fmt.Sprintf("update %s set %s where 1=1 %s", TablName, UniField, SqlWhere)

		SqlValue = append(xValue, zValue...)
	}

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: update.sql:", SqlQuery)
		fmt.Println("UniEngine: update.val:", SqlValue)
	}

	eror = self.prepareCtx(ctx, SqlQuery)
	if eror != nil {
		return eror
	}
	defer self.release()

	_, eror = self.st.ExecContext(ctx, SqlValue...)
	if eror != nil {
		return eror
	}

	return nil
}

func (self *TUniEngine) Insert(i interface{}, args ...interface{}) error {
	return self.InsertCtx(context.Background(), i, args...)
}

func (self *TUniEngine) InsertCtx(ctx context.Context, i interface{}, args ...interface{}) error {

	var eror error
	var mrok bool
	var TablName string

	if len(args) > 0 {
		TablName, mrok = args[0].(string)
		if !mrok {
			return errors.New("UniEngine: method [Insert] the second paramter need a string value.")
		}
	}

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Struct {
		return errors.New("UniEngine: method [Insert] only retun a struct; may be you should try [InsertL]")
	}

	UniTable, Valid := self.HashTabl[t.String()]
	if !Valid {
		return fmt.Errorf("UniEngine: no such class registered: %s", t.String())
	}
	if TablName == "" {
		TablName = UniTable.TableName
	}

	if !validIdent(TablName) {
		return errors.New("UniEngine: invalid table name: " + TablName)
	}

	v := reflect.Indirect(reflect.ValueOf(i))

	ColIndex := 1
	UniField := ""
	SqlParam := ""

	SqlQuery := ""
	SqlValue := make([]interface{}, 0)

	if x, ok := v.Interface().(HasGetSqlInsert); ok {
		SqlQuery = x.GetSqlInsert(*self, TablName)
	}
	if x, ok := v.Interface().(HasSetSqlValues); ok {
		x.SetSqlValues(*self, EtInsert, &SqlValue)
	}

	if SqlQuery == "" && len(SqlValue) == 0 {

		for _, ItemPara := range UniTable.HashField {

			if ItemPara.ReadOnly {
				continue
			}

			UniField = UniField + "," + self.getColParam(ItemPara.FieldName)
			SqlParam = SqlParam + "," + self.getValParam(ColIndex)
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

		UniField = string(UniField[1:])
		SqlParam = string(SqlParam[1:])

		SqlQuery = fmt.Sprintf("insert into %s ( %s ) values ( %s ) ", TablName, UniField, SqlParam)
	}

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: insert.sql", SqlQuery)
		fmt.Println("UniEngine: insert.val", SqlValue)
	}

	eror = self.prepareCtx(ctx, SqlQuery)
	if eror != nil {
		return eror
	}
	defer self.release()

	_, eror = self.st.ExecContext(ctx, SqlValue...)
	if eror != nil {
		return eror
	}

	return nil
}

func (self *TUniEngine) InsertL(i interface{}, args ...interface{}) error {
	return self.InsertLCtx(context.Background(), i, args...)
}

func (self *TUniEngine) InsertLCtx(ctx context.Context, i interface{}, args ...interface{}) error {

	var eror error
	var mrok bool
	var TablName string

	if len(args) > 0 {
		TablName, mrok = args[0].(string)
		if !mrok {
			return errors.New("UniEngine: method [InsertL] the second paramter need a string value.")
		}
	}

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Slice {
		return errors.New("UniEngine: method [InsertL] need a slice params; check your code;")
	}
	t = t.Elem()

	if self.runDebug {
		fmt.Println(t)
	}

	UniTable, Valid := self.HashTabl[t.String()]
	if !Valid {
		return fmt.Errorf("UniEngine: no such class registered: %s", t.String())
	}
	if TablName == "" {
		TablName = UniTable.TableName
	}

	if !validIdent(TablName) {
		return errors.New("UniEngine: invalid table name: " + TablName)
	}

	v := reflect.Indirect(reflect.ValueOf(i))

	if v.Len() == 0 {
		return nil
	}

	ColIndex := 1
	UniField := ""
	SqlParam := ""
	EndParam := ""

	SqlQuery := ""
	SqlValue := make([]interface{}, 0)

	if x, ok := v.Index(0).Interface().(HasGetSqlInsertL); ok {
		SqlQuery = x.GetSqlInsertL(*self, TablName, int64(v.Len()))
	}

	if x, ok := v.Index(0).Interface().(HasSetSqlValuesL); ok {
		for m := 0; m < v.Len(); m++ {
			f := v.Index(m)
			x.SetSqlValuesL(*self, EtInsert, f, &SqlValue)
		}
	}

	if SqlQuery == "" && len(SqlValue) == 0 {

		//#先设置TablName,UniField
		//#switch map to slice
		var HashField = make([]TUniField, 0)
		for _, ItemPara := range UniTable.HashField {
			if ItemPara.ReadOnly {
				continue
			}
			HashField = append(HashField, ItemPara)
		}

		for _, ItemPara := range HashField {
			UniField = UniField + "," + self.getColParam(ItemPara.FieldName)
		}
		UniField = UniField[1:]

		switch self.Provider {
		case DtORACLE:
			{
				//#先设置TablName,UniField,再设置SqlParam,SqlValue
				for m := 0; m < v.Len(); m++ {

					f := v.Index(m)

					SqlParam = ""
					for _, ItemPara := range HashField {

						SqlParam = SqlParam + "," + self.getValParam(ColIndex)
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

					SqlParam = SqlParam[1:]
					EndParam = EndParam + " " + fmt.Sprintf("into %s ( %s ) values ( %s )", TablName, UniField, SqlParam)
				}

				EndParam = EndParam[1:]
				SqlQuery = fmt.Sprintf("insert all %s select 1 from dual", EndParam)
			}
		default:
			{
				//#先设置TablName,UniField,再设置SqlParam,SqlValue
				for m := 0; m < v.Len(); m++ {

					f := v.Index(m)

					SqlParam = ""
					for _, ItemPara := range HashField {

						SqlParam = SqlParam + "," + self.getValParam(ColIndex)
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

					SqlParam = SqlParam[1:]
					EndParam = EndParam + "," + fmt.Sprintf("( %s )", SqlParam)
				}

				EndParam = EndParam[1:]
				SqlQuery = fmt.Sprintf("insert into %s ( %s ) values %s", TablName, UniField, EndParam)
			}
		}
	}

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: insert.sql:", SqlQuery)
		fmt.Println("UniEngine: insert.val:", SqlValue)
	}

	eror = self.prepareCtx(ctx, SqlQuery)
	if eror != nil {
		return eror
	}
	defer self.release()

	_, eror = self.st.ExecContext(ctx, SqlValue...)
	if eror != nil {
		return fmt.Errorf("%s@UniEngine: if errored too many parameters; try [InsertP(PageSize)] method;", eror.Error())
	}

	return nil
}

func (self *TUniEngine) CopyInL(i interface{}, args ...interface{}) error {
	return self.CopyInLCtx(context.Background(), i, args...)
}

// #已废弃:旧版命名,新代码请用 CopyInL;仅为源码兼容保留
func (self *TUniEngine) SpecialInsertL(i interface{}, args ...interface{}) error {
	return self.CopyInLCtx(context.Background(), i, args...)
}

// #已废弃:旧版命名,新代码请用 CopyInLCtx;仅为源码兼容保留
func (self *TUniEngine) SpecialInsertLCtx(ctx context.Context, i interface{}, args ...interface{}) error {
	return self.CopyInLCtx(ctx, i, args...)
}

func (self *TUniEngine) CopyInLCtx(ctx context.Context, i interface{}, args ...interface{}) error {

	var eror error
	var mrok bool
	var TablName string

	if len(args) > 0 {
		TablName, mrok = args[0].(string)
		if !mrok {
			return errors.New("UniEngine: method [CopyInL] the second paramter need a string value.")
		}
	}

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Slice {
		return errors.New("UniEngine: method [CopyInL] need a slice params; check your code;")
	}
	t = t.Elem()

	if self.runDebug {
		fmt.Println(t)
	}

	UniTable, Valid := self.HashTabl[t.String()]
	if !Valid {
		return fmt.Errorf("UniEngine: no such class registered: %s", t.String())
	}
	if TablName == "" {
		TablName = UniTable.TableName
	}

	if !validIdent(TablName) {
		return errors.New("UniEngine: invalid table name: " + TablName)
	}

	v := reflect.Indirect(reflect.ValueOf(i))

	if v.Len() == 0 {
		return nil
	}

	UniField := ""

	SqlQuery := make([]string, 0)
	SqlValue := make([][]interface{}, 0)

	if x, ok := v.Index(0).Interface().(HasCopyInGetSqlInsertL); ok {
		SqlQuery = x.CopyInGetSqlInsertL(*self, TablName, int64(v.Len()))
	} else if x, ok := v.Index(0).Interface().(HasSpecialGetSqlInsertL); ok {
		//#兼容旧版接口
		SqlQuery = x.SpecialGetSqlInsertL(*self, TablName, int64(v.Len()))
	}

	if x, ok := v.Index(0).Interface().(HasCopyInSetSqlValuesL); ok {
		for m := 0; m < v.Len(); m++ {
			f := v.Index(m)
			x.CopyInSetSqlValuesL(*self, EtInsert, f, &SqlValue)
		}
	} else if x, ok := v.Index(0).Interface().(HasSpecialSetSqlValuesL); ok {
		//#兼容旧版接口
		for m := 0; m < v.Len(); m++ {
			f := v.Index(m)
			x.SpecialSetSqlValuesL(*self, EtInsert, f, &SqlValue)
		}
	}

	if len(SqlQuery) == 0 && len(SqlValue) == 0 {

		//#先设置TablName,UniField
		//#switch map to slice
		var HashField = make([]TUniField, 0)
		for _, ItemPara := range UniTable.HashField {
			if ItemPara.ReadOnly {
				continue
			}
			HashField = append(HashField, ItemPara)
		}

		for _, ItemPara := range HashField {
			UniField = UniField + "," + self.getColParam(ItemPara.FieldName)
			SqlQuery = append(SqlQuery, ItemPara.FieldName)
		}

		//#先设置TablName,UniField,再设置SqlParam,SqlValue
		for m := 0; m < v.Len(); m++ {

			f := v.Index(m)

			var row = make([]interface{}, 0)

			for _, ItemPara := range HashField {

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

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: insert.sql:", SqlQuery)
		fmt.Println("UniEngine: insert.val:", SqlValue)
	}

	//#PolarDB
	Sql4Text := strings.ToLower(pq.CopyIn(TablName, SqlQuery...))

	eror = self.prepareCtx(ctx, Sql4Text)
	if eror != nil {
		return fmt.Errorf("%s@UniEngine: if errored too many parameters; try [CopyInP(PageSize)] method;", eror.Error())
	}
	defer self.st.Close()

	// 执行所有行
	for _, row := range SqlValue {
		_, eror = self.st.ExecContext(ctx, row...)
		if eror != nil {
			return eror
		}
	}

	// 完成 COPY
	_, eror = self.st.ExecContext(ctx)
	if eror != nil {
		return fmt.Errorf("%s@UniEngine: if errored too many parameters; try [CopyInP(PageSize)] method;", eror.Error())
	}

	return nil
}

func (self *TUniEngine) InsertP(i interface{}, PageSize int64, args ...interface{}) error {
	return self.InsertPCtx(context.Background(), i, PageSize, args...)
}

func (self *TUniEngine) InsertPCtx(ctx context.Context, i interface{}, PageSize int64, args ...interface{}) error {

	var eror error

	if PageSize == 0 || PageSize == -1 {
		PageSize = 999
	}

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Slice {
		return errors.New("UniEngine: method [InsertP] need a slice params; check your code;")
	}

	if self.runDebug {
		fmt.Println(t)
	}

	var All4Data = reflect.Indirect(reflect.ValueOf(i))
	var ListData = reflect.MakeSlice(t, 0, 0)

	for I := 0; I < All4Data.Len(); I++ {

		Value := All4Data.Index(I)
		ListData = reflect.Append(ListData, Value)

		if ListData.Len() == int(PageSize) {

			switch self.Supplier {
			case DtPOLODB:
				{
					eror = self.CopyInLCtx(ctx, ListData.Interface(), args...)
				}
			default:
				{
					eror = self.InsertLCtx(ctx, ListData.Interface(), args...)
				}
			}

			if eror != nil {
				return eror
			}
			ListData = reflect.MakeSlice(t, 0, 0)
		}
	}

	switch self.Supplier {
	case DtPOLODB:
		{
			eror = self.CopyInLCtx(ctx, ListData.Interface(), args...)
		}
	default:
		{
			eror = self.InsertLCtx(ctx, ListData.Interface(), args...)
		}
	}

	if eror != nil {
		return eror
	}

	return nil
}

func (self *TUniEngine) CopyInP(i interface{}, PageSize int64, args ...interface{}) error {
	return self.CopyInPCtx(context.Background(), i, PageSize, args...)
}

// #已废弃:旧版命名,新代码请用 CopyInP;仅为源码兼容保留
func (self *TUniEngine) SpecialInsertP(i interface{}, PageSize int64, args ...interface{}) error {
	return self.CopyInPCtx(context.Background(), i, PageSize, args...)
}

// #已废弃:旧版命名,新代码请用 CopyInPCtx;仅为源码兼容保留
func (self *TUniEngine) SpecialInsertPCtx(ctx context.Context, i interface{}, PageSize int64, args ...interface{}) error {
	return self.CopyInPCtx(ctx, i, PageSize, args...)
}

func (self *TUniEngine) CopyInPCtx(ctx context.Context, i interface{}, PageSize int64, args ...interface{}) error {

	var eror error

	if PageSize == 0 || PageSize == -1 {
		PageSize = 999
	}

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Slice {
		return errors.New("UniEngine: method [CopyInP] need a slice params; check your code;")
	}

	if self.runDebug {
		fmt.Println(t)
	}

	var All4Data = reflect.Indirect(reflect.ValueOf(i))
	var ListData = reflect.MakeSlice(t, 0, 0)

	for I := 0; I < All4Data.Len(); I++ {

		Value := All4Data.Index(I)
		ListData = reflect.Append(ListData, Value)

		if ListData.Len() == int(PageSize) {
			eror = self.CopyInLCtx(ctx, ListData.Interface(), args...)
			if eror != nil {
				return eror
			}
			ListData = reflect.MakeSlice(t, 0, 0)
		}
	}

	eror = self.CopyInLCtx(ctx, ListData.Interface(), args...)
	if eror != nil {
		return eror
	}

	return nil
}

func (self *TUniEngine) Delete(i interface{}, args ...interface{}) error {
	return self.DeleteCtx(context.Background(), i, args...)
}

func (self *TUniEngine) DeleteCtx(ctx context.Context, i interface{}, args ...interface{}) error {

	var eror error
	var mrok bool
	var TablName string

	if len(args) > 0 {
		TablName, mrok = args[0].(string)
		if !mrok {
			return errors.New("UniEngine: method [Delete] the second paramter need a string value.")
		}
	}

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	UniTable, Valid := self.HashTabl[t.String()]
	if !Valid {
		return fmt.Errorf("UniEngine: no such class registered: %s", t.String())
	}
	if len(UniTable.HashPkeys) == 0 {
		return fmt.Errorf("UniEngine: no pkeys column in class registered: %s", t.String())
	}

	if TablName == "" {
		TablName = UniTable.TableName
	}

	if !validIdent(TablName) {
		return errors.New("UniEngine: invalid table name: " + TablName)
	}

	v := reflect.Indirect(reflect.ValueOf(i))

	ColIndex := 1
	SqlWhere := ""
	SqlValue := make([]interface{}, 0)

	for _, ItemPara := range UniTable.HashPkeys {

		SqlWhere = SqlWhere + " and " + self.getColParam(ItemPara.FieldName) + "=" + self.getValParam(ColIndex)
		ColIndex = ColIndex + 1

		SqlValue = append(SqlValue, v.FieldByName(ItemPara.AttriName).Interface())
	}

	SqlWhere = string(SqlWhere[4:])

	SqlQuery := fmt.Sprintf("delete from %s where %s", TablName, SqlWhere)

	if self.runDebug {
		fmt.Println("UniEngine: delete.sql:", SqlQuery)
		fmt.Println("UniEngine: delete.val:", SqlValue)
	}

	eror = self.prepareCtx(ctx, SqlQuery)
	if eror != nil {
		return eror
	}
	defer self.release()

	_, eror = self.st.ExecContext(ctx, SqlValue...)
	if eror != nil {
		return eror
	}

	return nil
}

func (self *TUniEngine) Execute(SqlQuery string, args ...interface{}) error {
	return self.ExecuteCtx(context.Background(), SqlQuery, args...)
}

func (self *TUniEngine) ExecuteCtx(ctx context.Context, SqlQuery string, args ...interface{}) error {

	var eror error

	SqlQuery = self.getSqlQuery(SqlQuery, args)

	if self.runDebug {
		fmt.Printf("UniEngine: execute.sql:%s\n", SqlQuery)
	}

	if self.canClose {
		self.st, eror = self.Db.PrepareContext(ctx, SqlQuery)
	} else {
		self.st, eror = self.tx.PrepareContext(ctx, SqlQuery)
	}

	if eror != nil {
		return eror
	}
	defer self.release()

	_, eror = self.st.ExecContext(ctx, args...)
	if eror != nil {
		return eror
	}

	return nil
}

func (self *TUniEngine) ExecuteMust(SqlQuery string, args ...interface{}) error {
	return self.ExecuteMustCtx(context.Background(), SqlQuery, args...)
}

func (self *TUniEngine) ExecuteMustCtx(ctx context.Context, SqlQuery string, args ...interface{}) error {

	var eror error

	SqlQuery = self.getSqlQuery(SqlQuery, args)

	if self.canClose {
		self.st, eror = self.Db.PrepareContext(ctx, SqlQuery)
	} else {
		self.st, eror = self.tx.PrepareContext(ctx, SqlQuery)
	}

	if eror != nil {
		return eror
	}
	defer self.release()

	result, eror := self.st.ExecContext(ctx, args...)
	if eror != nil {
		return eror
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

	var eror error
	var cSQL string

	mrok, eror := self.ExistViewsCtx(ctx, TableName)
	if eror != nil {
		return false, eror
	}

	switch mrok {
	case true:
		{
			cSQL = fmt.Sprintf("DROP VIEW %s", TableName)
			eror = self.ExecuteCtx(ctx, cSQL)
			if eror != nil {
				return false, eror
			}
		}
	default:
		{
		}
	}

	return true, nil
}

func (self *TUniEngine) ExistTable(TableName string) (bool, error) {
	return self.ExistTableCtx(context.Background(), TableName)
}

func (self *TUniEngine) ExistTableCtx(ctx context.Context, TableName string) (bool, error) {

	if !validIdent(TableName) {
		return false, errors.New("UniEngine: invalid table name: " + TableName)
	}

	var eror error
	cSQL := ""

	switch self.Provider {
	case DtPOSTGR:
		{
			var ExistTable4POSTGR = TExistTable4POSTGR{}
			cSQL = ExistTable4POSTGR.GetSqlExistTable(*self, TableName)
		}
	case DtSQLSRV:
		{
			var ExistTable4SQLSRV = TExistTable4SQLSRV{}
			cSQL = ExistTable4SQLSRV.GetSqlExistTable(*self, TableName)
		}
	case DtORACLE:
		{
			var ExistTable4ORACLE = TExistTable4ORACLE{}
			cSQL = ExistTable4ORACLE.GetSqlExistTable(*self, TableName)
		}
	case DtMYSQLN:
		{
			if self.DataBase == "" {
				return false, errors.New("UniEngine: database is not specified")
			}
			var ExistTable4MYSQLN = TExistTable4MYSQLN{}
			cSQL = ExistTable4MYSQLN.GetSqlExistTable(*self, TableName, self.DataBase)
		}
	}

	if self.runDebug {
		fmt.Printf("UniEngine: existtable.sql:%s\n", cSQL)
	}

	if cSQL == "" {
		return false, errors.New("UniEngine: no sql for existtable")
	}

	Size, eror := self.SelectDCtx(ctx, cSQL)
	if eror != nil {
		return false, eror
	}
	if Size == 0 {
		return false, nil
	}

	return true, eror
}

func (self *TUniEngine) ExistViews(TableName string) (bool, error) {
	return self.ExistViewsCtx(context.Background(), TableName)
}

func (self *TUniEngine) ExistViewsCtx(ctx context.Context, TableName string) (bool, error) {

	if !validIdent(TableName) {
		return false, errors.New("UniEngine: invalid table name: " + TableName)
	}

	var eror error
	cSQL := ""

	switch self.Provider {
	case DtPOSTGR:
		{
			var ExistTable4POSTGR = TExistTable4POSTGR{}
			cSQL = ExistTable4POSTGR.GetSqlExistViews(*self, TableName)
		}
	case DtSQLSRV:
		{
			var ExistTable4SQLSRV = TExistTable4SQLSRV{}
			cSQL = ExistTable4SQLSRV.GetSqlExistViews(*self, TableName)
		}
	case DtORACLE:
		{
			var ExistTable4ORACLE = TExistTable4ORACLE{}
			cSQL = ExistTable4ORACLE.GetSqlExistViews(*self, TableName)
		}
	case DtMYSQLN:
		{
			if self.DataBase == "" {
				return false, errors.New("UniEngine: database is not specified")
			}
			var ExistTable4MYSQLN = TExistTable4MYSQLN{}
			cSQL = ExistTable4MYSQLN.GetSqlExistViews(*self, TableName, self.DataBase)
		}
	}

	if self.runDebug {
		fmt.Printf("UniEngine: existtable.sql:%s\n", cSQL)
	}

	if cSQL == "" {
		return false, errors.New("UniEngine: no sql for existtable")
	}

	Size, eror := self.SelectDCtx(ctx, cSQL)
	if eror != nil {
		return false, eror
	}
	if Size == 0 {
		return false, nil
	}

	return true, eror
}

func (self *TUniEngine) ExistField(TableName, FieldName string) (bool, error) {
	return self.ExistFieldCtx(context.Background(), TableName, FieldName)
}

func (self *TUniEngine) ExistFieldCtx(ctx context.Context, TableName, FieldName string) (bool, error) {

	if !validIdent(TableName) || !validIdent(FieldName) {
		return false, errors.New("UniEngine: invalid table/field name: " + TableName + "." + FieldName)
	}

	var eror error
	cSQL := ""

	switch self.Provider {
	case DtPOSTGR:
		{
			var ExistField4POSTGR = TExistField4POSTGR{}
			cSQL = ExistField4POSTGR.GetSqlExistField(*self, TableName, FieldName)
		}
	case DtSQLSRV:
		{
			var ExistField4SQLSRV = TExistField4SQLSRV{}
			cSQL = ExistField4SQLSRV.GetSqlExistField(*self, TableName, FieldName)
		}
	case DtORACLE:
		{
			var ExistField4ORACLE = TExistField4ORACLE{}
			cSQL = ExistField4ORACLE.GetSqlExistField(*self, TableName, FieldName)
		}
	case DtMYSQLN:
		{
			if self.DataBase == "" {
				return false, errors.New("UniEngine: database is not specified")
			}
			var ExistField4MYSQLN = TExistField4MYSQLN{}
			cSQL = ExistField4MYSQLN.GetSqlExistField(*self, TableName, FieldName, self.DataBase)
		}
	}

	if cSQL == "" {
		return false, errors.New("UniEngine: no sql for existfield")
	}

	Size, eror := self.SelectDCtx(ctx, cSQL)
	if eror != nil {
		return false, eror
	}
	if Size == 0 {
		return false, nil
	}

	return true, eror
}

// #ExistConst 判断指定类型的约束是否存在(按约束名/列名匹配)
func (self *TUniEngine) ExistConst(aConstType TConstType, aConstName string) (bool, error) {
	return self.ExistConstCtx(context.Background(), aConstType, aConstName)
}

func (self *TUniEngine) ExistConstCtx(ctx context.Context, aConstType TConstType, aConstName string) (bool, error) {

	if !validIdent(aConstName) {
		return false, errors.New("UniEngine: invalid constraint name: " + aConstName)
	}

	var eror error
	cSQL := ""

	switch self.Provider {
	case DtPOSTGR:
		{
			var ExistConst4POSTGR = TExistConst4POSTGR{}
			cSQL = ExistConst4POSTGR.GetSqlExistConst(*self, aConstType, aConstName)
		}
	case DtSQLSRV:
		{
			var ExistConst4SQLSRV = TExistConst4SQLSRV{}
			cSQL = ExistConst4SQLSRV.GetSqlExistConst(*self, aConstType, aConstName)
		}
	case DtORACLE:
		{
			var ExistConst4ORACLE = TExistConst4ORACLE{}
			cSQL = ExistConst4ORACLE.GetSqlExistConst(*self, aConstType, aConstName)
		}
	case DtMYSQLN:
		{
			if self.DataBase == "" {
				return false, errors.New("UniEngine: database is not specified")
			}
			var ExistConst4MYSQLN = TExistConst4MYSQLN{}
			cSQL = ExistConst4MYSQLN.GetSqlExistConst(*self, aConstType, aConstName, self.DataBase)
		}
	}

	if self.runDebug {
		fmt.Println("UniEngine: existconst.sql", cSQL)
	}

	if cSQL == "" {
		return false, errors.New("UniEngine: no sql for existconst")
	}

	Size, eror := self.SelectDCtx(ctx, cSQL)
	if eror != nil {
		return false, eror
	}
	if Size == 0 {
		return false, nil
	}

	return true, nil
}

func (self *TUniEngine) prepareCtx(ctx context.Context, SqlQuery string) error {

	var eror error

	switch self.canClose {
	case true:
		{
			self.st, eror = self.Db.PrepareContext(ctx, SqlQuery)
		}
	default:
		{
			self.st, eror = self.tx.PrepareContext(ctx, SqlQuery)
		}
	}

	return eror
}

func (self *TUniEngine) release() error {

	var eror error

	eror = self.st.Close()

	return eror
}

func (self *TUniEngine) Begin() error {
	return self.BeginCtx(context.Background())
}

func (self *TUniEngine) BeginCtx(ctx context.Context) error {

	var eror error

	self.tx, eror = self.Db.BeginTx(ctx, nil)
	if eror != nil {
		return eror
	}
	self.canClose = false

	return nil
}

func (self *TUniEngine) Cancel() error {

	var eror error

	if self.canClose {
		return errors.New("UniEngine: no transaction")
	}

	eror = self.tx.Rollback()
	if eror != nil {
		return eror
	}

	self.canClose = true

	return nil
}

func (self *TUniEngine) Commit() error {

	var eror error

	if self.canClose {
		return errors.New("UniEngine: no transaction")
	}

	eror = self.tx.Commit()
	if eror != nil {
		return eror
	}

	self.canClose = true

	return nil
}

func (self *TUniEngine) CanClose() error {

	//debug helper: intentionally a no-op now that the debug print was removed.

	return nil
}

func (self *TUniEngine) RunDebug(Value bool) error {

	self.runDebug = Value

	return nil
}

func (self *TUniEngine) Initialize() error {

	//#并发契约:注册(RegisterClass/RegisterTable/...)须在并发查询开始前完成;
	//#本锁仅保证注册互斥与 HashTabl 初始化,读路径不持锁
	self.initLock()

	self.RegisterClass(TUniTable{}, "github.com/kazarus/uniengine/unitable")
	self.RegisterClass(TUniField{}, "github.com/kazarus/uniengine/unifield")

	self.canClose = true
	self.runDebug = false

	return nil
}
