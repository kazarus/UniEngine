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

	ListTabl map[string]TUniTable
	ColLabel string
	ColParam string
	Provider TDriveType
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

func (self *TUniEngine) RegisterClass(aClass interface{}, aTableName string) *TUniTable {

	if self.ListTabl == nil {
		self.ListTabl = make(map[string]TUniTable, 0)
	}

	t := reflect.TypeOf(aClass)
	n := t.NumField()

	var cTable = TUniTable{}
	cTable.ListField = make(map[string]TUniField, 0)
	cTable.ListPkeys = make(map[string]TUniField, 0)
	cTable.TableName = aTableName

	for i := 0; i < n; i++ {
		f := t.Field(i)

		var cField = TUniField{}
		cField.AttriName = f.Name
		//@		cField.FieldType = f.Type

		cField.initialize(f.Tag.Get(self.ColLabel))

		cTable.ListField[strings.ToLower(cField.FieldName)] = cField
	}

	self.ListTabl[t.String()] = cTable

	return &cTable
}

//return int64;
func (self *TUniEngine) SelectD(sqlQuery string, args ...interface{}) (int64, error) {

	var eror error
	var size sql.NullInt64

	sqlQuery = self.getSqlQuery(sqlQuery, args)

	eror = self.prepare(sqlQuery)
	if eror != nil {
		return 0, eror
	}

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

//return float64;
func (self *TUniEngine) SelectF(sqlQuery string, args ...interface{}) (float64, error) {

	var eror error
	var size sql.NullFloat64

	sqlQuery = self.getSqlQuery(sqlQuery, args)

	eror = self.prepare(sqlQuery)
	if eror != nil {
		return 0, eror
	}

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

//return string;
func (self *TUniEngine) SelectS(sqlQuery string, args ...interface{}) (string, error) {

	var eror error
	var text sql.NullString

	sqlQuery = self.getSqlQuery(sqlQuery, args)

	eror = self.prepare(sqlQuery)
	if eror != nil {
		return "", eror
	}

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

//return struct;
func (self *TUniEngine) Select(i interface{}, sqlQuery string, args ...interface{}) error {

	var eror error

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Struct {
		//TO DO:
		return errors.New("UniEngine:method [Select] only retun a struct; may be you should try [SelectL]")
	}

	sqlQuery = self.getSqlQuery(sqlQuery, args)

	cName := t.String()
	cTable, Valid := self.ListTabl[cName]
	if !Valid {
		return errors.New(fmt.Sprintf("UniEngine:no such class registered:", cName))
	}

	//-<
	eror = self.prepare(sqlQuery)
	if eror != nil {
		return eror
	}

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

		if x, ok := i.(HasSetSqlResult); ok {
			for i := 0; i < cCount; i++ {
				values[i] = &fields[i]
			}

			eror = rows.Scan(values...)
			if eror != nil {
				return eror
			}

			x.SetSqlResult(i, column, fields)

		} else {

			var Result = reflect.Indirect(reflect.ValueOf(i))

			for cIndx, cItem := range column {
				cField, Valid := cTable.ListField[strings.ToLower(cItem)]
				if !Valid {
					return errors.New(fmt.Sprintf("UniEngine:database have field[%s], but not in class[%s]", cItem, t.String()))
				}
				values[cIndx] = Result.FieldByName(cField.AttriName).Addr().Interface()
			}

			eror = rows.Scan(values...)
			if eror != nil {
				return eror
			}
		}
	}

	return nil
}

//return slice of struct;
func (self *TUniEngine) SelectL(i interface{}, sqlQuery string, args ...interface{}) error {

	var eror error

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Slice {
		//TO DO:
		return errors.New("UniEngine:method [Select] only retun a struct; may be you should try [SelectL]")
	}

	if t.Kind() == reflect.Slice {
		t = t.Elem()
	}

	sqlQuery = self.getSqlQuery(sqlQuery, args)

	cName := t.String()
	cTable, Valid := self.ListTabl[cName]
	if !Valid {
		return errors.New(fmt.Sprintf("UniEngine:no such class registered:", cName))
	}

	//-<
	eror = self.prepare(sqlQuery)
	if eror != nil {
		return eror
	}

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

		if t.Implements(THasSetSqlResult) {

			for i := 0; i < cCount; i++ {
				values[i] = &fields[i]
			}

			eror = rows.Scan(values...)
			if eror != nil {
				return eror
			}

			x := u.Interface().(HasSetSqlResult)
			x.SetSqlResult(u.Interface(), column, fields)

			Result.Set(reflect.Append(Result, u.Elem()))

		} else {

			for cIndx, cItem := range column {
				cField, Valid := cTable.ListField[strings.ToLower(cItem)]
				if !Valid {
					return errors.New(fmt.Sprintf("UniEngine:database have field[%s], but not in class[%s]", cItem, t.String()))
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

	return nil
}

//return map of struct;user;GetMapUnique;
func (self *TUniEngine) SelectM(i interface{}, sqlQuery string, args ...interface{}) error {

	var eror error

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Map {
		//TO DO:
		return errors.New("UniEngine:method [Select] only retun a struct; may be you should try [SelectL]")
	}

	if t.Kind() == reflect.Map {
		t = t.Elem()
	}

	sqlQuery = self.getSqlQuery(sqlQuery, args)

	cName := t.String()
	cTable, Valid := self.ListTabl[cName]
	if !Valid {
		return errors.New(fmt.Sprintf("UniEngine:no such class registered:", cName))
	}

	if !t.Implements(THasGetMapUnique) {
		return errors.New(fmt.Sprintf("UniEngine:the class registered:[%s] does not Implemented [HasGetMapUnique] ", cName))
	}

	//-<
	eror = self.prepare(sqlQuery)
	if eror != nil {
		return eror
	}

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

		if t.Implements(THasSetSqlResult) {

			for i := 0; i < cCount; i++ {
				values[i] = &fields[i]
			}

			eror = rows.Scan(values...)
			if eror != nil {
				return eror
			}

			x := u.Interface().(HasSetSqlResult)
			x.SetSqlResult(u.Interface(), column, fields)

			var MapUnique string
			if x, ok := u.Interface().(HasGetMapUnique); ok {
				MapUnique = x.GetMapUnique()
			}
			Result.SetMapIndex(reflect.ValueOf(MapUnique), u.Elem())

		} else {

			for cIndx, cItem := range column {
				cField, Valid := cTable.ListField[strings.ToLower(cItem)]
				if !Valid {
					return errors.New(fmt.Sprintf("UniEngine:database have field[%s], but not in class[%s]", cItem, t.String()))
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

	return nil
}

//return map;use custom function;

/*
	cSQL = "SELECT * FROM ANTV_DATA WHERE 1=1 AND WHO_BUILD=$1 AND USER_INDX=$2 AND SOURCE_ND=$3 AND SOURCE_QJ=$4"
	eror = UniEngineEx.SelectH(&listData, func(u interface{}) string {
		cDATA := u.(TDATA)
		return fmt.Sprintf("%d-%d-%d-%d", cDATA.ANTVMAIN, cDATA.UNITINDX, cDATA.SOURCEND, cDATA.SOURCEQJ)
	}, cSQL, whobuild, userindx, sourcend, sourceqj)
*/

func (self *TUniEngine) SelectH(i interface{}, f GetMapUnique, sqlQuery string, args ...interface{}) error {

	var eror error

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Map {
		//TO DO:
		return errors.New("UniEngine:method [select] only retun a struct; may be you should try [SelectL]")
	}

	if t.Kind() == reflect.Map {
		t = t.Elem()
	}

	sqlQuery = self.getSqlQuery(sqlQuery, args)

	cName := t.String()
	cTable, Valid := self.ListTabl[cName]
	if !Valid {
		return errors.New(fmt.Sprintf("UniEngine:no such class registered:", cName))
	}

	//-<
	eror = self.prepare(sqlQuery)
	if eror != nil {
		return eror
	}

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

		if t.Implements(THasSetSqlResult) {

			for i := 0; i < cCount; i++ {
				values[i] = &fields[i]
			}

			eror = rows.Scan(values...)
			if eror != nil {
				return eror
			}

			x := u.Interface().(HasSetSqlResult)
			x.SetSqlResult(u.Interface(), column, fields)

			var MapUnique string
			if f != nil {
				MapUnique = f(u.Elem().Interface())
			}
			Result.SetMapIndex(reflect.ValueOf(MapUnique), u.Elem())

		} else {

			for cIndx, cItem := range column {
				cField, Valid := cTable.ListField[strings.ToLower(cItem)]
				if !Valid {
					return errors.New(fmt.Sprintf("UniEngine:database have field[%s], but not in class[%s]", cItem, t.String()))
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

	return nil
}

func (self *TUniEngine) SaveIt(i interface{}, args ...interface{}) error {

	var eror error

	cTableName := ""
	if len(args) > 0 {
		cTableName = args[0].(string)
	}

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	cTable, Valid := self.ListTabl[t.String()]
	if !Valid {
		return errors.New(fmt.Sprintf("UniEngine:no such class registered:", t.String()))
	}
	if len(cTable.ListPkeys) == 0 {
		return errors.New(fmt.Sprintf("UniEngine:no pkeys column in class registered:", t.String()))
	}

	if cTableName == "" {
		cTableName = cTable.TableName
	}

	v := reflect.Indirect(reflect.ValueOf(i))

	cIndex := 1
	//@cField := ""
	cWhere := ""

	cQuery := ""
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

	if cQuery == "" && len(cValue) == 0 {
		for _, item := range cTable.ListField {

			if item.ReadOnly {
				continue
			}

			if _, valid := cTable.ListPkeys[item.FieldName]; valid {
				cWhere = cWhere + " and " + fmt.Sprintf(`"`+item.FieldName+`"`) + "=" + fmt.Sprintf("$%d", cIndex)
				cValue = append(cValue, v.FieldByName(item.AttriName).Interface())
				cIndex = cIndex + 1
			}
		}

		//@cField = string(cField[1:])
		cWhere = string(cWhere[4:])

		cQuery = fmt.Sprintf("select count(1) from %s where %s", cTableName, cWhere)
	}

	//#打印语句
	if self.runDebug {
		fmt.Println("select.sql", cQuery)
		fmt.Println("select.val", cValue)
	}

	cCount, eror := self.SelectD(cQuery, cValue...)
	if eror != nil {
		return eror
	}

	if self.runDebug {
		fmt.Println("select.cnt", cCount)
	}

	if cCount == 1 {
		return self.Update(i, args...)
	} else {
		return self.Insert(i, args...)
	}

	return nil
}

func (self *TUniEngine) Update(i interface{}, args ...interface{}) error {

	var eror error

	cTableName := ""
	if len(args) > 0 {
		cTableName = args[0].(string)
	}

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	cTable, Valid := self.ListTabl[t.String()]
	if !Valid {
		return errors.New(fmt.Sprintf("UniEngine:no such class registered:", t.String()))
	}
	if len(cTable.ListPkeys) == 0 {
		return errors.New(fmt.Sprintf("UniEngine:no pkeys column in class registered:", t.String()))
	}
	if cTableName == "" {
		cTableName = cTable.TableName
	}

	//#打印语句
	if self.runDebug {
		fmt.Println(fmt.Sprintf("UniEngine:try update table:%s", cTable))
	}

	v := reflect.Indirect(reflect.ValueOf(i))

	cIndex := 1
	cField := ""
	cWhere := ""

	cQuery := ""
	cValue := make([]interface{}, 0)
	xValue := make([]interface{}, 0)
	zValue := make([]interface{}, 0)

	if x, ok := v.Interface().(HasGetSqlUpdate); ok {
		cQuery = x.GetSqlUpdate(cTableName)
	}
	/*
		if x, ok := v.Interface().(HasGetSqlValues); ok {
			cValue = x.GetSqlValues(EtUpdate)
		}
	*/
	if x, ok := v.Interface().(HasSetSqlValues); ok {
		x.SetSqlValues(EtUpdate, &cValue)
	}

	if cQuery == "" && len(cValue) == 0 {
		for _, item := range cTable.ListField {

			if item.ReadOnly {
				continue
			}

			/*
				if _, valid := cTable.ListPkeys[strings.ToLower(item.FieldName)]; valid {
					cWhere = cWhere + " and " + fmt.Sprintf(`"`+item.FieldName+`"`) + "=" + fmt.Sprintf("%s%d", self.ColParam, cIndex)
				} else {
					cField = cField + "," + fmt.Sprintf(`"`+item.FieldName+`"`) + "=" + fmt.Sprintf("%s%d", self.ColParam, cIndex)
				}
			*/

			/*
				if _, valid := cTable.ListPkeys[strings.ToLower(item.FieldName)]; valid {
					cWhere = cWhere + " and " + self.getColParam(item.FieldName) + "=" + self.getValParam(cIndex)
					zValue = append(zValue, v.FieldByName(item.AttriName).Interface())
				} else {
					cField = cField + "," + self.getColParam(item.FieldName) + "=" + self.getValParam(cIndex)
					xValue = append(xValue, v.FieldByName(item.AttriName).Interface())
				}
			*/

			if _, valid := cTable.ListPkeys[strings.ToLower(item.FieldName)]; valid {
				continue
			}

			cField = cField + "," + self.getColParam(item.FieldName) + "=" + self.getValParam(cIndex)
			xValue = append(xValue, v.FieldByName(item.AttriName).Interface())

			cIndex = cIndex + 1
		}

		for _, item := range cTable.ListPkeys {
			cWhere = cWhere + " and " + self.getColParam(item.FieldName) + "=" + self.getValParam(cIndex)
			zValue = append(zValue, v.FieldByName(item.AttriName).Interface())
			cIndex = cIndex + 1
		}

		cField = string(cField[1:])
		cWhere = string(cWhere[4:])

		cQuery = fmt.Sprintf("update %s set %s where %s", cTableName, cField, cWhere)

		cValue = append(xValue, zValue...)
	}

	//#打印语句
	if self.runDebug {
		fmt.Println("update.sql:", cQuery)
		fmt.Println("update.val:", cValue)
	}

	eror = self.prepare(cQuery)
	if eror != nil {
		return eror
	}

	_, eror = self.st.Exec(cValue...)
	if eror != nil {
		return eror
	}

	return nil
}

func (self *TUniEngine) Insert(i interface{}, args ...interface{}) error {

	cTableName := ""
	if len(args) > 0 {
		cTableName = args[0].(string)
	}

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	cTable, Valid := self.ListTabl[t.String()]
	if !Valid {
		return errors.New(fmt.Sprintf("UniEngine:no such class registered:", t.String()))
	}
	if cTableName == "" {
		cTableName = cTable.TableName
	}

	v := reflect.Indirect(reflect.ValueOf(i))

	cIndex := 1
	cField := ""
	cParam := ""

	cQuery := ""
	cValue := make([]interface{}, 0)

	if x, ok := v.Interface().(HasGetSqlInsert); ok {
		cQuery = x.GetSqlInsert(cTableName)
	}
	/*
		if x, ok := v.Interface().(HasGetSqlValues); ok {
			cValue = x.GetSqlValues(EtInsert)
		}
	*/
	if x, ok := v.Interface().(HasSetSqlValues); ok {
		x.SetSqlValues(EtInsert, &cValue)
	}

	if cQuery == "" && len(cValue) == 0 {

		for _, cItem := range cTable.ListField {

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

		cQuery = fmt.Sprintf("insert into %s ( %s ) values ( %s ) ", cTableName, cField, cParam)
	}

	var eror error

	//#打印语句
	if self.runDebug {
		fmt.Println("insert.sql", cQuery)
		fmt.Println("insert.val", cValue)
	}

	eror = self.prepare(cQuery)
	if eror != nil {
		return eror
	}

	_, eror = self.st.Exec(cValue...)
	if eror != nil {
		return eror
	}

	return nil
}

func (self *TUniEngine) Delete(i interface{}, args ...interface{}) error {

	cTableName := ""
	if len(args) > 0 {
		cTableName = args[0].(string)
	}

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	cTable, Valid := self.ListTabl[t.String()]
	if !Valid {
		return errors.New(fmt.Sprintf("UniEngine:no such class registered:", t.String()))
	}
	if len(cTable.ListPkeys) == 0 {
		return errors.New(fmt.Sprintf("UniEngine:no pkeys column in class registered:", t.String()))
	}

	if cTableName == "" {
		cTableName = cTable.TableName
	}

	v := reflect.Indirect(reflect.ValueOf(i))

	cIndex := 1
	cWhere := ""
	cValue := make([]interface{}, 0)

	for _, cItem := range cTable.ListPkeys {

		//cWhere = cWhere + " and " + fmt.Sprintf(`"`+cItem.FieldName+`"`) + "=" + fmt.Sprintf("%s%d", self.ColParam, cIndex)
		cWhere = cWhere + " and " + self.getColParam(cItem.FieldName) + "=" + self.getValParam(cIndex)
		cIndex = cIndex + 1

		cValue = append(cValue, v.FieldByName(cItem.AttriName).Interface())
	}

	cWhere = string(cWhere[4:])

	cQuery := fmt.Sprintf("delete from %s where %s", cTableName, cWhere)

	if self.runDebug {
		fmt.Println("delete.sql:", cQuery)
		fmt.Println("delete.val:", cValue)
	}

	var eror error
	eror = self.prepare(cQuery)
	if eror != nil {
		return eror
	}

	_, eror = self.st.Exec(cValue...)
	if eror != nil {
		return eror
	}

	return nil
}

func (self *TUniEngine) Execute(sqlQuery string, args ...interface{}) error {

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
	_, eror = self.st.Exec(args...)
	if eror != nil {
		return eror
	}

	return nil
}

func (self *TUniEngine) ExecuteMust(sqlQuery string, args ...interface{}) error {

	var eror error

	sqlQuery = self.getSqlQuery(sqlQuery,args)

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
		return errors.New("UniEngine:the row count of affected is zero")
	}

	return nil
}

func (self *TUniEngine) ExistTable(aTableName string, GetSqlExistTable ...interface{}) (bool, error) {

	var eror error
	cSQL := ""

	if len(GetSqlExistTable) > 0 {

		if x, ok := GetSqlExistTable[0].(HasGetSqlExistTable); ok {
			cSQL = x.GetSqlExistTable(aTableName)
		}

	} else {

		var ExistTable4POSTGR = TExistTable4POSTGR{}
		cSQL = ExistTable4POSTGR.GetSqlExistTable(aTableName)

	}

	if cSQL == "" {
		return false, errors.New("UniEngine:no sql for existtable")
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

func (self *TUniEngine) ExistField(aTableName, aFieldName string, GetSqlExistField ...interface{}) (bool, error) {

	var eror error
	cSQL := ""

	if len(GetSqlExistField) > 0 {

		if x, ok := GetSqlExistField[0].(HasGetSqlExistField); ok {
			cSQL = x.GetSqlExistField(aTableName, aFieldName)
		}

	} else {

		var ExistField4POSTGR = TExistField4POSTGR{}
		cSQL = ExistField4POSTGR.GetSqlExistField(aTableName, aFieldName)

	}

	if cSQL == "" {
		return false, errors.New("UniEngine:no sql for existfield")
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

	if self.canClose {
		self.st, eror = self.Db.Prepare(sqlQuery)
	} else {
		self.st, eror = self.tx.Prepare(sqlQuery)
	}
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
		return errors.New("UniEngine:no transaction")
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
		return errors.New("UniEngine:no transaction")
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

	self.RegisterClass(TUniField{}, "github.com/kazarus/uniengine")

	self.canClose = true
	self.runDebug = false

	return nil
}
