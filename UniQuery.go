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
func (this *TUniEngine) selectColsCtx(Ctx context.Context, SqlQuery string, Dst sql.Scanner, Args []interface{}) error {

	SqlQuery = this.getSqlQuery(SqlQuery, Args...)

	this.sqlDebug("select", SqlQuery, Args)

	St, eror := this.prepareCtx(Ctx, SqlQuery)
	if eror != nil {
		return eror
	}
	defer St.Close()

	Rows, eror := St.QueryContext(Ctx, Args...)
	if eror != nil {
		return eror
	}
	defer Rows.Close()

	for Rows.Next() {
		if eror = Rows.Scan(Dst); eror != nil {
			return eror
		}
	}

	//#Rows.Next() 因错误提前结束(而非读完)时必须上抛,否则错误被静默吞掉、数据表现为截断
	return Rows.Err()
}

// selectRowsCtx 行查询公共核心,Select/L/M/H 共用：逐行解码后交给 sink 写入目标容器
// （单个 struct / 切片 / map 由调用方决定）。HasSetSqlResult 钩子类的探测结果由
// 调用方传入,保持各方法原有的钩子识别语义。
// sink 第二参数为列→字段下标(热路径预计算结果,钩子路径传原始空切片)——
// 单 struct 目标用它做按列合并,其余容器目标忽略即可。
func (this *TUniEngine) selectRowsCtx(Ctx context.Context, elemType reflect.Type, HasHook bool, SqlQuery string, sink func(reflect.Value, []int) error, Args ...interface{}) error {

	SqlQuery = this.getSqlQuery(SqlQuery, Args...)

	TablName := elemType.String()
	UniTable := this.tableByType(TablName)
	if UniTable == nil {
		return fmt.Errorf("%w: %s", ErrUnregisteredClass, TablName)
	}

	this.sqlDebug("select", SqlQuery, Args)

	St, eror := this.prepareCtx(Ctx, SqlQuery)
	if eror != nil {
		return eror
	}
	defer St.Close()

	Rows, eror := St.QueryContext(Ctx, Args...)
	if eror != nil {
		return eror
	}
	defer Rows.Close()

	Column, eror := Rows.Columns()
	if eror != nil {
		return eror
	}
	length := len(Column)
	Fields := make([]interface{}, length)
	Values := make([]interface{}, length)

	//#热路径预计算:列→字段下标一次解析,行循环内避免每行每列的 FieldByName 线性扫描
	FieldIdx := make([]int, length)
	EncryIdx := make([]int, 0, 2)

	//#完整性验签预解析:SELECT 列覆盖全部保护列与 MAC 列时返回上下文,
	//#否则为 nil(partial select 无法还原载荷,该行跳过验签)
	var RowVerify *verifyRowCtx

	if !HasHook {
		for ColIndex, ItemPara := range Column {
			UniField, Valid := UniTable.HashField[strings.ToLower(ItemPara)]
			if !Valid {
				return fmt.Errorf("UniEngine: database have field[%s], but not in class[%s]", ItemPara, elemType.String())
			}
			Sf, Ok := elemType.FieldByName(UniField.AttriName)
			if !Ok {
				return fmt.Errorf("UniEngine: class[%s] has no attribute[%s]", elemType.String(), UniField.AttriName)
			}
			//#仅支持扁平类:内嵌结构体的提升字段 Index 长度>1,取 [0] 会错绑到内嵌字段本身
			if len(Sf.Index) != 1 {
				return fmt.Errorf("UniEngine: class[%s] field[%s] is promoted from an embedded struct; flat classes only", elemType.String(), UniField.AttriName)
			}
			FieldIdx[ColIndex] = Sf.Index[0]

			if UniField.SecretOn {
				EncryIdx = append(EncryIdx, Sf.Index[0])
			}
		}

		RowVerify = this.verifyCtxOf(UniTable, Column, FieldIdx)
	}

	for Rows.Next() {

		u := reflect.New(elemType)

		if HasHook {
			for i := 0; i < length; i++ {
				Values[i] = &Fields[i]
			}

			if eror = Rows.Scan(Values...); eror != nil {
				return eror
			}

			x := u.Interface().(HasSetSqlResult)
			x.SetSqlResult(this, u.Interface(), Column, Fields)
		} else {
			Elem := u.Elem()
			for ColIndex := range Column {
				Values[ColIndex] = Elem.Field(FieldIdx[ColIndex]).Addr().Interface()
			}

			if eror = Rows.Scan(Values...); eror != nil {
				return eror
			}

			if eror = this.decryptRow(Elem, EncryIdx); eror != nil {
				return eror
			}

			//#完整性验签:先解密后验签(secreton 列参与载荷的是明文)
			if eror = this.verifyRow(Elem, RowVerify); eror != nil {
				return eror
			}
		}

		if eror = sink(u.Elem(), FieldIdx); eror != nil {
			return eror
		}
	}

	//#Rows.Next() 因错误提前结束(而非读完)时必须上抛,否则错误被静默吞掉、数据表现为截断
	return Rows.Err()
}

// return int64;

// return int64;
func (this *TUniEngine) SelectD(SqlQuery string, Args ...interface{}) (int64, error) {
	return this.SelectDCtx(context.Background(), SqlQuery, Args...)
}

func (this *TUniEngine) SelectDCtx(Ctx context.Context, SqlQuery string, Args ...interface{}) (int64, error) {

	var Size sql.NullInt64
	if eror := this.selectColsCtx(Ctx, SqlQuery, &Size, Args); eror != nil {
		return 0, eror
	}

	return Size.Int64, nil
}

// return float64;

// return float64;
func (this *TUniEngine) SelectF(SqlQuery string, Args ...interface{}) (float64, error) {
	return this.SelectFCtx(context.Background(), SqlQuery, Args...)
}

func (this *TUniEngine) SelectFCtx(Ctx context.Context, SqlQuery string, Args ...interface{}) (float64, error) {

	var Size sql.NullFloat64
	if eror := this.selectColsCtx(Ctx, SqlQuery, &Size, Args); eror != nil {
		return 0, eror
	}

	return Size.Float64, nil
}

// return string;

// return string;
func (this *TUniEngine) SelectS(SqlQuery string, Args ...interface{}) (string, error) {
	return this.SelectSCtx(context.Background(), SqlQuery, Args...)
}

func (this *TUniEngine) SelectSCtx(Ctx context.Context, SqlQuery string, Args ...interface{}) (string, error) {

	var Text sql.NullString
	if eror := this.selectColsCtx(Ctx, SqlQuery, &Text, Args); eror != nil {
		return "", eror
	}

	//#标量路径同步支持应用解密,与列路径(selectRowsCtx/decryptRow)同一套语义:
	//#SecretOn=0 直通;带 ENC: 标签的密文还原;存量明文(无标签)直通
	return this.Secret(Text.String, false)
}

// return struct;查询返回多行时报错(旧版静默保留最后一行),需要多行请用 SelectL。

// return struct;查询返回多行时报错(旧版静默保留最后一行),需要多行请用 SelectL。
func (this *TUniEngine) Select(i interface{}, SqlQuery string, Args ...interface{}) error {
	return this.SelectCtx(context.Background(), i, SqlQuery, Args...)
}

func (this *TUniEngine) SelectCtx(Ctx context.Context, i interface{}, SqlQuery string, Args ...interface{}) error {

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Struct {
		return errors.New("UniEngine: method [Select] only returns a struct; may be you should try [SelectL]")
	}

	var Result = reflect.Indirect(reflect.ValueOf(i))

	//#钩子探测保持旧语义:以传入值本身是否实现 HasSetSqlResult 为准
	_, HasHook := i.(HasSetSqlResult)

	//#单 struct 目标按列合并:仅 SELECT 列覆盖目标字段,未出现在结果列里的字段
	//#保留调用方原值(修"SELECT 少量列灌回业务结构体"把其余字段清零的坑,如
	//#VerifyDX 的 FROM_WHO_BUILD);钩子类仍整结构体替换,填充范围由类自身
	//#SetSqlResult 决定;零行结果不触发 sink,目标原样保留
	RowCount := 0
	return this.selectRowsCtx(Ctx, t, HasHook, SqlQuery, func(Row reflect.Value, FieldIdx []int) error {
		RowCount++
		if RowCount > 1 {
			return errors.New("UniEngine: method [Select] expects a single row, but the query returned more; use [SelectL]")
		}
		if HasHook {
			Result.Set(Row)
			return nil
		}
		for ColIndex := range FieldIdx {
			Result.Field(FieldIdx[ColIndex]).Set(Row.Field(FieldIdx[ColIndex]))
		}
		return nil
	}, Args...)
}

// return slice of struct;

// return slice of struct;
func (this *TUniEngine) SelectL(i interface{}, SqlQuery string, Args ...interface{}) error {
	return this.SelectLCtx(context.Background(), i, SqlQuery, Args...)
}

func (this *TUniEngine) SelectLCtx(Ctx context.Context, i interface{}, SqlQuery string, Args ...interface{}) error {

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Slice {
		return errors.New("UniEngine: method [SelectL] needs a slice param; may be you should try [Select]")
	}
	t = t.Elem()

	var Result = reflect.Indirect(reflect.ValueOf(i))

	return this.selectRowsCtx(Ctx, t, t.Implements(THasSetSqlResult), SqlQuery, func(Row reflect.Value, _ []int) error {
		Result.Set(reflect.Append(Result, Row))
		return nil
	}, Args...)
}

// return map of struct;User;GetMapUnique;

// return map of struct;User;GetMapUnique;
func (this *TUniEngine) SelectM(i interface{}, SqlQuery string, Args ...interface{}) error {
	return this.SelectMCtx(context.Background(), i, SqlQuery, Args...)
}

func (this *TUniEngine) SelectMCtx(Ctx context.Context, i interface{}, SqlQuery string, Args ...interface{}) error {

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

	return this.selectRowsCtx(Ctx, t, t.Implements(THasSetSqlResult), SqlQuery, func(Row reflect.Value, _ []int) error {
		MapUnique := Row.Interface().(HasGetMapUnique).GetMapUnique()
		Result.SetMapIndex(reflect.ValueOf(MapUnique), Row)
		return nil
	}, Args...)
}

// return map;use custom function;

// return map;use custom function;
func (this *TUniEngine) SelectH(i interface{}, f GetMapUnique, SqlQuery string, Args ...interface{}) error {
	return this.SelectHCtx(context.Background(), i, f, SqlQuery, Args...)
}

func (this *TUniEngine) SelectHCtx(Ctx context.Context, i interface{}, f GetMapUnique, SqlQuery string, Args ...interface{}) error {

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Map {
		return errors.New("UniEngine: method [SelectH] needs a map param; may be you should try [SelectL]")
	}
	t = t.Elem()

	var Result = reflect.Indirect(reflect.ValueOf(i))

	return this.selectRowsCtx(Ctx, t, t.Implements(THasSetSqlResult), SqlQuery, func(Row reflect.Value, _ []int) error {
		var MapUnique string
		if f != nil {
			MapUnique = f(Row.Interface())
		}
		Result.SetMapIndex(reflect.ValueOf(MapUnique), Row)
		return nil
	}, Args...)
}

// ---------------------------------------------------------------------------
// 写入公共前置
// ---------------------------------------------------------------------------

// resolveTarget 统一写方法的公共前置：解析可选的表名参数(TableName[0],空切片时用
// 注册表中的默认表名)、反射取元素类型并查注册表、标识符校验。
// WantKind 指定期望的容器种类(Insert 传 Struct,InsertL/CopyInL 传 Slice,
// 无需检查传 reflect.Invalid);NeedPkeys 要求类已注册主键。
