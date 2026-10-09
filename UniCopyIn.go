// UniCopyIn
// #批量写入:COPY 协议(CopyInHook)与分页
package UniEngine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
)

func (this *TUniEngine) CopyInL(i interface{}, TableName ...string) error {
	return this.CopyInLCtx(context.Background(), i, TableName...)
}

// #已废弃:旧版命名,新代码请用 CopyInL;仅为源码兼容保留

// #已废弃:旧版命名,新代码请用 CopyInL;仅为源码兼容保留
func (this *TUniEngine) SpecialInsertL(i interface{}, TableName ...string) error {
	return this.CopyInLCtx(context.Background(), i, TableName...)
}

// #已废弃:旧版命名,新代码请用 CopyInLCtx;仅为源码兼容保留

// #已废弃:旧版命名,新代码请用 CopyInLCtx;仅为源码兼容保留
func (this *TUniEngine) SpecialInsertLCtx(ctx context.Context, i interface{}, TableName ...string) error {
	return this.CopyInLCtx(ctx, i, TableName...)
}

func (this *TUniEngine) CopyInLCtx(ctx context.Context, i interface{}, TableName ...string) error {

	UniTable, v, TablName, eror := this.resolveTarget("CopyInL", i, TableName, reflect.Slice, false)
	if eror != nil {
		return eror
	}

	if v.Len() == 0 {
		return nil
	}

	SqlQuery := make([]string, 0)
	SqlValue := make([][]interface{}, 0)

	if x, ok := v.Index(0).Interface().(HasGetSqlCopyInL); ok {
		SqlQuery = x.GetSqlCopyInL(this, TablName, int64(v.Len()))
	} else if x, ok := v.Index(0).Interface().(HasSpecialGetSqlInsertL); ok {
		//#兼容旧版接口
		SqlQuery = x.SpecialGetSqlInsertL(this, TablName, int64(v.Len()))
	}

	if x, ok := v.Index(0).Interface().(HasCopyInSetSqlValuesL); ok {
		for m := 0; m < v.Len(); m++ {
			f := v.Index(m)
			x.CopyInSetSqlValuesL(this, EtInsert, f, &SqlValue)
		}
	} else if x, ok := v.Index(0).Interface().(HasSpecialSetSqlValuesL); ok {
		//#兼容旧版接口
		for m := 0; m < v.Len(); m++ {
			f := v.Index(m)
			x.SpecialSetSqlValuesL(this, EtInsert, f, &SqlValue)
		}
	}

	if len(SqlQuery) == 0 && len(SqlValue) == 0 {

		fields := UniTable.writableFields()
		if len(fields) == 0 {
			return fmt.Errorf("UniEngine: no insertable column in class registered: %s", UniTable.TableName)
		}

		for _, ItemPara := range fields {
			SqlQuery = append(SqlQuery, ItemPara.FieldName)
		}

		for m := 0; m < v.Len(); m++ {

			f := v.Index(m)

			var row = make([]interface{}, 0)

			for _, ItemPara := range fields {

				if UniTable.secretColumn(ItemPara) {
					Value, eror := this.secretEncrypt(f.FieldByName(ItemPara.AttriName))
					if eror != nil {
						return eror
					}
					row = append(row, Value)
					continue
				}
				row = append(row, f.FieldByName(ItemPara.AttriName).Interface())
			}

			SqlValue = append(SqlValue, row)
		}
	}

	this.debugSQL("copyin", SqlQuery, SqlValue)

	//#COPY 仅 PostgreSQL 协议族可用;其余方言显式报错,不再发送必然失败的语句
	if this.dbFamily() != FmPOSTGR {
		return fmt.Errorf("UniEngine: CopyInL requires a PostgreSQL-family provider (DtPOSTGR/DtKINGES/DtOPENGS/DtPOLODB), got [%d]", this.Provider)
	}

	//#应用钩子优先(pgx 等驱动用 contrib/pgxcopy 接管原生 CopyFrom);
	//#未处理(handled=false)时回退内置 pq 风格逐行协议
	if this.CopyInHook != nil {

		//#钩子从连接池取连接,无法路由到 *sql.Tx;事务期间显式拒绝,避免数据落到事务外
		if this.currentTx() != nil {
			return errors.New("UniEngine: CopyInHook can not run inside a transaction (pool connection, not tx-routed); commit or cancel first")
		}

		handled, eror := this.CopyInHook(ctx, this.Db, TablName, SqlQuery, SqlValue)
		if eror != nil {
			return fmt.Errorf("UniEngine: copyin fail: %w", eror)
		}
		if handled {
			return nil
		}
	}

	Sql4Text := copyInStmt(TablName, SqlQuery)

	st, eror := this.prepareCtx(ctx, Sql4Text)
	if eror != nil {
		return fmt.Errorf("UniEngine: copyin fail: %w (hint: too many parameters? try [CopyInP])", eror)
	}
	defer st.Close()

	// 执行所有行
	for _, row := range SqlValue {
		_, eror = st.ExecContext(ctx, row...)
		if eror != nil {
			return eror
		}
	}

	// 完成 COPY 的收尾调用(空参数)
	_, eror = st.ExecContext(ctx)
	if eror != nil {
		return fmt.Errorf("UniEngine: copyin fail: %w (hint: too many parameters? try [CopyInP])", eror)
	}

	return nil
}

func (this *TUniEngine) InsertP(i interface{}, PageSize int64, TableName ...string) error {
	return this.InsertPCtx(context.Background(), i, PageSize, TableName...)
}

func (this *TUniEngine) InsertPCtx(ctx context.Context, i interface{}, PageSize int64, TableName ...string) error {

	if PageSize == 0 || PageSize == -1 {
		PageSize = 999
	}

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
			if eror := this.insertPage(ctx, ListData, TableName...); eror != nil {
				return eror
			}
			ListData = reflect.MakeSlice(t, 0, 0)
		}
	}

	return this.insertPage(ctx, ListData, TableName...)
}

// insertPage 按方言路由单页批量写入(PolarDB 走 COPY 协议,其余走 INSERT)。

// insertPage 按方言路由单页批量写入(PolarDB 走 COPY 协议,其余走 INSERT)。
func (this *TUniEngine) insertPage(ctx context.Context, ListData reflect.Value, TableName ...string) error {

	if this.Supplier == DtPOLODB {
		return this.CopyInLCtx(ctx, ListData.Interface(), TableName...)
	}

	return this.InsertLCtx(ctx, ListData.Interface(), TableName...)
}

func (this *TUniEngine) CopyInP(i interface{}, PageSize int64, TableName ...string) error {
	return this.CopyInPCtx(context.Background(), i, PageSize, TableName...)
}

// #已废弃:旧版命名,新代码请用 CopyInP;仅为源码兼容保留

// #已废弃:旧版命名,新代码请用 CopyInP;仅为源码兼容保留
func (this *TUniEngine) SpecialInsertP(i interface{}, PageSize int64, TableName ...string) error {
	return this.CopyInPCtx(context.Background(), i, PageSize, TableName...)
}

// #已废弃:旧版命名,新代码请用 CopyInPCtx;仅为源码兼容保留

// #已废弃:旧版命名,新代码请用 CopyInPCtx;仅为源码兼容保留
func (this *TUniEngine) SpecialInsertPCtx(ctx context.Context, i interface{}, PageSize int64, TableName ...string) error {
	return this.CopyInPCtx(ctx, i, PageSize, TableName...)
}

func (this *TUniEngine) CopyInPCtx(ctx context.Context, i interface{}, PageSize int64, TableName ...string) error {

	if PageSize == 0 || PageSize == -1 {
		PageSize = 999
	}

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
			if eror := this.CopyInLCtx(ctx, ListData.Interface(), TableName...); eror != nil {
				return eror
			}
			ListData = reflect.MakeSlice(t, 0, 0)
		}
	}

	return this.CopyInLCtx(ctx, ListData.Interface(), TableName...)
}
