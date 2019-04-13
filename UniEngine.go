package UniEngine

import "fmt"
import "errors"
import "strings"
import "reflect"
import "database/sql"

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
	canClose bool //default is true;if is transaction,canClose = false
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
func (self *TUniEngine) SelectD(query string, args ...interface{}) (int64, error) {

	var eror error

	var size sql.NullInt64

	eror = self.prepare(query)
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
func (self *TUniEngine) SelectF(query string, args ...interface{}) (float64, error) {

	var eror error
	var size sql.NullFloat64

	eror = self.prepare(query)
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
func (self *TUniEngine) SelectS(query string, args ...interface{}) (string, error) {

	var eror error

	var text sql.NullString

	eror = self.prepare(query)
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
func (self *TUniEngine) Select(i interface{}, query string, args ...interface{}) error {

	var eror error

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Struct {
		//TO DO:
		return errors.New("UniEngine:method [select] only retun a struct; may be you should try [SelectL]")
	}

	cName := t.String()
	cTable, Valid := self.ListTabl[cName]
	if !Valid {
		return errors.New(fmt.Sprintf("UniEngine:no such class registered:", cName))
	}

	//-<
	eror = self.prepare(query)
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
				cField, Valid := cTable.ListField[cItem]
				if !Valid {
					return errors.New(fmt.Sprintf("UniEngine:database have field[%s],but not in class[%s]", cItem, t.String()))
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
func (self *TUniEngine) SelectL(i interface{}, query string, args ...interface{}) error {

	var eror error

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Slice {
		//TO DO:
		return errors.New("UniEngine:method [select] only retun a struct; may be you should try [SelectL]")
	}

	if t.Kind() == reflect.Slice {
		t = t.Elem()
	}

	cName := t.String()
	cTable, Valid := self.ListTabl[cName]
	if !Valid {
		return errors.New(fmt.Sprintf("UniEngine:no such class registered:", cName))
	}

	//-<
	eror = self.prepare(query)
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
					return errors.New(fmt.Sprintf("UniEngine:database have field[%s],but not in class[%s]", cItem, t.String()))
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
func (self *TUniEngine) SelectM(i interface{}, query string, args ...interface{}) error {

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

	cName := t.String()
	cTable, Valid := self.ListTabl[cName]
	if !Valid {
		return errors.New(fmt.Sprintf("UniEngine:no such class registered:", cName))
	}

	if !t.Implements(THasGetMapUnique) {
		return errors.New(fmt.Sprintf("UniEngine:the class registered:[%s] does not Implemented [HasGetMapUnique] ", cName))
	}

	//-<
	eror = self.prepare(query)
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
				cField, Valid := cTable.ListField[cItem]
				if !Valid {
					return errors.New(fmt.Sprintf("UniEngine:database have field[%s],but not in class[%s]", cItem, t.String()))
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
func (self *TUniEngine) SelectH(i interface{}, f GetMapUnique, query string, args ...interface{}) error {

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

	cName := t.String()
	cTable, Valid := self.ListTabl[cName]
	if !Valid {
		return errors.New(fmt.Sprintf("UniEngine:no such class registered:", cName))
	}

	//-<
	eror = self.prepare(query)
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
				cField, Valid := cTable.ListField[cItem]
				if !Valid {
					return errors.New(fmt.Sprintf("UniEngine:database have field[%s],but not in class[%s]", cItem, t.String()))
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
	//#cField := ""
	cWhere := ""

	cQuery := ""
	cValue := make([]interface{}, 0)

	if x, ok := v.Interface().(HasGetSqlUpdate); ok {
		cQuery = x.GetSqlUpdate()
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

			if _, valid := cTable.ListPkeys[item.FieldName]; valid {
				cWhere = cWhere + " and " + fmt.Sprintf(`"`+item.FieldName+`"`) + "=" + fmt.Sprintf("$%d", cIndex)
				cValue = append(cValue, v.FieldByName(item.AttriName).Interface())
				cIndex = cIndex + 1
			}
		}

		//#cField = string(cField[1:])
		cWhere = string(cWhere[4:])

		cQuery = fmt.Sprintf("select count(1) from %s where %s", cTableName, cWhere)
	}

	cCount, eror := self.SelectD(cQuery, cValue...)
	if eror != nil {
		return eror
	}
	if cCount == 1 {
		self.Update(i, args...)
	} else {
		self.Insert(i, args...)
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

	fmt.Println(cTable)

	v := reflect.Indirect(reflect.ValueOf(i))

	cIndex := 1
	cField := ""
	cWhere := ""

	cQuery := ""
	cValue := make([]interface{}, 0)

	if x, ok := v.Interface().(HasGetSqlUpdate); ok {
		cQuery = x.GetSqlUpdate()
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

			if _, valid := cTable.ListPkeys[strings.ToLower(item.FieldName)]; valid {
				fmt.Println("here")
				cWhere = cWhere + " and " + fmt.Sprintf(`"`+item.FieldName+`"`) + "=" + fmt.Sprintf("%s%d", self.ColParam, cIndex)
			} else {
				fmt.Println("here")
				cField = cField + "," + fmt.Sprintf(`"`+item.FieldName+`"`) + "=" + fmt.Sprintf("%s%d", self.ColParam, cIndex)
			}

			cIndex = cIndex + 1

			cValue = append(cValue, v.FieldByName(item.AttriName).Interface())
		}

		fmt.Println(cField)
		fmt.Println(cWhere)
		cField = string(cField[1:])
		cWhere = string(cWhere[4:])

		cQuery = fmt.Sprintf("update %s set %s where %s", cTableName, cField, cWhere)
	}
	fmt.Println(cQuery)
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
		cQuery = x.GetSqlInsert()
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

			cField = cField + "," + fmt.Sprintf(`"`+cItem.FieldName+`"`)
			cParam = cParam + "," + fmt.Sprintf("%s%d", self.ColParam, cIndex)
			cIndex = cIndex + 1

			cValue = append(cValue, v.FieldByName(cItem.AttriName).Interface())
		}

		cField = string(cField[1:])
		cParam = string(cParam[1:])

		cQuery = fmt.Sprintf("insert into %s ( %s ) values ( %s ) ", cTableName, cField, cParam)
	}

	var eror error

	fmt.Println(cQuery)
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

		cWhere = cWhere + " and " + fmt.Sprintf(`"`+cItem.FieldName+`"`) + "=" + fmt.Sprintf("%s%d", self.ColParam, cIndex)
		cIndex = cIndex + 1

		cValue = append(cValue, v.FieldByName(cItem.AttriName).Interface())
	}

	fmt.Println(cWhere)
	cWhere = string(cWhere[4:])

	cQuery := fmt.Sprintf("delete from %s where %s", cTableName, cWhere)
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

func (self *TUniEngine) ExistTable(cTableName string) (bool, error) {

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

func (self *TUniEngine) ExistField(cTableName, aFieldName string) (bool, error) {

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
		return errors.New("uniegine:no transaction")
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
		return errors.New("uniegine:no transaction")
	}

	eror = self.tx.Commit()
	if eror != nil {
		return eror
	}

	self.canClose = true
	return nil
}

func (self *TUniEngine) Print() error {
	fmt.Println("canclose:", self.canClose)
	return nil
}

func (self *TUniEngine) Initialize() error {

	self.RegisterClass(TUniField{}, "github.com/kazarus/uniengine")

	self.canClose = true
	return nil
}
