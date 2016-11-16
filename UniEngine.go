package UniEngine

import "fmt"
import "errors"
import "reflect"
import "database/sql"

type TExeccuteType int

const (
	EtSelect TExeccuteType = 1 + iota //0
	EtInsert                          //1
	EtUpdate                          //2
	EtDelele                          //3
)

type TUniEngine struct {
	Db *sql.DB
	tx *sql.Tx
	st *sql.Stmt

	ListTabl map[string]TUniTable
	ColLabel string

	canClose bool //default is true;if is transaction,canClose = false
}

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

//return struct;
func (self *TUniEngine) Select(i interface{}, query string, args ...interface{}) error {

	var eror error
	fmt.Println("i:", i)

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Struct {
		fmt.Println("no struct")
		//TO DO:
		return errors.New("UniEngine:method [select] only retun a struct; may be you should try [SelectL]")
	}

	cName := t.String()
	cTable, Valid := self.ListTabl[cName]
	if !Valid {
		return errors.New(fmt.Sprintf("UniEngine:no such class registered:", cName))
	}
	fmt.Println(cTable)

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

	column, _ := rows.Columns()
	cCount := len(column)
	fields := make([]interface{}, cCount)
	values := make([]interface{}, cCount)
	fmt.Println("column", column)
	fmt.Println("fields", fields)
	fmt.Println("values", values)

	var Result = reflect.Indirect(reflect.ValueOf(i))
	fmt.Println("result", &Result)
	if x, ok := Result.Interface().(HasSetSqlResult); ok {
		fmt.Println("HasSetSqlResult")
		fmt.Println(x)

		for rows.Next() {
			for i := 0; i < cCount; i++ {
				values[i] = &fields[i]
			}
			eror = rows.Scan(values...)
			if eror != nil {
				return eror
			}
			x.SetSqlResult(i, fields, column)
			//v := x.GetSqlResult(fields, column)
			//#x.SetSqlResult(), fields, column)
			//			fmt.Println("v:", v)
			//		Result = reflect.ValueOf(&v)
		}
	} else {
		for rows.Next() {
			for indx, item := range column {
				cField, Valid := cTable.ListField[item]
				if !Valid {
					return errors.New(fmt.Sprintf("UniEngine:database have field[%s],but not in class[%s]", item, t.String()))
				}
				values[indx] = Result.FieldByName(cField.AttriName).Addr().Interface()
			}

			eror = rows.Scan(values...)
			if eror != nil {
				return eror
			}
		}
	}
	return nil

	//var sValue = reflect.Indirect(reflect.ValueOf(i))
	//u := reflect.New(t)

	/*
		for rows.Next() {
			if isList {
				fmt.Println("kazarus:u.type", u.Type())
				if x, ok := u.Interface().(HasGetSqlResult); ok {
					fmt.Println("HasGetSqlResult")
					for i := 0; i < len(cols); i++ {
						values[i] = &fields[i]
					}
					fmt.Println(values)
					fmt.Println(fields)
					eror = rows.Scan(values...)
					if eror != nil {
						return eror
					}
					fmt.Println(values)
					fmt.Println(fields)
					fmt.Println(x)
					v := x.GetSqlResult(fields, cols)
					fmt.Println(v)
					//@x.SetSqlResult(u.Elem().Interface(), fields, cols)

					sValue.Set(reflect.Append(sValue, reflect.ValueOf(v)))
					//@sValue.Set(reflect.Append(sValue, u.Elem()))
				} else {
					for indx, item := range cols {
						cField, Valid := cTable.ListField[item]
						if !Valid {
							return errors.New(fmt.Sprintf("UniEngine:database have field[%s],but not in class[%s]", item, t.String()))
						}
						values[indx] = u.Elem().FieldByName(cField.AttriName).Addr().Interface()
					}

					eror = rows.Scan(values...)
					if eror != nil {
						return eror
					}

					sValue.Set(reflect.Append(sValue, u.Elem()))
				}
			} else {
				if x, ok := u.Interface().(HasGetSqlResult); ok {
					fmt.Println("HasGetSqlResult")
					for i := 0; i < len(cols); i++ {
						values[i] = &fields[i]
					}
					fmt.Println(values)
					fmt.Println(fields)
					eror = rows.Scan(values...)
					if eror != nil {
						return eror
					}
					fmt.Println(values)
					fmt.Println(fields)
					fmt.Println(x)
					v := x.GetSqlResult(fields, cols)
					fmt.Println(v)
					sValue = reflect.ValueOf(v)
				} else {
					for indx, item := range cols {
						cField, Valid := cTable.ListField[item]
						if !Valid {
							return errors.New(fmt.Sprintf("UniEngine:database have field[%s],but not in class[%s]", item, t.String()))
						}
						values[indx] = sValue.FieldByName(cField.AttriName).Addr().Interface()
					}

					eror = rows.Scan(values...)
					if eror != nil {
						return eror
					}
				}
			}
		}
	*/

	/*	cols, _ := rows.Columns()

		dest := make([]interface{}, len(cols))

		var sValue = reflect.Indirect(reflect.ValueOf(i))

		for rows.Next() {

			if isList {
				u := reflect.New(t)
				for indx, item := range cols {
					cField, Valid := cTable.ListField[item]
					if !Valid {
						return errors.New(fmt.Sprintf("UniEngine:database have field[%s],but not in class[%s]", item, t.String()))
					}
					dest[indx] = u.Elem().FieldByName(cField.AttriName).Addr().Interface()
				}

				eror = rows.Scan(dest...)
				if eror != nil {
					return eror
				}

				sValue.Set(reflect.Append(sValue, u.Elem()))

			} else {

				for indx, item := range cols {
					cField, Valid := cTable.ListField[item]
					if !Valid {
						return errors.New(fmt.Sprintf("UniEngine:database have field[%s],but not in class[%s]", item, t.String()))
					}
					dest[indx] = sValue.FieldByName(cField.AttriName).Addr().Interface()
				}

				eror = rows.Scan(dest...)
				if eror != nil {
					return eror
				}
			}

		}
		return nil
	*/

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

func (self *TUniEngine) SelectL(i interface{}, query string, args ...interface{}) error {
	return nil
}

//return map;use HasMapIndex;
func (self *TUniEngine) SelectM(i interface{}, query string, args ...interface{}) error {

	var eror error

	eror = self.prepare(query)
	if eror != nil {
		return eror
	}

	rows, eror := self.st.Query(args...)
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

	cTable, Valid := self.ListTabl[cName]
	if !Valid {
		return errors.New(fmt.Sprintf("UniEngine:no such class registered:", cName))
	}

	cols, _ := rows.Columns()

	dest := make([]interface{}, len(cols))

	var sValue = reflect.Indirect(reflect.ValueOf(i))

	for rows.Next() {

		u := reflect.New(t)

		for indx, item := range cols {
			cField, Valid := cTable.ListField[item]
			if !Valid {
				return errors.New(fmt.Sprintf("UniEngine:database have field[%s],but not in class[%s]", item, t.String()))
			}
			dest[indx] = u.Elem().FieldByName(cField.AttriName).Addr().Interface()
		}

		eror = rows.Scan(dest...)
		if eror != nil {
			return eror
		}

		var MapUnique string
		if x, ok := u.Interface().(HasGetMapUnique); ok {
			MapUnique = x.GetMapUnique()
		}
		sValue.SetMapIndex(reflect.ValueOf(MapUnique), u.Elem())
	}

	return nil
}

//return map;use custom function;
func (self *TUniEngine) SelectH(i interface{}, f GetMapUnique, query string, args ...interface{}) error {

	var eror error

	eror = self.prepare(query)
	if eror != nil {
		return eror
	}

	rows, eror := self.st.Query(args...)
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

	cTable, Valid := self.ListTabl[cName]
	if !Valid {
		return errors.New(fmt.Sprintf("UniEngine:no such class registered:", cName))
	}

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
			cField, Valid := cTable.ListField[item]
			if !Valid {
				return errors.New(fmt.Sprintf("UniEngine:database have field[%s],but not in class[%s]", item, t.String()))
			}
			dest[indx] = u.Elem().FieldByName(cField.AttriName).Addr().Interface()
		}

		eror = rows.Scan(dest...)
		if eror != nil {
			//#fmt.Println(eror)
		}

		//#fmt.Println("u.value", u)

		var MapUnique string
		if f != nil {
			MapUnique = f(u.Elem().Interface())
		}
		sValue.SetMapIndex(reflect.ValueOf(MapUnique), u.Elem())

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

		cQuery = fmt.Sprintf("update %s set %s where %s", cTableName, cField, cWhere)
	}
	fmt.Println("cQuery:", cQuery)
	fmt.Println("cValue:", cValue)

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
		fmt.Println("no")
		for _, cItem := range cTable.ListField {

			if cItem.ReadOnly {
				continue
			}

			cField = cField + "," + fmt.Sprintf(`"`+cItem.FieldName+`"`)
			cParam = cParam + "," + fmt.Sprintf("$%d", cIndex)
			cIndex = cIndex + 1

			cValue = append(cValue, v.FieldByName(cItem.AttriName).Interface())
		}

		cField = string(cField[1:])
		cParam = string(cParam[1:])

		cQuery = fmt.Sprintf("insert into %s ( %s ) values ( %s ) ", cTableName, cField, cParam)
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

	for _, item := range cTable.ListPkeys {
		//#fmt.Println("kazarus:item.fieldname", item.FieldName)

		cWhere = cWhere + " and " + fmt.Sprintf(`"`+item.FieldName+`"`) + "=" + fmt.Sprintf("$%d", cIndex)
		cIndex = cIndex + 1

		cValue = append(cValue, v.FieldByName(item.AttriName).Interface())
	}

	//#fmt.Println("cValue:", cValue)
	cWhere = string(cWhere[4:])
	//#fmt.Println(cWhere)

	cQuery := fmt.Sprintf("delete from %s where %s", cTableName, cWhere)
	//#fmt.Println(cSQL)
	//@ _, eror := self.DB.Exec(cSQL, cValue...)
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
	self.canClose = true
	return nil
}
