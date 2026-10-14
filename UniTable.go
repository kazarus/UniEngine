// UniTable
package UniEngine

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

type TUniTable struct {
	IPriority int64  `db:"i_priority" json:"iPriority"`
	ParentTab string `db:"parent_tab" json:"parentTab"`
	TableName string `db:"table_name" json:"tableName"`

	SqlSelect string `db:"sql_select" json:"sqlSelect"`
	SqlInsert string `db:"sql_insert" json:"sqlInsert"`
	SqlUpdate string `db:"sql_update" json:"sqlUpdate"`
	SqlDelete string `db:"sql_delete" json:"sqlDelete"`

	ClassType reflect.Type `db:"-" json:"-"` //#注册类类型(RegisterClass 登记;RegisterTable/RegisterField 路径为空)#供注册表驱动的反射构造读行切片

	SecretOn  bool        //#是否含加密字段#SetSecret或tag检测到加密列时置位#层级:TUniEngine.SecretOn→TUniTable.SecretOn→TUniField.SecretOn
	ColSecret []TUniField //#栏目加密

	VerifyOn  bool        //#是否含完整性保护字段#SetVerify登记时置位
	VerifyAt  string      //#MAC伴随列(字段名,小写)#如 hmac_text,建议建列 varchar(200) not null default ''
	ColVerify []TUniField //#保护列(登记顺序=载荷顺序)#MAC覆盖 who_build/user_indx 等授权键与 data_stat 等状态列

	HashField map[string]TUniField //ToLower
	HashPkeys map[string]TUniField //ToLower

	ListField []TUniField //ToLower
	ListPkeys []TUniField //ToLower

	LastError error //#链式登记(SetKeys/SetSecret)的首个错误#置位后短路后续登记,链尾检查
}

func (this *TUniTable) HasField(FieldName string) (bool, error) {

	if _, Valid := this.HashField[strings.ToLower(FieldName)]; Valid {
		return true, nil
	}

	return false, nil
}

// orderedField 返回全部已注册字段的确定序:先按 ListField(类声明序/既有列表序),
// 其后按字段名排序补齐仅写入 HashField 的字段(如 RegisterField 直写);同名字段以
// 先出现者为准。CRUD 生成 SQL 一律走本序,保证同一输入产生相同语句文本,利于数据库端语句缓存。
func (this *TUniTable) orderedField() []TUniField {

	if len(this.HashField) == 0 {
		return nil
	}

	Seen := make(map[string]bool, len(this.ListField))
	List := make([]TUniField, 0, len(this.HashField))

	for _, ItemPara := range this.ListField {
		LowerTxt := strings.ToLower(ItemPara.FieldName)
		if _, Valid := this.HashField[LowerTxt]; !Valid || Seen[LowerTxt] {
			continue
		}
		Seen[LowerTxt] = true
		List = append(List, ItemPara)
	}

	var Rest []string
	for LowerTxt := range this.HashField {
		if !Seen[LowerTxt] {
			Rest = append(Rest, LowerTxt)
		}
	}
	sort.Strings(Rest)
	for _, LowerTxt := range Rest {
		List = append(List, this.HashField[LowerTxt])
	}

	return List
}

// orderedPkeys 返回全部主键字段的确定序:ListPkeys 顺序优先,
// 其后按字段名排序补齐仅写入 HashPkeys 的字段(如 RegisterPkeys 直写)。
func (this *TUniTable) orderedPkeys() []TUniField {

	if len(this.HashPkeys) == 0 {
		return nil
	}

	Seen := make(map[string]bool, len(this.ListPkeys))
	List := make([]TUniField, 0, len(this.HashPkeys))

	for _, ItemPara := range this.ListPkeys {
		LowerTxt := strings.ToLower(ItemPara.FieldName)
		if _, Valid := this.HashPkeys[LowerTxt]; !Valid || Seen[LowerTxt] {
			continue
		}
		Seen[LowerTxt] = true
		List = append(List, ItemPara)
	}

	var Rest []string
	for LowerTxt := range this.HashPkeys {
		if !Seen[LowerTxt] {
			Rest = append(Rest, LowerTxt)
		}
	}
	sort.Strings(Rest)
	for _, LowerTxt := range Rest {
		List = append(List, this.HashPkeys[LowerTxt])
	}

	return List
}

// writableFields 返回可经反射赋值的写入列(非只读且有 AttriName 映射),按确定序。
// RegisterField 直写的字段无 AttriName,不参与反射赋值路径。
func (this *TUniTable) writableFields() []TUniField {

	var Result []TUniField

	for _, ItemPara := range this.orderedField() {
		if ItemPara.ReadOnly || ItemPara.AttriName == "" {
			continue
		}
		Result = append(Result, ItemPara)
	}

	return Result
}

// pkeyFields 返回可用于 where 定位的主键列(有 AttriName 映射),按确定序。
func (this *TUniTable) pkeyFields() []TUniField {

	var Result []TUniField

	for _, ItemPara := range this.orderedPkeys() {
		if ItemPara.AttriName == "" {
			continue
		}
		Result = append(Result, ItemPara)
	}

	return Result
}

// secretColumn 判断写入路径是否对该列应用加密:主键值一律保持明文
// (WHERE 定位行的语义,密文无法与库中存量匹配),仅数据列参与应用加密。
// SetSecret/SetKeys/AutoKeys 已在登记时双向拒绝 secreton×主键,
// 此处对绕过守卫直接改注册表的用法兜底。
func (this *TUniTable) secretColumn(ItemPara TUniField) bool {

	if !ItemPara.SecretOn {
		return false
	}

	_, IsPkey := this.HashPkeys[strings.ToLower(ItemPara.FieldName)]

	return !IsPkey
}

// #SetKeys 登记主键并返回本表,支持链式:
// #  UniEngineEx.RegisterClass(core.TSYSPATH{}, "sys_path").SetKeys("WHO_BUILD", "USER_INDX", "SYS_PATHC").SetSecret("MAST_NAME")
// #链中首个错误记入 LastError 并短路后续登记,链尾以 if tbl.LastError != nil 检查
func (this *TUniTable) SetKeys(Fields ...interface{}) *TUniTable {

	if this.LastError != nil {
		return this
	}

	for _, ItemPara := range Fields {

		FieldName, Ok := ItemPara.(string)
		if !Ok {
			this.LastError = fmt.Errorf("UniEngine: SetKeys argument %v is not a string", ItemPara)
			return this
		}

		Field, Valid := this.HashField[strings.ToLower(FieldName)]
		if !Valid {
			this.LastError = fmt.Errorf("UniEngine: field[%s.%s] is unregistered", this.TableName, strings.ToLower(FieldName))
			return this
		}

		//#主键值一律明文(WHERE 定位语义),与 secreton 互斥
		if Field.SecretOn {
			this.LastError = fmt.Errorf("UniEngine: field[%s.%s] is secreton-marked; pkey Values stay plaintext for WHERE matching, secreton does not apply to pkeys", this.TableName, strings.ToLower(FieldName))
			return this
		}

		LowerTxt := strings.ToLower(FieldName)
		if _, Exists := this.HashPkeys[LowerTxt]; !Exists {
			this.HashPkeys[LowerTxt] = Field
			this.ListPkeys = append(this.ListPkeys, Field)
		}
	}

	return this
}

// #SetSecret 标记加密列并返回本表,支持链式(错误处理同 SetKeys:记入 LastError 并短路)
func (this *TUniTable) SetSecret(Fields ...interface{}) *TUniTable {

	if this.LastError != nil {
		return this
	}

	for _, ItemPara := range Fields {

		FieldName, Ok := ItemPara.(string)
		if !Ok {
			this.LastError = fmt.Errorf("UniEngine: SetSecret argument %v is not a string", ItemPara)
			return this
		}

		LowerTxt := strings.ToLower(FieldName)

		Field, Valid := this.HashField[LowerTxt]
		if !Valid {
			this.LastError = fmt.Errorf("UniEngine: field[%s.%s] is unregistered;", this.TableName, LowerTxt)
			return this
		}

		//#主键值一律明文(WHERE 定位语义),与 secreton 互斥
		if _, IsPkey := this.HashPkeys[LowerTxt]; IsPkey {
			this.LastError = fmt.Errorf("UniEngine: field[%s.%s] is a pkey; pkey Values stay plaintext for WHERE matching, secreton does not apply to pkeys", this.TableName, LowerTxt)
			return this
		}

		Field.SecretOn = true
		this.HashField[LowerTxt] = Field

		//#表级标记同步置位(任一列被标记即认为该表含加密字段)
		this.SecretOn = true

		//#加密列清单同步登记(重复 SetSecret 同列不重复登记)
		var Seen bool
		for i := range this.ColSecret {
			if strings.ToLower(this.ColSecret[i].FieldName) == LowerTxt {
				Seen = true
				break
			}
		}
		if !Seen {
			this.ColSecret = append(this.ColSecret, Field)
		}

		//#ListField 是 HashField 的另一份副本,须同步标记,否则 PrepareRunSQL 路径会读到旧值
		for i := range this.ListField {
			if strings.ToLower(this.ListField[i].FieldName) == LowerTxt {
				this.ListField[i].SecretOn = true
			}
		}
	}

	return this
}

// #SetVerify 登记存储完整性保护并返回本表,支持链式(错误处理同 SetKeys:记入 LastError 并短路):
// #  UniEngineEx.RegisterClass(core.TUSERPART{}, "tbl_user_part").SetKeys(...).SetVerify("mac_text", "who_build", "user_indx", "data_stat")
// #MacField 为 MAC 伴随列(须为已登记的 string 字段,建议建列 varchar(64) NOT NULL DEFAULT ”,
// #NOT NULL 缺省空串——存量行扫描 NULL 会报错),Fields 为保护列(登记顺序=载荷顺序);
// #写路径自动重算 MacField 回填,读路径逐行验签(平台:等保/密评 访问控制信息存储完整性)
// #载荷含注册表名做域分隔,MAC 不可跨表移植;保护列可含主键列与 secreton 列
// #(secreton 列参与载荷的是解密后的明文,与读路径先解密后验签的顺序一致)
func (this *TUniTable) SetVerify(VerifyAt string, Fields ...string) *TUniTable {

	if this.LastError != nil {
		return this
	}

	//#参数形态检查最廉且最根本,先于 MAC 列校验(fail-fast:调用缺保护列时不必陪跑后续查找)
	if len(Fields) == 0 {
		this.LastError = fmt.Errorf("UniEngine: SetVerify[%s.%s] needs at least one protected field", this.TableName, strings.ToLower(VerifyAt))
		return this
	}

	LowerCol := strings.ToLower(VerifyAt)

	FieldCol, Valid := this.HashField[LowerCol]
	if !Valid {
		this.LastError = fmt.Errorf("UniEngine: field[%s.%s] is unregistered;", this.TableName, LowerCol)
		return this
	}
	if FieldCol.AttriName == "" {
		this.LastError = fmt.Errorf("UniEngine: field[%s.%s] has no class attribute; Mac Column needs a writable string field", this.TableName, LowerCol)
		return this
	}
	if FieldCol.ReadOnly {
		this.LastError = fmt.Errorf("UniEngine: field[%s.%s] is readonly; Mac Column must be writable", this.TableName, LowerCol)
		return this
	}
	if _, IsPkey := this.HashPkeys[LowerCol]; IsPkey {
		this.LastError = fmt.Errorf("UniEngine: field[%s.%s] is a pkey; Mac Column is engine-computed and can not be a pkey", this.TableName, LowerCol)
		return this
	}
	//#MAC 值写入 string 字段;其它类型无法回填,登记期直接拒绝
	if this.ClassType != nil {
		if Sf, Ok := this.ClassType.FieldByName(FieldCol.AttriName); !Ok || Sf.Type.Kind() != reflect.String {
			this.LastError = fmt.Errorf("UniEngine: field[%s.%s] maps to a non-string attribute; Mac Column must be string", this.TableName, LowerCol)
			return this
		}
	}

	var ListCols = make([]TUniField, 0, len(Fields))
	var HashList = make(map[string]bool, len(Fields))

	for _, ItemPara := range Fields {

		LowerTxt := strings.ToLower(ItemPara)

		if LowerTxt == LowerCol {
			this.LastError = fmt.Errorf("UniEngine: field[%s.%s] is the Mac Column itself; it can not protect itself", this.TableName, LowerTxt)
			return this
		}

		Field, Valid := this.HashField[LowerTxt]
		if !Valid {
			this.LastError = fmt.Errorf("UniEngine: field[%s.%s] is unregistered;", this.TableName, LowerTxt)
			return this
		}
		if Field.AttriName == "" {
			this.LastError = fmt.Errorf("UniEngine: field[%s.%s] has no class attribute; protected Fields must map to class attributes", this.TableName, LowerTxt)
			return this
		}
		if HashList[LowerTxt] {
			this.LastError = fmt.Errorf("UniEngine: field[%s.%s] is duplicated in SetVerify", this.TableName, LowerTxt)
			return this
		}

		HashList[LowerTxt] = true
		ListCols = append(ListCols, Field)
	}

	this.VerifyOn = true
	this.VerifyAt = LowerCol
	this.ColVerify = ListCols

	return this
}

// AutoKeys 按数据库元数据自动登记主键。UniEngineEx 为引擎指针(第二阶段起不再按值拷贝)。
func (this *TUniTable) AutoKeys(UniEngineEx *TUniEngine, GetSqlAutoKeys ...interface{}) error {

	var SQL string

	if len(GetSqlAutoKeys) > 0 {

		if x, Ok := GetSqlAutoKeys[0].(HasGetSqlAutoKeys); Ok {
			var eror error
			SQL, eror = x.GetSqlAutoKeys(UniEngineEx, this.TableName)
			if eror != nil {
				return eror
			}
		}

	} else {

		var eror error
		SQL, eror = TAutoKeys4POSTGR{}.GetSqlAutoKeys(UniEngineEx, this.TableName)
		if eror != nil {
			return eror
		}

	}

	if SQL == "" {
		return fmt.Errorf("UniEngine: can not build autokeys sql for table:%s", this.TableName)
	}

	if UniEngineEx.debugging() {
		fmt.Println(SQL)
	}

	var ListData = make([]TUniField, 0)
	if eror := UniEngineEx.SelectL(&ListData, SQL); eror != nil {
		return fmt.Errorf("UniEngine: table:%s: %w", this.TableName, eror)
	}

	if len(ListData) == 0 {
		return fmt.Errorf("UniEngine: table:%s or its primary Key may not exist.", this.TableName)
	}

	var CodeText string
	for _, ItemPara := range ListData {
		ItemCopy, Valid := this.HashField[strings.ToLower(ItemPara.FieldName)]
		if !Valid {
			return fmt.Errorf("UniEngine: database have field[%s.%s], but class not.", this.TableName, ItemPara.FieldName)
		}

		//#主键值一律明文(WHERE 定位语义),与 secreton 互斥
		if ItemCopy.SecretOn {
			return fmt.Errorf("UniEngine: field[%s.%s] is secreton-marked; pkey Values stay plaintext for WHERE matching, secreton does not apply to pkeys", this.TableName, ItemPara.FieldName)
		}

		LowerTxt := strings.ToLower(ItemPara.FieldName)
		if _, Exists := this.HashPkeys[LowerTxt]; !Exists {
			this.HashPkeys[LowerTxt] = ItemCopy
			this.ListPkeys = append(this.ListPkeys, ItemCopy)
		}
		CodeText = CodeText + "," + `"` + ItemPara.FieldName + `"`
	}
	CodeText = fmt.Sprintf(".SetKeys( %s )", CodeText[1:])
	if UniEngineEx.debugging() {
		fmt.Printf("UniEngine: recommend this line instead of [%s.AutoKeys]:%s\n", this.TableName, CodeText)
	}

	return nil
}
