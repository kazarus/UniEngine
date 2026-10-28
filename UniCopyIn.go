// UniCopyIn
// #批量写入:COPY 协议(CopyInHook)与分页

package UniEngine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

func (this *TUniEngine) CopyInL(i interface{}, TableName ...string) error {
	return this.CopyInLCtx(context.Background(), i, TableName...)
}

// #已废弃:旧版命名,新代码请用 CopyInL;仅为源码兼容保留
func (this *TUniEngine) SpecialInsertL(i interface{}, TableName ...string) error {
	return this.CopyInLCtx(context.Background(), i, TableName...)
}

// #已废弃:旧版命名,新代码请用 CopyInLCtx;仅为源码兼容保留
func (this *TUniEngine) SpecialInsertLCtx(Ctx context.Context, i interface{}, TableName ...string) error {
	return this.CopyInLCtx(Ctx, i, TableName...)
}

// guardCopyInHook 校验并就地加密自定义 COPY 行:
//   - 验签激活:钩子列清单必须显式包含 MAC 伴随列,且逐行实参等于引擎回填值;
//     缺失/值不符时 fail-fast(钩子列清单无法由引擎自行附加签名);
//   - 加密激活:列名映射到注册表,secreton 列实参就地加密;列名不可映射/行长度不符/
//     实参类型非 string([]byte)时 fail-fast,绝不把明文交给 COPY。
//
// SecretOn=0 且验签未激活时不做任何干预,与默认路径同口径。
func (this *TUniEngine) guardCopyInHook(UniTable *TUniTable, Columns []string, Rows [][]interface{}, MacValues []string) error {
	if this.verifyActive(UniTable) {
		MacIdx := -1
		for Pos, Col := range Columns {
			if strings.EqualFold(Col, UniTable.VerifyAt) {
				MacIdx = Pos
				break
			}
		}
		if MacIdx < 0 || len(Rows) != len(MacValues) {
			return fmt.Errorf("UniEngine: CopyInL on table [%s] takes the custom column/value hook path, which bypasses engine-side verify: the hook must carry the MAC companion column [%s] with the engine-computed value; drop the hook or disable verify for this table", UniTable.TableName, UniTable.VerifyAt)
		}
		for RowIdx, Row := range Rows {
			if MacIdx >= len(Row) {
				return fmt.Errorf("UniEngine: CopyInL on table [%s] hook row[%d] shorter than MAC column index [%d]", UniTable.TableName, RowIdx, MacIdx)
			}
			Got, Ok := macArgString(Row[MacIdx])
			if !Ok || Got != MacValues[RowIdx] {
				return fmt.Errorf("UniEngine: CopyInL on table [%s] hook row[%d] MAC value does not match the engine-computed value; refusing to write", UniTable.TableName, RowIdx)
			}
		}
	}

	if this.SecretOn == 0 || len(UniTable.ColSecret) == 0 {
		return nil
	}

	if len(Columns) == 0 {
		return fmt.Errorf("UniEngine: CopyInL on table [%s] custom hook returned values without a column list; cannot map secreton columns safely", UniTable.TableName)
	}
	if len(Rows) == 0 {
		return fmt.Errorf("UniEngine: CopyInL on table [%s] custom hook returned a column list but no rows; refusing to report success without writing", UniTable.TableName)
	}

	SecretIdx := make([]int, 0, 2)
	for Pos, Col := range Columns {
		ColKey := hookColKey(Col)
		Field, Ok := UniTable.HashField[ColKey]
		if !Ok {
			return fmt.Errorf("UniEngine: CopyInL on table [%s] custom hook column [%s] does not map to a registered field; cannot guarantee secreton encryption", UniTable.TableName, Col)
		}
		if UniTable.secretColumn(Field) {
			SecretIdx = append(SecretIdx, Pos)
		}
	}

	for RowIdx, Row := range Rows {
		if len(Row) != len(Columns) {
			return fmt.Errorf("UniEngine: CopyInL on table [%s] custom hook row[%d] has %d values for %d columns", UniTable.TableName, RowIdx, len(Row), len(Columns))
		}

		for _, Pos := range SecretIdx {
			switch Value := Row[Pos].(type) {
			case string:
				Encrypted, eror := this.Secret(Value, true)
				if eror != nil {
					return eror
				}
				Row[Pos] = Encrypted
			case []byte:
				Encrypted, eror := this.Secret(string(Value), true)
				if eror != nil {
					return eror
				}
				Row[Pos] = Encrypted
			default:
				return fmt.Errorf("UniEngine: CopyInL on table [%s] custom hook row[%d] column[%s] for a secreton column must be string, got [%T]", UniTable.TableName, RowIdx, Columns[Pos], Row[Pos])
			}
		}
	}

	return nil
}

func (this *TUniEngine) CopyInLCtx(Ctx context.Context, i interface{}, TableName ...string) error {
	UniTable, v, TablName, eror := this.resolveTarget("CopyInL", i, TableName, reflect.Slice, false)
	if eror != nil {
		return eror
	}

	if v.Len() == 0 {
		return nil
	}

	//#完整性:保护列下标解析一次,逐行重算 MAC 回填伴随字段(CopyInP/SpecialInsertL* 分页均汇入本路径)
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

	SqlQuery := make([]string, 0)
	SqlValue := make([][]interface{}, 0)

	//#钩子按首行探测;slice 元素可寻址,指针接收者实现同样可被识别
	First := v.Index(0)
	if x, Ok := hookOf[HasCopyInGetSqlInsertL](First); Ok {
		SqlQuery = x.CopyInGetSqlInsertL(this, TablName, int64(v.Len()))
	} else if x, Ok := hookOf[HasSpecialGetSqlInsertL](First); Ok {
		//#兼容旧版接口
		SqlQuery = x.SpecialGetSqlInsertL(this, TablName, int64(v.Len()))
	}

	if x, Ok := hookOf[HasCopyInSetSqlValuesL](First); Ok {
		for m := 0; m < v.Len(); m++ {
			f := v.Index(m)
			x.CopyInSetSqlValuesL(this, EtInsert, f, &SqlValue)
		}
	} else if x, Ok := hookOf[HasSpecialSetSqlValuesL](First); Ok {
		//#兼容旧版接口
		for m := 0; m < v.Len(); m++ {
			f := v.Index(m)
			x.SpecialSetSqlValuesL(this, EtInsert, f, &SqlValue)
		}
	}

	//#自定义 COPY 列/值同样受加密与验签约束:默认分支的 secretEncrypt 不覆盖钩子行,
	//#旧实现会把 secreton 明文直接写库且不报错。守卫要么就地加密,要么 fail-fast。
	if len(SqlQuery) > 0 || len(SqlValue) > 0 {
		if eror := this.guardCopyInHook(UniTable, SqlQuery, SqlValue, MacValues); eror != nil {
			return eror
		}
	}

	if len(SqlQuery) == 0 && len(SqlValue) == 0 {
		Fields := UniTable.writableFields()
		if len(Fields) == 0 {
			return fmt.Errorf("UniEngine: no insertable column in class registered: %s", UniTable.TableName)
		}

		for _, ItemPara := range Fields {
			SqlQuery = append(SqlQuery, ItemPara.FieldName)
		}

		for m := 0; m < v.Len(); m++ {
			f := v.Index(m)

			var Row = make([]interface{}, 0)

			for _, ItemPara := range Fields {
				if UniTable.secretColumn(ItemPara) {
					Value, eror := this.secretEncrypt(f.FieldByName(ItemPara.AttriName))
					if eror != nil {
						return eror
					}
					Row = append(Row, Value)
					continue
				}
				Row = append(Row, f.FieldByName(ItemPara.AttriName).Interface())
			}

			SqlValue = append(SqlValue, Row)
		}
	}

	this.sqlDebug("copyin", SqlQuery, SqlValue)

	//#COPY 仅 PostgreSQL 协议族可用;其余方言显式报错,不再发送必然失败的语句
	if this.dbFamily() != FmPOSTGR {
		return fmt.Errorf("UniEngine: CopyInL requires a PostgreSQL-family provider (DtPOSTGR/DtKINGES/DtOPENGS/DtPOLODB), got [%d]", this.Provider)
	}

	//#应用钩子优先(pgx 等驱动用 contrib/pgxcopy 接管原生 CopyFrom);
	//#未处理(handled=false)时回退内置 pq 风格逐行协议
	if this.CopyInHook != nil {
		if eror := this.ensureDb(); eror != nil {
			return eror
		}
		//#钩子从连接池取连接,无法路由到 *sql.Tx;事务期间显式拒绝,避免数据落到事务外
		if this.currentTx() != nil {
			return errors.New("UniEngine: CopyInHook can not run inside a transaction (pool connection, not tx-routed); commit or cancel first")
		}

		handled, eror := this.CopyInHook(Ctx, this.Db, TablName, SqlQuery, SqlValue)
		if eror != nil {
			return fmt.Errorf("UniEngine: copyin fail: %w", eror)
		}
		if handled {
			return nil
		}
	}

	Sql4Text := copyInStmt(TablName, SqlQuery)

	St, eror := this.prepareCtx(Ctx, Sql4Text)
	if eror != nil {
		return fmt.Errorf("UniEngine: copyin fail: %w (hint: too many parameters? try [CopyInP])", eror)
	}
	defer St.Close()

	// 执行所有行
	for _, Row := range SqlValue {
		_, eror = St.ExecContext(Ctx, Row...)
		if eror != nil {
			return eror
		}
	}

	// 完成 COPY 的收尾调用(空参数)
	_, eror = St.ExecContext(Ctx)
	if eror != nil {
		return fmt.Errorf("UniEngine: copyin fail: %w (hint: too many parameters? try [CopyInP])", eror)
	}

	return nil
}

func (this *TUniEngine) InsertP(i interface{}, PageSize int64, TableName ...string) error {
	return this.InsertPCtx(context.Background(), i, PageSize, TableName...)
}

func (this *TUniEngine) InsertPCtx(Ctx context.Context, i interface{}, PageSize int64, TableName ...string) error {
	//#nil 校验必须先于 reflect.TypeOf/Kind(评审 P0)
	if eror := nilParamOf("InsertP", i); eror != nil {
		return eror
	}

	UniTable, _, _, eror := this.resolveTarget("InsertP", i, TableName, reflect.Slice, false)
	if eror != nil {
		return eror
	}
	PageSize = this.normalizeBatchPageSize(PageSize, len(UniTable.writableFields()))

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Slice {
		return errors.New("UniEngine: method [InsertP] needs a slice param; check your code;")
	}

	var All4Data = reflect.Indirect(reflect.ValueOf(i))
	var ListData = reflect.MakeSlice(t, 0, 0)

	for I := 0; I < All4Data.Len(); I++ {
		ListData = reflect.Append(ListData, All4Data.Index(I))

		if ListData.Len() == int(PageSize) {
			if eror := this.insertPage(Ctx, ListData, TableName...); eror != nil {
				return eror
			}
			ListData = reflect.MakeSlice(t, 0, 0)
		}
	}

	return this.insertPage(Ctx, ListData, TableName...)
}

// insertPage 按方言路由单页批量写入(PolarDB 走 COPY 协议,其余走 INSERT)。
func (this *TUniEngine) insertPage(Ctx context.Context, ListData reflect.Value, TableName ...string) error {
	if this.Supplier == DtPOLODB {
		return this.CopyInLCtx(Ctx, ListData.Interface(), TableName...)
	}

	return this.InsertLCtx(Ctx, ListData.Interface(), TableName...)
}

func (this *TUniEngine) CopyInP(i interface{}, PageSize int64, TableName ...string) error {
	return this.CopyInPCtx(context.Background(), i, PageSize, TableName...)
}

// #已废弃:旧版命名,新代码请用 CopyInP;仅为源码兼容保留
func (this *TUniEngine) SpecialInsertP(i interface{}, PageSize int64, TableName ...string) error {
	return this.CopyInPCtx(context.Background(), i, PageSize, TableName...)
}

// #已废弃:旧版命名,新代码请用 CopyInPCtx;仅为源码兼容保留
func (this *TUniEngine) SpecialInsertPCtx(Ctx context.Context, i interface{}, PageSize int64, TableName ...string) error {
	return this.CopyInPCtx(Ctx, i, PageSize, TableName...)
}

func (this *TUniEngine) CopyInPCtx(Ctx context.Context, i interface{}, PageSize int64, TableName ...string) error {
	//#nil 校验必须先于 reflect.TypeOf/Kind(评审 P0)
	if eror := nilParamOf("CopyInP", i); eror != nil {
		return eror
	}

	PageSize = this.normalizeBatchPageSize(PageSize, 0)

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Slice {
		return errors.New("UniEngine: method [CopyInP] needs a slice param; check your code;")
	}

	var All4Data = reflect.Indirect(reflect.ValueOf(i))
	var ListData = reflect.MakeSlice(t, 0, 0)

	for I := 0; I < All4Data.Len(); I++ {
		ListData = reflect.Append(ListData, All4Data.Index(I))

		if ListData.Len() == int(PageSize) {
			if eror := this.CopyInLCtx(Ctx, ListData.Interface(), TableName...); eror != nil {
				return eror
			}
			ListData = reflect.MakeSlice(t, 0, 0)
		}
	}

	return this.CopyInLCtx(Ctx, ListData.Interface(), TableName...)
}
