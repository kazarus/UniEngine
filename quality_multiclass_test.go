// quality_multiclass_test
// #生产兼容规模:read_only 一张表挂 89 个投影类、no_matter 运行时动态追加。
// #本用例用 reflect.StructOf 生成 90 个不同类,验证类键全保留、表键指向最后注册。
package UniEngine

import (
	"reflect"
	"testing"
)

func TestRegisterClassManyClassesSameTable(t *testing.T) {
	eng := newTestEngine()
	const table = "read_only"

	var last *TUniTable
	for i := 0; i < 90; i++ {
		typ := reflect.StructOf([]reflect.StructField{{
			Name: "F" + string(rune('A'+i%26)) + string(rune('A'+i/26)),
			Type: reflect.TypeOf(int64(0)),
			Tag:  `db:"id"`,
		}})
		obj := reflect.New(typ).Elem().Interface()
		tbl := eng.RegisterClass(obj, table)
		if tbl.LastError != nil {
			t.Fatalf("class %d: %v", i, tbl.LastError)
		}
		last = tbl
	}
	if got := eng.GetTable(table); got != last {
		t.Fatal("table key must point at the latest class")
	}
	for i := 0; i < 90; i++ {
		typ := reflect.StructOf([]reflect.StructField{{
			Name: "F" + string(rune('A'+i%26)) + string(rune('A'+i/26)),
			Type: reflect.TypeOf(int64(0)),
			Tag:  `db:"id"`,
		}})
		if got := eng.GetTableByClass(typ.String()); got == nil {
			t.Fatalf("class %d key missing", i)
		}
	}
}
