// UniVerify
// #存储完整性校验:写路径对保护列算 HMAC 回填伴随列,读路径逐行验签

package UniEngine

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// #应用完整性钩子:输入规范化载荷,返回 MAC 原始字节。
// #应用可实现自己的 MAC 算法(如 HMAC-SM3/对接密码机),
// #然后赋值给 TUniEngine.VerifyHook,即可接管全部保护列的签名与验签
type TVerifyHook func(Payload []byte) ([]byte, error)

const (
	// #MAC 标签:内置 HMAC-SHA256,载荷=表名+登记序保护列值(分隔符 verifySep);
	// #读取自识别,带标签的值必验,不带标签的存量行按 VerifyMode 处理
	UniVerifyTag = "MAC:"
	// #载荷分隔符(ASCII 单元分隔符):授权键/状态列取值均为业务字符,不含该控制符
	verifySep = "\x1f"
)

// #verifyRowCtx 读路径逐行验签的预解析上下文(查询列覆盖保护列+MAC列时才非 nil;
// #列不全的查询无法验签,整行跳过——与 decryptRow 的 EncryIdx 同一预计算模式)
type verifyRowCtx struct {
	TablName string //#注册表名,仅用于报错定位
	MacIdx   int    //#MAC伴随列的结构体字段下标
	Cols     []int  //#保护列的结构体字段下标,顺序=SetVerify登记顺序=载荷顺序
}

// #verifyActive 引擎与表两侧开关同时打开才生效
func (this *TUniEngine) verifyActive(UniTable *TUniTable) bool {
	if this.VerifyOn == 0 || UniTable == nil || !UniTable.VerifyOn {
		return false
	}

	return UniTable.VerifyAt != "" && len(UniTable.ColVerify) > 0
}

// #verifyStrict 严格模式:无标签的存量行按篡改处理(存量回填完成后切换)
func (this *TUniEngine) verifyStrict() bool {
	return this.VerifyMode == 2
}

// #verifySign MAC 计算:VerifyHook 非空走应用钩子,否则内置 HMAC-SHA256(VerifyBy)
func (this *TUniEngine) verifySign(Payload []byte) ([]byte, error) {
	if this.VerifyHook != nil {
		return this.VerifyHook(Payload)
	}

	if this.VerifyBy == "" {
		return nil, errors.New("UniEngine: VerifyOn is on, but VerifyBy is empty")
	}

	Mac := hmac.New(sha256.New, []byte(this.VerifyBy))
	Mac.Write(Payload)

	return Mac.Sum(nil), nil
}

// #verifyFormat 保护列值的规范化:定长类型走 strconv 确定序格式(同值必同串),
// #不支持的类型 fast-fail——静默降级会产出验不过的行
func verifyFormat(FieldValue reflect.Value) ([]byte, error) {
	switch FieldValue.Kind() {
	case reflect.String:
		return []byte(FieldValue.String()), nil

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return []byte(strconv.FormatInt(FieldValue.Int(), 10)), nil

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return []byte(strconv.FormatUint(FieldValue.Uint(), 10)), nil

	case reflect.Float32:
		return []byte(strconv.FormatFloat(FieldValue.Float(), 'g', -1, 32)), nil

	case reflect.Float64:
		return []byte(strconv.FormatFloat(FieldValue.Float(), 'g', -1, 64)), nil

	case reflect.Bool:
		if FieldValue.Bool() {
			return []byte("1"), nil
		}
		return []byte("0"), nil
	}

	return nil, fmt.Errorf("UniEngine: verify does not support field kind [%s]", FieldValue.Kind().String())
}

// #verifyPayload 规范化载荷:注册表名 + 分隔符 + 登记序保护列值(域分隔防跨表移植 MAC)
func verifyPayload(TablName string, Cols []int, v reflect.Value) ([]byte, error) {
	Buf := make([]byte, 0, 128)
	Buf = append(Buf, TablName...)

	for _, Idx := range Cols {
		Buf = append(Buf, verifySep...)

		item, eror := verifyFormat(v.Field(Idx))
		if eror != nil {
			return nil, eror
		}
		Buf = append(Buf, item...)
	}

	return Buf, nil
}

// #verifyTextOf MAC 伴随值:标签 + base64(原始MAC)
func verifyTextOf(Sum []byte) string {
	return UniVerifyTag + base64.StdEncoding.EncodeToString(Sum)
}

// #verifyColsOf 解析保护列的结构体字段下标(顺序=登记顺序=载荷顺序);
// #引擎或表未开启时返回 (nil,nil)。批量写入路径解析一次循环复用
func (this *TUniEngine) verifyColsOf(UniTable *TUniTable, t reflect.Type) ([]int, error) {
	if !this.verifyActive(UniTable) {
		return nil, nil
	}

	Cols := make([]int, 0, len(UniTable.ColVerify))

	for _, ItemPara := range UniTable.ColVerify {
		Sf, Ok := t.FieldByName(ItemPara.AttriName)
		if !Ok {
			return nil, fmt.Errorf("UniEngine: verify protected field[%s] not Found in class[%s]", ItemPara.AttriName, t.String())
		}
		//#仅支持扁平类:内嵌结构体的提升字段 Index 长度>1(与 selectRowsCtx 同一约束)
		if len(Sf.Index) != 1 {
			return nil, fmt.Errorf("UniEngine: class[%s] field[%s] is promoted from an embedded struct; flat classes only", t.String(), ItemPara.AttriName)
		}
		Cols = append(Cols, Sf.Index[0])
	}

	return Cols, nil
}

// #verifySignCols 按已解析下标重算 MAC 并回填结构体的伴随字段。
// #Insert/Update/InsertL/saveUpsert 在组装 SQL 前调用;幂等(重算覆盖),
// #SaveIt 的 saveUpsert→saveByCount 回退链重复调用无副作用。
// #签名以结构体为源:Update 是整行语义(全部可写列入 SET),更新前须先读后改——
// #这与本引擎 Update 的既有契约一致
func (this *TUniEngine) verifySignCols(v reflect.Value, UniTable *TUniTable, Cols []int) error {
	if len(Cols) == 0 || v.Kind() != reflect.Struct {
		return nil
	}

	MacCol, Valid := UniTable.HashField[UniTable.VerifyAt]
	if !Valid || MacCol.AttriName == "" {
		return nil
	}

	Payload, eror := verifyPayload(UniTable.TableName, Cols, v)
	if eror != nil {
		return eror
	}

	sum, eror := this.verifySign(Payload)
	if eror != nil {
		return eror
	}

	macField := v.FieldByName(MacCol.AttriName)
	if macField.Kind() != reflect.String {
		return fmt.Errorf("UniEngine: verify Column[%s] maps to a non-string field", MacCol.FieldName)
	}

	//#按值传入的行不可寻址,MAC 无法回填调用方结构体;必须在此返回普通错误,
	//#而不是让 SetString 产生反射 panic(评审 Q4;此处先于 SQL 组装,错误即不执行)
	if !macField.CanSet() {
		return fmt.Errorf("UniEngine: verify Column[%s.%s] is not addressable; pass the row as a pointer so the MAC can be backfilled", UniTable.TableName, MacCol.FieldName)
	}

	macField.SetString(verifyTextOf(sum))

	return nil
}

// #verifySignRow 写路径签名(单行):解析+签名一步到位
func (this *TUniEngine) verifySignRow(v reflect.Value, UniTable *TUniTable) error {
	if !this.verifyActive(UniTable) || v.Kind() != reflect.Struct {
		return nil
	}

	Cols, eror := this.verifyColsOf(UniTable, v.Type())
	if eror != nil {
		return eror
	}

	return this.verifySignCols(v, UniTable, Cols)
}

// #verifyCtxOf 读路径预解析:SELECT 列覆盖全部保护列与 MAC 列时返回验签上下文,
// #否则返回 nil(该行跳过验签,partial select 无法还原载荷)
func (this *TUniEngine) verifyCtxOf(UniTable *TUniTable, Column []string, FieldIdx []int) *verifyRowCtx {
	if !this.verifyActive(UniTable) {
		return nil
	}

	MacCol, Valid := UniTable.HashField[UniTable.VerifyAt]
	if !Valid || MacCol.AttriName == "" {
		return nil
	}

	LowerCol := UniTable.VerifyAt

	var Cols []int

	for _, ItemPara := range UniTable.ColVerify {
		Found := -1
		LowerTxt := strings.ToLower(ItemPara.FieldName)

		for ColIndex, ItemCol := range Column {
			if strings.ToLower(ItemCol) == LowerTxt {
				Found = ColIndex
				break
			}
		}
		if Found < 0 {
			return nil //#保护列不在查询结果中,载荷无法还原,整行跳过验签
		}

		Cols = append(Cols, FieldIdx[Found])
	}

	MacIdx := -1
	for ColIndex, ItemCol := range Column {
		if strings.ToLower(ItemCol) == LowerCol {
			MacIdx = FieldIdx[ColIndex]
			break
		}
	}
	if MacIdx < 0 {
		return nil
	}

	return &verifyRowCtx{TablName: UniTable.TableName, MacIdx: MacIdx, Cols: Cols}
}

// #verifyRow 读路径验签:重算期望 MAC 与伴随列常量时间比对。
// #无标签的空值按 VerifyMode 分流(宽容=存量直通/严格=报错);
// #非空但无标签属非法值(本引擎写路径必带标签),一律报错。
// #比对失败即数据被篡改,报错文案不含载荷与行值(避免敏感授权数据泄漏进日志)
func (this *TUniEngine) verifyRow(v reflect.Value, RowCtx *verifyRowCtx) error {
	if RowCtx == nil || v.Kind() != reflect.Struct {
		return nil
	}

	stored := v.Field(RowCtx.MacIdx).String()

	if stored == "" {
		if this.verifyStrict() {
			return fmt.Errorf("UniEngine: verify fail: table[%s] row has no mac value (legacy row before backfill?)", RowCtx.TablName)
		}
		return nil
	}

	if !strings.HasPrefix(stored, UniVerifyTag) {
		return fmt.Errorf("UniEngine: verify fail: table[%s] Row Mac value is invalid", RowCtx.TablName)
	}

	Payload, eror := verifyPayload(RowCtx.TablName, RowCtx.Cols, v)
	if eror != nil {
		return eror
	}

	sum, eror := this.verifySign(Payload)
	if eror != nil {
		return eror
	}

	expect, eror := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, UniVerifyTag))
	if eror != nil {
		return fmt.Errorf("UniEngine: verify fail: table[%s] Row Mac value is not base64", RowCtx.TablName)
	}

	if !hmac.Equal(expect, sum) {
		return fmt.Errorf("UniEngine: verify fail: table[%s] Row integrity check failed", RowCtx.TablName)
	}

	return nil
}
