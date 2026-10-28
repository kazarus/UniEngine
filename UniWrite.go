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

// macArgString 取钩子实参中的 MAC 文本;只接受 string/[]byte。
func macArgString(Value interface{}) (string, bool) {
	switch Item := Value.(type) {
	case string:
		return Item, true
	case []byte:
		return string(Item), true
	default:
		return "", false
	}
}

// verifyMacValues 取验签回填后的 MAC 值(单行=一项;切片=逐行),用于校验写钩子实参。
func (this *TUniEngine) verifyMacValues(v reflect.Value, UniTable *TUniTable) ([]string, error) {
	Field, Ok := UniTable.HashField[UniTable.VerifyAt]
	if !Ok || Field.AttriName == "" {
		return nil, fmt.Errorf("UniEngine: table [%s] verify column [%s] has no mapped class attribute", UniTable.TableName, UniTable.VerifyAt)
	}
	Read := func(Row reflect.Value) (string, error) {
		if Row.Kind() == reflect.Ptr {
			if Row.IsNil() {
				return "", fmt.Errorf("UniEngine: table [%s] verify column [%s] got a nil row pointer", UniTable.TableName, UniTable.VerifyAt)
			}
			Row = Row.Elem()
		}
		F := Row.FieldByName(Field.AttriName)
		if !F.IsValid() || F.Kind() != reflect.String {
			return "", fmt.Errorf("UniEngine: table [%s] verify column [%s] must map to a string field", UniTable.TableName, UniTable.VerifyAt)
		}
		return F.String(), nil
	}
	if v.Kind() == reflect.Slice {
		Out := make([]string, 0, v.Len())
		for i := 0; i < v.Len(); i++ {
			Value, eror := Read(v.Index(i))
			if eror != nil {
				return nil, eror
			}
			Out = append(Out, Value)
		}
		return Out, nil
	}
	Value, eror := Read(v)
	if eror != nil {
		return nil, eror
	}
	return []string{Value}, nil
}

// hookWriteGuard 写钩子统一守卫:
//
//	验签激活 → 钩子 SQL 必须显式携带 MAC 伴随列,且该列实参必须等于引擎回填值;
//	缺失/形态不可信/值不符 → fail-fast(钩子语句无法由引擎自行附加签名);
//	加密激活 → 解析钩子 SQL 的"列→实参"映射(UniHookSql.go),就地加密 secreton
//	列实参——与读钩子"交接前解密"对称,钩子方按列序给实参即拿到与默认路径同口径
//	的加密语义;形态不可信解析(字面量赋值/表达式/未知结构/实参数不匹配)→ 维持
//	fail-fast,不静默写明文。
//
// SecretOn=0 且验签未激活时不拦截,与默认路径同口径。
func (this *TUniEngine) hookWriteGuard(Method string, UniTable *TUniTable, SqlQuery string, SqlValue []interface{}, MacValues ...string) error {
	if this.verifyActive(UniTable) {
		Plan, Ok := resolveHookMacArgs(UniTable, SqlQuery)
		if !Ok || len(Plan.Idx) == 0 || len(Plan.Idx) != len(MacValues) || Plan.ArgCount != len(SqlValue) {
			return fmt.Errorf("UniEngine: method [%s] on table [%s] takes the custom SQL write hook path, which bypasses engine-side verify: the hook must carry the MAC companion column [%s] with the engine-computed value; drop the write hook or disable the feature for this table", Method, UniTable.TableName, UniTable.VerifyAt)
		}
		for i, Idx := range Plan.Idx {
			if Idx < 0 || Idx >= len(SqlValue) {
				return fmt.Errorf("UniEngine: method [%s] on table [%s] MAC arg index [%d] out of [%d] args", Method, UniTable.TableName, Idx, len(SqlValue))
			}
			Got, Ok := macArgString(SqlValue[Idx])
			if !Ok || Got != MacValues[i] {
				return fmt.Errorf("UniEngine: method [%s] on table [%s] hook arg for MAC column [%s] does not match the engine-computed value; refusing to write", Method, UniTable.TableName, UniTable.VerifyAt)
			}
		}
	}
	if this.SecretOn == 0 || !UniTable.hasSecretDataCols() {
		return nil
	}
	Plan, Ok := resolveHookSecretArgs(UniTable, SqlQuery)
	if !Ok {
		return fmt.Errorf("UniEngine: method [%s] on table [%s] takes the custom SQL write hook path; engine cannot map hook args to secreton columns from this SQL shape, secreton would be bypassed; drop the write hook or disable secreton for this table", Method, UniTable.TableName)
	}
	if Plan.ArgCount != len(SqlValue) {
		return fmt.Errorf("UniEngine: method [%s] on table [%s] write hook SQL declares %d args but %d given; cannot map secreton columns safely", Method, UniTable.TableName, Plan.ArgCount, len(SqlValue))
	}
	for _, Idx := range Plan.Idx {
		if Idx < 0 || Idx >= len(SqlValue) {
			return fmt.Errorf("UniEngine: method [%s] on table [%s] write hook secret mapping needs arg index %d, but only %d args given", Method, UniTable.TableName, Idx, len(SqlValue))
		}
		switch Value := SqlValue[Idx].(type) {
		case string:
			Encrypted, eror := this.Secret(Value, true)
			if eror != nil {
				return eror
			}
			SqlValue[Idx] = Encrypted
		case []byte:
			Encrypted, eror := this.Secret(string(Value), true)
			if eror != nil {
				return eror
			}
			SqlValue[Idx] = Encrypted
		default:
			return fmt.Errorf("UniEngine: method [%s] on table [%s] write hook arg[%d] for a secreton column must be string, got [%T]", Method, UniTable.TableName, Idx, SqlValue[Idx])
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// SaveIt / SaveItWhenNotExist:方言原生 UPSERT + count-then-dispatch 回退
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

	//#写钩子须携带的 MAC 值:在钩子执行前锁定,防止钩子篡改结构体后自证
	var MacValues []string
	if this.verifyActive(UniTable) {
		MacValues, eror = this.verifyMacValues(v, UniTable)
		if eror != nil {
			return eror
		}
	}

	SqlQuery := ""
	SqlValue := make([]interface{}, 0)

	if x, Ok := hookOf[HasGetSqlUpdate](v); Ok {
		SqlQuery = x.GetSqlUpdate(this, TablName)
	}
	if x, Ok := hookOf[HasSetSqlValues](v); Ok {
		x.SetSqlValues(this, EtUpdate, &SqlValue)
	}

	//#钩子路径守卫:激活的加密/验签不能被自定义 SQL 静默绕过;验签激活时钩子须携带 MAC 列
	if SqlQuery != "" || len(SqlValue) > 0 {
		if eror := this.hookWriteGuard("Update", UniTable, SqlQuery, SqlValue, MacValues...); eror != nil {
			return eror
		}
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

	//#写钩子须携带的 MAC 值:在钩子执行前锁定,防止钩子篡改结构体后自证
	var MacValues []string
	if this.verifyActive(UniTable) {
		MacValues, eror = this.verifyMacValues(v, UniTable)
		if eror != nil {
			return eror
		}
	}

	SqlQuery := ""
	SqlValue := make([]interface{}, 0)

	if x, Ok := hookOf[HasGetSqlInsert](v); Ok {
		SqlQuery = x.GetSqlInsert(this, TablName)
	}
	if x, Ok := hookOf[HasSetSqlValues](v); Ok {
		x.SetSqlValues(this, EtInsert, &SqlValue)
	}

	//#钩子路径守卫:激活的加密/验签不能被自定义 SQL 静默绕过;验签激活时钩子须携带 MAC 列
	if SqlQuery != "" || len(SqlValue) > 0 {
		if eror := this.hookWriteGuard("Insert", UniTable, SqlQuery, SqlValue, MacValues...); eror != nil {
			return eror
		}
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

	//#InsertL 是"单条 SQL"语义:超过 999 行会撞绑定参数上限/Oracle INSERT ALL 性能拐点。
	//#大数据必须走 InsertP 自动分页,而不是静默生成一条超大 SQL。
	if int64(v.Len()) > maxBatchRows {
		return fmt.Errorf("%w: got %d rows, limit %d", ErrBatchTooLarge, v.Len(), maxBatchRows)
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

	//#写钩子须携带的 MAC 值:在钩子执行前锁定,逐行对应
	var MacValues []string
	if this.verifyActive(UniTable) {
		MacValues, eror = this.verifyMacValues(v, UniTable)
		if eror != nil {
			return eror
		}
	}

	SqlQuery := ""
	SqlValue := make([]interface{}, 0)

	//#钩子按首行探测;slice 元素可寻址,指针接收者实现同样可被识别
	First := v.Index(0)
	if x, Ok := hookOf[HasGetSqlInsertL](First); Ok {
		SqlQuery = x.GetSqlInsertL(this, TablName, int64(v.Len()))
	}

	if x, Ok := hookOf[HasSetSqlValuesL](First); Ok {
		for m := 0; m < v.Len(); m++ {
			f := v.Index(m)
			x.SetSqlValuesL(this, EtInsert, f, &SqlValue)
		}
	}

	//#钩子路径守卫:激活的加密/验签不能被自定义 SQL 静默绕过;验签激活时钩子须携带 MAC 列
	if SqlQuery != "" || len(SqlValue) > 0 {
		if eror := this.hookWriteGuard("InsertL", UniTable, SqlQuery, SqlValue, MacValues...); eror != nil {
			return eror
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
