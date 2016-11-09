// UniField
package UniEngine

import "fmt"
import "strings"
import "reflect"

type TUniField struct {
	AttriName string //attribute:field in class
	FieldName string //field in database
	FieldType reflect.Type

	ReadOnly bool
	AutoIncr bool
}

func (self *TUniField) initialize(aValue string) {
	cArguments := strings.Split(aValue, ",")
	self.FieldName = cArguments[0]

	for _, item := range cArguments[1:] {
		fmt.Println(item)
		item := strings.TrimSpace(item)
		switch item {

		case "readonly":
			self.ReadOnly = true
		default:
			//@panic(fmt.Sprintf("Unrecognized tag option for field %v: %v", self.FieldName, item))
		}

	}
}
