// UniHookDetect
// #扩展钩子的统一探测:值接收者与指针接收者一视同仁
//
// 旧实现用 v.Interface().(HasXxx)(结构体值)或 t.Implements(iface)(结构体类型)探测。
// Go 的方法集规则下,按惯用指针接收者 func (u *TUser) GetSqlUpdate(...) 实现的钩子
// 不会出现在 T 的方法集里——于是同一类在 Select 能识别、SelectL/Insert/SaveIt 却静默
// 跳过钩子,生成默认 SQL 而不报错。本文件提供两个入口,所有调用点统一使用:
//   - hookOf:取得可调用的钩子实现(必要时对不可寻址值做一次可寻址副本)
//   - typeHasHook:仅做能力探测(不调用),用于 SelectL/M/H 这类先决策后逐行处理的路径

package UniEngine

import "reflect"

// typeHasHook 判断类型 T 或 *T 是否实现 iface。
// 用于"先决定走不走钩子路径,再在循环里取实例"的场景。
func typeHasHook(t reflect.Type, iface reflect.Type) bool {
	if t == nil || iface == nil {
		return false
	}
	if t.Implements(iface) {
		return true
	}
	if t.Kind() != reflect.Ptr {
		return reflect.PointerTo(t).Implements(iface)
	}

	return false
}

// hookOf 返回 v 对应的钩子实现,同时覆盖值接收者与指针接收者:
//  1. v 本身是指针:直接断言;
//  2. v 可寻址:用 v.Addr() 断言指针接收者;
//  3. v 是值且不可寻址:先复制到可寻址的新值,再断言指针接收者。
//
// 第 3 步只影响非指针传参的写入口;钩子实现只应读取行值并产出 SQL/实参,
// 对副本的写入不会回传到调用方(写路径本来也不依赖钩子修改结构体)。
func hookOf[T any](v reflect.Value) (T, bool) {
	var Zero T

	if !v.IsValid() {
		return Zero, false
	}

	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return Zero, false
		}
		if Hook, Ok := v.Interface().(T); Ok {
			return Hook, true
		}
		v = v.Elem()
		if !v.IsValid() {
			return Zero, false
		}
	}

	if v.CanAddr() {
		if Hook, Ok := v.Addr().Interface().(T); Ok {
			return Hook, true
		}
	}

	if Hook, Ok := v.Interface().(T); Ok {
		return Hook, true
	}

	if v.Kind() == reflect.Struct {
		Copy := reflect.New(v.Type())
		Copy.Elem().Set(v)
		if Hook, Ok := Copy.Interface().(T); Ok {
			return Hook, true
		}
	}

	return Zero, false
}
