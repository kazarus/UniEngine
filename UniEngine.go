// UniEngine
package UniEngine

import "fmt"
import "errors"
import "reflect"
import "database/sql"

type TUniEngine struct {
	DB *sql.DB
	tx *sql.Tx
	st *sql.Stmt

	ListTabl map[string]TUniTable
	ColLabel string

	//#sqlQuery string
	canClose bool //default is true;if is transaction,canClose = false
}

type HasMapIndex interface {
	GetMapIndex() string
}

//type GetMapIndex() func{}
type MapHandler func(u interface{}) string

func (self *TUniEngine) RegisterClass(aClass interface{}, aTableName string) *TUniTable {

	//pass:when &tuser{}
	/*
		s := reflect.ValueOf(aClass).Elem()
		typeOfT := s.Type()
		for i := 0; i < s.NumField(); i++ {
			f := s.Field(i)
			fmt.Printf("%d %s %s = %v\n", i, typeOfT.Field(i).Name, f.Type(), f.Interface())
		}
	*/

	if self.ListTabl == nil {
		self.ListTabl = make(map[string]TUniTable, 0)
	}

	t := reflect.TypeOf(aClass)
	//#fmt.Println("aClass:", t)
	//#fmt.Println("aClass.Name", t.String())
	n := t.NumField()

	var cTable = TUniTable{}
	cTable.ListField = make(map[string]TUniField, 0)
	cTable.ListPkeys = make(map[string]TUniField, 0)
	cTable.TableName = aTableName

	for i := 0; i < n; i++ {
		f := t.Field(i)
		//#fmt.Println(f)

		var cField = TUniField{}
		cField.AttriName = f.Name
		cField.FieldType = f.Type

		cField.initialize(f.Tag.Get(self.ColLabel))

		cTable.ListField[cField.FieldName] = cField
	}
	//#fmt.Println(cTable)

	self.ListTabl[t.String()] = cTable
	//#fmt.Println(self.ListTabl)

	return &cTable
}

//return slice;
func (self *TUniEngine) Select(i interface{}, query string, args ...interface{}) error {

	rows, eror := self.DB.Query(query, args...)
	if eror != nil {
		return eror
	}
	defer rows.Close()

	t := reflect.TypeOf(i)

	var isList bool
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() == reflect.Slice {
		t = t.Elem()
		isList = true
	} else if t.Kind() == reflect.Map {
		t = t.Elem()
	}

	cName := t.String()
	cTable := self.ListTabl[cName]

	cols, _ := rows.Columns()

	dest := make([]interface{}, len(cols))

	var sValue = reflect.Indirect(reflect.ValueOf(i))

	for rows.Next() {

		if isList {

			u := reflect.New(t)
			for indx, item := range cols {
				cField := cTable.ListField[item]
				dest[indx] = u.Elem().FieldByName(cField.AttriName).Addr().Interface()
			}

			eror = rows.Scan(dest...)
			if eror != nil {
				return eror
			}

			sValue.Set(reflect.Append(sValue, u.Elem()))

		} else {

			for indx, item := range cols {
				cField := cTable.ListField[item]
				dest[indx] = sValue.FieldByName(cField.AttriName).Addr().Interface()
			}

			eror = rows.Scan(dest...)
			if eror != nil {
				return eror
			}
		}

	}
	return nil
}

//return map;use HasMapIndex;
func (self *TUniEngine) SelectM(i interface{}, query string, args ...interface{}) error {

	rows, eror := self.DB.Query(query, args...)
	if eror != nil {
		return eror
	}
	defer rows.Close()

	t := reflect.TypeOf(i)

	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() == reflect.Slice {
		t = t.Elem()
	} else if t.Kind() == reflect.Map {
		t = t.Elem()
	}

	cName := t.String()

	cTable := self.ListTabl[cName]

	cols, _ := rows.Columns()

	dest := make([]interface{}, len(cols))

	var sValue = reflect.Indirect(reflect.ValueOf(i))

	for rows.Next() {

		u := reflect.New(t)

		for indx, item := range cols {
			cField := cTable.ListField[item]
			dest[indx] = u.Elem().FieldByName(cField.AttriName).Addr().Interface()
		}

		eror = rows.Scan(dest...)
		if eror != nil {
			return eror
		}

		var mapIndex string
		if x, ok := u.Interface().(HasMapIndex); ok {
			mapIndex = x.GetMapIndex()
		}
		sValue.SetMapIndex(reflect.ValueOf(mapIndex), u.Elem())
	}

	return nil
}

//return map;use custom function;
func (self *TUniEngine) SelectF(i interface{}, f MapHandler, query string, args ...interface{}) error {

	rows, eror := self.DB.Query(query, args...)
	if eror != nil {
		return eror
	}
	defer rows.Close()

	//#fmt.Println(rows)

	t := reflect.TypeOf(i)
	//#fmt.Println("t.kjnd", t.Kind())

	if t.Kind() == reflect.Ptr {
		t = t.Elem()
		//#fmt.Println("true")
	}
	if t.Kind() == reflect.Slice {
		t = t.Elem()
	} else if t.Kind() == reflect.Map {
		t = t.Elem()
	}
	//#fmt.Println("t.elem", t, "-", t.Name(), "-", t.Kind())

	cName := t.String()

	//#fmt.Println(t.Kind())

	cTable := self.ListTabl[cName]
	//#fmt.Println(cTable)

	cols, _ := rows.Columns()

	dest := make([]interface{}, len(cols))

	var sValue = reflect.Indirect(reflect.ValueOf(i))

	//#fmt.Println("s.value.type", sValue.Type())
	//#fmt.Println("s.value.kjnd", sValue.Kind())

	for rows.Next() {

		u := reflect.New(t)
		//#fmt.Println("kz", u)

		for indx, item := range cols {
			//#dest[indx] = &userindx
			//#fmt.Println(indx, item)
			cField := cTable.ListField[item]
			//#fmt.Println(cField.AttriName)
			dest[indx] = u.Elem().FieldByName(cField.AttriName).Addr().Interface()
		}

		eror = rows.Scan(dest...)
		if eror != nil {
			//#fmt.Println(eror)
		}

		//#fmt.Println("u.value", u)

		var mapIndex string
		if f != nil {
			mapIndex = f(u.Elem().Interface())
		}
		sValue.SetMapIndex(reflect.ValueOf(mapIndex), u.Elem())

	}

	return nil
}

func (self *TUniEngine) Update(i interface{}, args ...interface{}) error {
	cTableName := ""
	if len(args) > 0 {
		cTableName = args[0].(string)
	}

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	cTable := self.ListTabl[t.String()]
	if cTableName == "" {
		cTableName = cTable.TableName
	}

	v := reflect.Indirect(reflect.ValueOf(i))

	cIndex := 1
	cField := ""
	cWhere := ""
	cValue := make([]interface{}, 0)

	for _, item := range cTable.ListField {
		//#fmt.Println("kazarus:item.fieldname", item.FieldName)

		if item.ReadOnly {
			continue
		}

		if _, valid := cTable.ListPkeys[item.FieldName]; valid {
			cWhere = cWhere + " and " + fmt.Sprintf(`"`+item.FieldName+`"`) + "=" + fmt.Sprintf("$%d", cIndex)
		} else {
			cField = cField + "," + fmt.Sprintf(`"`+item.FieldName+`"`) + "=" + fmt.Sprintf("$%d", cIndex)
		}

		cIndex = cIndex + 1

		cValue = append(cValue, v.FieldByName(item.AttriName).Interface())
	}

	cField = string(cField[1:])
	cWhere = string(cWhere[4:])
	//#fmt.Println(cField)
	//#fmt.Println(cWhere)

	cSQL := fmt.Sprintf("update %s set %s where %s", cTableName, cField, cWhere)
	//#fmt.Println(cSQL)
	var eror error
	eror = self.prepare(cSQL)
	if eror != nil {
		return eror
	}

	//@ _, eror := self.DB.Exec(cSQL,cValue...)
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
	cTable := self.ListTabl[t.String()]
	if cTableName == "" {
		cTableName = cTable.TableName
	}
	//#fmt.Println(cTable)
	//#fmt.Println("insert.tablename:", cTableName)

	v := reflect.Indirect(reflect.ValueOf(i))

	//#fmt.Println("t", t)
	//#fmt.Println("v", v)
	//#fmt.Println("v.kjnd", v.Kind())

	cIndex := 1
	cField := ""
	cParam := ""
	cValue := make([]interface{}, 0)

	for _, item := range cTable.ListField {
		//#fmt.Println(indx, item.FieldName)

		if item.ReadOnly {
			continue
		}

		cField = cField + "," + fmt.Sprintf(`"`+item.FieldName+`"`)
		cParam = cParam + "," + fmt.Sprintf("$%d", cIndex)
		cIndex = cIndex + 1
		//#fmt.Println(t.FieldByName(item.AttriName))
		////#fmt.Println(t.FieldByName(item.AttriName).Interface())
		cValue = append(cValue, v.FieldByName(item.AttriName).Interface())
	}

	//#fmt.Println("cValue:", cValue)

	cField = string(cField[1:])
	cParam = string(cParam[1:])
	//#fmt.Println("cField:", string(cField[1:]))
	//#fmt.Println("cParam:", string(cParam[1:]))

	cSQL := fmt.Sprintf("insert into %s ( %s ) values ( %s ) ", cTableName, cField, cParam)
	fmt.Println(cSQL)
	var eror error

	eror = self.prepare(cSQL)
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
	cTable := self.ListTabl[t.String()]
	if cTableName == "" {
		cTableName = cTable.TableName
	}

	v := reflect.Indirect(reflect.ValueOf(i))

	cIndex := 1
	cWhere := ""
	cValue := make([]interface{}, 0)

	for _, item := range cTable.ListPkeys {
		//#fmt.Println("kazarus:item.fieldname", item.FieldName)

		cWhere = cWhere + " and " + fmt.Sprintf(`"`+item.FieldName+`"`) + "=" + fmt.Sprintf("$%d", cIndex)
		cIndex = cIndex + 1

		cValue = append(cValue, v.FieldByName(item.AttriName).Interface())
	}

	//#fmt.Println("cValue:", cValue)
	cWhere = string(cWhere[4:])
	//#fmt.Println(cWhere)

	cSQL := fmt.Sprintf("delete from %s where %s", cTableName, cWhere)
	//#fmt.Println(cSQL)
	//@ _, eror := self.DB.Exec(cSQL, cValue...)
	var eror error
	eror = self.prepare(cSQL)
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
		self.st, eror = self.DB.Prepare(sqlQuery)
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

func (self *TUniEngine) prepare(sqlQuery string) error {
	var eror error

	if self.canClose {
		self.st, eror = self.DB.Prepare(sqlQuery)
	} else {
		self.st, eror = self.tx.Prepare(sqlQuery)
	}
	return eror
}

func (self *TUniEngine) Begin() error {
	var eror error
	self.tx, eror = self.DB.Begin()
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
	self.canClose = true
	return nil
}

//i just do not like this function name
/*
func (self *TUniEngine) Rollback() error {
	return self.Cancel()
}
*/
