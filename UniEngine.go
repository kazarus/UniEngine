package UniEngine

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/lib/pq"
)

/*
  when write data to struct, should be ptr;
  when read data from struct, whoever, ptr or struct;
*/

type TUniEngine struct {
	Db *sql.DB
	tx *sql.Tx
	st *sql.Stmt

	ColLabel string //#字段字号
	ColParam string //#参数符号
	HashTabl map[string]TUniTable

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

	return fmt.Sprintf(`"` + FieldName + `"`)
}

func (self *TUniEngine) getSqlQuery(SqlQuery string, args ...interface{}) string {

	//#kazarus:2020_10_31_<
	if self.Provider == DtORACLE && len(args) > 0 {

		if self.runDebug {
			fmt.Println(`UniEngine: Oracle驱动时,替换"$"到":"`)
		}

		SqlQuery = strings.ReplaceAll(SqlQuery, "$", self.ColParam)
	}
	//#kazarus:2020_10_31_>

	//#kazarus:2025_10_17_<
	if self.Provider == DtMYSQLN && len(args) > 0 {

		if self.runDebug {
			fmt.Println(`UniEngine: MySQL驱动时,替换"$"到"?"`)
		}

		re, eror := regexp.Compile(`\$\d+`)
		if eror != nil {
			return ""
		}

		SqlQuery = re.ReplaceAllString(SqlQuery, "?")
	}
	//#kazarus:2025_10_17_>

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

	if self.HashTabl == nil {
		self.HashTabl = make(map[string]TUniTable, 0)
	}

	t := reflect.TypeOf(aClass)
	n := t.NumField()

	var UniTable = TUniTable{}
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
	}

	self.HashTabl[t.String()] = UniTable

	return &UniTable
}

func (self *TUniEngine) RegisterTable(TableName string, IPriority int64) *TUniTable {

	if self.HashTabl == nil {
		self.HashTabl = make(map[string]TUniTable, 0)
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
			UniTable.HashField = make(map[string]TUniField, 0)
			UniTable.HashPkeys = make(map[string]TUniField, 0)
			UniTable.TableName = strings.ToLower(TableName)
			UniTable.IPriority = IPriority
		}
	}

	self.HashTabl[strings.ToLower(TableName)] = UniTable

	return &UniTable
}

func (self *TUniEngine) RegisterField(TableName string, FieldName string) *TUniTable {

	if self.HashTabl == nil {
		self.HashTabl = make(map[string]TUniTable, 0)
	}

	UniTable, Valid := self.HashTabl[strings.ToLower(TableName)]
	switch Valid {
	case true:
		{
			//@UniTable = self.HashTabl[strings.ToLower(TableName)]
		}
	default:
		{
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

	return &UniTable
}

func (self *TUniEngine) RegisterPkeys(TableName string, FieldName string) *TUniTable {

	if self.HashTabl == nil {
		self.HashTabl = make(map[string]TUniTable, 0)
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
			//UniTable.HashField = make(map[string]TUniField, 0)
			//UniTable.HashPkeys = make(map[string]TUniField, 0)
			//UniTable.TableName = strings.ToLower(TableName)
		}
	}

	return &UniTable
}

func (self *TUniEngine) GetTable(TableName string) *TUniTable {

	if self.HashTabl == nil {
		self.HashTabl = make(map[string]TUniTable, 0)
	}

	UniTable, _ := self.HashTabl[strings.ToLower(TableName)]

	return &UniTable
}

func (self *TUniEngine) PrepareTables(TableName string) error {

	if self.HashTabl == nil {
		self.HashTabl = make(map[string]TUniTable, 0)
	}

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

	if self.HashTabl == nil {
		self.HashTabl = make(map[string]TUniTable, 0)
	}

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
					var SqlField string = ""
					var SqlParam string = ""
					var SqlWhere string = ""

					if len(UniTable.HashPkeys) > 0 {

						for _, ItemPara := range UniTable.ListPkeys {

							if ItemPara.ReadOnly {
								continue
							}

							/*
									UniField = UniField + "," + self.getColParam(ItemPara.FieldName)
								    SqlParam = SqlParam + "," + self.getValParam(ColIndex)
							*/

							SqlField = self.getColParam(ItemPara.FieldName)
							SqlParam = self.getValParam(ColIndex)
							SqlWhere = SqlWhere + fmt.Sprintf("    and %s=%s", SqlField, SqlParam)
							ColIndex = ColIndex + 1
						}

						SqlField = string(SqlField[1:])
						SqlParam = string(SqlParam[1:])

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

							/*
									UniField = UniField + "," + self.getColParam(ItemPara.FieldName)
								    SqlParam = SqlParam + "," + self.getValParam(ColIndex)
							*/

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
					var SqlParam string = ""
					var SqlWhere string = ""

					fmt.Println(SqlParam)

					if len(UniTable.HashField) > 0 {

						for _, ItemPara := range UniTable.ListField {

							if ItemPara.ReadOnly {
								continue
							}

							/*
								if _, valid := UniTable.HashPkeys[strings.ToLower(item.FieldName)]; valid {
									SqlWhere = SqlWhere + " and " + fmt.Sprintf(`"`+item.FieldName+`"`) + "=" + fmt.Sprintf("%s%d", self.ColParam, ColIndex)
								} else {
									UniField = UniField + "," + fmt.Sprintf(`"`+item.FieldName+`"`) + "=" + fmt.Sprintf("%s%d", self.ColParam, ColIndex)
								}
							*/

							/*
								if _, valid := UniTable.HashPkeys[strings.ToLower(item.FieldName)]; valid {
									SqlWhere = SqlWhere + " and " + self.getColParam(item.FieldName) + "=" + self.getValParam(ColIndex)
									zValue = append(zValue, v.FieldByName(item.AttriName).Interface())
								} else {
									UniField = UniField + "," + self.getColParam(item.FieldName) + "=" + self.getValParam(ColIndex)
									xValue = append(xValue, v.FieldByName(item.AttriName).Interface())
								}
							*/

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
			//UniTable.HashField = make(map[string]TUniField, 0)
			//UniTable.HashPkeys = make(map[string]TUniField, 0)
			//UniTable.TableName = strings.ToLower(TableName)
		}
	}

	return SqlResult, ListField, UniTable.ListPkeys, nil
}

// return int64;
func (self *TUniEngine) SelectD(SqlQuery string, args ...interface{}) (int64, error) {

	var eror error
	var size sql.NullInt64

	SqlQuery = self.getSqlQuery(SqlQuery, args)

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: select.sql", SqlQuery)
		fmt.Println("UniEngine: select.val", args)
	}

	eror = self.prepare(SqlQuery)
	if eror != nil {
		return 0, eror
	}
	defer self.release()

	rows, eror := self.st.Query(args...)
	if eror != nil {
		return 0, eror
	}
	defer rows.Close()

	/*
		if !rows.Next() {
			return 0, sql.ErrNoRows
		}
		rows.Scan(&size)
		return size, nil
	*/
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

	var eror error
	var size sql.NullFloat64

	SqlQuery = self.getSqlQuery(SqlQuery, args)

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: select.sql", SqlQuery)
		fmt.Println("UniEngine: select.val", args)
	}

	eror = self.prepare(SqlQuery)
	if eror != nil {
		return 0, eror
	}
	defer self.release()

	rows, eror := self.st.Query(args...)

	if eror != nil {
		return 0, eror
	}
	defer rows.Close()

	/*
		if !rows.Next() {
			return 0, sql.ErrNoRows
		}
		rows.Scan(&size)
		return size, nil
	*/

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

	var eror error
	var text sql.NullString

	SqlQuery = self.getSqlQuery(SqlQuery, args)

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: select.sql", SqlQuery)
		fmt.Println("UniEngine: select.val", args)
	}

	eror = self.prepare(SqlQuery)
	if eror != nil {
		return "", eror
	}
	defer self.release()

	rows, eror := self.st.Query(args...)
	if eror != nil {
		return "", eror
	}
	defer rows.Close()

	/*
		if !rows.Next() {
			return 0, sql.ErrNoRows
		}
		rows.Scan(&size)
		return size, nil
	*/

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

	var eror error

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
		return errors.New(fmt.Sprintf("UniEngine: no such class registered:", TablName))
	}

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: select.sql", SqlQuery)
		fmt.Println("UniEngine: select.val", args)
	}

	//-<
	eror = self.prepare(SqlQuery)
	if eror != nil {
		return eror
	}
	defer self.release()

	rows, eror := self.st.Query(args...)
	if eror != nil {
		return eror
	}
	defer rows.Close()
	//->

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
				for i := 0; i < cCount; i++ {
					values[i] = &fields[i]
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
						return errors.New(fmt.Sprintf("UniEngine: database have field[%s], but not in class[%s]", ItemPara, t.String()))
					}
					values[ItemIndx] = Result.FieldByName(UniField.AttriName).Addr().Interface()
				}

				eror = rows.Scan(values...)
				if eror != nil {
					return eror
				}
			}
		}

		//if x, ok := i.(HasSetSqlResult); ok {
		//	for i := 0; i < cCount; i++ {
		//		values[i] = &fields[i]
		//	}
		//
		//	eror = rows.Scan(values...)
		//	if eror != nil {
		//		return eror
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
		//	eror = rows.Scan(values...)
		//	if eror != nil {
		//		return eror
		//	}
		//}
	}

	return nil
}

// return slice of struct;
func (self *TUniEngine) SelectL(i interface{}, SqlQuery string, args ...interface{}) error {

	var eror error

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
		return errors.New(fmt.Sprintf("UniEngine: no such class registered:", TablName))
	}

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: select.sql", SqlQuery)
		fmt.Println("UniEngine: select.val", args)
	}

	//-<
	eror = self.prepare(SqlQuery)
	if eror != nil {
		return eror
	}
	defer self.release()

	rows, eror := self.st.Query(args...)
	if eror != nil {
		return eror
	}
	defer rows.Close()
	//->

	//columnstype,eror := rows.ColumnTypes()
	//fmt.Println(columnstype)

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
						return errors.New(fmt.Sprintf("UniEngine: database have field[%s], but not in class[%s]", ItemPara, t.String()))
					}
					values[ColIndex] = u.Elem().FieldByName(UniField.AttriName).Addr().Interface()
				}

				eror = rows.Scan(values...)
				if eror != nil {
					return eror
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
		//	eror = rows.Scan(values...)
		//	if eror != nil {
		//		return eror
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
		//	eror = rows.Scan(values...)
		//	if eror != nil {
		//		return eror
		//	}
		//
		//	Result.Set(reflect.Append(Result, u.Elem()))
		//}
	}

	return nil
}

// return map of struct;user;GetMapUnique;
func (self *TUniEngine) SelectM(i interface{}, SqlQuery string, args ...interface{}) error {

	var eror error

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
		return errors.New(fmt.Sprintf("UniEngine: no such class registered:", TablName))
	}

	if !t.Implements(THasGetMapUnique) {
		return errors.New(fmt.Sprintf("UniEngine: the class registered:[%s] does not Implemented [HasGetMapUnique]", TablName))
	}

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: select.sql", SqlQuery)
		fmt.Println("UniEngine: select.val", args)
	}

	//-<
	eror = self.prepare(SqlQuery)
	if eror != nil {
		return eror
	}
	defer self.release()

	rows, eror := self.st.Query(args...)
	if eror != nil {
		return eror
	}
	defer rows.Close()
	//->

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
						return errors.New(fmt.Sprintf("UniEngine: database have field[%s], but not in class[%s]", ItemPara, t.String()))
					}
					values[ColIndex] = u.Elem().FieldByName(UniField.AttriName).Addr().Interface()
				}

				eror = rows.Scan(values...)
				if eror != nil {
					return eror
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
		//	eror = rows.Scan(values...)
		//	if eror != nil {
		//		return eror
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
		//	eror = rows.Scan(values...)
		//	if eror != nil {
		//		return eror
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

/*
    var HashData = make(map[string]TDATA, 0)

	cSQL = "SELECT * FROM ANTV_DATA WHERE 1=1 AND WHO_BUILD=$1 AND USER_INDX=$2 AND SOURCE_ND=$3 AND SOURCE_QJ=$4"
	eror = UniEngineEx.SelectH(&HashData, func(u interface{}) string {
		DataPara := u.(TDATA)
		return fmt.Sprintf("%d-%d-%d-%d", DataPara.ANTVMAIN, DataPara.UNITINDX, DataPara.SOURCEND, DataPara.SOURCEQJ)
	}, cSQL, whobuild, userindx, sourcend, sourceqj)

    ExistVal, Valid := HashData[fmt.Sprintf("%d-%d-%d-%d", ItemPara.ANTVMAIN, ItemPara.UNITINDX, ItemPara.SOURCEND, ItemPara.SOURCEQJ)]
    if Valid {
        ItemPara.DataIndx = ExistVal.DataIndx

        continue
    }

   ExistVal,
   Imok,Mrok,
*/

func (self *TUniEngine) SelectH(i interface{}, f GetMapUnique, SqlQuery string, args ...interface{}) error {

	var eror error

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
		return errors.New(fmt.Sprintf("UniEngine: no such class registered:", TablName))
	}

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: select.sql", SqlQuery)
		fmt.Println("UniEngine: select.val", args)
	}

	//-<
	eror = self.prepare(SqlQuery)
	if eror != nil {
		return eror
	}
	defer self.release()

	rows, eror := self.st.Query(args...)
	if eror != nil {
		return eror
	}
	defer rows.Close()
	//->

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
						return errors.New(fmt.Sprintf("UniEngine: database have field[%s], but not in class[%s]", ItemPara, t.String()))
					}
					values[ColIndex] = u.Elem().FieldByName(UniField.AttriName).Addr().Interface()
				}

				eror = rows.Scan(values...)
				if eror != nil {
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
		return errors.New(fmt.Sprintf("UniEngine: no such class registered:", t.String()))
	}
	if len(UniTable.HashPkeys) == 0 {
		return errors.New(fmt.Sprintf("UniEngine: no pkeys column in class registered:", t.String()))
	}

	if TablName == "" {
		TablName = UniTable.TableName
	}

	v := reflect.Indirect(reflect.ValueOf(i))

	ColIndex := 1
	//@UniField := ""
	SqlWhere := ""

	SqlQuery := ""
	SqlValue := make([]interface{}, 0)

	//@SaveIt方法不需要这一组
	/*
		if x, ok := v.Interface().(HasGetSqlUpdate); ok {
			cQuery = x.GetSqlUpdate(UniTableName)
		}

		if x, ok := v.Interface().(HasSetSqlValues); ok {
			x.SetSqlValues(EtUpdate, &SqlValue)
		}
	*/

	if SqlQuery == "" && len(SqlValue) == 0 {

		for _, ItemPara := range UniTable.HashField {

			if ItemPara.ReadOnly {
				continue
			}

			if _, valid := UniTable.HashPkeys[ItemPara.FieldName]; valid {
				//@SqlWhere = SqlWhere + " and " + fmt.Sprintf(`"`+item.FieldName+`"`) + "=" + fmt.Sprintf("$%d", ColIndex)
				SqlWhere = SqlWhere + " and " + self.getColParam(ItemPara.FieldName) + "=" + self.getValParam(ColIndex)
				SqlValue = append(SqlValue, v.FieldByName(ItemPara.AttriName).Interface())
				ColIndex = ColIndex + 1
			}
		}

		//@UniField = string(UniField[1:])
		SqlWhere = string(SqlWhere[4:])

		SqlQuery = fmt.Sprintf("select count(1) from %s where %s", TablName, SqlWhere)
	}

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: select.sql", SqlQuery)
		fmt.Println("UniEngine: select.val", SqlValue)
	}

	cCount, eror := self.SelectD(SqlQuery, SqlValue...)
	if eror != nil {
		return eror
	}

	if self.runDebug {
		fmt.Println("UniEngine: select.cnt", cCount)
	}

	if cCount == 1 {
		return self.Update(i, args...)
	} else {
		return self.Insert(i, args...)
	}

	return nil
}

func (self *TUniEngine) SaveItWhenNotExist(i interface{}, args ...interface{}) error {

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
		return errors.New(fmt.Sprintf("UniEngine: no such class registered:", t.String()))
	}
	if len(UniTable.HashPkeys) == 0 {
		return errors.New(fmt.Sprintf("UniEngine: no pkeys column in class registered:", t.String()))
	}

	if TablName == "" {
		TablName = UniTable.TableName
	}

	v := reflect.Indirect(reflect.ValueOf(i))

	ColIndex := 1
	//@UniField := ""
	SqlWhere := ""

	SqlQuery := ""
	SqlValue := make([]interface{}, 0)

	//@SaveIt方法不需要这一组
	/*
		if x, ok := v.Interface().(HasGetSqlUpdate); ok {
			cQuery = x.GetSqlUpdate(UniTableName)
		}

		if x, ok := v.Interface().(HasSetSqlValues); ok {
			x.SetSqlValues(EtUpdate, &SqlValue)
		}
	*/

	if SqlQuery == "" && len(SqlValue) == 0 {
		for _, item := range UniTable.HashField {

			if item.ReadOnly {
				continue
			}

			if _, valid := UniTable.HashPkeys[item.FieldName]; valid {
				//@SqlWhere = SqlWhere + " and " + fmt.Sprintf(`"`+item.FieldName+`"`) + "=" + fmt.Sprintf("$%d", ColIndex)
				SqlWhere = SqlWhere + " and " + self.getColParam(item.FieldName) + "=" + self.getValParam(ColIndex)
				SqlValue = append(SqlValue, v.FieldByName(item.AttriName).Interface())
				ColIndex = ColIndex + 1
			}
		}

		//@UniField = string(UniField[1:])
		SqlWhere = string(SqlWhere[4:])

		SqlQuery = fmt.Sprintf("select count(1) from %s where %s", TablName, SqlWhere)
	}

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: select.sql", SqlQuery)
		fmt.Println("UniEngine: select.val", SqlValue)
	}

	cCount, eror := self.SelectD(SqlQuery, SqlValue...)
	if eror != nil {
		return eror
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
		return errors.New(fmt.Sprintf("UniEngine: no such class registered:", t.String()))
	}
	if len(UniTable.HashPkeys) == 0 {
		return errors.New(fmt.Sprintf("UniEngine: no pkeys column in class registered:", t.String()))
	}
	if TablName == "" {
		TablName = UniTable.TableName
	}

	//#打印语句
	if self.runDebug {
		fmt.Println(fmt.Sprintf("UniEngine: try update table:%s", UniTable))
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
	/*
		if x, ok := v.Interface().(HasGetSqlValues); ok {
			SqlValue = x.GetSqlValues(EtUpdate)
		}
	*/
	if x, ok := v.Interface().(HasSetSqlValues); ok {
		x.SetSqlValues(*self, EtUpdate, &SqlValue)
	}

	if SqlQuery == "" && len(SqlValue) == 0 {
		for _, ItemPara := range UniTable.HashField {

			if ItemPara.ReadOnly {
				continue
			}

			/*
				if _, valid := UniTable.HashPkeys[strings.ToLower(item.FieldName)]; valid {
					SqlWhere = SqlWhere + " and " + fmt.Sprintf(`"`+item.FieldName+`"`) + "=" + fmt.Sprintf("%s%d", self.ColParam, ColIndex)
				} else {
					UniField = UniField + "," + fmt.Sprintf(`"`+item.FieldName+`"`) + "=" + fmt.Sprintf("%s%d", self.ColParam, ColIndex)
				}
			*/

			/*
				if _, valid := UniTable.HashPkeys[strings.ToLower(item.FieldName)]; valid {
					SqlWhere = SqlWhere + " and " + self.getColParam(item.FieldName) + "=" + self.getValParam(ColIndex)
					zValue = append(zValue, v.FieldByName(item.AttriName).Interface())
				} else {
					UniField = UniField + "," + self.getColParam(item.FieldName) + "=" + self.getValParam(ColIndex)
					xValue = append(xValue, v.FieldByName(item.AttriName).Interface())
				}
			*/

			if _, valid := UniTable.HashPkeys[strings.ToLower(ItemPara.FieldName)]; valid {
				continue
			}

			UniField = UniField + "," + self.getColParam(ItemPara.FieldName) + "=" + self.getValParam(ColIndex)
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

	eror = self.prepare(SqlQuery)
	if eror != nil {
		return eror
	}
	defer self.release()

	_, eror = self.st.Exec(SqlValue...)
	if eror != nil {
		return eror
	}

	return nil
}

func (self *TUniEngine) Insert(i interface{}, args ...interface{}) error {

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
		//TO DO:
		return errors.New("UniEngine: method [Insert] only retun a struct; may be you should try [InsertL]")
	}

	UniTable, Valid := self.HashTabl[t.String()]
	if !Valid {
		return errors.New(fmt.Sprintf("UniEngine: no such class registered:", t.String()))
	}
	if TablName == "" {
		TablName = UniTable.TableName
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
	/*
		if x, ok := v.Interface().(HasGetSqlValues); ok {
			SqlValue = x.GetSqlValues(EtInsert)
		}
	*/
	if x, ok := v.Interface().(HasSetSqlValues); ok {
		x.SetSqlValues(*self, EtInsert, &SqlValue)
	}

	if SqlQuery == "" && len(SqlValue) == 0 {

		for _, ItemPara := range UniTable.HashField {

			if ItemPara.ReadOnly {
				continue
			}

			/*
				UniField = UniField + "," + fmt.Sprintf(`"`+ItemPara.FieldName+`"`)
				SqlParam = SqlParam + "," + fmt.Sprintf("%s%d", self.ColParam, ColIndex)
			*/

			UniField = UniField + "," + self.getColParam(ItemPara.FieldName)
			SqlParam = SqlParam + "," + self.getValParam(ColIndex)
			ColIndex = ColIndex + 1

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

	eror = self.prepare(SqlQuery)
	if eror != nil {
		return eror
	}
	defer self.release()

	_, eror = self.st.Exec(SqlValue...)
	if eror != nil {
		return eror
	}

	return nil
}

func (self *TUniEngine) InsertL(i interface{}, args ...interface{}) error {

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
		return errors.New(fmt.Sprintf("UniEngine: no such class registered:", t.String()))
	}
	if TablName == "" {
		TablName = UniTable.TableName
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

	eror = self.prepare(SqlQuery)
	if eror != nil {
		return eror
	}
	defer self.release()

	_, eror = self.st.Exec(SqlValue...)
	if eror != nil {
		return errors.New(fmt.Sprintf("%s@UniEngine: if errored too many parameters; try [InsertP(PageSize)] method;", eror.Error()))
	}

	return nil
}

func (self *TUniEngine) SpecialInsertL(i interface{}, args ...interface{}) error {

	var eror error
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
		return errors.New(fmt.Sprintf("UniEngine: no such class registered:", t.String()))
	}
	if TablName == "" {
		TablName = UniTable.TableName
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

	//#PolarDB
	Sql4Text := strings.ToLower(pq.CopyIn(TablName, SqlQuery...))
	//#fmt.Println(Sql4Text)

	self.st, eror = self.tx.Prepare(Sql4Text)
	if eror != nil {
		return errors.New(fmt.Sprintf("%s@UniEngine: if errored too many parameters; try [SpecialInsertP(PageSize)] method;", eror.Error()))
	}
	defer self.st.Close()

	// 执行所有行
	for _, row := range SqlValue {
		_, eror = self.st.Exec(row...)
		if eror != nil {
			return eror
		}
	}

	// 完成 COPY
	_, eror = self.st.Exec()
	if eror != nil {
		return errors.New(fmt.Sprintf("%s@UniEngine: if errored too many parameters; try [SpecialInsertP(PageSize)] method;", eror.Error()))
	}

	return nil
}

func (self *TUniEngine) InsertP(i interface{}, PageSize int64, args ...interface{}) error {

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
					eror = self.SpecialInsertL(ListData.Interface(), args...)
				}
			default:
				{
					eror = self.InsertL(ListData.Interface(), args...)
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
			eror = self.SpecialInsertL(ListData.Interface(), args...)
		}
	default:
		{
			eror = self.InsertL(ListData.Interface(), args...)
		}
	}

	if eror != nil {
		return eror
	}

	return nil
}

func (self *TUniEngine) SpecialInsertP(i interface{}, PageSize int64, args ...interface{}) error {

	var eror error

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
			eror = self.SpecialInsertL(ListData.Interface(), args...)
			if eror != nil {
				return eror
			}
			ListData = reflect.MakeSlice(t, 0, 0)
		}
	}

	eror = self.SpecialInsertL(ListData.Interface(), args...)
	if eror != nil {
		return eror
	}

	return nil
}

func (self *TUniEngine) Delete(i interface{}, args ...interface{}) error {

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
		return errors.New(fmt.Sprintf("UniEngine: no such class registered:", t.String()))
	}
	if len(UniTable.HashPkeys) == 0 {
		return errors.New(fmt.Sprintf("UniEngine: no pkeys column in class registered:", t.String()))
	}

	if TablName == "" {
		TablName = UniTable.TableName
	}

	v := reflect.Indirect(reflect.ValueOf(i))

	ColIndex := 1
	SqlWhere := ""
	SqlValue := make([]interface{}, 0)

	for _, ItemPara := range UniTable.HashPkeys {

		//SqlWhere = SqlWhere + " and " + fmt.Sprintf(`"`+ItemPara.FieldName+`"`) + "=" + fmt.Sprintf("%s%d", self.ColParam, ColIndex)
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

	eror = self.prepare(SqlQuery)
	if eror != nil {
		return eror
	}
	defer self.release()

	_, eror = self.st.Exec(SqlValue...)
	if eror != nil {
		return eror
	}

	return nil
}

func (self *TUniEngine) Execute(SqlQuery string, args ...interface{}) error {

	var eror error

	SqlQuery = self.getSqlQuery(SqlQuery, args)

	if self.runDebug {
		fmt.Println(fmt.Sprintf("UniEngine: execute.sql:%s", SqlQuery))
	}

	if self.canClose {
		self.st, eror = self.Db.Prepare(SqlQuery)
	} else {
		self.st, eror = self.tx.Prepare(SqlQuery)
	}

	if eror != nil {
		return eror
	}
	_, eror = self.st.Exec(args...)
	if eror != nil {
		return eror
	}

	return nil
}

func (self *TUniEngine) ExecuteMust(SqlQuery string, args ...interface{}) error {

	var eror error

	SqlQuery = self.getSqlQuery(SqlQuery, args)

	if self.canClose {
		self.st, eror = self.Db.Prepare(SqlQuery)
	} else {
		self.st, eror = self.tx.Prepare(SqlQuery)
	}

	if eror != nil {
		return eror
	}

	result, eror := self.st.Exec(args...)
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

	var eror error
	var cSQL string

	mrok, eror := self.ExistViews(TableName)
	if eror != nil {
		return false, eror
	}

	switch mrok {
	case true:
		{
			cSQL = fmt.Sprintf("DROP VIEW %s", TableName)
			eror = self.Execute(cSQL)
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

	var eror error
	cSQL := ""

	/*
		if len(GetSqlExistTable) > 0 {

			if x, ok := GetSqlExistTable[0].(HasGetSqlExistTable); ok {
				cSQL = x.GetSqlExistTable(TableName)
			}

		} else {

			var ExistTable4POSTGR = TExistTable4POSTGR{}
			cSQL = ExistTable4POSTGR.GetSqlExistTable(TableName)

		}
	*/

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

	Size, eror := self.SelectD(cSQL)
	if eror != nil {
		return false, eror
	}
	if Size == 0 {
		return false, nil
	}

	return true, eror
}

func (self *TUniEngine) ExistViews(TableName string) (bool, error) {

	var eror error
	cSQL := ""

	/*
		if len(GetSqlExistTable) > 0 {

			if x, ok := GetSqlExistTable[0].(HasGetSqlExistTable); ok {
				cSQL = x.GetSqlExistTable(TableName)
			}

		} else {

			var ExistTable4POSTGR = TExistTable4POSTGR{}
			cSQL = ExistTable4POSTGR.GetSqlExistTable(TableName)

		}
	*/

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

	Size, eror := self.SelectD(cSQL)
	if eror != nil {
		return false, eror
	}
	if Size == 0 {
		return false, nil
	}

	return true, eror
}

func (self *TUniEngine) ExistField(TableName, FieldName string) (bool, error) {

	var eror error
	cSQL := ""

	/*
		if len(GetSqlExistField) > 0 {

			if x, ok := GetSqlExistField[0].(HasGetSqlExistField); ok {
				cSQL = x.GetSqlExistField(TableName, FieldName)
			}

		} else {

			var ExistField4POSTGR = TExistField4POSTGR{}
			cSQL = ExistField4POSTGR.GetSqlExistField(TableName, FieldName)

		}
	*/

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

	Size, eror := self.SelectD(cSQL)
	if eror != nil {
		return false, eror
	}
	if Size == 0 {
		return false, nil
	}

	return true, eror
}

func (self *TUniEngine) ExistConst(aConstType TConstType, aConstName string) (bool, error) {

	var eror error

	cSQL := ""
	Size, eror := self.SelectD(cSQL)
	if eror != nil {
		return false, eror
	}
	if Size == 0 {
		return false, nil
	}

	return true, eror
}

func (self *TUniEngine) prepare(SqlQuery string) error {

	var eror error

	switch self.canClose {
	case true:
		{
			self.st, eror = self.Db.Prepare(SqlQuery)
		}
	default:
		{
			self.st, eror = self.tx.Prepare(SqlQuery)
		}
	}
	//if self.canClose {
	//	self.st, eror = self.Db.Prepare(SqlQuery)
	//} else {
	//	self.st, eror = self.tx.Prepare(SqlQuery)
	//}
	return eror
}

func (self *TUniEngine) release() error {

	var eror error

	eror = self.st.Close()

	return eror
}

func (self *TUniEngine) Begin() error {

	var eror error

	self.tx, eror = self.Db.Begin()
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

	fmt.Println("canclose:", self.canClose)

	return nil
}

func (self *TUniEngine) RunDebug(Value bool) error {

	self.runDebug = Value

	return nil
}

func (self *TUniEngine) Initialize() error {

	self.RegisterClass(TUniTable{}, "github.com/kazarus/uniengine/unitable")
	self.RegisterClass(TUniField{}, "github.com/kazarus/uniengine/unifield")

	self.canClose = true
	self.runDebug = false

	return nil
}
