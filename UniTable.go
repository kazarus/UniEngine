// UniTable
package UniEngine

import "fmt"
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

		fieldName, ok := ItemPara.(string)
		if !ok {
			return fmt.Errorf("UniEngine: SetKeys argument %v is not a string", ItemPara)
		}

		Field, Valid = self.HashField[strings.ToLower(fieldName)]
		if Valid {
			lowerName := strings.ToLower(fieldName)
			if _, exists := self.HashPkeys[lowerName]; !exists {
				self.HashPkeys[lowerName] = Field
				self.ListPkeys = append(self.ListPkeys, Field)
			}
		} else {
			return fmt.Errorf("UniEngine: field[%s.%s] is unregistered", self.TableName, strings.ToLower(fieldName))
		}
	}

	return nil
}

func (self *TUniTable) AutoKeys(this TUniEngine, GetSqlAutoKeys ...interface{}) error {

	var err error
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

	if cSQL == "" {
		return fmt.Errorf("UniEngine: can not build autokeys sql for table:%s", self.TableName)
	}

	var ListData = make([]TUniField, 0)
	err = this.SelectL(&ListData, cSQL)
	if err != nil {
		return fmt.Errorf("UniEngine: table:%s, %w", self.TableName, err)
	}

	if len(ListData) == 0 {
		return fmt.Errorf("UniEngine: table:%s or its primary key may be not exist.", self.TableName)
	}

	var CodeText string
	for _, ItemPara := range ListData {
		ItemCopy, Valid := self.HashField[strings.ToLower(ItemPara.FieldName)]

		switch Valid {
		case true:
			{
				lowerName := strings.ToLower(ItemPara.FieldName)
				if _, exists := self.HashPkeys[lowerName]; !exists {
					self.HashPkeys[lowerName] = ItemCopy
					self.ListPkeys = append(self.ListPkeys, ItemCopy)
				}
				CodeText = CodeText + "," + `"` + ItemPara.FieldName + `"`
			}
		default:
			{
				return fmt.Errorf("UniEngine: database have field[%s.%s], but class not.", self.TableName, ItemPara.FieldName)
			}
		}
	}
	CodeText = fmt.Sprintf(".SetKeys( %s )", CodeText[1:])
	fmt.Println(fmt.Sprintf("UniEngine: recommend this line instead of [%s.AutoKeys]:%s", self.TableName, CodeText))

	return nil
}
