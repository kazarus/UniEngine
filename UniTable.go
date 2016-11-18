// UniTable
package UniEngine

//#import "fmt"
import "strings"

type TUniTable struct {
	TableName string
	ListField map[string]TUniField
	ListPkeys map[string]TUniField
}

func (self *TUniTable) SetKeys(cFields ...interface{}) error {

	for _, cItem := range cFields {
		if dItem, valid := self.ListField[cItem.(string)]; valid {
			self.ListPkeys[cItem.(string)] = dItem
		}
	}

	return nil
}

func (self *TUniTable) AutoKeys(this TUniEngine) error {

	var eror error

	cSQL := "select attname as field_name" +
		"    from pg_attribute" +
		"    left join pg_class on  pg_attribute.attrelid = pg_class.oid " +
		"    where pg_class.relname = $1  and attstattarget=-1 " +
		"    and exists (select * from pg_constraint where  pg_constraint.conrelid = pg_class.oid  and pg_constraint.contype='p' and attnum=any(conkey))"

	var listData = make([]TUniField, 0)
	eror = this.SelectL(&listData, cSQL, strings.ToLower(self.TableName))
	if eror != nil {
		panic(eror)
	}
	for _, cItem := range listData {
		if dItem, valid := self.ListField[cItem.FieldName]; valid {
			self.ListPkeys[cItem.FieldName] = dItem
		}
	}
	return nil
}
