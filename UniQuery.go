// UniQuery
// #查询:行扫描核心与 SelectD/F/S/L/M/H
package UniEngine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// ---------------------------------------------------------------------------
// 查询公共核心
// ---------------------------------------------------------------------------

// selectColsCtx 标量查询公共核心：执行并取最后一行的单列值（SelectD/F/S 共用）。
func (this *TUniEngine) selectColsCtx(ctx context.Context, SqlQuery string, dst sql.Scanner, args []interface{}) error {

	SqlQuery = this.getSqlQuery(SqlQuery, args...)

	this.sqlDebug("select", SqlQuery, args)

	st, eror := this.prepareCtx(ctx, SqlQuery)
	if eror != nil {
		return eror
	}
	defer st.Close()

	rows, eror := st.QueryContext(ctx, args...)
	if eror != nil {
		return eror
	}
	defer rows.Close()

	for rows.Next() {
		if eror = rows.Scan(dst); eror != nil {
			return eror
		}
	}

	//#rows.Next() 因错误提前结束(而非读完)时必须上抛,否则错误被静默吞掉、数据表现为截断
	return rows.Err()
}

// queryRowsCtx 行查询公共核心：逐行解码后交给 sink 写入目标容器
// （单个 struct / 切片 / map 由调用方决定）。HasSetSqlResult 钩子类的探测结果由
// 调用方传入,保持各方法原有的钩子识别语义。
// sink 第二参数为列→字段下标(热路径预计算结果,钩子路径传原始空切片)——
// 单 struct 目标用它做按列合并,其余容器目标忽略即可。
func (this *TUniEngine) queryRowsCtx(ctx context.Context, elemType reflect.Type, hasHook bool, SqlQuery string, sink func(reflect.Value, []int) error, args ...interface{}) error {

	SqlQuery = this.getSqlQuery(SqlQuery, args...)

	TablName := elemType.String()
	UniTable := this.tableByType(TablName)
	if UniTable == nil {
		return fmt.Errorf("%w: %s", ErrUnregisteredClass, TablName)
	}

	this.sqlDebug("select", SqlQuery, args)

	st, eror := this.prepareCtx(ctx, SqlQuery)
	if eror != nil {
		return eror
	}
	defer st.Close()

	rows, eror := st.QueryContext(ctx, args...)
	if eror != nil {
		return eror
	}
	defer rows.Close()

	column, eror := rows.Columns()
	if eror != nil {
		return eror
	}
	Count := len(column)
	fields := make([]interface{}, Count)
	values := make([]interface{}, Count)

	//#热路径预计算:列→字段下标一次解析,行循环内避免每行每列的 FieldByName 线性扫描
	fieldIdx := make([]int, Count)
	encryptIdx := make([]int, 0, 2)
	if !hasHook {
		for ColIndex, ItemPara := range column {
			UniField, Valid := UniTable.HashField[strings.ToLower(ItemPara)]
			if !Valid {
				return fmt.Errorf("UniEngine: database have field[%s], but not in class[%s]", ItemPara, elemType.String())
			}
			sf, ok := elemType.FieldByName(UniField.AttriName)
			if !ok {
				return fmt.Errorf("UniEngine: class[%s] has no attribute[%s]", elemType.String(), UniField.AttriName)
			}
			//#仅支持扁平类:内嵌结构体的提升字段 Index 长度>1,取 [0] 会错绑到内嵌字段本身
			if len(sf.Index) != 1 {
				return fmt.Errorf("UniEngine: class[%s] field[%s] is promoted from an embedded struct; flat classes only", elemType.String(), UniField.AttriName)
			}
			fieldIdx[ColIndex] = sf.Index[0]

			if UniField.SecretOn {
				encryptIdx = append(encryptIdx, sf.Index[0])
			}
		}
	}

	for rows.Next() {

		u := reflect.New(elemType)

		if hasHook {
			for i := 0; i < Count; i++ {
				values[i] = &fields[i]
			}

			if eror = rows.Scan(values...); eror != nil {
				return eror
			}

			x := u.Interface().(HasSetSqlResult)
			x.SetSqlResult(this, u.Interface(), column, fields)
		} else {
			elem := u.Elem()
			for ColIndex := range column {
				values[ColIndex] = elem.Field(fieldIdx[ColIndex]).Addr().Interface()
			}

			if eror = rows.Scan(values...); eror != nil {
				return eror
			}

			if eror = this.decryptRow(elem, encryptIdx); eror != nil {
				return eror
			}
		}

		if eror = sink(u.Elem(), fieldIdx); eror != nil {
			return eror
		}
	}

	//#rows.Next() 因错误提前结束(而非读完)时必须上抛,否则错误被静默吞掉、数据表现为截断
	return rows.Err()
}

// return int64;

// return int64;
func (this *TUniEngine) SelectD(SqlQuery string, args ...interface{}) (int64, error) {
	return this.SelectDCtx(context.Background(), SqlQuery, args...)
}

func (this *TUniEngine) SelectDCtx(ctx context.Context, SqlQuery string, args ...interface{}) (int64, error) {

	var size sql.NullInt64
	if eror := this.selectColsCtx(ctx, SqlQuery, &size, args); eror != nil {
		return 0, eror
	}

	return size.Int64, nil
}

// return float64;

// return float64;
func (this *TUniEngine) SelectF(SqlQuery string, args ...interface{}) (float64, error) {
	return this.SelectFCtx(context.Background(), SqlQuery, args...)
}

func (this *TUniEngine) SelectFCtx(ctx context.Context, SqlQuery string, args ...interface{}) (float64, error) {

	var size sql.NullFloat64
	if eror := this.selectColsCtx(ctx, SqlQuery, &size, args); eror != nil {
		return 0, eror
	}

	return size.Float64, nil
}

// return string;

// return string;
func (this *TUniEngine) SelectS(SqlQuery string, args ...interface{}) (string, error) {
	return this.SelectSCtx(context.Background(), SqlQuery, args...)
}

func (this *TUniEngine) SelectSCtx(ctx context.Context, SqlQuery string, args ...interface{}) (string, error) {

	var text sql.NullString
	if eror := this.selectColsCtx(ctx, SqlQuery, &text, args); eror != nil {
		return "", eror
	}

	return text.String, nil
}

// return struct;查询返回多行时报错(旧版静默保留最后一行),需要多行请用 SelectL。

// return struct;查询返回多行时报错(旧版静默保留最后一行),需要多行请用 SelectL。
func (this *TUniEngine) Select(i interface{}, SqlQuery string, args ...interface{}) error {
	return this.SelectCtx(context.Background(), i, SqlQuery, args...)
}

func (this *TUniEngine) SelectCtx(ctx context.Context, i interface{}, SqlQuery string, args ...interface{}) error {

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Struct {
		return errors.New("UniEngine: method [Select] only returns a struct; may be you should try [SelectL]")
	}

	var Result = reflect.Indirect(reflect.ValueOf(i))

	//#钩子探测保持旧语义:以传入值本身是否实现 HasSetSqlResult 为准
	_, hasHook := i.(HasSetSqlResult)

	//#单 struct 目标按列合并:仅 SELECT 列覆盖目标字段,未出现在结果列里的字段
	//#保留调用方原值(修"SELECT 少量列灌回业务结构体"把其余字段清零的坑,如
	//#VerifyDX 的 FROM_WHO_BUILD);钩子类仍整结构体替换,填充范围由类自身
	//#SetSqlResult 决定;零行结果不触发 sink,目标原样保留
	rowCount := 0
	return this.queryRowsCtx(ctx, t, hasHook, SqlQuery, func(row reflect.Value, fieldIdx []int) error {
		rowCount++
		if rowCount > 1 {
			return errors.New("UniEngine: method [Select] expects a single row, but the query returned more; use [SelectL]")
		}
		if hasHook {
			Result.Set(row)
			return nil
		}
		for ColIndex := range fieldIdx {
			Result.Field(fieldIdx[ColIndex]).Set(row.Field(fieldIdx[ColIndex]))
		}
		return nil
	}, args...)
}

// return slice of struct;

// return slice of struct;
func (this *TUniEngine) SelectL(i interface{}, SqlQuery string, args ...interface{}) error {
	return this.SelectLCtx(context.Background(), i, SqlQuery, args...)
}

func (this *TUniEngine) SelectLCtx(ctx context.Context, i interface{}, SqlQuery string, args ...interface{}) error {

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Slice {
		return errors.New("UniEngine: method [SelectL] needs a slice param; may be you should try [Select]")
	}
	t = t.Elem()

	var Result = reflect.Indirect(reflect.ValueOf(i))

	return this.queryRowsCtx(ctx, t, t.Implements(THasSetSqlResult), SqlQuery, func(row reflect.Value, _ []int) error {
		Result.Set(reflect.Append(Result, row))
		return nil
	}, args...)
}

// return map of struct;user;GetMapUnique;

// return map of struct;user;GetMapUnique;
func (this *TUniEngine) SelectM(i interface{}, SqlQuery string, args ...interface{}) error {
	return this.SelectMCtx(context.Background(), i, SqlQuery, args...)
}

func (this *TUniEngine) SelectMCtx(ctx context.Context, i interface{}, SqlQuery string, args ...interface{}) error {

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Map {
		return errors.New("UniEngine: method [SelectM] needs a map param; may be you should try [SelectL]")
	}
	t = t.Elem()

	if !t.Implements(THasGetMapUnique) {
		return fmt.Errorf("UniEngine: the class registered:[%s] does not Implemented [HasGetMapUnique]", t.String())
	}

	var Result = reflect.Indirect(reflect.ValueOf(i))

	return this.queryRowsCtx(ctx, t, t.Implements(THasSetSqlResult), SqlQuery, func(row reflect.Value, _ []int) error {
		MapUnique := row.Interface().(HasGetMapUnique).GetMapUnique()
		Result.SetMapIndex(reflect.ValueOf(MapUnique), row)
		return nil
	}, args...)
}

// return map;use custom function;

// return map;use custom function;
func (this *TUniEngine) SelectH(i interface{}, f GetMapUnique, SqlQuery string, args ...interface{}) error {
	return this.SelectHCtx(context.Background(), i, f, SqlQuery, args...)
}

func (this *TUniEngine) SelectHCtx(ctx context.Context, i interface{}, f GetMapUnique, SqlQuery string, args ...interface{}) error {

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Map {
		return errors.New("UniEngine: method [SelectH] needs a map param; may be you should try [SelectL]")
	}
	t = t.Elem()

	var Result = reflect.Indirect(reflect.ValueOf(i))

	return this.queryRowsCtx(ctx, t, t.Implements(THasSetSqlResult), SqlQuery, func(row reflect.Value, _ []int) error {
		var MapUnique string
		if f != nil {
			MapUnique = f(row.Interface())
		}
		Result.SetMapIndex(reflect.ValueOf(MapUnique), row)
		return nil
	}, args...)
}

// ---------------------------------------------------------------------------
// 写入公共前置
// ---------------------------------------------------------------------------

// resolveTarget 统一写方法的公共前置：解析可选的表名参数(TableName[0],空切片时用
// 注册表中的默认表名)、反射取元素类型并查注册表、标识符校验。
// wantKind 指定期望的容器种类(Insert 传 Struct,InsertL/CopyInL 传 Slice,
// 无需检查传 reflect.Invalid);needPkeys 要求类已注册主键。
