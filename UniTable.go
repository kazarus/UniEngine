// UniTable
package UniEngine

import "fmt"
import "errors"
import "strings"

type TUniTable struct {
	TableName string
	ListField map[string]TUniField //ToLower
	ListPkeys map[string]TUniField //ToLower
}

func (self *TUniTable) SetKeys(cFields ...interface{}) error {

	for _, cItem := range cFields {
		if dItem, valid := self.ListField[strings.ToLower(cItem.(string))]; valid {
			self.ListPkeys[strings.ToLower(cItem.(string))] = dItem
		}
	}

	return nil
}

func (self *TUniTable) AutoKeys(this TUniEngine, GetSqlAutoKeys ...interface{}) error {

	var eror error
	var cSQL string = ""

	if len(GetSqlAutoKeys) > 0 {

		if x, ok := GetSqlAutoKeys[0].(HasGetSqlAutoKeys); ok {
			cSQL = x.GetSqlAutoKeys(self.TableName)
		}

	} else {

		var AutoKeys4POSTGR = TAutoKeys4POSTGR{}
		cSQL = AutoKeys4POSTGR.GetSqlAutoKeys(self.TableName)

	}

	fmt.Println(cSQL)
	var listData = make([]TUniField, 0)
	eror = this.SelectL(&listData, cSQL)
	if eror != nil {
		panic(errors.New(fmt.Sprintf("table:%s,%s", self.TableName, eror.Error())))
	}

	if len(listData) == 0 {
		panic(errors.New(fmt.Sprintf("table:%s may be not exist", self.TableName)))
	}

	var cTXT string
	for _, cItem := range listData {
		dItem, valid := self.ListField[strings.ToLower(cItem.FieldName)]
		if valid {
			self.ListPkeys[strings.ToLower(cItem.FieldName)] = dItem
			cTXT = cTXT + "," + fmt.Sprintf(`"`+cItem.FieldName+`"`)
		} else {
			panic(fmt.Sprintf("UniEngine: database have field[%s.%s], but class not.", self.TableName, cItem.FieldName))
		}
	}
	cTXT = fmt.Sprintf(".SetKeys( %s )", cTXT[1:])
	fmt.Println(fmt.Sprintf("UniEngine: recommend this line instead of [%s.AutoKeys]:%s", self.TableName, cTXT))

	return nil
}
