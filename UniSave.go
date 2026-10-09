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
// 参数只传一遍(列序:keys 在前,cols 在后)。返回 ok=false 表示当前方言不支持,
// 调用方回退 count-then-dispatch。
//
// 注意:MySQL 不在此列——ON DUPLICATE KEY UPDATE 由任一唯一键触发而非仅主键,
// 与 SaveIt 的主键语义不等价,故 MySQL 一律回退旧路径。

// upsertStmt 按方言生成原生 UPSERT 语句,消除 SaveIt 两步(count 后写入)之间的并发窗口。
// 参数只传一遍(列序:keys 在前,cols 在后)。返回 ok=false 表示当前方言不支持,
// 调用方回退 count-then-dispatch。
//
// 注意:MySQL 不在此列——ON DUPLICATE KEY UPDATE 由任一唯一键触发而非仅主键,
// 与 SaveIt 的主键语义不等价,故 MySQL 一律回退旧路径。
func (this *TUniEngine) upsertStmt(Mode int, TablName string, keys, cols []TUniField) (string, bool) {

	if len(keys) == 0 {
		return "", false
	}

	var colList []string
	var paramList []string
	ColIndex := 1
	for _, list := range [][]TUniField{keys, cols} {
		for _, ItemPara := range list {
			colList = append(colList, this.getColParam(ItemPara.FieldName))
			paramList = append(paramList, this.getValParam(ColIndex))
			ColIndex++
		}
	}

	var keyList []string
	for _, ItemPara := range keys {
		keyList = append(keyList, this.getColParam(ItemPara.FieldName))
	}

	updateMode := Mode == upsertModeUpdate && len(cols) > 0

	switch this.dbFamily() {

	case FmPOSTGR:
		{
			//#PG:excluded 伪表复用本次插入值,无需重复传参
			if !updateMode {
				return fmt.Sprintf("insert into %s ( %s ) values ( %s ) on conflict ( %s ) do nothing",
					TablName, strings.Join(colList, ","), strings.Join(paramList, ","), strings.Join(keyList, ",")), true
			}

			var setList []string
			for _, ItemPara := range cols {
				SqlCol := this.getColParam(ItemPara.FieldName)
				setList = append(setList, SqlCol+"=excluded."+SqlCol)
			}

			return fmt.Sprintf("insert into %s ( %s ) values ( %s ) on conflict ( %s ) do update set %s",
				TablName, strings.Join(colList, ","), strings.Join(paramList, ","), strings.Join(keyList, ","), strings.Join(setList, ",")), true
		}

	case FmORACLE:
		{
			//#Oracle:MERGE,源数据放 dual 子查询,参数只传一遍
			var selectList []string
			ColIndex = 1
			for _, list := range [][]TUniField{keys, cols} {
				for _, ItemPara := range list {
					selectList = append(selectList, fmt.Sprintf("%s %s", this.getValParam(ColIndex), ItemPara.FieldName))
					ColIndex++
				}
			}

			var onList []string
			for _, ItemPara := range keys {
				onList = append(onList, "t."+ItemPara.FieldName+"=src."+ItemPara.FieldName)
			}

			var insertVals []string
			for _, list := range [][]TUniField{keys, cols} {
				for _, ItemPara := range list {
					insertVals = append(insertVals, "src."+ItemPara.FieldName)
				}
			}

			cSQL := fmt.Sprintf("merge into %s t using (select %s from dual) src on (%s)",
				TablName, strings.Join(selectList, ","), strings.Join(onList, " and "))

			if updateMode {
				var setList []string
				for _, ItemPara := range cols {
					setList = append(setList, "t."+ItemPara.FieldName+"=src."+ItemPara.FieldName)
				}
				cSQL = cSQL + " when matched then update set " + strings.Join(setList, ",")
			}

			return cSQL + " when not matched then insert ( " + strings.Join(colList, ",") + " ) values ( " + strings.Join(insertVals, ",") + " )", true
		}

	case FmSQLSRV:
		{
			//#SQLServer:MERGE + HOLDLOCK,表值构造器传参一遍
			var onList []string
			for _, ItemPara := range keys {
				SqlCol := this.getColParam(ItemPara.FieldName)
				onList = append(onList, "t."+SqlCol+"=src."+SqlCol)
			}

			var insertVals []string
			for _, list := range [][]TUniField{keys, cols} {
				for _, ItemPara := range list {
					insertVals = append(insertVals, "src."+this.getColParam(ItemPara.FieldName))
				}
			}

			cSQL := fmt.Sprintf("merge into %s with (holdlock) as t using (values ( %s )) as src ( %s ) on (%s)",
				TablName, strings.Join(paramList, ","), strings.Join(colList, ","), strings.Join(onList, " and "))

			if updateMode {
				var setList []string
				for _, ItemPara := range cols {
					SqlCol := this.getColParam(ItemPara.FieldName)
					setList = append(setList, "t."+SqlCol+"=src."+SqlCol)
				}
				cSQL = cSQL + " when matched then update set " + strings.Join(setList, ",")
			}

			return cSQL + " when not matched then insert ( " + strings.Join(colList, ",") + " ) values ( " + strings.Join(insertVals, ",") + " );", true
		}
	}

	return "", false
}

// canNativeSave 判断该类是否可走方言原生 UPSERT:任一自定义 SQL 钩子
// (GetSqlUpdate/GetSqlInsert/SetSqlValues)存在时,自定义语句无法转为 UPSERT,回退旧路径。

// canNativeSave 判断该类是否可走方言原生 UPSERT:任一自定义 SQL 钩子
// (GetSqlUpdate/GetSqlInsert/SetSqlValues)存在时,自定义语句无法转为 UPSERT,回退旧路径。
func canNativeSave(v reflect.Value) bool {

	if _, ok := v.Interface().(HasGetSqlUpdate); ok {
		return false
	}
	if _, ok := v.Interface().(HasGetSqlInsert); ok {
		return false
	}
	if _, ok := v.Interface().(HasSetSqlValues); ok {
		return false
	}

	return true
}

// saveUpsert 方言原生 UPSERT 执行(keys/cols 按确定序收集,参数只传一遍)。
// 返回 done=false 表示方言不支持,调用方回退 count-then-dispatch。

// saveUpsert 方言原生 UPSERT 执行(keys/cols 按确定序收集,参数只传一遍)。
// 返回 done=false 表示方言不支持,调用方回退 count-then-dispatch。
func (this *TUniEngine) saveUpsert(ctx context.Context, UniTable *TUniTable, v reflect.Value, TablName string, Mode int) (bool, error) {

	keys := UniTable.pkeyFields()

	var cols []TUniField
	for _, ItemPara := range UniTable.writableFields() {
		if _, valid := UniTable.HashPkeys[strings.ToLower(ItemPara.FieldName)]; valid {
			continue
		}
		cols = append(cols, ItemPara)
	}

	if len(keys) == 0 {
		return false, nil
	}

	cSQL, ok := this.upsertStmt(Mode, TablName, keys, cols)
	if !ok {
		return false, nil
	}

	//#主键值一律明文(on conflict 的匹配即 WHERE 语义);仅数据列参与应用加密
	values := make([]interface{}, 0, len(keys)+len(cols))
	for _, ItemPara := range keys {
		values = append(values, v.FieldByName(ItemPara.AttriName).Interface())
	}
	for _, ItemPara := range cols {
		if UniTable.secretColumn(ItemPara) {
			Value, eror := this.secretEncrypt(v.FieldByName(ItemPara.AttriName))
			if eror != nil {
				return false, eror
			}
			values = append(values, Value)
			continue
		}
		values = append(values, v.FieldByName(ItemPara.AttriName).Interface())
	}

	this.debugSQL("upsert", cSQL, values)

	st, eror := this.prepareCtx(ctx, cSQL)
	if eror != nil {
		return false, eror
	}
	defer st.Close()

	if _, eror := st.ExecContext(ctx, values...); eror != nil {
		return false, fmt.Errorf("UniEngine: upsert fail: %w", eror)
	}

	return true, nil
}

// countByPkeys 按主键统计存在行数(count-then-dispatch 回退路径的探测语句)。

// countByPkeys 按主键统计存在行数(count-then-dispatch 回退路径的探测语句)。
func (this *TUniEngine) countByPkeys(ctx context.Context, UniTable *TUniTable, v reflect.Value, TablName string) (int64, error) {

	var keyCols []string
	SqlValue := make([]interface{}, 0)

	ColIndex := 1
	for _, ItemPara := range UniTable.pkeyFields() {
		keyCols = append(keyCols, this.getColParam(ItemPara.FieldName)+"="+this.getValParam(ColIndex))
		SqlValue = append(SqlValue, v.FieldByName(ItemPara.AttriName).Interface())
		ColIndex = ColIndex + 1
	}

	if len(keyCols) == 0 {
		return 0, fmt.Errorf("%w: %s", ErrNoPkeys, UniTable.TableName)
	}

	SqlQuery := fmt.Sprintf("select count(1) from %s where %s", TablName, strings.Join(keyCols, " and "))

	return this.SelectDCtx(ctx, SqlQuery, SqlValue...)
}

// saveByCount 旧的 count-then-dispatch 路径:钩子类与不支持原生 UPSERT 的方言(MySQL)回退使用。

// saveByCount 旧的 count-then-dispatch 路径:钩子类与不支持原生 UPSERT 的方言(MySQL)回退使用。
func (this *TUniEngine) saveByCount(ctx context.Context, i interface{}, UniTable *TUniTable, v reflect.Value, TablName string, TableName []string, updateWhenExist bool) error {

	cCount, eror := this.countByPkeys(ctx, UniTable, v, TablName)
	if eror != nil {
		return eror
	}

	if this.debugging() {
		fmt.Println("UniEngine: select.cnt", cCount)
	}

	if updateWhenExist {
		if cCount == 1 {
			return this.UpdateCtx(ctx, i, TableName...)
		}
		return this.InsertCtx(ctx, i, TableName...)
	}

	if cCount == 0 {
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
		done, eror := this.saveUpsert(ctx, UniTable, v, TablName, upsertModeUpdate)
		if eror != nil {
			return eror
		}
		if done {
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
		done, eror := this.saveUpsert(ctx, UniTable, v, TablName, upsertModeSkip)
		if eror != nil {
			return eror
		}
		if done {
			return nil
		}
	}

	return this.saveByCount(ctx, i, UniTable, v, TablName, TableName, false)
}

// ---------------------------------------------------------------------------
// Update / Insert / InsertL / CopyInL / Delete
// ---------------------------------------------------------------------------
