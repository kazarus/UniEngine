// UniTable
package UniEngine

import (
	"fmt"
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

	SecretOn bool `db:"secret_on" json:"secretOn"` //#是否含加密字段#SetSecret或tag检测到加密列时置位#层级:TUniEngine.SecretOn→TUniTable.SecretOn→TUniField.SecretOn

	HashField map[string]TUniField //ToLower
	HashPkeys map[string]TUniField //ToLower

	ListField []TUniField //ToLower
	ListPkeys []TUniField //ToLower

	Err error //#链式登记(SetKeys/SetSecret)的首个错误#置位后短路后续登记,链尾检查
}

func (this *TUniTable) HasField(FieldName string) (bool, error) {

	if _, Valid := this.HashField[strings.ToLower(FieldName)]; Valid {
		return true, nil
	}

	return false, nil
}

// orderedFields 返回全部已注册字段的确定序:先按 ListField(类声明序/既有列表序),
// 其后按字段名排序补齐仅写入 HashField 的字段(如 RegisterField 直写);同名字段以
// 先出现者为准。CRUD 生成 SQL 一律走本序,保证同一输入产生相同语句文本,利于数据库端语句缓存。
func (this *TUniTable) orderedFields() []TUniField {

	if len(this.HashField) == 0 {
		return nil
	}

	seen := make(map[string]bool, len(this.ListField))
	list := make([]TUniField, 0, len(this.HashField))

	for _, ItemPara := range this.ListField {
		lowerName := strings.ToLower(ItemPara.FieldName)
		if _, Valid := this.HashField[lowerName]; !Valid || seen[lowerName] {
			continue
		}
		seen[lowerName] = true
		list = append(list, ItemPara)
	}

	var rest []string
	for lowerName := range this.HashField {
		if !seen[lowerName] {
			rest = append(rest, lowerName)
		}
	}
	sort.Strings(rest)
	for _, lowerName := range rest {
		list = append(list, this.HashField[lowerName])
	}

	return list
}

// orderedPkeys 返回全部主键字段的确定序:ListPkeys 顺序优先,
// 其后按字段名排序补齐仅写入 HashPkeys 的字段(如 RegisterPkeys 直写)。
func (this *TUniTable) orderedPkeys() []TUniField {

	if len(this.HashPkeys) == 0 {
		return nil
	}

	seen := make(map[string]bool, len(this.ListPkeys))
	list := make([]TUniField, 0, len(this.HashPkeys))

	for _, ItemPara := range this.ListPkeys {
		lowerName := strings.ToLower(ItemPara.FieldName)
		if _, Valid := this.HashPkeys[lowerName]; !Valid || seen[lowerName] {
			continue
		}
		seen[lowerName] = true
		list = append(list, ItemPara)
	}

	var rest []string
	for lowerName := range this.HashPkeys {
		if !seen[lowerName] {
			rest = append(rest, lowerName)
		}
	}
	sort.Strings(rest)
	for _, lowerName := range rest {
		list = append(list, this.HashPkeys[lowerName])
	}

	return list
}

// writableFields 返回可经反射赋值的写入列(非只读且有 AttriName 映射),按确定序。
// RegisterField 直写的字段无 AttriName,不参与反射赋值路径。
func (this *TUniTable) writableFields() []TUniField {

	var fields []TUniField
	for _, ItemPara := range this.orderedFields() {
		if ItemPara.ReadOnly || ItemPara.AttriName == "" {
			continue
		}
		fields = append(fields, ItemPara)
	}

	return fields
}

// pkeyFields 返回可用于 where 定位的主键列(有 AttriName 映射),按确定序。
func (this *TUniTable) pkeyFields() []TUniField {

	var keys []TUniField
	for _, ItemPara := range this.orderedPkeys() {
		if ItemPara.AttriName == "" {
			continue
		}
		keys = append(keys, ItemPara)
	}

	return keys
}

// secretColumn 判断写入路径是否对该列应用加密:主键值一律保持明文
// (WHERE 定位行的语义,密文无法与库中存量匹配),仅数据列参与应用加密。
// SetSecret/SetKeys/AutoKeys 已在登记时双向拒绝 secreton×主键,
// 此处对绕过守卫直接改注册表的用法兜底。
func (this *TUniTable) secretColumn(ItemPara TUniField) bool {

	if !ItemPara.SecretOn {
		return false
	}

	_, isPkey := this.HashPkeys[strings.ToLower(ItemPara.FieldName)]

	return !isPkey
}

// #SetKeys 登记主键并返回本表,支持链式:
// #  UniEngineEx.RegisterClass(core.TSYSPATH{}, "sys_path").SetKeys("WHO_BUILD", "USER_INDX", "SYS_PATHC").SetSecret("MAST_NAME")
// #链中首个错误记入 Err 并短路后续登记,链尾以 if tbl.Err != nil 检查
func (this *TUniTable) SetKeys(Fields ...interface{}) *TUniTable {

	if this.Err != nil {
		return this
	}

	for _, ItemPara := range Fields {

		fieldName, ok := ItemPara.(string)
		if !ok {
			this.Err = fmt.Errorf("UniEngine: SetKeys argument %v is not a string", ItemPara)
			return this
		}

		Field, Valid := this.HashField[strings.ToLower(fieldName)]
		if !Valid {
			this.Err = fmt.Errorf("UniEngine: field[%s.%s] is unregistered", this.TableName, strings.ToLower(fieldName))
			return this
		}

		//#主键值一律明文(WHERE 定位语义),与 secreton 互斥
		if Field.SecretOn {
			this.Err = fmt.Errorf("UniEngine: field[%s.%s] is secreton-marked; pkey values stay plaintext for WHERE matching, secreton does not apply to pkeys", this.TableName, strings.ToLower(fieldName))
			return this
		}

		lowerName := strings.ToLower(fieldName)
		if _, exists := this.HashPkeys[lowerName]; !exists {
			this.HashPkeys[lowerName] = Field
			this.ListPkeys = append(this.ListPkeys, Field)
		}
	}

	return this
}

// #SetSecret 标记加密列并返回本表,支持链式(错误处理同 SetKeys:记入 Err 并短路)
func (this *TUniTable) SetSecret(Fields ...interface{}) *TUniTable {

	if this.Err != nil {
		return this
	}

	for _, ItemPara := range Fields {

		fieldName, ok := ItemPara.(string)
		if !ok {
			this.Err = fmt.Errorf("UniEngine: SetSecret argument %v is not a string", ItemPara)
			return this
		}

		lowerName := strings.ToLower(fieldName)

		Field, Valid := this.HashField[lowerName]
		if !Valid {
			this.Err = fmt.Errorf("UniEngine: field[%s.%s] is unregistered;", this.TableName, lowerName)
			return this
		}

		//#主键值一律明文(WHERE 定位语义),与 secreton 互斥
		if _, isPkey := this.HashPkeys[lowerName]; isPkey {
			this.Err = fmt.Errorf("UniEngine: field[%s.%s] is a pkey; pkey values stay plaintext for WHERE matching, secreton does not apply to pkeys", this.TableName, lowerName)
			return this
		}

		Field.SecretOn = true
		this.HashField[lowerName] = Field

		//#表级标记同步置位(任一列被标记即认为该表含加密字段)
		this.SecretOn = true

		//#ListField 是 HashField 的另一份副本,须同步标记,否则 PrepareRunSQL 路径会读到旧值
		for i := range this.ListField {
			if strings.ToLower(this.ListField[i].FieldName) == lowerName {
				this.ListField[i].SecretOn = true
			}
		}
	}

	return this
}

// AutoKeys 按数据库元数据自动登记主键。UniEngineEx 为引擎指针(第二阶段起不再按值拷贝)。
func (this *TUniTable) AutoKeys(UniEngineEx *TUniEngine, GetSqlAutoKeys ...interface{}) error {

	var cSQL string

	if len(GetSqlAutoKeys) > 0 {

		if x, ok := GetSqlAutoKeys[0].(HasGetSqlAutoKeys); ok {
			var eror error
			cSQL, eror = x.GetSqlAutoKeys(UniEngineEx, this.TableName)
			if eror != nil {
				return eror
			}
		}

	} else {

		var eror error
		cSQL, eror = TAutoKeys4POSTGR{}.GetSqlAutoKeys(UniEngineEx, this.TableName)
		if eror != nil {
			return eror
		}

	}

	if cSQL == "" {
		return fmt.Errorf("UniEngine: can not build autokeys sql for table:%s", this.TableName)
	}

	if UniEngineEx.debugging() {
		fmt.Println(cSQL)
	}

	var ListData = make([]TUniField, 0)
	if eror := UniEngineEx.SelectL(&ListData, cSQL); eror != nil {
		return fmt.Errorf("UniEngine: table:%s: %w", this.TableName, eror)
	}

	if len(ListData) == 0 {
		return fmt.Errorf("UniEngine: table:%s or its primary key may not exist.", this.TableName)
	}

	var CodeText string
	for _, ItemPara := range ListData {
		ItemCopy, Valid := this.HashField[strings.ToLower(ItemPara.FieldName)]
		if !Valid {
			return fmt.Errorf("UniEngine: database have field[%s.%s], but class not.", this.TableName, ItemPara.FieldName)
		}

		//#主键值一律明文(WHERE 定位语义),与 secreton 互斥
		if ItemCopy.SecretOn {
			return fmt.Errorf("UniEngine: field[%s.%s] is secreton-marked; pkey values stay plaintext for WHERE matching, secreton does not apply to pkeys", this.TableName, ItemPara.FieldName)
		}

		lowerName := strings.ToLower(ItemPara.FieldName)
		if _, exists := this.HashPkeys[lowerName]; !exists {
			this.HashPkeys[lowerName] = ItemCopy
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
