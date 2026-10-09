// UniWrite
// #写入:Update/Insert/InsertL/Delete
package UniEngine

import (
	"context"
	"fmt"
	"reflect"
	"strings"
)

// ---------------------------------------------------------------------------
// 写入公共前置
// ---------------------------------------------------------------------------

// resolveTarget 统一写方法的公共前置：解析可选的表名参数(TableName[0],空切片时用
// 注册表中的默认表名)、反射取元素类型并查注册表、标识符校验。
// wantKind 指定期望的容器种类(Insert 传 Struct,InsertL/CopyInL 传 Slice,
// 无需检查传 reflect.Invalid);needPkeys 要求类已注册主键。
func (this *TUniEngine) resolveTarget(Method string, i interface{}, TableName []string, wantKind reflect.Kind, needPkeys bool) (*TUniTable, reflect.Value, string, error) {

	var TablName string
	if len(TableName) > 0 {
		TablName = TableName[0]
	}

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	//#typed nil 指针:Indirect 返回零值 Value,后续反射取值会 panic,此处显式拒绝
	if rv := reflect.ValueOf(i); rv.Kind() == reflect.Ptr && rv.IsNil() {
		return nil, reflect.Value{}, "", fmt.Errorf("UniEngine: method [%s] got a nil pointer param; check your code;", Method)
	}

	if wantKind == reflect.Slice {
		if t.Kind() != reflect.Slice {
			return nil, reflect.Value{}, "", fmt.Errorf("UniEngine: method [%s] needs a slice param; check your code;", Method)
		}
		t = t.Elem()
	} else if wantKind == reflect.Struct && t.Kind() != reflect.Struct {
		return nil, reflect.Value{}, "", fmt.Errorf("UniEngine: method [%s] only returns a struct; may be you should try [InsertL]", Method)
	}

	UniTable := this.tableByType(t.String())
	if UniTable == nil {
		return nil, reflect.Value{}, "", fmt.Errorf("%w: %s", ErrUnregisteredClass, t.String())
	}
	if needPkeys && len(UniTable.HashPkeys) == 0 {
		return nil, reflect.Value{}, "", fmt.Errorf("%w: %s", ErrNoPkeys, t.String())
	}

	if TablName == "" {
		TablName = UniTable.TableName
	}

	if !validIdent(TablName) {
		return nil, reflect.Value{}, "", fmt.Errorf("%w: %s", ErrInvalidTableName, TablName)
	}

	return UniTable, reflect.Indirect(reflect.ValueOf(i)), TablName, nil
}

// ---------------------------------------------------------------------------
// SaveIt / SaveItWhenNotExist:方言原生 UPSERT + count-then-dispatch 回退
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Update / Insert / InsertL / CopyInL / Delete
// ---------------------------------------------------------------------------

func (this *TUniEngine) Update(i interface{}, TableName ...string) error {
	return this.UpdateCtx(context.Background(), i, TableName...)
}

func (this *TUniEngine) UpdateCtx(ctx context.Context, i interface{}, TableName ...string) error {

	UniTable, v, TablName, eror := this.resolveTarget("Update", i, TableName, reflect.Invalid, true)
	if eror != nil {
		return eror
	}

	SqlQuery := ""
	SqlValue := make([]interface{}, 0)

	if x, ok := v.Interface().(HasGetSqlUpdate); ok {
		SqlQuery = x.GetSqlUpdate(this, TablName)
	}
	if x, ok := v.Interface().(HasSetSqlValues); ok {
		x.SetSqlValues(this, EtUpdate, &SqlValue)
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

			setCols = append(setCols, this.getColParam(ItemPara.FieldName)+"="+this.getValParam(ColIndex))

			if ItemPara.Encrypt {
				Value, eror := this.secretEncrypt(v.FieldByName(ItemPara.AttriName))
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
			keyCols = append(keyCols, this.getColParam(ItemPara.FieldName)+"="+this.getValParam(ColIndex))
			zValue = append(zValue, v.FieldByName(ItemPara.AttriName).Interface())
			ColIndex = ColIndex + 1
		}

		if len(setCols) == 0 {
			return fmt.Errorf("UniEngine: no updatable column in class registered: %s", UniTable.TableName)
		}
		if len(keyCols) == 0 {
			return fmt.Errorf("%w: %s", ErrNoPkeys, UniTable.TableName)
		}

		SqlQuery = fmt.Sprintf("update %s set %s where 1=1 and %s", TablName, strings.Join(setCols, ","), strings.Join(keyCols, " and "))
		SqlValue = append(xValue, zValue...)
	}

	this.debugSQL("update", SqlQuery, SqlValue)

	st, eror := this.prepareCtx(ctx, SqlQuery)
	if eror != nil {
		return eror
	}
	defer st.Close()

	if _, eror = st.ExecContext(ctx, SqlValue...); eror != nil {
		return fmt.Errorf("UniEngine: update fail: %w", eror)
	}

	return nil
}

func (this *TUniEngine) Insert(i interface{}, TableName ...string) error {
	return this.InsertCtx(context.Background(), i, TableName...)
}

func (this *TUniEngine) InsertCtx(ctx context.Context, i interface{}, TableName ...string) error {

	UniTable, v, TablName, eror := this.resolveTarget("Insert", i, TableName, reflect.Struct, false)
	if eror != nil {
		return eror
	}

	SqlQuery := ""
	SqlValue := make([]interface{}, 0)

	if x, ok := v.Interface().(HasGetSqlInsert); ok {
		SqlQuery = x.GetSqlInsert(this, TablName)
	}
	if x, ok := v.Interface().(HasSetSqlValues); ok {
		x.SetSqlValues(this, EtInsert, &SqlValue)
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

			colList = append(colList, this.getColParam(ItemPara.FieldName))
			paramList = append(paramList, this.getValParam(ColIndex))
			ColIndex = ColIndex + 1

			if UniTable.secretColumn(ItemPara) {
				Value, eror := this.secretEncrypt(v.FieldByName(ItemPara.AttriName))
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

	this.debugSQL("insert", SqlQuery, SqlValue)

	st, eror := this.prepareCtx(ctx, SqlQuery)
	if eror != nil {
		return eror
	}
	defer st.Close()

	if _, eror = st.ExecContext(ctx, SqlValue...); eror != nil {
		return fmt.Errorf("UniEngine: insert fail: %w", eror)
	}

	return nil
}

func (this *TUniEngine) InsertL(i interface{}, TableName ...string) error {
	return this.InsertLCtx(context.Background(), i, TableName...)
}

func (this *TUniEngine) InsertLCtx(ctx context.Context, i interface{}, TableName ...string) error {

	UniTable, v, TablName, eror := this.resolveTarget("InsertL", i, TableName, reflect.Slice, false)
	if eror != nil {
		return eror
	}

	if v.Len() == 0 {
		return nil
	}

	SqlQuery := ""
	SqlValue := make([]interface{}, 0)

	if x, ok := v.Index(0).Interface().(HasGetSqlInsertL); ok {
		SqlQuery = x.GetSqlInsertL(this, TablName, int64(v.Len()))
	}

	if x, ok := v.Index(0).Interface().(HasSetSqlValuesL); ok {
		for m := 0; m < v.Len(); m++ {
			f := v.Index(m)
			x.SetSqlValuesL(this, EtInsert, f, &SqlValue)
		}
	}

	if SqlQuery == "" && len(SqlValue) == 0 {

		fields := UniTable.writableFields()
		if len(fields) == 0 {
			return fmt.Errorf("UniEngine: no insertable column in class registered: %s", UniTable.TableName)
		}

		var colList []string
		for _, ItemPara := range fields {
			colList = append(colList, this.getColParam(ItemPara.FieldName))
		}

		var rowList []string
		ColIndex := 1
		for m := 0; m < v.Len(); m++ {

			f := v.Index(m)

			var paramList []string
			for _, ItemPara := range fields {

				paramList = append(paramList, this.getValParam(ColIndex))
				ColIndex = ColIndex + 1

				if UniTable.secretColumn(ItemPara) {
					Value, eror := this.secretEncrypt(f.FieldByName(ItemPara.AttriName))
					if eror != nil {
						return eror
					}
					SqlValue = append(SqlValue, Value)
					continue
				}
				SqlValue = append(SqlValue, f.FieldByName(ItemPara.AttriName).Interface())
			}

			if this.dbFamily() == FmORACLE {
				//#Oracle:INSERT ALL 多行语法
				rowList = append(rowList, fmt.Sprintf("into %s ( %s ) values ( %s )", TablName, strings.Join(colList, ","), strings.Join(paramList, ",")))
			} else {
				rowList = append(rowList, fmt.Sprintf("( %s )", strings.Join(paramList, ",")))
			}
		}

		if this.dbFamily() == FmORACLE {
			SqlQuery = fmt.Sprintf("insert all %s select 1 from dual", strings.Join(rowList, " "))
		} else {
			SqlQuery = fmt.Sprintf("insert into %s ( %s ) values %s", TablName, strings.Join(colList, ","), strings.Join(rowList, ","))
		}
	}

	this.debugSQL("insert", SqlQuery, SqlValue)

	st, eror := this.prepareCtx(ctx, SqlQuery)
	if eror != nil {
		return eror
	}
	defer st.Close()

	if _, eror = st.ExecContext(ctx, SqlValue...); eror != nil {
		return fmt.Errorf("UniEngine: insert fail: %w (hint: too many parameters? try [InsertP])", eror)
	}

	return nil
}

func (this *TUniEngine) Delete(i interface{}, TableName ...string) error {
	return this.DeleteCtx(context.Background(), i, TableName...)
}

func (this *TUniEngine) DeleteCtx(ctx context.Context, i interface{}, TableName ...string) error {

	UniTable, v, TablName, eror := this.resolveTarget("Delete", i, TableName, reflect.Invalid, true)
	if eror != nil {
		return eror
	}

	var keyCols []string
	SqlValue := make([]interface{}, 0)

	ColIndex := 1
	for _, ItemPara := range UniTable.pkeyFields() {
		keyCols = append(keyCols, this.getColParam(ItemPara.FieldName)+"="+this.getValParam(ColIndex))
		SqlValue = append(SqlValue, v.FieldByName(ItemPara.AttriName).Interface())
		ColIndex = ColIndex + 1
	}

	if len(keyCols) == 0 {
		return fmt.Errorf("%w: %s", ErrNoPkeys, UniTable.TableName)
	}

	SqlQuery := fmt.Sprintf("delete from %s where %s", TablName, strings.Join(keyCols, " and "))

	this.debugSQL("delete", SqlQuery, SqlValue)

	st, eror := this.prepareCtx(ctx, SqlQuery)
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
