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
// WantKind 指定期望的容器种类(Insert 传 Struct,InsertL/CopyInL 传 Slice,
// 无需检查传 reflect.Invalid);NeedPkeys 要求类已注册主键。
func (this *TUniEngine) resolveTarget(Method string, i interface{}, TableName []string, WantKind reflect.Kind, NeedPkeys bool) (*TUniTable, reflect.Value, string, error) {

	var TablName string
	if len(TableName) > 0 {
		TablName = TableName[0]
	}

	//#nil 校验必须先于 reflect.TypeOf/Kind:裸 nil 时 TypeOf 返回 nil Type,取 Kind 直接 panic
	if eror := nilParamOf(Method, i); eror != nil {
		return nil, reflect.Value{}, "", eror
	}

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if WantKind == reflect.Slice {
		if t.Kind() != reflect.Slice {
			return nil, reflect.Value{}, "", fmt.Errorf("UniEngine: method [%s] needs a slice param; check your code;", Method)
		}
		t = t.Elem()
	} else if WantKind == reflect.Struct && t.Kind() != reflect.Struct {
		return nil, reflect.Value{}, "", fmt.Errorf("UniEngine: method [%s] only returns a struct; may be you should try [InsertL]", Method)
	}

	UniTable := this.tableByType(t.String())
	if UniTable == nil {
		return nil, reflect.Value{}, "", fmt.Errorf("%w: %s", ErrUnregisteredClass, t.String())
	}
	if NeedPkeys && len(UniTable.HashPkeys) == 0 {
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

func (this *TUniEngine) UpdateCtx(Ctx context.Context, i interface{}, TableName ...string) error {

	UniTable, v, TablName, eror := this.resolveTarget("Update", i, TableName, reflect.Invalid, true)
	if eror != nil {
		return eror
	}

	//#完整性:组装 SQL 前按结构体当前值重算 MAC 回填伴随字段
	if eror := this.verifySignRow(v, UniTable); eror != nil {
		return eror
	}

	SqlQuery := ""
	SqlValue := make([]interface{}, 0)

	if x, Ok := v.Interface().(HasGetSqlUpdate); Ok {
		SqlQuery = x.GetSqlUpdate(this, TablName)
	}
	if x, Ok := v.Interface().(HasSetSqlValues); Ok {
		x.SetSqlValues(this, EtUpdate, &SqlValue)
	}

	if SqlQuery == "" && len(SqlValue) == 0 {

		var SetCols []string
		var KeyCols []string
		xValue := make([]interface{}, 0)
		zValue := make([]interface{}, 0)

		ColIndex := 1
		for _, ItemPara := range UniTable.writableFields() {

			if _, Valid := UniTable.HashPkeys[strings.ToLower(ItemPara.FieldName)]; Valid {
				continue
			}

			SetCols = append(SetCols, this.getColParam(ItemPara.FieldName)+"="+this.getValParam(ColIndex))

			if ItemPara.SecretOn {
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
			KeyCols = append(KeyCols, this.getColParam(ItemPara.FieldName)+"="+this.getValParam(ColIndex))
			zValue = append(zValue, v.FieldByName(ItemPara.AttriName).Interface())
			ColIndex = ColIndex + 1
		}

		if len(SetCols) == 0 {
			return fmt.Errorf("UniEngine: no updatable column in class registered: %s", UniTable.TableName)
		}
		if len(KeyCols) == 0 {
			return fmt.Errorf("%w: %s", ErrNoPkeys, UniTable.TableName)
		}

		SqlQuery = fmt.Sprintf("update %s set %s where 1=1 and %s", TablName, strings.Join(SetCols, ","), strings.Join(KeyCols, " and "))
		SqlValue = append(xValue, zValue...)
	}

	this.sqlDebug("update", SqlQuery, SqlValue)

	St, eror := this.prepareCtx(Ctx, SqlQuery)
	if eror != nil {
		return eror
	}
	defer St.Close()

	if _, eror = St.ExecContext(Ctx, SqlValue...); eror != nil {
		return fmt.Errorf("UniEngine: update fail: %w", eror)
	}

	return nil
}

func (this *TUniEngine) Insert(i interface{}, TableName ...string) error {
	return this.InsertCtx(context.Background(), i, TableName...)
}

func (this *TUniEngine) InsertCtx(Ctx context.Context, i interface{}, TableName ...string) error {

	UniTable, v, TablName, eror := this.resolveTarget("Insert", i, TableName, reflect.Struct, false)
	if eror != nil {
		return eror
	}

	//#完整性:组装 SQL 前按结构体当前值重算 MAC 回填伴随字段
	if eror := this.verifySignRow(v, UniTable); eror != nil {
		return eror
	}

	SqlQuery := ""
	SqlValue := make([]interface{}, 0)

	if x, Ok := v.Interface().(HasGetSqlInsert); Ok {
		SqlQuery = x.GetSqlInsert(this, TablName)
	}
	if x, Ok := v.Interface().(HasSetSqlValues); Ok {
		x.SetSqlValues(this, EtInsert, &SqlValue)
	}

	if SqlQuery == "" && len(SqlValue) == 0 {

		Fields := UniTable.writableFields()
		if len(Fields) == 0 {
			return fmt.Errorf("UniEngine: no insertable column in class registered: %s", UniTable.TableName)
		}

		var ColList []string
		var ValList []string
		ColIndex := 1
		for _, ItemPara := range Fields {

			ColList = append(ColList, this.getColParam(ItemPara.FieldName))
			ValList = append(ValList, this.getValParam(ColIndex))
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

		SqlQuery = fmt.Sprintf("insert into %s ( %s ) values ( %s ) ", TablName, strings.Join(ColList, ","), strings.Join(ValList, ","))
	}

	this.sqlDebug("insert", SqlQuery, SqlValue)

	St, eror := this.prepareCtx(Ctx, SqlQuery)
	if eror != nil {
		return eror
	}
	defer St.Close()

	if _, eror = St.ExecContext(Ctx, SqlValue...); eror != nil {
		return fmt.Errorf("UniEngine: insert fail: %w", eror)
	}

	return nil
}

func (this *TUniEngine) InsertL(i interface{}, TableName ...string) error {
	return this.InsertLCtx(context.Background(), i, TableName...)
}

func (this *TUniEngine) InsertLCtx(Ctx context.Context, i interface{}, TableName ...string) error {

	UniTable, v, TablName, eror := this.resolveTarget("InsertL", i, TableName, reflect.Slice, false)
	if eror != nil {
		return eror
	}

	if v.Len() == 0 {
		return nil
	}

	//#完整性:保护列下标解析一次,逐行重算 MAC 回填伴随字段
	VerifyCols, eror := this.verifyColsOf(UniTable, v.Type().Elem())
	if eror != nil {
		return eror
	}
	for m := 0; m < v.Len(); m++ {
		if eror := this.verifySignCols(v.Index(m), UniTable, VerifyCols); eror != nil {
			return eror
		}
	}

	SqlQuery := ""
	SqlValue := make([]interface{}, 0)

	if x, Ok := v.Index(0).Interface().(HasGetSqlInsertL); Ok {
		SqlQuery = x.GetSqlInsertL(this, TablName, int64(v.Len()))
	}

	if x, Ok := v.Index(0).Interface().(HasSetSqlValuesL); Ok {
		for m := 0; m < v.Len(); m++ {
			f := v.Index(m)
			x.SetSqlValuesL(this, EtInsert, f, &SqlValue)
		}
	}

	if SqlQuery == "" && len(SqlValue) == 0 {

		Fields := UniTable.writableFields()
		if len(Fields) == 0 {
			return fmt.Errorf("UniEngine: no insertable column in class registered: %s", UniTable.TableName)
		}

		var ColList []string
		for _, ItemPara := range Fields {
			ColList = append(ColList, this.getColParam(ItemPara.FieldName))
		}

		var RowList []string
		ColIndex := 1
		for m := 0; m < v.Len(); m++ {

			f := v.Index(m)

			var ValList []string
			for _, ItemPara := range Fields {

				ValList = append(ValList, this.getValParam(ColIndex))
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
				RowList = append(RowList, fmt.Sprintf("into %s ( %s ) values ( %s )", TablName, strings.Join(ColList, ","), strings.Join(ValList, ",")))
			} else {
				RowList = append(RowList, fmt.Sprintf("( %s )", strings.Join(ValList, ",")))
			}
		}

		if this.dbFamily() == FmORACLE {
			SqlQuery = fmt.Sprintf("insert all %s select 1 from dual", strings.Join(RowList, " "))
		} else {
			SqlQuery = fmt.Sprintf("insert into %s ( %s ) values %s", TablName, strings.Join(ColList, ","), strings.Join(RowList, ","))
		}
	}

	this.sqlDebug("insert", SqlQuery, SqlValue)

	St, eror := this.prepareCtx(Ctx, SqlQuery)
	if eror != nil {
		return eror
	}
	defer St.Close()

	if _, eror = St.ExecContext(Ctx, SqlValue...); eror != nil {
		return fmt.Errorf("UniEngine: insert fail: %w (hint: too many parameters? try [InsertP])", eror)
	}

	return nil
}

func (this *TUniEngine) Delete(i interface{}, TableName ...string) error {
	return this.DeleteCtx(context.Background(), i, TableName...)
}

func (this *TUniEngine) DeleteCtx(Ctx context.Context, i interface{}, TableName ...string) error {

	UniTable, v, TablName, eror := this.resolveTarget("Delete", i, TableName, reflect.Invalid, true)
	if eror != nil {
		return eror
	}

	var KeyCols []string
	SqlValue := make([]interface{}, 0)

	ColIndex := 1
	for _, ItemPara := range UniTable.pkeyFields() {
		KeyCols = append(KeyCols, this.getColParam(ItemPara.FieldName)+"="+this.getValParam(ColIndex))
		SqlValue = append(SqlValue, v.FieldByName(ItemPara.AttriName).Interface())
		ColIndex = ColIndex + 1
	}

	if len(KeyCols) == 0 {
		return fmt.Errorf("%w: %s", ErrNoPkeys, UniTable.TableName)
	}

	SqlQuery := fmt.Sprintf("delete from %s where %s", TablName, strings.Join(KeyCols, " and "))

	this.sqlDebug("delete", SqlQuery, SqlValue)

	St, eror := this.prepareCtx(Ctx, SqlQuery)
	if eror != nil {
		return eror
	}
	defer St.Close()

	if _, eror = St.ExecContext(Ctx, SqlValue...); eror != nil {
		return fmt.Errorf("UniEngine: delete fail: %w", eror)
	}

	return nil
}

// ---------------------------------------------------------------------------
// Execute / 元数据探测
// ---------------------------------------------------------------------------
