// UniTable
package UniEngine

import "fmt"
import "errors"
import "strings"

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
				panic(fmt.Sprintf("UniEngine: field[%s.%s] is unregistered;", self.TableName, strings.ToLower(ItemPara.(string))))
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
			cSQL = x.GetSqlAutoKeys(this, self.TableName)
		}

	} else {

		var AutoKeys4POSTGR = TAutoKeys4POSTGR{}
		cSQL = AutoKeys4POSTGR.GetSqlAutoKeys(this, self.TableName)

	}

	if this.runDebug {
		fmt.Println(cSQL)
	}

	var ListData = make([]TUniField, 0)
	eror = this.SelectL(&ListData, cSQL)
	if eror != nil {
		panic(errors.New(fmt.Sprintf("UniEngine: table:%s,%s", self.TableName, eror.Error())))
	}

	if len(ListData) == 0 {
		panic(errors.New(fmt.Sprintf("UniEngine: table:%s or his.primary key may be not exist.", self.TableName)))
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
				panic(fmt.Sprintf("UniEngine: database have field[%s.%s], but class not.", self.TableName, ItemPara.FieldName))
			}
		}
	}
	CodeText = fmt.Sprintf(".SetKeys( %s )", CodeText[1:])
	fmt.Println(fmt.Sprintf("UniEngine: recommend this line instead of [%s.AutoKeys]:%s", self.TableName, CodeText))

	return nil
}
