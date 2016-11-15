// UniTable
package UniEngine

//#import "fmt"

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
