// style_test
// #仓库卫生门禁:把两类"机械残留"写成可执行断言,不依赖审查者的记性。
//
// #背景:这两类残留曾被批量清理过一次,随后又被新代码引入(v9 清理 → v13 回归 19 处)。
// #约定若只写在文档里就会漂移,故在此固化为测试;CI 的 build-test 矩阵每次都会跑。
//
// #覆盖库本体与 integration/、contrib/ 子目录(均为磁盘上的 .go 文件,跨模块也可遍历)。
package UniEngine

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #styleSkipDir 遍历时跳过的目录
var styleSkipDir = map[string]bool{
	".git": true, "testdata": true, "vendor": true, "node_modules": true,
}

// styleWalkGoFiles 遍历仓库内全部 .go 文件并逐个交给 Visit(path, body)
func styleWalkGoFiles(t *testing.T, Visit func(path string, body string)) {
	t.Helper()

	eror := filepath.WalkDir(".", func(Path string, Entry fs.DirEntry, eror error) error {
		if eror != nil {
			return eror
		}
		if Entry.IsDir() {
			if Path != "." && styleSkipDir[Entry.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(Entry.Name(), ".go") {
			return nil
		}

		Data, eror := os.ReadFile(Path)
		if eror != nil {
			return eror
		}

		Visit(Path, string(Data))

		return nil
	})
	if eror != nil {
		t.Fatalf("遍历仓库失败: %v", eror)
	}
}

// styleCodeLines 切分源码行,并给出"处于代码中"的行下标。
// #排除三类非代码行:原始字符串(反引号)内部、块注释内部、以及 // 起首的注释行。
// #不排除这些会误报——字符串字面量里完全可能以 { 结尾或出现成对注释。
func styleCodeLines(body string) ([]string, []int) {
	Lines := strings.Split(body, "\n")

	var Code []int

	InRaw := false
	InBlock := false

	for Idx, Line := range Lines {
		if !InBlock {
			//#Go 的原始字符串不能包含反引号,故按行奇偶切换即可判定
			if strings.Count(Line, "`")%2 == 1 {
				InRaw = !InRaw
			}
		}
		if !InRaw {
			if strings.Contains(Line, "/*") && !strings.Contains(Line, "*/") {
				InBlock = true
			} else if InBlock && strings.Contains(Line, "*/") {
				InBlock = false
			}
		}

		if InRaw || InBlock || strings.HasPrefix(strings.TrimSpace(Line), "//") {
			continue
		}

		Code = append(Code, Idx)
	}

	return Lines, Code
}

// styleSameLines 判断两个行区间是否逐行完全相同
func styleSameLines(A []string, B []string) bool {
	if len(A) != len(B) {
		return false
	}
	for Idx := range A {
		if A[Idx] != B[Idx] {
			return false
		}
	}

	return true
}

// #测试1:开括号后不得紧跟空行(行尾 { 与下一行空行的组合属改写脚本残留)
func TestStyleNoBlankLineAfterOpenBrace(t *testing.T) {
	styleWalkGoFiles(t, func(Path string, body string) {
		Lines, Code := styleCodeLines(body)

		for _, Idx := range Code {
			if !strings.HasSuffix(strings.TrimRight(Lines[Idx], " \t"), "{") {
				continue
			}
			if Idx+1 < len(Lines) && strings.TrimSpace(Lines[Idx+1]) == "" {
				t.Errorf("%s:%d 开括号后紧跟空行", Path, Idx+1)
			}
		}
	})
}

// #测试2:不得出现"注释块 + 空行 + 完全相同的注释块"(同一段文档注释被写两遍)
func TestStyleNoDuplicatedDocComments(t *testing.T) {
	styleWalkGoFiles(t, func(Path string, body string) {
		Lines := strings.Split(body, "\n")

		for Idx := 0; Idx < len(Lines); {
			if !strings.HasPrefix(strings.TrimSpace(Lines[Idx]), "//") {
				Idx++
				continue
			}

			End := Idx
			for End < len(Lines) && strings.HasPrefix(strings.TrimSpace(Lines[End]), "//") {
				End++
			}
			Block := Lines[Idx:End]

			if End < len(Lines) && strings.TrimSpace(Lines[End]) == "" &&
				End+1+len(Block) <= len(Lines) && styleSameLines(Lines[End+1:End+1+len(Block)], Block) {
				t.Errorf("%s:%d 注释块与空行之后的注释块完全重复", Path, Idx+1)
			}

			Idx = End
		}
	})
}
