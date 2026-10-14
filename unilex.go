// unilex
// #SQL 占位符改写的轻量词法扫描(评审 Q5):
// #正则替换会误改字符串常量与注释里的 $N;本扫描器以状态机区分代码区/字符串/注释,
// #仅对代码区的 $N 做方言转换,并为 MySQL 分支收集出现序供实参重排。

package UniEngine

import (
	"strconv"
	"strings"
)

// #占位符改写风格
const (
	phOracle = 1 //#代码区 $N -> :N(编号语义不变,实参原样)
	phMySQL  = 2 //#代码区 $N -> ?(收集出现序,由调用方按序重排实参)
)

// rewriteParamPlaceholder 词法扫描 SqlQuery,仅代码区的 $N 参与改写:
// 单引号串('..',仅 ” 双写转义)/双引号串(".." 或 MySQL 标识 "..")/反引号标识(`..`)
// /行注释(-- 后随空白或行尾)/块注释(/* */,不嵌套)内的 $N 一律原样保留。
// 返回改写后 SQL 与代码区占位符编号的**出现序**(phOracle 下亦返回,供诊断)。
// 已知边界:不处理反斜杠转义(MySQL 'don\'t' 写法)——请用 ” 写法或原生占位符(README)。
func rewriteParamPlaceholder(SqlQuery string, Style int) (string, []int, error) {
	var Buf strings.Builder
	var Seq []int

	Size := len(SqlQuery)
	Index := 0

	for Index < Size {
		Char := SqlQuery[Index]

		switch Char {
		case '\'':
			Next := scanQuoted(SqlQuery, Index, '\'')
			Buf.WriteString(SqlQuery[Index:Next])
			Index = Next

		case '"', '`':
			Next := scanQuoted(SqlQuery, Index, Char)
			Buf.WriteString(SqlQuery[Index:Next])
			Index = Next

		case '-':
			//#行注释按 MySQL 语义保守识别:-- 后随空白/行尾才算注释,
			//#避免把 "a--b"(PG 注释歧义写法)或减法表达式误吞进注释
			if Index+1 < Size && SqlQuery[Index+1] == '-' && (Index+2 >= Size || isBlank(SqlQuery[Index+2])) {
				Next := Index
				for Next < Size && SqlQuery[Next] != '\n' {
					Next++
				}
				Buf.WriteString(SqlQuery[Index:Next])
				Index = Next
			} else {
				Buf.WriteByte(Char)
				Index++
			}

		case '/':
			if Index+1 < Size && SqlQuery[Index+1] == '*' {
				Next := Index + 2
				for Next+1 < Size && !(SqlQuery[Next] == '*' && SqlQuery[Next+1] == '/') {
					Next++
				}
				if Next+1 < Size {
					Next += 2
				} else {
					Next = Size //#未闭合的块注释:整段原样保留
				}
				Buf.WriteString(SqlQuery[Index:Next])
				Index = Next
			} else {
				Buf.WriteByte(Char)
				Index++
			}

		case '$':
			Next := Index + 1
			for Next < Size && SqlQuery[Next] >= '0' && SqlQuery[Next] <= '9' {
				Next++
			}
			if Next == Index+1 { //#$ 后无数字,不是占位符
				Buf.WriteByte(Char)
				Index++
				continue
			}

			Number, eror := strconv.Atoi(SqlQuery[Index+1 : Next])
			if eror != nil {
				return "", nil, eror
			}
			Seq = append(Seq, Number)

			if Style == phMySQL {
				Buf.WriteByte('?')
			} else {
				Buf.WriteByte(':')
				Buf.WriteString(SqlQuery[Index+1 : Next])
			}
			Index = Next

		default:
			Buf.WriteByte(Char)
			Index++
		}
	}

	return Buf.String(), Seq, nil
}

// scanQuoted 返回 [Start, 结束) 的区间终点:跳过 Quote 包裹的内容。
// 单引号处理 ” 双写转义;双引号/反引号不做转义(相邻引号自然分段,不影响跳过 $N)
func scanQuoted(Text string, Start int, Quote byte) int {
	Size := len(Text)
	Index := Start + 1

	for Index < Size {
		if Text[Index] != Quote {
			Index++
			continue
		}
		if Quote == '\'' && Index+1 < Size && Text[Index+1] == '\'' {
			Index += 2
			continue
		}
		return Index + 1
	}

	return Size //#未闭合:整段按字符串处理
}

func isBlank(Char byte) bool {
	return Char == ' ' || Char == '\t' || Char == '\r' || Char == '\n'
}
