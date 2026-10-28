// UniVerify
// #存储完整性校验:写路径对保护列算 HMAC 回填伴随列,读路径逐行验签

package UniEngine

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"reflect"
	"strconv"
	"strings"
)

// #应用完整性钩子:输入规范化载荷,返回 MAC 原始字节。
// #应用可实现自己的 MAC 算法(如 HMAC-SM3/对接密码机),
// #然后赋值给 TUniEngine.VerifyHook,即可接管全部保护列的签名与验签
type TVerifyHook func(Payload []byte) ([]byte, error)

// #TUniPkey 行主键实参:Column 为登记列名(小写),Value 为该字段的原始 Go 值。
// #供下游审计定位(写审计表/追查具体行);JSON 标签便于直接序列化落库。
type TUniPkey struct {
	Column string      `json:"column"`
	Value  interface{} `json:"value"`
}

// #完整性校验失败事件:交给 TStatusHook 的载荷。
// #携带定位信息、引擎实例与当前行主键,供应用写日志/监控或直接落审计表;
// #不含主键之外的行值、MAC 与 HMAC 载荷。Pkeys 含主键实参,写日志时请注意脱敏。
type TUniStatus struct {
	Engine   *TUniEngine //#触发事件的引擎实例(多引擎共用钩子/审计落库时用于取 Db/Provider/DataBase)
	Table    string      //#注册表名(SetVerify 所在表)
	Method   string      //#触发入口:Select/SelectL/SelectM/SelectH(*Ctx 变体同名)
	Mode     int64       //#本次生效的 VerificationMode;0/1=宽容,2=严格
	Row      int         //#结果集行号(0 基);-1 表示非行级失败(列投影不足,尚未读到行)
	Pkeys    []TUniPkey  //#当前行主键实参(登记顺序,仅限结果集实际投影的主键列);Row=-1 或未投影时为空
	Accepted bool        //#true=宽容模式已放行(行仍返回调用方);false=失败已作为 error 上抛
	Err      error       //#失败原因;恒非 nil,文本不含行值/载荷
}

// #应用完整性状态钩子:验签失败时同步回调一次——严格模式报错、宽容模式放行、
// #严格模式列投影不足都在此列。钩子只观察、不参与判定:返回值与错误文本不受影响;
// #实现须并发安全且不得阻塞查询(引擎在查询 goroutine 内同步调用,不另起协程);
// #panic 与其它应用钩子(SecretHook/VerifyHook)一样沿调用栈向上传播。
type TStatusHook func(Status TUniStatus)

const (
	// #MAC 标签:内置 HMAC-SHA256;唯一载荷格式为“版本头+表名+列名+值长度+值字节”。
	UniVerifyTag = "MAC:"
	// #载荷版本头
	verifyMacHead = "UniMac"
	// #载荷分隔符(ASCII 单元分隔符):授权键/状态列取值均为业务字符,不含该控制符
	verifySep = "\x1f"
)

// #verifyRowCtx 读路径逐行验签的预解析上下文(查询列覆盖保护列+MAC列时才非 nil;
// #列不全的查询无法验签,整行跳过——与 decryptRow 的 EncryIdx 同一预计算模式)
type verifyRowCtx struct {
	TablName string   //#注册表名,仅用于报错定位
	MacIdx   int      //#MAC伴随列的结构体字段下标
	Cols     []int    //#保护列的结构体字段下标,顺序=SetVerify登记顺序=载荷顺序
	ColsName []string //#保护列小写名,与 Cols 同序(v2 载荷逐列编码列名)
	PkeyIdx  []int    //#结果集实际投影的主键字段下标(StatusHook 审计定位用,未投影则不出现)
	PkeyName []string //#主键列小写名,与 PkeyIdx 同序
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
	return this.VerificationMode == 2
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

// verifyKindSupported 报告 verifyFormat 是否能规范化该 kind。
// SetVerify 在登记期用它校验保护列类型,避免 time.Time/[]byte 等"登记成功、
// 首次读写才失败"的迟到错误。
func verifyKindSupported(Kind reflect.Kind) bool {
	switch Kind {
	case reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64, reflect.Bool:
		return true
	}

	return false
}

// #verifyFormat 保护列值的规范化:定长类型走 strconv 确定序格式(同值必同串),
// #不支持的类型 fast-fail——静默降级会产出验不过的行
func verifyFormat(FieldValue reflect.Value) ([]byte, error) {
	if !verifyKindSupported(FieldValue.Kind()) {
		return nil, fmt.Errorf("UniEngine: verify does not support field kind [%s]", FieldValue.Kind().String())
	}

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

// #verifyPayload 无歧义载荷:版本头+表名+列数,逐列"列名+值长度+值字节"。
// #值带长度前缀,值中任意字节(含 verifySep)均被长度界定,不同元组必得不同字节串——
// #构造性消除 v1 拼接歧义,无需禁止值中出现分隔符。列名取登记小写
// #(identGuard 白名单字符,天然不含分隔符);列数防字段截断拼接
func verifyPayload(TablName string, Names []string, Cols []int, v reflect.Value) ([]byte, error) {
	Buf := make([]byte, 0, 128+len(TablName))
	Buf = append(Buf, verifyMacHead...)
	Buf = append(Buf, verifySep...)
	Buf = append(Buf, TablName...)
	Buf = append(Buf, verifySep...)
	Buf = append(Buf, strconv.Itoa(len(Cols))...)

	for Pos, Idx := range Cols {
		item, eror := verifyFormat(v.Field(Idx))
		if eror != nil {
			return nil, eror
		}

		Buf = append(Buf, verifySep...)
		Buf = append(Buf, Names[Pos]...)
		Buf = append(Buf, verifySep...)
		Buf = append(Buf, strconv.Itoa(len(item))...)
		Buf = append(Buf, verifySep...)
		Buf = append(Buf, item...)
	}

	return Buf, nil
}

// #verifyTextOf MAC 伴随值:标签 + base64(原始MAC);只有一种格式。
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

	//#载荷列名与 Cols 同序(verifyColsOf 按 ColVerify 登记序解析下标);v2 载荷需要
	ListName := make([]string, len(Cols))
	for Pos := range Cols {
		if Pos >= len(UniTable.ColVerify) {
			return fmt.Errorf("UniEngine: verify protected columns out of sync on table[%s]", UniTable.TableName)
		}
		ListName[Pos] = strings.ToLower(UniTable.ColVerify[Pos].FieldName)
	}

	Payload, eror := verifyPayload(UniTable.TableName, ListName, Cols, v)
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

// VerifySignRow 应用侧存量回填入口:对已注册结构体按当前字段值重算 MAC 并回填伴随字段,
// 不组装 SQL、不产生任何写库动作。与写路径(Insert/Update/InsertL)签名同一实现,
// 载荷与钩子(VerifyHook)口径天然一致;典型用法=读侧解密后的行先本方法
// 签名,再由应用自行批量落库 MAC 列。行须传指针:值传入时 MAC 无法回填,返回普通错误
func (this *TUniEngine) VerifySignRow(i interface{}) error {
	if eror := nilParamOf("VerifySignRow", i); eror != nil {
		return eror
	}

	t := reflect.TypeOf(i)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return fmt.Errorf("UniEngine: method [VerifySignRow] only takes a struct; got [%s]", t.Kind().String())
	}

	UniTable := this.tableByType(t.String())
	if UniTable == nil {
		return fmt.Errorf("%w: %s", ErrUnregisteredClass, t.String())
	}

	return this.verifySignRow(reflect.Indirect(reflect.ValueOf(i)), UniTable)
}

// #verifyCtxOf 读路径预解析:宽容模式下部分列查询保留旧行为;严格模式拒绝
// 缺少保护列或 MAC 列的结果,避免悄悄绕过验签。
func (this *TUniEngine) verifyCtxOf(UniTable *TUniTable, Column []string, FieldIdx []int) (*verifyRowCtx, error) {
	if !this.verifyActive(UniTable) {
		return nil, nil
	}

	MacCol, Valid := UniTable.HashField[UniTable.VerifyAt]
	if !Valid || MacCol.AttriName == "" {
		return nil, fmt.Errorf("%w: table[%s] has no mapped MAC column", ErrIncompleteVerification, UniTable.TableName)
	}

	LowerCol := UniTable.VerifyAt

	var Cols []int
	var ColsName []string

	for _, ItemPara := range UniTable.ColVerify {
		Found := -1
		LowerTxt := strings.ToLower(ItemPara.FieldName)

		for ColIndex, ItemCol := range Column {
			if strings.ToLower(ItemCol) == LowerTxt {
				Found = ColIndex
				break
			}
		}
		if Found < 0 || FieldIdx[Found] < 0 {
			if this.verifyStrict() {
				return nil, fmt.Errorf("%w: table[%s] query omits protected column[%s]", ErrIncompleteVerification, UniTable.TableName, LowerTxt)
			}
			return nil, nil
		}

		Cols = append(Cols, FieldIdx[Found])
		ColsName = append(ColsName, LowerTxt)
	}

	MacIdx := -1
	for ColIndex, ItemCol := range Column {
		if strings.ToLower(ItemCol) == LowerCol {
			MacIdx = FieldIdx[ColIndex]
			break
		}
	}
	if MacIdx < 0 {
		if this.verifyStrict() {
			return nil, fmt.Errorf("%w: table[%s] query omits MAC column[%s]", ErrIncompleteVerification, UniTable.TableName, LowerCol)
		}
		return nil, nil
	}

	//#审计定位:只收结果集实际投影的主键列(VerifyUnkeyed 时主键可能不在查询里,
	//#此时结构体对应字段是零值,不能作为主键上报)
	var PkeyIdx []int
	var PkeyName []string

	for _, ItemPara := range UniTable.orderedPkeys() {
		LowerTxt := strings.ToLower(ItemPara.FieldName)
		for ColIndex, ItemCol := range Column {
			if strings.ToLower(ItemCol) == LowerTxt && FieldIdx[ColIndex] >= 0 {
				PkeyIdx = append(PkeyIdx, FieldIdx[ColIndex])
				PkeyName = append(PkeyName, LowerTxt)
				break
			}
		}
	}

	return &verifyRowCtx{TablName: UniTable.TableName, MacIdx: MacIdx, Cols: Cols, ColsName: ColsName,
		PkeyIdx: PkeyIdx, PkeyName: PkeyName}, nil
}

// #verifyRow 读路径验签:重算期望 MAC 与伴随列常量时间比对。
// #无标签的空值按 VerificationMode 分流(宽容=存量直通/严格=报错);
// #非空但无标签属非法值(本引擎写路径必带标签),一律报错。
// #非空、格式合法但 MAC 不匹配:严格模式报错;宽容模式(0/1)记录告警后放行。
// #告警/报错/StatusHook 文案均不含载荷与行值(避免敏感授权数据泄漏进日志)。
// #任何失败(含宽容模式放行)都经 StatusHook 上报一次;钩子为空时行为与旧版一致。
func (this *TUniEngine) verifyRow(v reflect.Value, RowCtx *verifyRowCtx, Method string, Row int) error {
	if RowCtx == nil || v.Kind() != reflect.Struct {
		return nil
	}

	eror, Accepted := this.verifyRowVerdict(v, RowCtx)
	if eror == nil {
		return nil
	}

	if Accepted {
		//#宽容模式(VerificationMode=0/1):放行并告警,不阻断前端取值。
		//#不打印载荷/行值,避免授权数据进入日志。
		log.Printf("UniEngine: verify warning: table[%s] MAC mismatch; VerificationMode=%d lenient, row accepted", RowCtx.TablName, this.VerificationMode)
	}

	this.verifyStatus(TUniStatus{
		Method:   Method,
		Row:      Row,
		Accepted: Accepted,
		Err:      eror,
	}, RowCtx, v)

	if Accepted {
		return nil
	}

	return eror
}

// #verifyRowVerdict 单行验签判定:eror 为 nil 表示通过;Accepted=true 表示宽容模式
// #失配——eror 仍携带失配原因供 StatusHook 记录,由 verifyRow 负责放行。
func (this *TUniEngine) verifyRowVerdict(v reflect.Value, RowCtx *verifyRowCtx) (eror error, accepted bool) {
	stored := v.Field(RowCtx.MacIdx).String()

	if stored == "" {
		if this.verifyStrict() {
			return fmt.Errorf("UniEngine: verify fail: table[%s] row has no mac value (legacy row before backfill?)", RowCtx.TablName), false
		}
		return nil, false
	}

	if !strings.HasPrefix(stored, UniVerifyTag) {
		return fmt.Errorf("UniEngine: verify fail: table[%s] Row Mac value is invalid", RowCtx.TablName), false
	}

	Payload, eror := verifyPayload(RowCtx.TablName, RowCtx.ColsName, RowCtx.Cols, v)
	if eror != nil {
		return eror, false
	}

	sum, eror := this.verifySign(Payload)
	if eror != nil {
		return eror, false
	}

	expect, eror := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, UniVerifyTag))
	if eror != nil {
		return fmt.Errorf("UniEngine: verify fail: table[%s] Row Mac value is not base64", RowCtx.TablName), false
	}

	if !hmac.Equal(expect, sum) {
		return fmt.Errorf("UniEngine: verify fail: table[%s] Row integrity check failed", RowCtx.TablName), !this.verifyStrict()
	}

	return nil, false
}

// #verifyStatus 把一次验签失败交给应用钩子;未设置钩子时直接返回(不构造主键载荷)。
// #引擎侧补齐 Engine/Mode;行级事件从 RowCtx 取表名与已投影主键,投影不足时 RowCtx 为 nil。
// #在查询 goroutine 内同步调用;Pkeys 为主键实参(审计定位用),不含其它行值。
func (this *TUniEngine) verifyStatus(Status TUniStatus, RowCtx *verifyRowCtx, v reflect.Value) {
	Hook := this.StatusHook
	if Hook == nil {
		return
	}

	Status.Engine = this
	Status.Mode = this.VerificationMode
	if RowCtx != nil {
		Status.Table = RowCtx.TablName
		Status.Pkeys = pkeysOfRow(v, RowCtx)
	}

	Hook(Status)
}

// #pkeysOfRow 按登记顺序取当前行的主键实参(列名+原始值);
// #只使用结果集实际投影的主键字段(verifyCtxOf 预计算),未投影的主键不会以零值冒充。
func pkeysOfRow(v reflect.Value, RowCtx *verifyRowCtx) []TUniPkey {
	if RowCtx == nil || len(RowCtx.PkeyIdx) == 0 || !v.IsValid() || v.Kind() != reflect.Struct {
		return nil
	}

	Keys := make([]TUniPkey, 0, len(RowCtx.PkeyIdx))
	for Pos, Idx := range RowCtx.PkeyIdx {
		Keys = append(Keys, TUniPkey{Column: RowCtx.PkeyName[Pos], Value: v.Field(Idx).Interface()})
	}

	return Keys
}
