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
// #列不全的查询无法验签,整行跳过——与 decryptRow 的 encryIdx 同一预计算模式)
type verifyRowCtx struct {
	tablName string //#注册表名,仅用于报错定位
	macIdx   int    //#MAC伴随列的结构体字段下标
	cols     []int  //#保护列的结构体字段下标,顺序=SetVerify登记顺序=载荷顺序
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

	mac := hmac.New(sha256.New, []byte(this.VerifyBy))
	mac.Write(Payload)

	return mac.Sum(nil), nil
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
func verifyPayload(TablName string, cols []int, v reflect.Value) ([]byte, error) {

	buf := make([]byte, 0, 128)
	buf = append(buf, TablName...)

	for _, idx := range cols {

		buf = append(buf, verifySep...)

		item, eror := verifyFormat(v.Field(idx))
		if eror != nil {
			return nil, eror
		}
		buf = append(buf, item...)
	}

	return buf, nil
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

	cols := make([]int, 0, len(UniTable.ColVerify))

	for _, ItemPara := range UniTable.ColVerify {

		sf, ok := t.FieldByName(ItemPara.AttriName)
		if !ok {
			return nil, fmt.Errorf("UniEngine: verify protected field[%s] not found in class[%s]", ItemPara.AttriName, t.String())
		}
		//#仅支持扁平类:内嵌结构体的提升字段 Index 长度>1(与 selectRowsCtx 同一约束)
		if len(sf.Index) != 1 {
			return nil, fmt.Errorf("UniEngine: class[%s] field[%s] is promoted from an embedded struct; flat classes only", t.String(), ItemPara.AttriName)
		}
		cols = append(cols, sf.Index[0])
	}

	return cols, nil
}

// #verifySignCols 按已解析下标重算 MAC 并回填结构体的伴随字段。
// #Insert/Update/InsertL/saveUpsert 在组装 SQL 前调用;幂等(重算覆盖),
// #SaveIt 的 saveUpsert→saveByCount 回退链重复调用无副作用。
// #签名以结构体为源:Update 是整行语义(全部可写列入 SET),更新前须先读后改——
// #这与本引擎 Update 的既有契约一致
func (this *TUniEngine) verifySignCols(v reflect.Value, UniTable *TUniTable, cols []int) error {

	if len(cols) == 0 || v.Kind() != reflect.Struct {
		return nil
	}

	MacCol, Valid := UniTable.HashField[UniTable.VerifyAt]
	if !Valid || MacCol.AttriName == "" {
		return nil
	}

	payload, eror := verifyPayload(UniTable.TableName, cols, v)
	if eror != nil {
		return eror
	}

	sum, eror := this.verifySign(payload)
	if eror != nil {
		return eror
	}

	macField := v.FieldByName(MacCol.AttriName)
	if macField.Kind() != reflect.String {
		return fmt.Errorf("UniEngine: verify column[%s] maps to a non-string field", MacCol.FieldName)
	}

	macField.SetString(verifyTextOf(sum))

	return nil
}

// #verifySignRow 写路径签名(单行):解析+签名一步到位
func (this *TUniEngine) verifySignRow(v reflect.Value, UniTable *TUniTable) error {

	if !this.verifyActive(UniTable) || v.Kind() != reflect.Struct {
		return nil
	}

	cols, eror := this.verifyColsOf(UniTable, v.Type())
	if eror != nil {
		return eror
	}

	return this.verifySignCols(v, UniTable, cols)
}

// #verifyCtxOf 读路径预解析:SELECT 列覆盖全部保护列与 MAC 列时返回验签上下文,
// #否则返回 nil(该行跳过验签,partial select 无法还原载荷)
func (this *TUniEngine) verifyCtxOf(UniTable *TUniTable, column []string, fieldIdx []int) *verifyRowCtx {

	if !this.verifyActive(UniTable) {
		return nil
	}

	MacCol, Valid := UniTable.HashField[UniTable.VerifyAt]
	if !Valid || MacCol.AttriName == "" {
		return nil
	}

	lowerMac := UniTable.VerifyAt

	var cols []int

	for _, ItemPara := range UniTable.ColVerify {

		found := -1
		lowerName := strings.ToLower(ItemPara.FieldName)

		for ColIndex, ItemCol := range column {
			if strings.ToLower(ItemCol) == lowerName {
				found = ColIndex
				break
			}
		}
		if found < 0 {
			return nil //#保护列不在查询结果中,载荷无法还原,整行跳过验签
		}

		cols = append(cols, fieldIdx[found])
	}

	macIdx := -1
	for ColIndex, ItemCol := range column {
		if strings.ToLower(ItemCol) == lowerMac {
			macIdx = fieldIdx[ColIndex]
			break
		}
	}
	if macIdx < 0 {
		return nil
	}

	return &verifyRowCtx{tablName: UniTable.TableName, macIdx: macIdx, cols: cols}
}

// #verifyRow 读路径验签:重算期望 MAC 与伴随列常量时间比对。
// #无标签的空值按 VerifyMode 分流(宽容=存量直通/严格=报错);
// #非空但无标签属非法值(本引擎写路径必带标签),一律报错。
// #比对失败即数据被篡改,报错文案不含载荷与行值(避免敏感授权数据泄漏进日志)
func (this *TUniEngine) verifyRow(v reflect.Value, RowCtx *verifyRowCtx) error {

	if RowCtx == nil || v.Kind() != reflect.Struct {
		return nil
	}

	stored := v.Field(RowCtx.macIdx).String()

	if stored == "" {
		if this.verifyStrict() {
			return fmt.Errorf("UniEngine: verify fail: table[%s] row has no mac value (legacy row before backfill?)", RowCtx.tablName)
		}
		return nil
	}

	if !strings.HasPrefix(stored, UniVerifyTag) {
		return fmt.Errorf("UniEngine: verify fail: table[%s] row mac value is invalid", RowCtx.tablName)
	}

	payload, eror := verifyPayload(RowCtx.tablName, RowCtx.cols, v)
	if eror != nil {
		return eror
	}

	sum, eror := this.verifySign(payload)
	if eror != nil {
		return eror
	}

	expect, eror := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, UniVerifyTag))
	if eror != nil {
		return fmt.Errorf("UniEngine: verify fail: table[%s] row mac value is not base64", RowCtx.tablName)
	}

	if !hmac.Equal(expect, sum) {
		return fmt.Errorf("UniEngine: verify fail: table[%s] row integrity check failed", RowCtx.tablName)
	}

	return nil
}
