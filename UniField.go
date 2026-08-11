// UniField
package UniEngine

import "strings"

type TUniField struct {
	TableName string `db:"table_name" json:"tableName"` //table in database
	FieldName string `db:"field_name" json:"fieldName"` //field in database
	AttriName string `db:"attri_name" json:"attriName"` //field in class(attribute)

	//#字段元数据(以下字段当前未被引擎使用,仅作登记)
	TypeName string `db:"type_name" json:"typeName"` //#字段类型
	DataLen  string `db:"data_leng" json:"dataLeng"` //#字段长度
	DataSize int    `db:"data_size" json:"dataSize"` //#字段精度
	ReadOnly bool   `db:"read_only" json:"readOnly"` //#是否只读
	PkeyOnly bool   `db:"pkey_only" json:"pkeyOnly"` //#是否主键#数据同步时用到,其他地方不要用,未初始化;
}

func (self *TUniField) initialize(aValue string) {

	cArguments := strings.Split(aValue, ",")
	self.FieldName = cArguments[0]
	if self.FieldName == "-" || self.FieldName == "_" {
		self.ReadOnly = true
	}

	for _, item := range cArguments[1:] {
		item := strings.TrimSpace(item)
		switch item {
		case "readonly":
			self.ReadOnly = true
		default:
			//未识别的 tag 选项,忽略
		}
	}
}
