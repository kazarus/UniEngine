// UniField
package UniEngine

import "strings"

type TUniField struct {
	TableName string `db:"table_name" json:"tableName"` //table in database
	FieldName string `db:"field_name" json:"fieldName"` //field in database
	AttriName string `db:"attri_name" json:"attriName"` //field in class(attribute)

	//@FieldType reflect.Type

	ReadOnly bool //#是否只读
	PkeyOnly bool //#是否主键#数据同步时用到,其他地方不要用,未初始化;
}

func (self *TUniField) initialize(aValue string) {

	cArguments := strings.Split(aValue, ",")
	self.FieldName = cArguments[0]
	if self.FieldName == "-" {
		self.ReadOnly = true
	}

	for _, item := range cArguments[1:] {
		item := strings.TrimSpace(item)
		//@fmt.Println("UniField:Initialzie:", item)
		switch item {
		case "readonly":
			self.ReadOnly = true
		default:
			//@panic(fmt.Sprintf("Unrecognized tag option for field %v: %v", self.FieldName, item))
		}

	}
}
