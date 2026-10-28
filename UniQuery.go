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

// selectColsCtx 标量查询公共核心：执行并取单行单列值（SelectD/F/S 共用）。
// 多行结果显式报错:静默取最后一行会让"条件写漏"表现为偶发取错金额/状态,极难排查。
func (this *TUniEngine) selectColsCtx(Ctx context.Context, SqlQuery string, Dst sql.Scanner, Args []interface{}) error {
	SqlQuery, Args, eror := this.getSqlQuery(SqlQuery, Args...)
	if eror != nil {
		return eror
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

	RowCount := 0
	for Rows.Next() {
		RowCount++
		if RowCount > 1 {
			return fmt.Errorf("%w: scalar SelectD/F/S expects a single row; use SelectL or add a uniqueness predicate", ErrMultipleRows)
		}
		if eror = Rows.Scan(Dst); eror != nil {
			return eror
		}
	}

	//#Rows.Next() 因错误提前结束(而非读完)时必须上抛,否则错误被静默吞掉、数据表现为截断
	return Rows.Err()
}

// selectRowsCtx 行查询公共核心,Select/L/M/H 共用：逐行解码后交给 sink 写入目标容器
// （单个 struct / 切片 / map 由调用方决定）。HasSetSqlResult 钩子类的探测结果由
// 调用方传入,保持各方法原有的钩子识别语义。Method 为触发入口名,仅用于 StatusHook
// 定位(SelectCtx 等变体与同名方法一致)。
// sink 第二参数为列→字段下标(热路径预计算结果,钩子路径传原始空切片)——
// 单 struct 目标用它做按列合并,其余容器目标忽略即可。
func (this *TUniEngine) selectRowsCtx(Ctx context.Context, elemType reflect.Type, HasHook bool, Method string, SqlQuery string, sink func(reflect.Value, []int) error, Args ...interface{}) error {
	SqlQuery, Args, eror := this.getSqlQuery(SqlQuery, Args...)
	if eror != nil {
		return eror
	}

	TablName := elemType.String()
	UniTable := this.tableByType(TablName)
	if UniTable == nil {
		return fmt.Errorf("%w: %s", ErrUnregisteredClass, TablName)
	}
	//#结果钩子路径验签:钩子接管整行填充,但引擎仍持有结果列清单,按与默认路径
	//#相同的"列覆盖"口径预解析验签上下文,并在钩子填好结构体后补一次验签;
	//#钩子未投影保护列/MAC 列时,严格模式报 ErrIncompleteVerification、宽容模式
	//#沿用跳过语义。加密侧:secreton 结果列在交接钩子前解密(见下)

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

	//#热路径预计算:列→字段下标一次解析,行循环内避免每行每列的 FieldByName 线性扫描。
	//#非钩子路径要求结果列全部能在类中定位;钩子路径允许钩子自取计算列/别名,
	//#只映射能识别的列(验签只关心保护列与 MAC 列,其余列由钩子自行消费)。
	FieldIdx := make([]int, length)
	for Idx := range FieldIdx {
		FieldIdx[Idx] = -1
	}
	EncryIdx := make([]int, 0, 2)

	for ColIndex, ItemPara := range Column {
		UniField, Valid := UniTable.HashField[strings.ToLower(ItemPara)]
		if !Valid {
			if HasHook {
				continue
			}
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

		if !HasHook && UniField.SecretOn {
			EncryIdx = append(EncryIdx, Sf.Index[0])
		}
	}

	//#完整性验签预解析:SELECT 列覆盖全部保护列与 MAC 列时返回上下文;
	//#宽容模式下部分列查询沿用跳过验签语义,严格模式返回 ErrIncompleteVerification。
	RowVerify, eror := this.verifyCtxOf(UniTable, Column, FieldIdx)
	if eror != nil {
		//#投影不足=无法验签:严格模式在读到任何行之前即失败,行号记 -1 交给 StatusHook
		this.verifyStatus(TUniStatus{Table: UniTable.TableName, Method: Method, Row: -1, Err: eror}, nil, reflect.Value{})
		return eror
	}

	//#结果钩子路径的解密列预解析:secreton 结果列在交接钩子前还原明文,
	//#钩子与默认解码路径(EncryIdx→decryptRow)拿同一口径的数据
	HookDecryIdx := make([]int, 0, 2)
	if HasHook && this.SecretOn == 1 {
		for ColIndex, ItemPara := range Column {
			if UniField, Valid := UniTable.HashField[strings.ToLower(ItemPara)]; Valid && UniField.SecretOn {
				HookDecryIdx = append(HookDecryIdx, ColIndex)
			}
		}
	}

	RowNo := -1
	for Rows.Next() {
		RowNo++
		u := reflect.New(elemType)

		if HasHook {
			for i := 0; i < length; i++ {
				Values[i] = &Fields[i]
			}

			if eror = Rows.Scan(Values...); eror != nil {
				return eror
			}

			//#SelectL 时对数据解密:secreton 结果列在交接钩子前还原明文,失败报错
			//#中断整次查询;无标签存量明文经 Secret 直通,与默认路径同口径
			for _, Idx := range HookDecryIdx {
				Raw, ok := Fields[Idx].(string)
				if !ok {
					if RawBytes, IsBytes := Fields[Idx].([]byte); IsBytes {
						Raw, ok = string(RawBytes), true
					}
				}
				if !ok {
					continue
				}
				PlainText, eror := this.Secret(Raw, false)
				if eror != nil {
					return eror
				}
				Fields[Idx] = PlainText
			}

			x := u.Interface().(HasSetSqlResult)
			x.SetSqlResult(this, u.Interface(), Column, Fields)

			//#钩子填好结构体后补验签:载荷与默认路径同一口径(表名域分隔+保护列明文);
			//#列覆盖不足时 verifyCtxOf 已按严格/宽容语义处理(RowVerify 为 nil 即跳过)
			if eror = this.verifyRow(u.Elem(), RowVerify, Method, RowNo); eror != nil {
				return eror
			}
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
			if eror = this.verifyRow(Elem, RowVerify, Method, RowNo); eror != nil {
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
func (this *TUniEngine) Select(i interface{}, SqlQuery string, Args ...interface{}) error {
	return this.SelectCtx(context.Background(), i, SqlQuery, Args...)
}

func (this *TUniEngine) SelectCtx(Ctx context.Context, i interface{}, SqlQuery string, Args ...interface{}) error {
	//#nil 校验必须先于 reflect.TypeOf/Kind(评审 P0)
	if eror := nilParamOf("Select", i); eror != nil {
		return eror
	}

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Struct {
		return errors.New("UniEngine: method [Select] only returns a struct; may be you should try [SelectL]")
	}

	//#单 struct 目标需要可寻址(逐列回写),传值会 reflect.Value.Set panic
	if reflect.ValueOf(i).Kind() != reflect.Ptr {
		return errors.New("UniEngine: method [Select] needs a pointer param; check your code;")
	}

	var Result = reflect.Indirect(reflect.ValueOf(i))

	//#钩子探测统一走 hookOf:值接收者与指针接收者一视同仁
	_, HasHook := hookOf[HasSetSqlResult](reflect.ValueOf(i))

	//#单 struct 目标按列合并:仅 SELECT 列覆盖目标字段,未出现在结果列里的字段
	//#保留调用方原值(修"SELECT 少量列灌回业务结构体"把其余字段清零的坑,如
	//#VerifyDX 的 FROM_WHO_BUILD);钩子类仍整结构体替换,填充范围由类自身
	//#SetSqlResult 决定;零行结果不触发 sink,目标原样保留
	RowCount := 0
	return this.selectRowsCtx(Ctx, t, HasHook, "Select", SqlQuery, func(Row reflect.Value, FieldIdx []int) error {
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
func (this *TUniEngine) SelectL(i interface{}, SqlQuery string, Args ...interface{}) error {
	return this.SelectLCtx(context.Background(), i, SqlQuery, Args...)
}

func (this *TUniEngine) SelectLCtx(Ctx context.Context, i interface{}, SqlQuery string, Args ...interface{}) error {
	//#nil 校验必须先于 reflect.TypeOf/Kind(评审 P0);typed nil 切片指针由此拦截
	if eror := nilParamOf("SelectL", i); eror != nil {
		return eror
	}

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Slice {
		return errors.New("UniEngine: method [SelectL] needs a slice param; may be you should try [Select]")
	}

	//#切片逐行 Append 回写需要可寻址,传值会 reflect.Value.Set panic
	if reflect.ValueOf(i).Kind() != reflect.Ptr {
		return errors.New("UniEngine: method [SelectL] needs a pointer param; check your code;")
	}
	t = t.Elem()

	var Result = reflect.Indirect(reflect.ValueOf(i))

	//#整批语义:查询中途失败(扫描错误/rows.Err)时回滚本次已追加的行,
	//#与 README/doc.go 的"SelectL 失败即整批丢弃"保持一致;调用方预置的元素不动。
	StartLen := Result.Len()
	eror := this.selectRowsCtx(Ctx, t, typeHasHook(t, THasSetSqlResult), "SelectL", SqlQuery, func(Row reflect.Value, _ []int) error {
		Result.Set(reflect.Append(Result, Row))
		return nil
	}, Args...)
	if eror != nil {
		Result.SetLen(StartLen)
		return eror
	}

	return nil
}

// return map of struct;User;GetMapUnique;
func (this *TUniEngine) SelectM(i interface{}, SqlQuery string, Args ...interface{}) error {
	return this.SelectMCtx(context.Background(), i, SqlQuery, Args...)
}

func (this *TUniEngine) SelectMCtx(Ctx context.Context, i interface{}, SqlQuery string, Args ...interface{}) error {
	//#nil 校验必须先于 reflect.TypeOf/Kind(评审 P0)
	if eror := nilParamOf("SelectM", i); eror != nil {
		return eror
	}

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Map {
		return errors.New("UniEngine: method [SelectM] needs a map param; may be you should try [SelectL]")
	}

	//#键语义校验(对抗性复查):Kind==String 只是"类型种类"近似——命名字符串类型
	//#(type MyKey string)Kind 仍为 String,直接写 string 值会不可赋值 panic;
	//#map[any]T 则 string 本可赋值,Kind 拒绝属误杀。三分支:
	//#string 可直接赋值(any/接口) → 直写;命名 string 类型 → 写时 Convert;其余 → 报错
	KeyType := t.Key()
	KeyDirect := reflect.TypeOf("").AssignableTo(KeyType)
	KeyConvert := !KeyDirect && KeyType.Kind() == reflect.String
	if !KeyDirect && !KeyConvert {
		return fmt.Errorf("UniEngine: method [SelectM] needs a map key assignable from string (string key or named string type), but got key type [%s]; check your code;", KeyType)
	}

	//#nil map(直接传入或经指针间接)无法写入,SetMapIndex 会 panic
	if Map := reflect.Indirect(reflect.ValueOf(i)); Map.Kind() == reflect.Map && Map.IsNil() {
		return errors.New("UniEngine: method [SelectM] got a nil map; initialize the map before calling; check your code;")
	}
	t = t.Elem()

	if !typeHasHook(t, THasGetMapUnique) {
		return fmt.Errorf("UniEngine: the class registered:[%s] does not Implemented [HasGetMapUnique]", t.String())
	}

	var Result = reflect.Indirect(reflect.ValueOf(i))

	return this.selectRowsCtx(Ctx, t, typeHasHook(t, THasSetSqlResult), "SelectM", SqlQuery, func(Row reflect.Value, _ []int) error {
		UniqueHook, Ok := hookOf[HasGetMapUnique](Row)
		if !Ok {
			return fmt.Errorf("UniEngine: class[%s] does not Implemented [HasGetMapUnique]", t.String())
		}
		MapUnique := UniqueHook.GetMapUnique()
		KeyValue := reflect.ValueOf(MapUnique)
		if !KeyDirect {
			KeyValue = KeyValue.Convert(KeyType)
		}
		Result.SetMapIndex(KeyValue, Row)
		return nil
	}, Args...)
}

// return map;use custom function;
func (this *TUniEngine) SelectH(i interface{}, f GetMapUnique, SqlQuery string, Args ...interface{}) error {
	return this.SelectHCtx(context.Background(), i, f, SqlQuery, Args...)
}

func (this *TUniEngine) SelectHCtx(Ctx context.Context, i interface{}, f GetMapUnique, SqlQuery string, Args ...interface{}) error {
	//#nil 校验必须先于 reflect.TypeOf/Kind(评审 P0)
	if eror := nilParamOf("SelectH", i); eror != nil {
		return eror
	}

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Map {
		return errors.New("UniEngine: method [SelectH] needs a map param; may be you should try [SelectL]")
	}

	//#键语义校验同 SelectM 三分支(对抗性复查:Kind 近似会漏命名 string 键/误杀 map[any]T)
	KeyType := t.Key()
	KeyDirect := reflect.TypeOf("").AssignableTo(KeyType)
	KeyConvert := !KeyDirect && KeyType.Kind() == reflect.String
	if !KeyDirect && !KeyConvert {
		return fmt.Errorf("UniEngine: method [SelectH] needs a map key assignable from string (string key or named string type), but got key type [%s]; check your code;", KeyType)
	}
	if Map := reflect.Indirect(reflect.ValueOf(i)); Map.Kind() == reflect.Map && Map.IsNil() {
		return errors.New("UniEngine: method [SelectH] got a nil map; initialize the map before calling; check your code;")
	}
	//#nil 键函数会让所有行折叠到键 "",map 静默只剩一条;在容器/键校验之后拒绝,
	//#保证 nil map / 非法键仍先报各自更具体的错误。
	if f == nil {
		return errors.New("UniEngine: method [SelectH] got a nil key function; check your code;")
	}
	t = t.Elem()

	var Result = reflect.Indirect(reflect.ValueOf(i))

	return this.selectRowsCtx(Ctx, t, typeHasHook(t, THasSetSqlResult), "SelectH", SqlQuery, func(Row reflect.Value, _ []int) error {
		MapUnique := f(Row.Interface())
		KeyValue := reflect.ValueOf(MapUnique)
		if !KeyDirect {
			KeyValue = KeyValue.Convert(KeyType)
		}
		Result.SetMapIndex(KeyValue, Row)
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
