// UniTable
package UniEngine

import (
	"fmt"
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

func (self *TUniTable) SetKeys(Fields ...interface{}) error {

	var Valid bool
	var Field TUniField

	for _, ItemPara := range Fields {

		Field, Valid = self.HashField[strings.ToLower(ItemPara.(string))]
		switch Valid {
		case true:
			{
				self.HashPkeys[strings.ToLower(ItemPara.(string))] = Field
			}
		default:
			{
				return fmt.Errorf("UniEngine: field[%s.%s] is unregistered;", self.TableName, strings.ToLower(ItemPara.(string)))
			}
		}
	}

	return nil
}

func (self *TUniTable) AutoKeys(this TUniEngine, GetSqlAutoKeys ...interface{}) error {

	var eror error
	var cSQL string = ""

	if len(GetSqlAutoKeys) > 0 {

		if x, ok := GetSqlAutoKeys[0].(HasGetSqlAutoKeys); ok {
			cSQL, eror = x.GetSqlAutoKeys(this, self.TableName)
			if eror != nil {
				return eror
			}
		}

	} else {

		var AutoKeys4POSTGR = TAutoKeys4POSTGR{}
		cSQL, eror = AutoKeys4POSTGR.GetSqlAutoKeys(this, self.TableName)
		if eror != nil {
			return eror
		}

	}

	if this.runDebug {
		fmt.Println(cSQL)
	}

	var ListData = make([]TUniField, 0)
	eror = this.SelectL(&ListData, cSQL)
	if eror != nil {
		return fmt.Errorf("UniEngine: table:%s,%s", self.TableName, eror.Error())
	}

	if len(ListData) == 0 {
		return fmt.Errorf("UniEngine: table:%s or his.primary key may be not exist.", self.TableName)
	}

	var CodeText string
	for _, ItemPara := range ListData {
		ItemCopy, Valid := self.HashField[strings.ToLower(ItemPara.FieldName)]

		switch Valid {
		case true:
			{
				self.HashPkeys[strings.ToLower(ItemPara.FieldName)] = ItemCopy
				CodeText = CodeText + "," + fmt.Sprintf(`"`+ItemPara.FieldName+`"`)
			}
		default:
			{
				return fmt.Errorf("UniEngine: database have field[%s.%s], but class not.", self.TableName, ItemPara.FieldName)
			}
		}
	}
	CodeText = fmt.Sprintf(".SetKeys( %s )", CodeText[1:])
	fmt.Printf("UniEngine: recommend this line instead of [%s.AutoKeys]:%s\n", self.TableName, CodeText)

	return nil
}
