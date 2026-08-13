package UniEngine

import (
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

// TUniEngine 引擎实例。注意：单实例非并发安全——st/tx 为共享可变状态，
// 多 goroutine 请各自创建实例（底层 *sql.DB 连接池仍复用）；
// mu 仅保护 HashTabl 的懒初始化与注册阶段，注册完成后应进入只读使用期。
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

	Instance string     //#数据库实例
	DataBase string     //#数据库名称
	DataUser string     //#数据库用户
	Provider TDriveType //#数据库驱动
	Supplier TDriveType //#数据库驱动(区分 DtORACLE VS DtDAMENG)

	canClose bool //default is true;if is transaction,canClose = false
	runDebug bool //default is false;print some sql;
}

// lockTables 保证锁可用并加锁（指针锁懒初始化）
func (self *TUniEngine) lockTables() {

	if self.mu == nil {
		self.mu = &sync.Mutex{}
	}
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
		return self.ColParam
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
	case DtACCESS:
		{
			result = "access"
		}
	case DtSQLITE:
		{
			result = "sqlite"
		}
	case DtKINGES:
		{
			result = "kingbase"
		}
	case DtDAMENG:
		{
			result = "dameng"
		}
	case DtOPENGS:
		{
			result = "opengauss"
		}
	case DtPOLODB:
		{
			result = "polardb"
		}
	case DtTAURUS:
		{
			result = "taurus"
		}
	default:
		{
			result = "unknown"
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
	default:
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
		//@UniField.FieldType = f.Type

		UniField.initialize(f.Tag.Get(self.ColLabel))

		UniTable.HashField[strings.ToLower(UniField.FieldName)] = UniField
		// 保留 struct 声明顺序，供 INSERT/UPDATE 列序生成
		UniTable.ListField = append(UniTable.ListField, UniField)
	}

	// 同时以小写表名（PrepareTables/PrepareRunSQL 路径）与类全名（SaveIt/Insert/Delete/Select* 路径）注册，
	// 统一两套 key 的可见性；两者指向同一表（map 值拷贝共享 HashField/HashPkeys 引用）
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
			//@UniTable = self.HashTabl[strings.ToLower(TableName)]
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
			//@UniTable = self.HashTabl[strings.ToLower(TableName)]
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
			//@UniTable = self.HashTabl[strings.ToLower(TableName)]

			var UniField = TUniField{}
			UniField.AttriName = ""
			UniField.FieldName = strings.ToLower(FieldName)
			UniField.TableName = strings.ToLower(TableName)

			UniTable.HashPkeys[strings.ToLower(UniField.FieldName)] = UniField

			self.HashTabl[strings.ToLower(TableName)] = UniTable
		}
	default:
		{
			UniTable = &TUniTable{}
			UniTable.HashField = make(map[string]TUniField, 0)
			UniTable.HashPkeys = make(map[string]TUniField, 0)
			UniTable.TableName = strings.ToLower(TableName)

			var UniField = TUniField{}
			UniField.AttriName = ""
			UniField.FieldName = strings.ToLower(FieldName)
			UniField.TableName = strings.ToLower(TableName)

			UniTable.HashPkeys[strings.ToLower(UniField.FieldName)] = UniField

			self.HashTabl[strings.ToLower(TableName)] = UniTable
		}
	}

	return UniTable
}

func (self *TUniEngine) GetTable(TableName string) *TUniTable {

	self.ensureTables()

	UniTable, _ := self.HashTabl[strings.ToLower(TableName)]
	if UniTable == nil {
		return &TUniTable{}
	}

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
			// 保留 RegisterClass 按声明顺序 / SetKeys 按注册顺序维护的有序列表，仅在为空时从 map 重建
			if len(UniTable.ListField) == 0 && len(UniTable.HashField) > 0 {

				for _, ItemPara := range UniTable.HashField {

					if ItemPara.ReadOnly {
						continue
					}

					UniTable.ListField = append(UniTable.ListField, ItemPara)
				}
			}

			if UniTable.ListPkeys == nil {
				UniTable.ListPkeys = make([]TUniField, 0)
			}
			if len(UniTable.ListPkeys) == 0 && len(UniTable.HashPkeys) > 0 {

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

						pkeys := UniTable.ListPkeys
						if len(pkeys) == 0 {
							// 兼容 RegisterPkeys 等仅写 HashPkeys 的注册路径
							for _, v := range UniTable.HashPkeys {
								pkeys = append(pkeys, v)
							}
						}

						for _, ItemPara := range pkeys {

							if ItemPara.ReadOnly {
								continue
							}

							SqlWhere = SqlWhere + " and " + self.getColParam(ItemPara.FieldName) + "=" + self.getValParam(ColIndex)
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

						if SqlField == "" || SqlParam == "" {
							return "", ListField, UniTable.ListPkeys, errors.New("UniEngine: no writable column in table:" + UniTable.TableName)
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

						pkeys := UniTable.ListPkeys
						if len(pkeys) == 0 {
							// 兼容 RegisterPkeys 等仅写 HashPkeys 的注册路径
							for _, v := range UniTable.HashPkeys {
								pkeys = append(pkeys, v)
							}
						}

						for _, item := range pkeys {

							SqlWhere = SqlWhere + " and " + self.getColParam(item.FieldName) + "=" + self.getValParam(ColIndex)
							ColIndex = ColIndex + 1
						}
					}

					if SqlField == "" {
						return "", ListField, UniTable.ListPkeys, errors.New("UniEngine: no writable column in table:" + UniTable.TableName)
					}

					SqlField = string(SqlField[1:])
					if SqlWhere != "" {
						SqlWhere = string(SqlWhere[1:])
					}

					SqlResult = fmt.Sprintf("update %s set %s where 1=1 %s", UniTable.TableName, SqlField, SqlWhere)
					UniTable.SqlUpdate = SqlResult
				}
			}

			self.HashTabl[strings.ToLower(TableName)] = UniTable
		}
	default:
		{
			//UniTable.HashField = make(map[string]TUniField, 0)
			//UniTable.HashPkeys = make(map[string]TUniField, 0)
			//UniTable.TableName = strings.ToLower(TableName)
		}
	}

	return SqlResult, ListField, UniTable.ListPkeys, nil
}

// return int64;
func (self *TUniEngine) SelectD(SqlQuery string, args ...interface{}) (int64, error) {

	var err error
	var size sql.NullInt64

	SqlQuery = self.getSqlQuery(SqlQuery, args)

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: select.sql", SqlQuery)
		fmt.Println("UniEngine: select.val", args)
	}

	err = self.prepare(SqlQuery)
	if err != nil {
		return 0, err
	}
	defer self.release()

	rows, err := self.st.Query(args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	for rows.Next() {
		err = rows.Scan(&size)
		if err != nil {
			return 0, err
		}
	}

	return size.Int64, nil
}

// return float64;
func (self *TUniEngine) SelectF(SqlQuery string, args ...interface{}) (float64, error) {

	var err error
	var size sql.NullFloat64

	SqlQuery = self.getSqlQuery(SqlQuery, args)

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: select.sql", SqlQuery)
		fmt.Println("UniEngine: select.val", args)
	}

	err = self.prepare(SqlQuery)
	if err != nil {
		return 0, err
	}
	defer self.release()

	rows, err := self.st.Query(args...)

	if err != nil {
		return 0, err
	}
	defer rows.Close()

	for rows.Next() {
		err = rows.Scan(&size)
		if err != nil {
			return 0, err
		}
	}
	return size.Float64, nil
}

// return string;
func (self *TUniEngine) SelectS(SqlQuery string, args ...interface{}) (string, error) {

	var err error
	var text sql.NullString

	SqlQuery = self.getSqlQuery(SqlQuery, args)

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: select.sql", SqlQuery)
		fmt.Println("UniEngine: select.val", args)
	}

	err = self.prepare(SqlQuery)
	if err != nil {
		return "", err
	}
	defer self.release()

	rows, err := self.st.Query(args...)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	for rows.Next() {
		err = rows.Scan(&text)
		if err != nil {
			return "", err
		}
	}

	return text.String, nil
}

// return struct;
func (self *TUniEngine) Select(i interface{}, SqlQuery string, args ...interface{}) error {

	var err error

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Struct {
		//TO DO:
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

	//-<
	err = self.prepare(SqlQuery)
	if err != nil {
		return err
	}
	defer self.release()

	rows, err := self.st.Query(args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	//->

	column, err := rows.Columns()
	if err != nil {
		return err
	}
	cCount := len(column)
	fields := make([]interface{}, cCount)
	values := make([]interface{}, cCount)

	for rows.Next() {

		x, ok := i.(HasSetSqlResult)
		switch ok {
		case true:
			{
				for i := 0; i < cCount; i++ {
					values[i] = &fields[i]
				}

				err = rows.Scan(values...)
				if err != nil {
					return err
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

				err = rows.Scan(values...)
				if err != nil {
					return err
				}
			}
		}

		//if x, ok := i.(HasSetSqlResult); ok {
		//	for i := 0; i < cCount; i++ {
		//		values[i] = &fields[i]
		//	}
		//
		//	err = rows.Scan(values...)
		//	if err != nil {
		//		return err
		//	}
		//
		//	x.SetSqlResult(*self, i, column, fields)
		//
		//} else {
		//
		//	var Result = reflect.Indirect(reflect.ValueOf(i))
		//
		//	for ColIndex, ItemPara := range column {
		//		UniField, Valid := UniTable.HashField[strings.ToLower(ItemPara)]
		//		if !Valid {
		//			return errors.New(fmt.Sprintf("UniEngine: database have field[%s], but not in class[%s]", ItemPara, t.String()))
		//		}
		//		values[ColIndex] = Result.FieldByName(UniField.AttriName).Addr().Interface()
		//	}
		//
		//	err = rows.Scan(values...)
		//	if err != nil {
		//		return err
		//	}
		//}
	}

	return nil
}

// return slice of struct;
func (self *TUniEngine) SelectL(i interface{}, SqlQuery string, args ...interface{}) error {

	var err error

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Slice {
		//TO DO:
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

	//-<
	err = self.prepare(SqlQuery)
	if err != nil {
		return err
	}
	defer self.release()

	rows, err := self.st.Query(args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	//->

	//columnstype,err := rows.ColumnTypes()
	//fmt.Println(columnstype)

	column, err := rows.Columns()
	if err != nil {
		return err
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

				err = rows.Scan(values...)
				if err != nil {
					return err
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

				err = rows.Scan(values...)
				if err != nil {
					return err
				}

				Result.Set(reflect.Append(Result, u.Elem()))
			}
		}

		//if t.Implements(THasSetSqlResult) {
		//
		//	for i := 0; i < cCount; i++ {
		//		values[i] = &fields[i]
		//	}
		//
		//	err = rows.Scan(values...)
		//	if err != nil {
		//		return err
		//	}
		//
		//	x := u.Interface().(HasSetSqlResult)
		//	x.SetSqlResult(*self, u.Interface(), column, fields)
		//
		//	Result.Set(reflect.Append(Result, u.Elem()))
		//
		//} else {
		//
		//	for ColIndex, ItemPara := range column {
		//		UniField, Valid := UniTable.HashField[strings.ToLower(ItemPara)]
		//		if !Valid {
		//			return errors.New(fmt.Sprintf("UniEngine: database have field[%s], but not in class[%s]", ItemPara, t.String()))
		//		}
		//		values[ColIndex] = u.Elem().FieldByName(UniField.AttriName).Addr().Interface()
		//	}
		//
		//	err = rows.Scan(values...)
		//	if err != nil {
		//		return err
		//	}
		//
		//	Result.Set(reflect.Append(Result, u.Elem()))
		//}
	}

	return nil
}

// return map of struct;user;GetMapUnique;
func (self *TUniEngine) SelectM(i interface{}, SqlQuery string, args ...interface{}) error {

	var err error

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Map {
		//TO DO:
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

	//-<
	err = self.prepare(SqlQuery)
	if err != nil {
		return err
	}
	defer self.release()

	rows, err := self.st.Query(args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	//->

	column, err := rows.Columns()
	if err != nil {
		return err
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

				err = rows.Scan(values...)
				if err != nil {
					return err
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

				err = rows.Scan(values...)
				if err != nil {
					return err
				}

				var MapUnique string
				if x, ok := u.Interface().(HasGetMapUnique); ok {
					MapUnique = x.GetMapUnique()
				}
				Result.SetMapIndex(reflect.ValueOf(MapUnique), u.Elem())
			}
		}
		//if t.Implements(THasSetSqlResult) {
		//
		//	for i := 0; i < cCount; i++ {
		//		values[i] = &fields[i]
		//	}
		//
		//	err = rows.Scan(values...)
		//	if err != nil {
		//		return err
		//	}
		//
		//	x := u.Interface().(HasSetSqlResult)
		//	x.SetSqlResult(*self, u.Interface(), column, fields)
		//
		//	var MapUnique string
		//	if x, ok := u.Interface().(HasGetMapUnique); ok {
		//		MapUnique = x.GetMapUnique()
		//	}
		//	Result.SetMapIndex(reflect.ValueOf(MapUnique), u.Elem())
		//
		//} else {
		//
		//	for ColIndex, ItemPara := range column {
		//		UniField, Valid := UniTable.HashField[strings.ToLower(ItemPara)]
		//		if !Valid {
		//			return errors.New(fmt.Sprintf("UniEngine: database have field[%s], but not in class[%s]", ItemPara, t.String()))
		//		}
		//		values[ColIndex] = u.Elem().FieldByName(UniField.AttriName).Addr().Interface()
		//	}
		//
		//	err = rows.Scan(values...)
		//	if err != nil {
		//		return err
		//	}
		//
		//	var MapUnique string
		//	if x, ok := u.Interface().(HasGetMapUnique); ok {
		//		MapUnique = x.GetMapUnique()
		//	}
		//	Result.SetMapIndex(reflect.ValueOf(MapUnique), u.Elem())
		//}
	}

	return nil
}

//return map;use custom function;

func (self *TUniEngine) SelectH(i interface{}, f GetMapUnique, SqlQuery string, args ...interface{}) error {

	var err error

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Map {
		//TO DO:
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

	//-<
	err = self.prepare(SqlQuery)
	if err != nil {
		return err
	}
	defer self.release()

	rows, err := self.st.Query(args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	//->

	column, err := rows.Columns()
	if err != nil {
		return err
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

				err = rows.Scan(values...)
				if err != nil {
					return err
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

				err = rows.Scan(values...)
				if err != nil {
					return err
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

	var err error
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
	//@UniField := ""
	SqlWhere := ""

	SqlQuery := ""
	SqlValue := make([]interface{}, 0)

	//@SaveIt方法不需要这一组

	if SqlQuery == "" && len(SqlValue) == 0 {

		pkeys := UniTable.ListPkeys
		if len(pkeys) == 0 {
			// 兼容未维护 ListPkeys 的注册路径
			for _, v := range UniTable.HashPkeys {
				pkeys = append(pkeys, v)
			}
		}

		for _, ItemPara := range pkeys {

			if ItemPara.ReadOnly {
				continue
			}

			SqlWhere = SqlWhere + " and " + self.getColParam(ItemPara.FieldName) + "=" + self.getValParam(ColIndex)
			SqlValue = append(SqlValue, v.FieldByName(ItemPara.AttriName).Interface())
			ColIndex = ColIndex + 1
		}

		//@UniField = string(UniField[1:])
		if SqlWhere == "" {
			return errors.New("UniEngine: no writable pkey column in class: " + t.String())
		}
		SqlWhere = string(SqlWhere[4:])

		SqlQuery = fmt.Sprintf("select count(1) from %s where %s", TablName, SqlWhere)
	}

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: select.sql", SqlQuery)
		fmt.Println("UniEngine: select.val", SqlValue)
	}

	cCount, err := self.SelectD(SqlQuery, SqlValue...)
	if err != nil {
		return err
	}

	if self.runDebug {
		fmt.Println("UniEngine: select.cnt", cCount)
	}

	if cCount == 1 {
		return self.Update(i, args...)
	} else {
		return self.Insert(i, args...)
	}
}

func (self *TUniEngine) SaveItWhenNotExist(i interface{}, args ...interface{}) error {

	var err error
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
	//@UniField := ""
	SqlWhere := ""

	SqlQuery := ""
	SqlValue := make([]interface{}, 0)

	//@SaveIt方法不需要这一组

	if SqlQuery == "" && len(SqlValue) == 0 {

		pkeys := UniTable.ListPkeys
		if len(pkeys) == 0 {
			// 兼容未维护 ListPkeys 的注册路径
			for _, v := range UniTable.HashPkeys {
				pkeys = append(pkeys, v)
			}
		}

		for _, item := range pkeys {

			if item.ReadOnly {
				continue
			}

			SqlWhere = SqlWhere + " and " + self.getColParam(item.FieldName) + "=" + self.getValParam(ColIndex)
			SqlValue = append(SqlValue, v.FieldByName(item.AttriName).Interface())
			ColIndex = ColIndex + 1
		}

		//@UniField = string(UniField[1:])
		if SqlWhere == "" {
			return errors.New("UniEngine: no writable pkey column in class: " + t.String())
		}
		SqlWhere = string(SqlWhere[4:])

		SqlQuery = fmt.Sprintf("select count(1) from %s where %s", TablName, SqlWhere)
	}

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: select.sql", SqlQuery)
		fmt.Println("UniEngine: select.val", SqlValue)
	}

	cCount, err := self.SelectD(SqlQuery, SqlValue...)
	if err != nil {
		return err
	}

	if self.runDebug {
		fmt.Println("UniEngine: select.cnt", cCount)
	}

	if cCount == 0 {
		return self.Insert(i, args...)
	}

	return nil
}

func (self *TUniEngine) Update(i interface{}, args ...interface{}) error {

	var err error
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
		fmt.Println("UniEngine: try update table:" + UniTable.TableName)
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
			xValue = append(xValue, v.FieldByName(ItemPara.AttriName).Interface())

			ColIndex = ColIndex + 1
		}

		pkeys := UniTable.ListPkeys
		if len(pkeys) == 0 {
			// 兼容未维护 ListPkeys 的注册路径
			for _, v := range UniTable.HashPkeys {
				pkeys = append(pkeys, v)
			}
		}

		for _, ItemPara := range pkeys {

			SqlWhere = SqlWhere + " and " + self.getColParam(ItemPara.FieldName) + "=" + self.getValParam(ColIndex)
			zValue = append(zValue, v.FieldByName(ItemPara.AttriName).Interface())
			ColIndex = ColIndex + 1
		}

		if UniField == "" {
			return errors.New("UniEngine: no writable column in class: " + t.String())
		}
		UniField = string(UniField[1:])
		if SqlWhere != "" {
			SqlWhere = string(SqlWhere[1:])
		}

		SqlQuery = fmt.Sprintf("update %s set %s where 1=1 %s", TablName, UniField, SqlWhere)

		SqlValue = append(xValue, zValue...)
	}

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: update.sql:", SqlQuery)
		fmt.Println("UniEngine: update.val:", SqlValue)
	}

	err = self.prepare(SqlQuery)
	if err != nil {
		return err
	}
	defer self.release()

	_, err = self.st.Exec(SqlValue...)
	if err != nil {
		return err
	}

	return nil
}

func (self *TUniEngine) Insert(i interface{}, args ...interface{}) error {

	var err error
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
		//TO DO:
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

			SqlValue = append(SqlValue, v.FieldByName(ItemPara.AttriName).Interface())
		}

		if UniField == "" || SqlParam == "" {
			return errors.New("UniEngine: no writable column in class: " + t.String())
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

	err = self.prepare(SqlQuery)
	if err != nil {
		return err
	}
	defer self.release()

	_, err = self.st.Exec(SqlValue...)
	if err != nil {
		return err
	}

	return nil
}

func (self *TUniEngine) InsertL(i interface{}, args ...interface{}) error {

	var err error
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
		if UniField == "" {
			return errors.New("UniEngine: no writable column in class: " + t.String())
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

	err = self.prepare(SqlQuery)
	if err != nil {
		return err
	}
	defer self.release()

	_, err = self.st.Exec(SqlValue...)
	if err != nil {
		return fmt.Errorf("%s@UniEngine: if errored too many parameters; try [InsertP(PageSize)] method;", err.Error())
	}

	return nil
}

func (self *TUniEngine) SpecialInsertL(i interface{}, args ...interface{}) error {

	var err error
	var mrok bool
	var TablName string

	if len(args) > 0 {
		TablName, mrok = args[0].(string)
		if !mrok {
			return errors.New("UniEngine: method [SpecialInsertL] the second paramter need a string value.")
		}
	}

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Slice {
		return errors.New("UniEngine: method [SpecialInsertL] need a slice params; check your code;")
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

	if x, ok := v.Index(0).Interface().(HasSpecialGetSqlInsertL); ok {
		SqlQuery = x.SpecialGetSqlInsertL(*self, TablName, int64(v.Len()))
	}

	if x, ok := v.Index(0).Interface().(HasSpecialSetSqlValuesL); ok {
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
			//SqlQuery=append(SqlQuery,self.getColParam(ItemPara.FieldName))
			SqlQuery = append(SqlQuery, ItemPara.FieldName)
		}
		//UniField = UniField[1:]

		//#先设置TablName,UniField,再设置SqlParam,SqlValue
		for m := 0; m < v.Len(); m++ {

			f := v.Index(m)

			var row = make([]interface{}, 0)

			for _, ItemPara := range HashField {

				row = append(row, f.FieldByName(ItemPara.AttriName).Interface())
			}

			SqlValue = append(SqlValue, row)
		}

		//SqlQuery = append(SqlQuery, fmt.Sprintf("%s", UniField))
		//SqlQuery = append(SqlQuery, UniField)
	}

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: insert.sql:", SqlQuery)
		fmt.Println("UniEngine: insert.val:", SqlValue)
	}

	//#PolarDB / PostgreSQL COPY 协议（pq.CopyIn 仅支持 PostgreSQL 系驱动）
	Sql4Text := pq.CopyIn(TablName, SqlQuery...)
	//#fmt.Println(Sql4Text)

	// 走统一 prepare 入口：无事务时使用 Db.Prepare（避免 self.tx 为 nil 的 panic）
	err = self.prepare(Sql4Text)
	if err != nil {
		return fmt.Errorf("%s@UniEngine: if errored too many parameters; try [SpecialInsertP(PageSize)] method;", err.Error())
	}
	defer self.release()

	// 执行所有行
	for _, row := range SqlValue {
		_, err = self.st.Exec(row...)
		if err != nil {
			return err
		}
	}

	// 完成 COPY
	_, err = self.st.Exec()
	if err != nil {
		return fmt.Errorf("%s@UniEngine: if errored too many parameters; try [SpecialInsertP(PageSize)] method;", err.Error())
	}

	return nil
}

func (self *TUniEngine) InsertP(i interface{}, PageSize int64, args ...interface{}) error {

	var err error

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
					err = self.SpecialInsertL(ListData.Interface(), args...)
				}
			default:
				{
					err = self.InsertL(ListData.Interface(), args...)
				}
			}

			if err != nil {
				return err
			}
			ListData = reflect.MakeSlice(t, 0, 0)
		}
	}

	switch self.Supplier {
	case DtPOLODB:
		{
			err = self.SpecialInsertL(ListData.Interface(), args...)
		}
	default:
		{
			err = self.InsertL(ListData.Interface(), args...)
		}
	}

	if err != nil {
		return err
	}

	return nil
}

func (self *TUniEngine) SpecialInsertP(i interface{}, PageSize int64, args ...interface{}) error {

	var err error

	if PageSize == 0 || PageSize == -1 {
		PageSize = 999
	}

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Slice {
		return errors.New("UniEngine: method [SpecialInsertP] need a slice params; check your code;")
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
			err = self.SpecialInsertL(ListData.Interface(), args...)
			if err != nil {
				return err
			}
			ListData = reflect.MakeSlice(t, 0, 0)
		}
	}

	err = self.SpecialInsertL(ListData.Interface(), args...)
	if err != nil {
		return err
	}

	return nil
}

func (self *TUniEngine) Delete(i interface{}, args ...interface{}) error {

	var err error
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

	pkeys := UniTable.ListPkeys
	if len(pkeys) == 0 {
		// 兼容未维护 ListPkeys 的注册路径
		for _, v := range UniTable.HashPkeys {
			pkeys = append(pkeys, v)
		}
	}

	for _, ItemPara := range pkeys {

		//SqlWhere = SqlWhere + " and " + fmt.Sprintf(`"`+ItemPara.FieldName+`"`) + "=" + fmt.Sprintf("%s%d", self.ColParam, ColIndex)
		SqlWhere = SqlWhere + " and " + self.getColParam(ItemPara.FieldName) + "=" + self.getValParam(ColIndex)
		ColIndex = ColIndex + 1

		SqlValue = append(SqlValue, v.FieldByName(ItemPara.AttriName).Interface())
	}

	if SqlWhere == "" {
		return errors.New("UniEngine: no pkey value to delete in class: " + t.String())
	}
	SqlWhere = string(SqlWhere[4:])

	SqlQuery := fmt.Sprintf("delete from %s where %s", TablName, SqlWhere)

	if self.runDebug {
		fmt.Println("UniEngine: delete.sql:", SqlQuery)
		fmt.Println("UniEngine: delete.val:", SqlValue)
	}

	err = self.prepare(SqlQuery)
	if err != nil {
		return err
	}
	defer self.release()

	_, err = self.st.Exec(SqlValue...)
	if err != nil {
		return err
	}

	return nil
}

func (self *TUniEngine) Execute(SqlQuery string, args ...interface{}) error {

	var err error

	SqlQuery = self.getSqlQuery(SqlQuery, args)

	if self.runDebug {
		fmt.Println(fmt.Sprintf("UniEngine: execute.sql:%s", SqlQuery))
	}

	err = self.prepare(SqlQuery)
	if err != nil {
		return err
	}
	defer self.release()

	_, err = self.st.Exec(args...)
	if err != nil {
		return err
	}

	return nil
}

func (self *TUniEngine) ExecuteMust(SqlQuery string, args ...interface{}) error {

	var err error

	SqlQuery = self.getSqlQuery(SqlQuery, args)

	err = self.prepare(SqlQuery)
	if err != nil {
		return err
	}
	defer self.release()

	result, err := self.st.Exec(args...)
	if err != nil {
		return err
	}

	size, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if size == 0 {
		return errors.New("UniEngine: the row count of affected is zero")
	}

	return nil
}

func (self *TUniEngine) IfDropView(TableName string) (bool, error) {

	if !validIdent(TableName) {
		return false, errors.New("UniEngine: invalid table name: " + TableName)
	}

	var err error
	var cSQL string

	mrok, err := self.ExistViews(TableName)
	if err != nil {
		return false, err
	}

	switch mrok {
	case true:
		{
			cSQL = fmt.Sprintf("DROP VIEW %s", TableName)
			err = self.Execute(cSQL)
			if err != nil {
				return false, err
			}
			return true, nil
		}
	default:
		{
			return false, nil
		}
	}
}

func (self *TUniEngine) ExistTable(TableName string) (bool, error) {

	if !validIdent(TableName) {
		return false, errors.New("UniEngine: invalid table name: " + TableName)
	}

	var err error
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
		fmt.Println(fmt.Sprintf("UniEngine: existtable.sql:%s", cSQL))
	}

	if cSQL == "" {
		return false, errors.New("UniEngine: no sql for existtable")
	}

	Size, err := self.SelectD(cSQL)
	if err != nil {
		return false, err
	}
	if Size == 0 {
		return false, nil
	}

	return true, err
}

func (self *TUniEngine) ExistViews(TableName string) (bool, error) {

	if !validIdent(TableName) {
		return false, errors.New("UniEngine: invalid table name: " + TableName)
	}

	var err error
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
		fmt.Println(fmt.Sprintf("UniEngine: existtable.sql:%s", cSQL))
	}

	if cSQL == "" {
		return false, errors.New("UniEngine: no sql for existtable")
	}

	Size, err := self.SelectD(cSQL)
	if err != nil {
		return false, err
	}
	if Size == 0 {
		return false, nil
	}

	return true, err
}

func (self *TUniEngine) ExistField(TableName, FieldName string) (bool, error) {

	if !validIdent(TableName) || !validIdent(FieldName) {
		return false, errors.New("UniEngine: invalid table/field name: " + TableName + "." + FieldName)
	}

	var err error
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

	Size, err := self.SelectD(cSQL)
	if err != nil {
		return false, err
	}
	if Size == 0 {
		return false, nil
	}

	return true, err
}

func (self *TUniEngine) ExistConst(aConstType TConstType, aConstName string) (bool, error) {

	var err error = errors.New("UniEngine: ExistConst is not implemented yet")
	return false, err
}

func (self *TUniEngine) prepare(SqlQuery string) error {

	var err error

	// 复用前关闭旧 stmt，避免 prepared statement / 连接资源泄漏
	if self.st != nil {
		_ = self.st.Close()
		self.st = nil
	}

	switch self.canClose {
	case true:
		{
			self.st, err = self.Db.Prepare(SqlQuery)
		}
	default:
		{
			self.st, err = self.tx.Prepare(SqlQuery)
		}
	}

	return err
}

func (self *TUniEngine) release() error {

	if self.st == nil {
		return nil
	}

	err := self.st.Close()
	self.st = nil

	return err
}

func (self *TUniEngine) Begin() error {

	var err error

	self.tx, err = self.Db.Begin()
	if err != nil {
		return err
	}
	self.canClose = false

	return nil
}

func (self *TUniEngine) Cancel() error {

	var err error

	if self.canClose {
		return errors.New("UniEngine: no transaction")
	}

	err = self.tx.Rollback()
	if err != nil {
		return err
	}

	self.canClose = true

	return nil
}

func (self *TUniEngine) Commit() error {

	var err error

	if self.canClose {
		return errors.New("UniEngine: no transaction")
	}

	err = self.tx.Commit()
	if err != nil {
		return err
	}

	self.canClose = true

	return nil
}

func (self *TUniEngine) CanClose() error {

	fmt.Println("canclose:", self.canClose)

	return nil
}

func (self *TUniEngine) RunDebug(Value bool) error {

	self.runDebug = Value

	return nil
}

func (self *TUniEngine) Initialize() error {

	// 注册 TUniField 供 AutoKeys 内部查询使用（SelectL 按类名查找）；
	// 使用内名避免污染业务表命名空间（TableName 仅作注册键，不用于 SQL）
	self.RegisterClass(TUniField{}, "__uniengine_unifield__")

	self.canClose = true
	self.runDebug = false

	return nil
}
