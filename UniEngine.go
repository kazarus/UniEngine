package UniEngine

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
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

	Instance string     //#数据库实例
	DataBase string     //#数据库名称
	DataUser string     //#数据库用户
	Provider TDriveType //#数据库驱动

	canClose bool //default is true;if is transaction,canClose = false
	runDebug bool //default is false;print some sql;
}

func (self *TUniEngine) getValParam(aIndex int) string {

	if self.Provider == DtMYSQLN {
		return fmt.Sprintf("%s", self.ColParam)
	}

	return fmt.Sprintf("%s%d", self.ColParam, aIndex)
}

func (self *TUniEngine) getColParam(aFieldName string) string {

	if self.Provider == DtMYSQLN {
		return fmt.Sprintf("%s", aFieldName)
	}

	if self.Provider == DtORACLE {
		return fmt.Sprintf("%s", aFieldName)
	}

	return fmt.Sprintf(`"` + aFieldName + `"`)
}

func (self *TUniEngine) getSqlQuery(aSqlQuery string, args ...interface{}) string {

	//#kazarus:2020_10_31_<
	if self.Provider == DtORACLE && len(args) > 0 {
		if self.runDebug {
			fmt.Println(`UniEngine: Oracle驱动时,替换"$"给":"`)
		}
		aSqlQuery = strings.ReplaceAll(aSqlQuery, "$", self.ColParam)
	}
	//#kazarus:2020_10_31_>

	return aSqlQuery
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
			PageSize = 20
		}
	case DtPOSTGR:
		{
			PageSize = 99
		}
	}

	return PageSize
}

func (self *TUniEngine) RegisterClass(aClass interface{}, aTableName string) *TUniTable {

	if self.HashTabl == nil {
		self.HashTabl = make(map[string]TUniTable, 0)
	}

	t := reflect.TypeOf(aClass)
	n := t.NumField()

	var cTable = TUniTable{}
	cTable.HashField = make(map[string]TUniField, 0)
	cTable.HashPkeys = make(map[string]TUniField, 0)
	cTable.TableName = aTableName

	for i := 0; i < n; i++ {

		f := t.Field(i)

		var cField = TUniField{}
		cField.AttriName = f.Name
		//@cField.FieldType = f.Type

		cField.initialize(f.Tag.Get(self.ColLabel))

		cTable.HashField[strings.ToLower(cField.FieldName)] = cField
	}

	self.HashTabl[t.String()] = cTable

	return &cTable
}

func (self *TUniEngine) RegisterTable(aTableName string, IPriority int64) *TUniTable {

	if self.HashTabl == nil {
		self.HashTabl = make(map[string]TUniTable, 0)
	}

	cTable, Valid := self.HashTabl[strings.ToLower(aTableName)]
	switch Valid {
	case true:
		{
			//@cTable = self.HashTabl[strings.ToLower(aTableName)]
			cTable.IPriority = IPriority
		}
	default:
		{
			cTable.HashField = make(map[string]TUniField, 0)
			cTable.HashPkeys = make(map[string]TUniField, 0)
			cTable.TableName = strings.ToLower(aTableName)
			cTable.IPriority = IPriority
		}
	}

	self.HashTabl[strings.ToLower(aTableName)] = cTable

	return &cTable
}

func (self *TUniEngine) RegisterField(aTableName string, aFieldName string) *TUniTable {

	if self.HashTabl == nil {
		self.HashTabl = make(map[string]TUniTable, 0)
	}

	cTable, Valid := self.HashTabl[strings.ToLower(aTableName)]
	switch Valid {
	case true:
		{
			//@cTable = self.HashTabl[strings.ToLower(aTableName)]
		}
	default:
		{
			cTable.HashField = make(map[string]TUniField, 0)
			cTable.HashPkeys = make(map[string]TUniField, 0)
			cTable.TableName = strings.ToLower(aTableName)
		}
	}

	var cField = TUniField{}
	cField.AttriName = ""
	cField.FieldName = strings.ToLower(aFieldName)
	cField.TableName = strings.ToLower(aTableName)

	cTable.HashField[strings.ToLower(cField.FieldName)] = cField

	self.HashTabl[strings.ToLower(aTableName)] = cTable

	return &cTable
}

func (self *TUniEngine) RegisterPkeys(aTableName string, aFieldName string) *TUniTable {

	if self.HashTabl == nil {
		self.HashTabl = make(map[string]TUniTable, 0)
	}

	cTable, Valid := self.HashTabl[strings.ToLower(aTableName)]
	switch Valid {
	case true:
		{
			//@cTable = self.HashTabl[strings.ToLower(aTableName)]

			var cField = TUniField{}
			cField.AttriName = ""
			cField.FieldName = strings.ToLower(aFieldName)
			cField.TableName = strings.ToLower(aTableName)

			cTable.HashPkeys[strings.ToLower(cField.FieldName)] = cField

			self.HashTabl[strings.ToLower(aTableName)] = cTable
		}
	default:
		{
			//cTable.HashField = make(map[string]TUniField, 0)
			//cTable.HashPkeys = make(map[string]TUniField, 0)
			//cTable.TableName = strings.ToLower(aTableName)
		}
	}

	return &cTable
}

func (self *TUniEngine) GetTable(aTableName string) *TUniTable {

	if self.HashTabl == nil {
		self.HashTabl = make(map[string]TUniTable, 0)
	}

	cTable, _ := self.HashTabl[strings.ToLower(aTableName)]

	return &cTable
}

func (self *TUniEngine) PrepareTables(aTableName string) error {

	if self.HashTabl == nil {
		self.HashTabl = make(map[string]TUniTable, 0)
	}

	cTable, Valid := self.HashTabl[strings.ToLower(aTableName)]
	switch Valid {
	case true:
		{
			if cTable.ListField == nil {
				cTable.ListField = make([]TUniField, 0)
			}
			if cTable.ListPkeys == nil {
				cTable.ListPkeys = make([]TUniField, 0)
			}

			if len(cTable.HashField) > 0 {
				for _, cItem := range cTable.HashField {

					if cItem.ReadOnly {
						continue
					}

					cTable.ListField = append(cTable.ListField, cItem)
				}
			}

			if len(cTable.HashPkeys) > 0 {
				for _, cItem := range cTable.HashPkeys {

					if cItem.ReadOnly {
						continue
					}

					cTable.ListPkeys = append(cTable.ListPkeys, cItem)
				}
			}

			self.HashTabl[strings.ToLower(aTableName)] = cTable
		}
	default:
		{
		}
	}

	return nil
}

func (self *TUniEngine) PrepareRunSQL(aTableName string, QueryType TQueryType) (string, []TUniField, []TUniField, error) {

	if self.HashTabl == nil {
		self.HashTabl = make(map[string]TUniTable, 0)
	}

	var SqlResult string
	var ListField = make([]TUniField, 0)

	cTable, Valid := self.HashTabl[strings.ToLower(aTableName)]
	switch Valid {
	case true:
		{

			switch QueryType {
			case EtSelect:
				{
					var cIndex int = 1
					var cField string = ""
					var cParam string = ""
					var cWhere string = ""

					if len(cTable.HashPkeys) > 0 {

						for _, cItem := range cTable.ListPkeys {

							if cItem.ReadOnly {
								continue
							}

							/*
									cField = cField + "," + self.getColParam(cItem.FieldName)
								    cParam = cParam + "," + self.getValParam(cIndex)
							*/

							cField = self.getColParam(cItem.FieldName)
							cParam = self.getValParam(cIndex)
							cWhere = cWhere + fmt.Sprintf("    and %s=%s", cField, cParam)
							cIndex = cIndex + 1
						}

						cField = string(cField[1:])
						cParam = string(cParam[1:])

						SqlResult = fmt.Sprintf("where 1=1 %s", cWhere)
						cTable.SqlSelect = SqlResult
					}
				}
			case EtInsert:
				{
					var cIndex int = 1
					var cField string = ""
					var cParam string = ""

					if len(cTable.HashField) > 0 {

						for _, cItem := range cTable.ListField {

							if cItem.ReadOnly {
								continue
							}

							/*
									cField = cField + "," + self.getColParam(cItem.FieldName)
								    cParam = cParam + "," + self.getValParam(cIndex)
							*/

							cField = cField + "," + self.getColParam(cItem.FieldName)
							cParam = cParam + "," + self.getValParam(cIndex)
							cIndex = cIndex + 1

							ListField = append(ListField, cItem)
						}

						cField = string(cField[1:])
						cParam = string(cParam[1:])

						SqlResult = fmt.Sprintf("insert into %s ( %s ) values ( %s ) ", cTable.TableName, cField, cParam)
						cTable.SqlInsert = SqlResult
					}
				}
			case EtUpdate:
				{
					var cIndex int = 1
					var cField string = ""
					var cParam string = ""
					var cWhere string = ""

					fmt.Println(cParam)

					if len(cTable.HashField) > 0 {

						for _, item := range cTable.ListField {

							if item.ReadOnly {
								continue
							}

							/*
								if _, valid := cTable.HashPkeys[strings.ToLower(item.FieldName)]; valid {
									cWhere = cWhere + " and " + fmt.Sprintf(`"`+item.FieldName+`"`) + "=" + fmt.Sprintf("%s%d", self.ColParam, cIndex)
								} else {
									cField = cField + "," + fmt.Sprintf(`"`+item.FieldName+`"`) + "=" + fmt.Sprintf("%s%d", self.ColParam, cIndex)
								}
							*/

							/*
								if _, valid := cTable.HashPkeys[strings.ToLower(item.FieldName)]; valid {
									cWhere = cWhere + " and " + self.getColParam(item.FieldName) + "=" + self.getValParam(cIndex)
									zValue = append(zValue, v.FieldByName(item.AttriName).Interface())
								} else {
									cField = cField + "," + self.getColParam(item.FieldName) + "=" + self.getValParam(cIndex)
									xValue = append(xValue, v.FieldByName(item.AttriName).Interface())
								}
							*/

							if _, valid := cTable.HashPkeys[strings.ToLower(item.FieldName)]; valid {
								item.PkeyOnly = true
								ListField = append(ListField, item)
								continue
							}

							cField = cField + "," + self.getColParam(item.FieldName) + "=" + self.getValParam(cIndex)

							cIndex = cIndex + 1

							ListField = append(ListField, item)
						}
					}

					if len(cTable.HashPkeys) > 0 {

						for _, item := range cTable.ListPkeys {

							cWhere = cWhere + " and " + self.getColParam(item.FieldName) + "=" + self.getValParam(cIndex)
							cIndex = cIndex + 1
						}
					}

					cField = string(cField[1:])
					cWhere = string(cWhere[1:])

					SqlResult = fmt.Sprintf("update %s set %s where 1=1 %s", cTable.TableName, cField, cWhere)
					cTable.SqlUpdate = SqlResult
				}
			}

			self.HashTabl[strings.ToLower(aTableName)] = cTable
		}
	default:
		{
			//cTable.HashField = make(map[string]TUniField, 0)
			//cTable.HashPkeys = make(map[string]TUniField, 0)
			//cTable.TableName = strings.ToLower(aTableName)
		}
	}

	return SqlResult, ListField, cTable.ListPkeys, nil
}

// return int64;
func (self *TUniEngine) SelectD(sqlQuery string, args ...interface{}) (int64, error) {

	var eror error
	var size sql.NullInt64

	sqlQuery = self.getSqlQuery(sqlQuery, args)

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: select.sql", sqlQuery)
		fmt.Println("UniEngine: select.val", args)
	}

	eror = self.prepare(sqlQuery)
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
func (self *TUniEngine) SelectF(sqlQuery string, args ...interface{}) (float64, error) {

	var eror error
	var size sql.NullFloat64

	sqlQuery = self.getSqlQuery(sqlQuery, args)

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: select.sql", sqlQuery)
		fmt.Println("UniEngine: select.val", args)
	}

	eror = self.prepare(sqlQuery)
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
func (self *TUniEngine) SelectS(sqlQuery string, args ...interface{}) (string, error) {

	var eror error
	var text sql.NullString

	sqlQuery = self.getSqlQuery(sqlQuery, args)

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: select.sql", sqlQuery)
		fmt.Println("UniEngine: select.val", args)
	}

	eror = self.prepare(sqlQuery)
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
func (self *TUniEngine) Select(i interface{}, sqlQuery string, args ...interface{}) error {

	var eror error

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Struct {
		//TO DO:
		return errors.New("UniEngine: method [Select] only retun a struct; may be you should try [SelectL]")
	}

	sqlQuery = self.getSqlQuery(sqlQuery, args)

	cName := t.String()
	cTable, Valid := self.HashTabl[cName]
	if !Valid {
		return errors.New(fmt.Sprintf("UniEngine: no such class registered:", cName))
	}

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: select.sql", sqlQuery)
		fmt.Println("UniEngine: select.val", args)
	}

	//-<
	eror = self.prepare(sqlQuery)
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

				for cIndx, cItem := range column {
					cField, Valid := cTable.HashField[strings.ToLower(cItem)]
					if !Valid {
						return errors.New(fmt.Sprintf("UniEngine: database have field[%s], but not in class[%s]", cItem, t.String()))
					}
					values[cIndx] = Result.FieldByName(cField.AttriName).Addr().Interface()
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
		//	for cIndx, cItem := range column {
		//		cField, Valid := cTable.HashField[strings.ToLower(cItem)]
		//		if !Valid {
		//			return errors.New(fmt.Sprintf("UniEngine: database have field[%s], but not in class[%s]", cItem, t.String()))
		//		}
		//		values[cIndx] = Result.FieldByName(cField.AttriName).Addr().Interface()
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
func (self *TUniEngine) SelectL(i interface{}, sqlQuery string, args ...interface{}) error {

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

	sqlQuery = self.getSqlQuery(sqlQuery, args)

	cName := t.String()
	cTable, Valid := self.HashTabl[cName]
	if !Valid {
		return errors.New(fmt.Sprintf("UniEngine: no such class registered:", cName))
	}

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: select.sql", sqlQuery)
		fmt.Println("UniEngine: select.val", args)
	}

	//-<
	eror = self.prepare(sqlQuery)
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

				Result.Set(reflect.Append(Result, u.Elem()))
			}
		default:
			{
				for cIndx, cItem := range column {
					cField, Valid := cTable.HashField[strings.ToLower(cItem)]
					if !Valid {
						return errors.New(fmt.Sprintf("UniEngine: database have field[%s], but not in class[%s]", cItem, t.String()))
					}
					values[cIndx] = u.Elem().FieldByName(cField.AttriName).Addr().Interface()
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
		//	for cIndx, cItem := range column {
		//		cField, Valid := cTable.HashField[strings.ToLower(cItem)]
		//		if !Valid {
		//			return errors.New(fmt.Sprintf("UniEngine: database have field[%s], but not in class[%s]", cItem, t.String()))
		//		}
		//		values[cIndx] = u.Elem().FieldByName(cField.AttriName).Addr().Interface()
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
func (self *TUniEngine) SelectM(i interface{}, sqlQuery string, args ...interface{}) error {

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

	sqlQuery = self.getSqlQuery(sqlQuery, args)

	cName := t.String()
	cTable, Valid := self.HashTabl[cName]
	if !Valid {
		return errors.New(fmt.Sprintf("UniEngine:no such class registered:", cName))
	}

	if !t.Implements(THasGetMapUnique) {
		return errors.New(fmt.Sprintf("UniEngine: the class registered:[%s] does not Implemented [HasGetMapUnique]", cName))
	}

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: select.sql", sqlQuery)
		fmt.Println("UniEngine: select.val", args)
	}

	//-<
	eror = self.prepare(sqlQuery)
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
				for cIndx, cItem := range column {
					cField, Valid := cTable.HashField[strings.ToLower(cItem)]
					if !Valid {
						return errors.New(fmt.Sprintf("UniEngine: database have field[%s], but not in class[%s]", cItem, t.String()))
					}
					values[cIndx] = u.Elem().FieldByName(cField.AttriName).Addr().Interface()
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
		//	for cIndx, cItem := range column {
		//		cField, Valid := cTable.HashField[strings.ToLower(cItem)]
		//		if !Valid {
		//			return errors.New(fmt.Sprintf("UniEngine: database have field[%s], but not in class[%s]", cItem, t.String()))
		//		}
		//		values[cIndx] = u.Elem().FieldByName(cField.AttriName).Addr().Interface()
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
	eror = UniEngineEx.SelectH(&listData, func(u interface{}) string {
		cItem := u.(TDATA)
		return fmt.Sprintf("%d-%d-%d-%d", cData.ANTVMAIN, cData.UNITINDX, cData.SOURCEND, cData.SOURCEQJ)
	}, cSQL, whobuild, userindx, sourcend, sourceqj)

    xData, Valid := HashData[fmt.Sprintf("%d-%d-%d-%d", cITEM.ANTVMAIN, cITEM.UNITINDX, cITEM.SOURCEND, cITEM.SOURCEQJ)]
    if Valid {
        cITEM.DATAINDX = xITEM.DATAINDX

        continue
    }
*/

func (self *TUniEngine) SelectH(i interface{}, f GetMapUnique, sqlQuery string, args ...interface{}) error {

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

	sqlQuery = self.getSqlQuery(sqlQuery, args)

	cName := t.String()
	cTable, Valid := self.HashTabl[cName]
	if !Valid {
		return errors.New(fmt.Sprintf("UniEngine: no such class registered:", cName))
	}

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: select.sql", sqlQuery)
		fmt.Println("UniEngine: select.val", args)
	}

	//-<
	eror = self.prepare(sqlQuery)
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
				for cIndx, cItem := range column {
					cField, Valid := cTable.HashField[strings.ToLower(cItem)]
					if !Valid {
						return errors.New(fmt.Sprintf("UniEngine: database have field[%s], but not in class[%s]", cItem, t.String()))
					}
					values[cIndx] = u.Elem().FieldByName(cField.AttriName).Addr().Interface()
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
		//	if f != nil {
		//		MapUnique = f(u.Elem().Interface())
		//	}
		//	Result.SetMapIndex(reflect.ValueOf(MapUnique), u.Elem())
		//
		//} else {
		//
		//	for cIndx, cItem := range column {
		//		cField, Valid := cTable.HashField[strings.ToLower(cItem)]
		//		if !Valid {
		//			return errors.New(fmt.Sprintf("UniEngine: database have field[%s], but not in class[%s]", cItem, t.String()))
		//		}
		//		values[cIndx] = u.Elem().FieldByName(cField.AttriName).Addr().Interface()
		//	}
		//
		//	eror = rows.Scan(values...)
		//	if eror != nil {
		//		return eror
		//	}
		//
		//	var MapUnique string
		//	if f != nil {
		//		MapUnique = f(u.Elem().Interface())
		//	}
		//	Result.SetMapIndex(reflect.ValueOf(MapUnique), u.Elem())
		//}
	}

	return nil
}

func (self *TUniEngine) SaveIt(i interface{}, args ...interface{}) error {

	var eror error
	var TablName string

	if len(args) > 0 {
		TablName = args[0].(string)
	}

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	cTable, Valid := self.HashTabl[t.String()]
	if !Valid {
		return errors.New(fmt.Sprintf("UniEngine: no such class registered:", t.String()))
	}
	if len(cTable.HashPkeys) == 0 {
		return errors.New(fmt.Sprintf("UniEngine: no pkeys column in class registered:", t.String()))
	}

	if TablName == "" {
		TablName = cTable.TableName
	}

	v := reflect.Indirect(reflect.ValueOf(i))

	cIndex := 1
	//@cField := ""
	SqlWhere := ""

	SqlQuery := ""
	cValue := make([]interface{}, 0)

	//@SaveIt方法不需要这一组
	/*
		if x, ok := v.Interface().(HasGetSqlUpdate); ok {
			cQuery = x.GetSqlUpdate(cTableName)
		}

		if x, ok := v.Interface().(HasSetSqlValues); ok {
			x.SetSqlValues(EtUpdate, &cValue)
		}
	*/

	if SqlQuery == "" && len(cValue) == 0 {
		for _, item := range cTable.HashField {

			if item.ReadOnly {
				continue
			}

			if _, valid := cTable.HashPkeys[item.FieldName]; valid {
				//@cWhere = cWhere + " and " + fmt.Sprintf(`"`+item.FieldName+`"`) + "=" + fmt.Sprintf("$%d", cIndex)
				SqlWhere = SqlWhere + " and " + self.getColParam(item.FieldName) + "=" + self.getValParam(cIndex)
				cValue = append(cValue, v.FieldByName(item.AttriName).Interface())
				cIndex = cIndex + 1
			}
		}

		//@cField = string(cField[1:])
		SqlWhere = string(SqlWhere[4:])

		SqlQuery = fmt.Sprintf("select count(1) from %s where %s", TablName, SqlWhere)
	}

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: select.sql", SqlQuery)
		fmt.Println("UniEngine: select.val", cValue)
	}

	cCount, eror := self.SelectD(SqlQuery, cValue...)
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
	var TablName string

	if len(args) > 0 {
		TablName = args[0].(string)
	}

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	cTable, Valid := self.HashTabl[t.String()]
	if !Valid {
		return errors.New(fmt.Sprintf("UniEngine: no such class registered:", t.String()))
	}
	if len(cTable.HashPkeys) == 0 {
		return errors.New(fmt.Sprintf("UniEngine: no pkeys column in class registered:", t.String()))
	}

	if TablName == "" {
		TablName = cTable.TableName
	}

	v := reflect.Indirect(reflect.ValueOf(i))

	cIndex := 1
	//@cField := ""
	SqlWhere := ""

	SqlQuery := ""
	cValue := make([]interface{}, 0)

	//@SaveIt方法不需要这一组
	/*
		if x, ok := v.Interface().(HasGetSqlUpdate); ok {
			cQuery = x.GetSqlUpdate(cTableName)
		}

		if x, ok := v.Interface().(HasSetSqlValues); ok {
			x.SetSqlValues(EtUpdate, &cValue)
		}
	*/

	if SqlQuery == "" && len(cValue) == 0 {
		for _, item := range cTable.HashField {

			if item.ReadOnly {
				continue
			}

			if _, valid := cTable.HashPkeys[item.FieldName]; valid {
				//@cWhere = cWhere + " and " + fmt.Sprintf(`"`+item.FieldName+`"`) + "=" + fmt.Sprintf("$%d", cIndex)
				SqlWhere = SqlWhere + " and " + self.getColParam(item.FieldName) + "=" + self.getValParam(cIndex)
				cValue = append(cValue, v.FieldByName(item.AttriName).Interface())
				cIndex = cIndex + 1
			}
		}

		//@cField = string(cField[1:])
		SqlWhere = string(SqlWhere[4:])

		SqlQuery = fmt.Sprintf("select count(1) from %s where %s", TablName, SqlWhere)
	}

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: select.sql", SqlQuery)
		fmt.Println("UniEngine: select.val", cValue)
	}

	cCount, eror := self.SelectD(SqlQuery, cValue...)
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
	var TablName string

	if len(args) > 0 {
		TablName = args[0].(string)
	}

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	cTable, Valid := self.HashTabl[t.String()]
	if !Valid {
		return errors.New(fmt.Sprintf("UniEngine: no such class registered:", t.String()))
	}
	if len(cTable.HashPkeys) == 0 {
		return errors.New(fmt.Sprintf("UniEngine: no pkeys column in class registered:", t.String()))
	}
	if TablName == "" {
		TablName = cTable.TableName
	}

	//#打印语句
	if self.runDebug {
		fmt.Println(fmt.Sprintf("UniEngine: try update table:%s", cTable))
	}

	v := reflect.Indirect(reflect.ValueOf(i))

	cIndex := 1
	cField := ""
	cWhere := ""

	SqlQuery := ""
	cValue := make([]interface{}, 0)
	xValue := make([]interface{}, 0)
	zValue := make([]interface{}, 0)

	if x, ok := v.Interface().(HasGetSqlUpdate); ok {
		SqlQuery = x.GetSqlUpdate(*self, TablName)
	}
	/*
		if x, ok := v.Interface().(HasGetSqlValues); ok {
			cValue = x.GetSqlValues(EtUpdate)
		}
	*/
	if x, ok := v.Interface().(HasSetSqlValues); ok {
		x.SetSqlValues(*self, EtUpdate, &cValue)
	}

	if SqlQuery == "" && len(cValue) == 0 {
		for _, item := range cTable.HashField {

			if item.ReadOnly {
				continue
			}

			/*
				if _, valid := cTable.HashPkeys[strings.ToLower(item.FieldName)]; valid {
					cWhere = cWhere + " and " + fmt.Sprintf(`"`+item.FieldName+`"`) + "=" + fmt.Sprintf("%s%d", self.ColParam, cIndex)
				} else {
					cField = cField + "," + fmt.Sprintf(`"`+item.FieldName+`"`) + "=" + fmt.Sprintf("%s%d", self.ColParam, cIndex)
				}
			*/

			/*
				if _, valid := cTable.HashPkeys[strings.ToLower(item.FieldName)]; valid {
					cWhere = cWhere + " and " + self.getColParam(item.FieldName) + "=" + self.getValParam(cIndex)
					zValue = append(zValue, v.FieldByName(item.AttriName).Interface())
				} else {
					cField = cField + "," + self.getColParam(item.FieldName) + "=" + self.getValParam(cIndex)
					xValue = append(xValue, v.FieldByName(item.AttriName).Interface())
				}
			*/

			if _, valid := cTable.HashPkeys[strings.ToLower(item.FieldName)]; valid {
				continue
			}

			cField = cField + "," + self.getColParam(item.FieldName) + "=" + self.getValParam(cIndex)
			xValue = append(xValue, v.FieldByName(item.AttriName).Interface())

			cIndex = cIndex + 1
		}

		for _, item := range cTable.HashPkeys {
			cWhere = cWhere + " and " + self.getColParam(item.FieldName) + "=" + self.getValParam(cIndex)
			zValue = append(zValue, v.FieldByName(item.AttriName).Interface())
			cIndex = cIndex + 1
		}

		cField = string(cField[1:])
		cWhere = string(cWhere[1:])

		SqlQuery = fmt.Sprintf("update %s set %s where 1=1 %s", TablName, cField, cWhere)

		cValue = append(xValue, zValue...)
	}

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: update.sql:", SqlQuery)
		fmt.Println("UniEngine: update.val:", cValue)
	}

	eror = self.prepare(SqlQuery)
	if eror != nil {
		return eror
	}
	defer self.release()

	_, eror = self.st.Exec(cValue...)
	if eror != nil {
		return eror
	}

	return nil
}

func (self *TUniEngine) Insert(i interface{}, args ...interface{}) error {

	var eror error
	var TablName string

	if len(args) > 0 {
		TablName = args[0].(string)
	}

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Struct {
		//TO DO:
		return errors.New("UniEngine: method [Insert] only retun a struct; may be you should try [InsertL]")
	}

	cTable, Valid := self.HashTabl[t.String()]
	if !Valid {
		return errors.New(fmt.Sprintf("UniEngine: no such class registered:", t.String()))
	}
	if TablName == "" {
		TablName = cTable.TableName
	}

	v := reflect.Indirect(reflect.ValueOf(i))

	cIndex := 1
	cField := ""
	cParam := ""

	SqlQuery := ""
	cValue := make([]interface{}, 0)

	if x, ok := v.Interface().(HasGetSqlInsert); ok {
		SqlQuery = x.GetSqlInsert(*self, TablName)
	}
	/*
		if x, ok := v.Interface().(HasGetSqlValues); ok {
			cValue = x.GetSqlValues(EtInsert)
		}
	*/
	if x, ok := v.Interface().(HasSetSqlValues); ok {
		x.SetSqlValues(*self, EtInsert, &cValue)
	}

	if SqlQuery == "" && len(cValue) == 0 {

		for _, cItem := range cTable.HashField {

			if cItem.ReadOnly {
				continue
			}

			/*
				cField = cField + "," + fmt.Sprintf(`"`+cItem.FieldName+`"`)
				cParam = cParam + "," + fmt.Sprintf("%s%d", self.ColParam, cIndex)
			*/

			cField = cField + "," + self.getColParam(cItem.FieldName)
			cParam = cParam + "," + self.getValParam(cIndex)
			cIndex = cIndex + 1

			cValue = append(cValue, v.FieldByName(cItem.AttriName).Interface())
		}

		cField = string(cField[1:])
		cParam = string(cParam[1:])

		SqlQuery = fmt.Sprintf("insert into %s ( %s ) values ( %s ) ", TablName, cField, cParam)
	}

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: insert.sql", SqlQuery)
		fmt.Println("UniEngine: insert.val", cValue)
	}

	eror = self.prepare(SqlQuery)
	if eror != nil {
		return eror
	}
	defer self.release()

	_, eror = self.st.Exec(cValue...)
	if eror != nil {
		return eror
	}

	return nil
}

func (self *TUniEngine) InsertL(i interface{}, args ...interface{}) error {

	var eror error
	var TablName string

	if len(args) > 0 {
		TablName = args[0].(string)
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

	cTable, Valid := self.HashTabl[t.String()]
	if !Valid {
		return errors.New(fmt.Sprintf("UniEngine: no such class registered:", t.String()))
	}
	if TablName == "" {
		TablName = cTable.TableName
	}

	v := reflect.Indirect(reflect.ValueOf(i))

	if v.Len() == 0 {
		return nil
	}

	cIndex := 1
	cField := ""
	cParam := ""
	zParam := ""

	SqlQuery := ""
	cValue := make([]interface{}, 0)

	if x, ok := v.Index(0).Interface().(HasGetSqlInsertL); ok {
		SqlQuery = x.GetSqlInsertL(*self, TablName, int64(v.Len()))
	}

	if x, ok := v.Index(0).Interface().(HasSetSqlValuesL); ok {
		for m := 0; m < v.Len(); m++ {
			f := v.Index(m)
			x.SetSqlValuesL(*self, EtInsert, f, &cValue)
		}
	}

	if SqlQuery == "" && len(cValue) == 0 {

		//#先设置TablName,cField
		//#switch map to slice
		var HashField = make([]TUniField, 0)
		for _, cItem := range cTable.HashField {
			if cItem.ReadOnly {
				continue
			}
			HashField = append(HashField, cItem)
		}

		for _, cItem := range HashField {
			cField = cField + "," + self.getColParam(cItem.FieldName)
		}
		cField = cField[1:]

		switch self.Provider {
		case DtORACLE:
			{
				//#先设置TablName,cField,再设置cParam,cValue
				for m := 0; m < v.Len(); m++ {

					f := v.Index(m)

					cParam = ""
					for _, cItem := range HashField {

						cParam = cParam + "," + self.getValParam(cIndex)
						cIndex = cIndex + 1
						cValue = append(cValue, f.FieldByName(cItem.AttriName).Interface())
					}

					cParam = cParam[1:]
					zParam = zParam + " " + fmt.Sprintf("into %s ( %s ) values ( %s )", TablName, cField, cParam)
				}

				zParam = zParam[1:]
				SqlQuery = fmt.Sprintf("insert all %s select 1 from dual", zParam)
			}
		default:
			{
				//#先设置TablName,cField,再设置cParam,cValue
				for m := 0; m < v.Len(); m++ {

					f := v.Index(m)

					cParam = ""
					for _, cItem := range HashField {

						cParam = cParam + "," + self.getValParam(cIndex)
						cIndex = cIndex + 1
						cValue = append(cValue, f.FieldByName(cItem.AttriName).Interface())
					}

					cParam = cParam[1:]
					zParam = zParam + "," + fmt.Sprintf("( %s )", cParam)
				}

				zParam = zParam[1:]
				SqlQuery = fmt.Sprintf("insert into %s ( %s ) values %s", TablName, cField, zParam)
			}
		}
	}

	//#打印语句
	if self.runDebug {
		fmt.Println("UniEngine: insert.sql:", SqlQuery)
		fmt.Println("UniEngine: insert.val:", cValue)
	}

	eror = self.prepare(SqlQuery)
	if eror != nil {
		return eror
	}
	defer self.release()

	_, eror = self.st.Exec(cValue...)
	if eror != nil {
		return errors.New(fmt.Sprintf("UniEngine: if errored too many parameters; try [InsertP(PageSize)] method;@%s", eror.Error()))
	}

	return nil
}

func (self *TUniEngine) InsertP(i interface{}, aPageSize int64, args ...interface{}) error {

	var eror error

	if aPageSize == 0 || aPageSize == -1 {
		aPageSize = 999
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

		if ListData.Len() == int(aPageSize) {
			eror = self.InsertL(ListData.Interface(), args...)
			if eror != nil {
				return eror
			}
			ListData = reflect.MakeSlice(t, 0, 0)
		}
	}

	eror = self.InsertL(ListData.Interface(), args...)
	if eror != nil {
		return eror
	}

	return nil
}

func (self *TUniEngine) Delete(i interface{}, args ...interface{}) error {

	var eror error
	var TablName string

	if len(args) > 0 {
		TablName = args[0].(string)
	}

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	cTable, Valid := self.HashTabl[t.String()]
	if !Valid {
		return errors.New(fmt.Sprintf("UniEngine: no such class registered:", t.String()))
	}
	if len(cTable.HashPkeys) == 0 {
		return errors.New(fmt.Sprintf("UniEngine: no pkeys column in class registered:", t.String()))
	}

	if TablName == "" {
		TablName = cTable.TableName
	}

	v := reflect.Indirect(reflect.ValueOf(i))

	cIndex := 1
	SqlWhere := ""
	cValue := make([]interface{}, 0)

	for _, cItem := range cTable.HashPkeys {

		//cWhere = cWhere + " and " + fmt.Sprintf(`"`+cItem.FieldName+`"`) + "=" + fmt.Sprintf("%s%d", self.ColParam, cIndex)
		SqlWhere = SqlWhere + " and " + self.getColParam(cItem.FieldName) + "=" + self.getValParam(cIndex)
		cIndex = cIndex + 1

		cValue = append(cValue, v.FieldByName(cItem.AttriName).Interface())
	}

	SqlWhere = string(SqlWhere[4:])

	SqlQuery := fmt.Sprintf("delete from %s where %s", TablName, SqlWhere)

	if self.runDebug {
		fmt.Println("UniEngine: delete.sql:", SqlQuery)
		fmt.Println("UniEngine: delete.val:", cValue)
	}

	eror = self.prepare(SqlQuery)
	if eror != nil {
		return eror
	}
	defer self.release()

	_, eror = self.st.Exec(cValue...)
	if eror != nil {
		return eror
	}

	return nil
}

func (self *TUniEngine) Execute(sqlQuery string, args ...interface{}) error {

	var eror error

	sqlQuery = self.getSqlQuery(sqlQuery, args)

	if self.runDebug {
		fmt.Println(fmt.Sprintf("UniEngine: execute.sql:%s", sqlQuery))
	}

	if self.canClose {
		self.st, eror = self.Db.Prepare(sqlQuery)
	} else {
		self.st, eror = self.tx.Prepare(sqlQuery)
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

func (self *TUniEngine) ExecuteMust(sqlQuery string, args ...interface{}) error {

	var eror error

	sqlQuery = self.getSqlQuery(sqlQuery, args)

	if self.canClose {
		self.st, eror = self.Db.Prepare(sqlQuery)
	} else {
		self.st, eror = self.tx.Prepare(sqlQuery)
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

func (self *TUniEngine) ExistTable(aTableName string) (bool, error) {

	var eror error
	cSQL := ""

	/*
		if len(GetSqlExistTable) > 0 {

			if x, ok := GetSqlExistTable[0].(HasGetSqlExistTable); ok {
				cSQL = x.GetSqlExistTable(aTableName)
			}

		} else {

			var ExistTable4POSTGR = TExistTable4POSTGR{}
			cSQL = ExistTable4POSTGR.GetSqlExistTable(aTableName)

		}
	*/

	switch self.Provider {
	case DtPOSTGR:
		{
			var ExistTable4POSTGR = TExistTable4POSTGR{}
			cSQL = ExistTable4POSTGR.GetSqlExistTable(*self, aTableName)
		}
	case DtSQLSRV:
		{
			var ExistTable4SQLSRV = TExistTable4SQLSRV{}
			cSQL = ExistTable4SQLSRV.GetSqlExistTable(*self, aTableName)
		}
	case DtORACLE:
		{
			var ExistTable4ORACLE = TExistTable4ORACLE{}
			cSQL = ExistTable4ORACLE.GetSqlExistTable(*self, aTableName)
		}
	case DtMYSQLN:
		{
			if self.DataBase == "" {
				return false, errors.New("UniEngine: DataBase is not specified")
			}
			var ExistTable4MYSQLN = TExistTable4MYSQLN{}
			cSQL = ExistTable4MYSQLN.GetSqlExistTable(*self, aTableName, self.DataBase)
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


func (self *TUniEngine) ExistViews(aTableName string) (bool, error) {

	var eror error
	cSQL := ""

	/*
		if len(GetSqlExistTable) > 0 {

			if x, ok := GetSqlExistTable[0].(HasGetSqlExistTable); ok {
				cSQL = x.GetSqlExistTable(aTableName)
			}

		} else {

			var ExistTable4POSTGR = TExistTable4POSTGR{}
			cSQL = ExistTable4POSTGR.GetSqlExistTable(aTableName)

		}
	*/

	switch self.Provider {
	case DtPOSTGR:
		{
			var ExistTable4POSTGR = TExistTable4POSTGR{}
			cSQL = ExistTable4POSTGR.GetSqlExistViews(*self, aTableName)
		}
	case DtSQLSRV:
		{
			var ExistTable4SQLSRV = TExistTable4SQLSRV{}
			cSQL = ExistTable4SQLSRV.GetSqlExistViews(*self, aTableName)
		}
	case DtORACLE:
		{
			var ExistTable4ORACLE = TExistTable4ORACLE{}
			cSQL = ExistTable4ORACLE.GetSqlExistViews(*self, aTableName)
		}
	case DtMYSQLN:
		{
			if self.DataBase == "" {
				return false, errors.New("UniEngine: DataBase is not specified")
			}
			var ExistTable4MYSQLN = TExistTable4MYSQLN{}
			cSQL = ExistTable4MYSQLN.GetSqlExistViews(*self, aTableName, self.DataBase)
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

func (self *TUniEngine) ExistField(aTableName, aFieldName string) (bool, error) {

	var eror error
	cSQL := ""

	/*
		if len(GetSqlExistField) > 0 {

			if x, ok := GetSqlExistField[0].(HasGetSqlExistField); ok {
				cSQL = x.GetSqlExistField(aTableName, aFieldName)
			}

		} else {

			var ExistField4POSTGR = TExistField4POSTGR{}
			cSQL = ExistField4POSTGR.GetSqlExistField(aTableName, aFieldName)

		}
	*/

	switch self.Provider {
	case DtPOSTGR:
		{
			var ExistField4POSTGR = TExistField4POSTGR{}
			cSQL = ExistField4POSTGR.GetSqlExistField(*self, aTableName, aFieldName)
		}
	case DtSQLSRV:
		{
			var ExistField4SQLSRV = TExistField4SQLSRV{}
			cSQL = ExistField4SQLSRV.GetSqlExistField(*self, aTableName, aFieldName)
		}
	case DtORACLE:
		{
			var ExistField4ORACLE = TExistField4ORACLE{}
			cSQL = ExistField4ORACLE.GetSqlExistField(*self, aTableName, aFieldName)
		}
	case DtMYSQLN:
		{
			if self.DataBase == "" {
				return false, errors.New("UniEngine: DataBase is not specified")
			}
			var ExistField4MYSQLN = TExistField4MYSQLN{}
			cSQL = ExistField4MYSQLN.GetSqlExistField(*self, aTableName, aFieldName, self.DataBase)
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

func (self *TUniEngine) prepare(sqlQuery string) error {

	var eror error

	switch self.canClose {
	case true:
		{
			self.st, eror = self.Db.Prepare(sqlQuery)
		}
	default:
		{
			self.st, eror = self.tx.Prepare(sqlQuery)
		}
	}
	//if self.canClose {
	//	self.st, eror = self.Db.Prepare(sqlQuery)
	//} else {
	//	self.st, eror = self.tx.Prepare(sqlQuery)
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
