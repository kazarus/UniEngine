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

	HashField map[string]TUniField //ToLower
	HashPkeys map[string]TUniField //ToLower

	ListField []TUniField //ToLower
	ListPkeys []TUniField //ToLower
}

func (self *TUniTable) HasField(FieldName string) (bool, error) {

	if _, Valid := self.HashField[strings.ToLower(FieldName)]; Valid {
		return true, nil
	}

	return false, nil
}

// orderedFields 返回全部已注册字段的确定序:先按 ListField(类声明序/既有列表序),
// 其后按字段名排序补齐仅写入 HashField 的字段(如 RegisterField 直写);同名字段以
// 先出现者为准。CRUD 生成 SQL 一律走本序,保证同一输入产生相同语句文本,利于数据库端语句缓存。
func (self *TUniTable) orderedFields() []TUniField {

	if len(self.HashField) == 0 {
		return nil
	}

	seen := make(map[string]bool, len(self.ListField))
	list := make([]TUniField, 0, len(self.HashField))

	for _, ItemPara := range self.ListField {
		lowerName := strings.ToLower(ItemPara.FieldName)
		if _, Valid := self.HashField[lowerName]; !Valid || seen[lowerName] {
			continue
		}
		seen[lowerName] = true
		list = append(list, ItemPara)
	}

	var rest []string
	for lowerName := range self.HashField {
		if !seen[lowerName] {
			rest = append(rest, lowerName)
		}
	}
	sort.Strings(rest)
	for _, lowerName := range rest {
		list = append(list, self.HashField[lowerName])
	}

	return list
}

// orderedPkeys 返回全部主键字段的确定序:ListPkeys 顺序优先,
// 其后按字段名排序补齐仅写入 HashPkeys 的字段(如 RegisterPkeys 直写)。
func (self *TUniTable) orderedPkeys() []TUniField {

	if len(self.HashPkeys) == 0 {
		return nil
	}

	seen := make(map[string]bool, len(self.ListPkeys))
	list := make([]TUniField, 0, len(self.HashPkeys))

	for _, ItemPara := range self.ListPkeys {
		lowerName := strings.ToLower(ItemPara.FieldName)
		if _, Valid := self.HashPkeys[lowerName]; !Valid || seen[lowerName] {
			continue
		}
		seen[lowerName] = true
		list = append(list, ItemPara)
	}

	var rest []string
	for lowerName := range self.HashPkeys {
		if !seen[lowerName] {
			rest = append(rest, lowerName)
		}
	}
	sort.Strings(rest)
	for _, lowerName := range rest {
		list = append(list, self.HashPkeys[lowerName])
	}

	return list
}

// writableFields 返回可经反射赋值的写入列(非只读且有 AttriName 映射),按确定序。
// RegisterField 直写的字段无 AttriName,不参与反射赋值路径。
func (self *TUniTable) writableFields() []TUniField {

	var fields []TUniField
	for _, ItemPara := range self.orderedFields() {
		if ItemPara.ReadOnly || ItemPara.AttriName == "" {
			continue
		}
		fields = append(fields, ItemPara)
	}

	return fields
}

// pkeyFields 返回可用于 where 定位的主键列(有 AttriName 映射),按确定序。
func (self *TUniTable) pkeyFields() []TUniField {

	var keys []TUniField
	for _, ItemPara := range self.orderedPkeys() {
		if ItemPara.AttriName == "" {
			continue
		}
		keys = append(keys, ItemPara)
	}

	return keys
}

func (self *TUniTable) SetKeys(Fields ...interface{}) error {

	for _, ItemPara := range Fields {

		fieldName, ok := ItemPara.(string)
		if !ok {
			return fmt.Errorf("UniEngine: SetKeys argument %v is not a string", ItemPara)
		}

		Field, Valid := self.HashField[strings.ToLower(fieldName)]
		if !Valid {
			return fmt.Errorf("UniEngine: field[%s.%s] is unregistered", self.TableName, strings.ToLower(fieldName))
		}

		lowerName := strings.ToLower(fieldName)
		if _, exists := self.HashPkeys[lowerName]; !exists {
			self.HashPkeys[lowerName] = Field
			self.ListPkeys = append(self.ListPkeys, Field)
		}
	}

	return nil
}

func (self *TUniTable) SetSecret(Fields ...interface{}) error {

	for _, ItemPara := range Fields {

		fieldName, ok := ItemPara.(string)
		if !ok {
			return fmt.Errorf("UniEngine: SetSecret argument %v is not a string", ItemPara)
		}

		lowerName := strings.ToLower(fieldName)

		Field, Valid := self.HashField[lowerName]
		if !Valid {
			return fmt.Errorf("UniEngine: field[%s.%s] is unregistered;", self.TableName, lowerName)
		}

		Field.Encrypt = true
		self.HashField[lowerName] = Field

		//#ListField 是 HashField 的另一份副本,须同步标记,否则 PrepareRunSQL 路径会读到旧值
		for i := range self.ListField {
			if strings.ToLower(self.ListField[i].FieldName) == lowerName {
				self.ListField[i].Encrypt = true
			}
		}
	}

	return nil
}

func (self *TUniTable) AutoKeys(this TUniEngine, GetSqlAutoKeys ...interface{}) error {

	var cSQL string

	if len(GetSqlAutoKeys) > 0 {

		if x, ok := GetSqlAutoKeys[0].(HasGetSqlAutoKeys); ok {
			var eror error
			cSQL, eror = x.GetSqlAutoKeys(this, self.TableName)
			if eror != nil {
				return eror
			}
		}

	} else {

		var eror error
		cSQL, eror = TAutoKeys4POSTGR{}.GetSqlAutoKeys(this, self.TableName)
		if eror != nil {
			return eror
		}

	}

	if cSQL == "" {
		return fmt.Errorf("UniEngine: can not build autokeys sql for table:%s", self.TableName)
	}

	if this.runDebug {
		fmt.Println(cSQL)
	}

	var ListData = make([]TUniField, 0)
	if eror := this.SelectL(&ListData, cSQL); eror != nil {
		return fmt.Errorf("UniEngine: table:%s: %w", self.TableName, eror)
	}

	if len(ListData) == 0 {
		return fmt.Errorf("UniEngine: table:%s or its primary key may not exist.", self.TableName)
	}

	var CodeText string
	for _, ItemPara := range ListData {
		ItemCopy, Valid := self.HashField[strings.ToLower(ItemPara.FieldName)]
		if !Valid {
			return fmt.Errorf("UniEngine: database have field[%s.%s], but class not.", self.TableName, ItemPara.FieldName)
		}

		lowerName := strings.ToLower(ItemPara.FieldName)
		if _, exists := self.HashPkeys[lowerName]; !exists {
			self.HashPkeys[lowerName] = ItemCopy
			self.ListPkeys = append(self.ListPkeys, ItemCopy)
		}
		CodeText = CodeText + "," + `"` + ItemPara.FieldName + `"`
	}
	CodeText = fmt.Sprintf(".SetKeys( %s )", CodeText[1:])
	fmt.Printf("UniEngine: recommend this line instead of [%s.AutoKeys]:%s\n", self.TableName, CodeText)

	return nil
}
