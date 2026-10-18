// fuzz_test
// #模糊测试:把"防注入白名单"的性质写成可自动探索的不变量
//
// seed 语料在每次 `go test` 都会跑;真正的随机探索需显式开启:
//
//	go test -run '^$' -fuzz FuzzValidIdent -fuzztime 30s .
//
// CI 中以 10s 的短时模糊作为冒烟(见 .github/workflows/ci.yml)。
package UniEngine

import (
	"strings"
	"testing"
)

// dangerousInIdent 列出"一旦出现在拼进 SQL 的标识符里就会改变语句结构"的字符/片段。
// validIdent 的白名单([A-Za-z0-9_.])本应排除它们;本测试把该性质固化为可证伪的断言。
var dangerousInIdent = []string{
	"'", `"`, ";", " ", "\t", "\n", "\r", "--", "/*", "*/", "\\", "(", ")", ",", "=", "%", "$", "?", ":", "@", "`",
}

// FuzzValidIdent 断言:凡 validIdent 放行的标识符,必不含任何危险字符。
// 反向不变量(白名单外的字符一律拒绝)已由 TestValidIdent 的用例表覆盖,这里做开放式探索。
func FuzzValidIdent(f *testing.F) {
	for _, seed := range []string{
		"t_user",
		"schema.table",
		"T_1",
		"a;drop table x",
		"a'b",
		"a--b",
		"a b",
		"",
		"-",
		"_",
		"用户表",
		"a/*x*/b",
		"a\x00b",
		"a\nb",
		"a`b",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, name string) {
		if !validIdent(name) {
			return // #未放行即无需断言(拒绝本身就是安全的)
		}

		//#放行 ⇒ 长度非 0 且仅由白名单字符构成
		if name == "" {
			t.Fatalf("validIdent 放行了空串")
		}
		for i := 0; i < len(name); i++ {
			c := name[i]
			ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.'
			if !ok {
				t.Fatalf("validIdent 放行了白名单外字节 %q (0x%02x) in %q", string(c), c, name)
			}
		}

		//#同一性质的字符串层面表述:不得含危险片段
		for _, bad := range dangerousInIdent {
			if strings.Contains(name, bad) {
				t.Fatalf("validIdent 放行了含危险片段 %q 的标识符 %q", bad, name)
			}
		}
	})
}

// FuzzUniFieldInitialize 断言 tag 解析的两个不变量:
// 逗号后的未知选项被忽略,且不会把 FieldName 污染成含逗号的值。
func FuzzUniFieldInitialize(f *testing.F) {
	for _, seed := range []string{
		"id",
		"password,secreton",
		"-",
		"_",
		"id,readonly",
		"name, secreton , readonly",
		",secreton",
		"a,b,c",
		"",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, tag string) {
		var fld TUniField
		fld.initialize(tag)

		if strings.Contains(fld.FieldName, ",") {
			t.Fatalf("FieldName 含逗号: tag=%q -> %q", tag, fld.FieldName)
		}
		//#首段为 "-" / "_" 时应置只读(与 TestUniFieldInitialize 的用例表同源)
		head := tag
		if i := strings.IndexByte(tag, ','); i >= 0 {
			head = tag[:i]
		}
		if head == "-" || head == "_" {
			if !fld.ReadOnly {
				t.Fatalf("tag=%q 应置只读", tag)
			}
		}
	})
}

// FuzzSQLMasking 把 hook 参数重写前的"掩码/占位符扫描"作为开放输入:
// 掩码必须保持原串长度(调用方按位置回切原串),扫描不得 panic/越界。
func FuzzSQLMasking(f *testing.F) {
	for _, seed := range []string{
		"insert into t(a,b) values (?,?)",
		"insert into t(a) values ('?')",
		`insert into t(a) values ("$1")`,
		"insert into t(a) values (?) -- ?\n",
		"insert into t(a) values (?) /* ? */",
		"select ':1', '?', \"x\", `y` from t where id=$12",
		"select 'unterminated",
		"select /* unterminated",
		"select $0, :0, ?",
		"",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, query string) {
		masked := maskSQLLiterals(query)
		if len(masked) != len(query) {
			t.Fatalf("mask changed length: in=%d out=%d query=%q masked=%q", len(query), len(masked), query, masked)
		}
		for _, place := range scanPlaceholders(masked) {
			if place.Pos < 0 || place.Pos >= len(masked) {
				t.Fatalf("placeholder pos out of range: pos=%d len=%d masked=%q", place.Pos, len(masked), masked)
			}
		}
	})
}
