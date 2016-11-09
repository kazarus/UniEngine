// UniTable
package UniEngine

import "fmt"

type TUniTable struct {
	TableName string
	ListField map[string]TUniField
	ListPkeys map[string]TUniField
}

func (self *TUniTable) SetKeys(cFields ...interface{}) error {
	/*
		fmt.Println("args:", cFields)
		for indx, item := range self.ListField {
			fmt.Println("item:", indx, item)
		}
	*/

	/*
		if self.ListPkeys == nil {
			self.ListPkeys = make(map[string]TUniField, 0)
			fmt.Println("self.ListPkeys = make(map[string]TUniField, 0)")
		}
	*/

	for _, cItem := range cFields {
		fmt.Println("citem:", cItem)
		if dItem, valid := self.ListField[cItem.(string)]; valid {
			fmt.Println("dItem:", dItem)
			self.ListPkeys[cItem.(string)] = dItem
		}
	}

	return nil
}
