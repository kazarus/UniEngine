// UniSave
// #SaveIt/SaveItWhenNotExist:方言原生 UPSERT 与 count 回退
package UniEngine

import (
	"context"
	"fmt"
	"reflect"
	"strings"
)

// ---------------------------------------------------------------------------
// SaveIt / SaveItWhenNotExist:方言原生 UPSERT + count-then-dispatch 回退
// ---------------------------------------------------------------------------

const (
	upsertModeUpdate = iota //#存在则更新,否则插入(SaveIt)
	upsertModeSkip          //#存在则跳过,否则插入(SaveItWhenNotExist)
)

// upsertStmt 按方言生成原生 UPSERT 语句,消除 SaveIt 两步(count 后写入)之间的并发窗口。
// 参数只传一遍(列序:Keys 在前,Cols 在后)。返回 Ok=false 表示当前方言不支持,
// 调用方回退 count-then-dispatch。
//
// 注意:MySQL 不在此列——ON DUPLICATE KEY UPDATE 由任一唯一键触发而非仅主键,
// 与 SaveIt 的主键语义不等价,故 MySQL 一律回退旧路径。

// upsertStmt 按方言生成原生 UPSERT 语句,消除 SaveIt 两步(count 后写入)之间的并发窗口。
// 参数只传一遍(列序:Keys 在前,Cols 在后)。返回 Ok=false 表示当前方言不支持,
// 调用方回退 count-then-dispatch。
//
// 注意:MySQL 不在此列——ON DUPLICATE KEY UPDATE 由任一唯一键触发而非仅主键,
// 与 SaveIt 的主键语义不等价,故 MySQL 一律回退旧路径。
func (this *TUniEngine) upsertStmt(Mode int, TablName string, Keys, Cols []TUniField) (string, bool) {

	if len(Keys) == 0 {
		return "", false
	}

	var ColList []string
	var ValList []string
	ColIndex := 1
	for _, List := range [][]TUniField{Keys, Cols} {
		for _, ItemPara := range List {
			ColList = append(ColList, this.getColParam(ItemPara.FieldName))
			ValList = append(ValList, this.getValParam(ColIndex))
			ColIndex++
		}
	}

	var KeyList []string
	for _, ItemPara := range Keys {
		KeyList = append(KeyList, this.getColParam(ItemPara.FieldName))
	}

	UpdateMode := Mode == upsertModeUpdate && len(Cols) > 0

	switch this.dbFamily() {

	case FmPOSTGR:
		{
			//#PG:excluded 伪表复用本次插入值,无需重复传参
			if !UpdateMode {
				return fmt.Sprintf("insert into %s ( %s ) values ( %s ) on conflict ( %s ) do nothing",
					TablName, strings.Join(ColList, ","), strings.Join(ValList, ","), strings.Join(KeyList, ",")), true
			}

			var SetList []string
			for _, ItemPara := range Cols {
				SqlCol := this.getColParam(ItemPara.FieldName)
				SetList = append(SetList, SqlCol+"=excluded."+SqlCol)
			}

			return fmt.Sprintf("insert into %s ( %s ) values ( %s ) on conflict ( %s ) do update set %s",
				TablName, strings.Join(ColList, ","), strings.Join(ValList, ","), strings.Join(KeyList, ","), strings.Join(SetList, ",")), true
		}

	case FmORACLE:
		{
			//#Oracle:MERGE,源数据放 dual 子查询,参数只传一遍
			var SelectList []string
			ColIndex = 1
			for _, List := range [][]TUniField{Keys, Cols} {
				for _, ItemPara := range List {
					SelectList = append(SelectList, fmt.Sprintf("%s %s", this.getValParam(ColIndex), ItemPara.FieldName))
					ColIndex++
				}
			}

			var OnList []string
			for _, ItemPara := range Keys {
				OnList = append(OnList, "t."+ItemPara.FieldName+"=src."+ItemPara.FieldName)
			}

			var InsertVals []string
			for _, List := range [][]TUniField{Keys, Cols} {
				for _, ItemPara := range List {
					InsertVals = append(InsertVals, "src."+ItemPara.FieldName)
				}
			}

			SQL := fmt.Sprintf("merge into %s t using (select %s from dual) src on (%s)",
				TablName, strings.Join(SelectList, ","), strings.Join(OnList, " and "))

			if UpdateMode {
				var SetList []string
				for _, ItemPara := range Cols {
					SetList = append(SetList, "t."+ItemPara.FieldName+"=src."+ItemPara.FieldName)
				}
				SQL = SQL + " when matched then update set " + strings.Join(SetList, ",")
			}

			return SQL + " when not matched then insert ( " + strings.Join(ColList, ",") + " ) values ( " + strings.Join(InsertVals, ",") + " )", true
		}

	case FmSQLSRV:
		{
			//#SQLServer:MERGE + HOLDLOCK,表值构造器传参一遍
			var OnList []string
			for _, ItemPara := range Keys {
				SqlCol := this.getColParam(ItemPara.FieldName)
				OnList = append(OnList, "t."+SqlCol+"=src."+SqlCol)
			}

			var InsertVals []string
			for _, List := range [][]TUniField{Keys, Cols} {
				for _, ItemPara := range List {
					InsertVals = append(InsertVals, "src."+this.getColParam(ItemPara.FieldName))
				}
			}

			SQL := fmt.Sprintf("merge into %s with (holdlock) as t using (values ( %s )) as src ( %s ) on (%s)",
				TablName, strings.Join(ValList, ","), strings.Join(ColList, ","), strings.Join(OnList, " and "))

			if UpdateMode {
				var SetList []string
				for _, ItemPara := range Cols {
					SqlCol := this.getColParam(ItemPara.FieldName)
					SetList = append(SetList, "t."+SqlCol+"=src."+SqlCol)
				}
				SQL = SQL + " when matched then update set " + strings.Join(SetList, ",")
			}

			return SQL + " when not matched then insert ( " + strings.Join(ColList, ",") + " ) values ( " + strings.Join(InsertVals, ",") + " );", true
		}
	}

	return "", false
}

// canNativeSave 判断该类是否可走方言原生 UPSERT:任一自定义 SQL 钩子
// (GetSqlUpdate/GetSqlInsert/SetSqlValues)存在时,自定义语句无法转为 UPSERT,回退旧路径。

// canNativeSave 判断该类是否可走方言原生 UPSERT:任一自定义 SQL 钩子
// (GetSqlUpdate/GetSqlInsert/SetSqlValues)存在时,自定义语句无法转为 UPSERT,回退旧路径。
func canNativeSave(v reflect.Value) bool {

	if _, Ok := v.Interface().(HasGetSqlUpdate); Ok {
		return false
	}
	if _, Ok := v.Interface().(HasGetSqlInsert); Ok {
		return false
	}
	if _, Ok := v.Interface().(HasSetSqlValues); Ok {
		return false
	}

	return true
}

// saveUpsert 方言原生 UPSERT 执行(Keys/Cols 按确定序收集,参数只传一遍)。
// 返回 Done=false 表示方言不支持,调用方回退 count-then-dispatch。

// saveUpsert 方言原生 UPSERT 执行(Keys/Cols 按确定序收集,参数只传一遍)。
// 返回 Done=false 表示方言不支持,调用方回退 count-then-dispatch。
func (this *TUniEngine) saveUpsert(ctx context.Context, UniTable *TUniTable, v reflect.Value, TablName string, Mode int) (bool, error) {

	Keys := UniTable.pkeyFields()

	var Cols []TUniField
	for _, ItemPara := range UniTable.writableFields() {
		if _, Valid := UniTable.HashPkeys[strings.ToLower(ItemPara.FieldName)]; Valid {
			continue
		}
		Cols = append(Cols, ItemPara)
	}

	if len(Keys) == 0 {
		return false, nil
	}

	//#完整性:组装参数前按结构体当前值重算 MAC 回填伴随字段
	if eror := this.verifySignRow(v, UniTable); eror != nil {
		return false, eror
	}

	SQL, Ok := this.upsertStmt(Mode, TablName, Keys, Cols)
	if !Ok {
		return false, nil
	}

	//#主键值一律明文(on conflict 的匹配即 WHERE 语义);仅数据列参与应用加密
	Values := make([]interface{}, 0, len(Keys)+len(Cols))
	for _, ItemPara := range Keys {
		Values = append(Values, v.FieldByName(ItemPara.AttriName).Interface())
	}
	for _, ItemPara := range Cols {
		if UniTable.secretColumn(ItemPara) {
			Value, eror := this.secretEncrypt(v.FieldByName(ItemPara.AttriName))
			if eror != nil {
				return false, eror
			}
			Values = append(Values, Value)
			continue
		}
		Values = append(Values, v.FieldByName(ItemPara.AttriName).Interface())
	}

	this.sqlDebug("upsert", SQL, Values)

	St, eror := this.prepareCtx(ctx, SQL)
	if eror != nil {
		return false, eror
	}
	defer St.Close()

	if _, eror := St.ExecContext(ctx, Values...); eror != nil {
		return false, fmt.Errorf("UniEngine: upsert fail: %w", eror)
	}

	return true, nil
}

// countByPkeys 按主键统计存在行数(count-then-dispatch 回退路径的探测语句)。

// countByPkeys 按主键统计存在行数(count-then-dispatch 回退路径的探测语句)。
func (this *TUniEngine) countByPkeys(ctx context.Context, UniTable *TUniTable, v reflect.Value, TablName string) (int64, error) {

	var KeyCols []string
	SqlValue := make([]interface{}, 0)

	ColIndex := 1
	for _, ItemPara := range UniTable.pkeyFields() {
		KeyCols = append(KeyCols, this.getColParam(ItemPara.FieldName)+"="+this.getValParam(ColIndex))
		SqlValue = append(SqlValue, v.FieldByName(ItemPara.AttriName).Interface())
		ColIndex = ColIndex + 1
	}

	if len(KeyCols) == 0 {
		return 0, fmt.Errorf("%w: %s", ErrNoPkeys, UniTable.TableName)
	}

	SqlQuery := fmt.Sprintf("select count(1) from %s where %s", TablName, strings.Join(KeyCols, " and "))

	return this.SelectDCtx(ctx, SqlQuery, SqlValue...)
}

// saveByCount 旧的 count-then-dispatch 路径:钩子类与不支持原生 UPSERT 的方言(MySQL)回退使用。

// saveByCount 旧的 count-then-dispatch 路径:钩子类与不支持原生 UPSERT 的方言(MySQL)回退使用。
func (this *TUniEngine) saveByCount(ctx context.Context, i interface{}, UniTable *TUniTable, v reflect.Value, TablName string, TableName []string, updateWhenExist bool) error {

	Count, eror := this.countByPkeys(ctx, UniTable, v, TablName)
	if eror != nil {
		return eror
	}

	if this.debugging() {
		fmt.Println("UniEngine: select.cnt", Count)
	}

	if updateWhenExist {
		if Count == 1 {
			return this.UpdateCtx(ctx, i, TableName...)
		}
		return this.InsertCtx(ctx, i, TableName...)
	}

	if Count == 0 {
		return this.InsertCtx(ctx, i, TableName...)
	}

	return nil
}

func (this *TUniEngine) SaveIt(i interface{}, TableName ...string) error {
	return this.SaveItCtx(context.Background(), i, TableName...)
}

func (this *TUniEngine) SaveItCtx(ctx context.Context, i interface{}, TableName ...string) error {

	UniTable, v, TablName, eror := this.resolveTarget("SaveIt", i, TableName, reflect.Invalid, true)
	if eror != nil {
		return eror
	}

	//#默认路径走方言原生 UPSERT,消除 count 与写入之间的并发窗口;
	//#钩子类/不支持的方言回退旧的 count-then-dispatch
	if canNativeSave(v) {
		Done, eror := this.saveUpsert(ctx, UniTable, v, TablName, upsertModeUpdate)
		if eror != nil {
			return eror
		}
		if Done {
			return nil
		}
	}

	return this.saveByCount(ctx, i, UniTable, v, TablName, TableName, true)
}

func (this *TUniEngine) SaveItWhenNotExist(i interface{}, TableName ...string) error {
	return this.SaveItWhenNotExistCtx(context.Background(), i, TableName...)
}

func (this *TUniEngine) SaveItWhenNotExistCtx(ctx context.Context, i interface{}, TableName ...string) error {

	UniTable, v, TablName, eror := this.resolveTarget("SaveItWhenNotExist", i, TableName, reflect.Invalid, true)
	if eror != nil {
		return eror
	}

	if canNativeSave(v) {
		Done, eror := this.saveUpsert(ctx, UniTable, v, TablName, upsertModeSkip)
		if eror != nil {
			return eror
		}
		if Done {
			return nil
		}
	}

	return this.saveByCount(ctx, i, UniTable, v, TablName, TableName, false)
}

// ---------------------------------------------------------------------------
// Update / Insert / InsertL / CopyInL / Delete
// ---------------------------------------------------------------------------
