// UniHookSql
// #写钩子自产 SQL 的"列→实参"映射解析(读钩子"交接前解密"的写侧对称物):
// #钩子语句与实参均由钩子方生成,引擎通过解析常规 INSERT/UPDATE 形态定位 secreton
// #列对应的实参下标并就地加密;形态不可信解析时返回 !Ok,由调用方维持 fail-fast。
// #扫描复用 unilex 的引号/注释语义:字符串常量/注释不参与关键词、括号与占位符识别。
// #支持形态:
//   INSERT: insert into t ( c1, c2, ... ) values ( ?... ), ( ?... ), ...   位置式/编号式
//   UPDATE: update t set c1 = ?, c2 = ? where ...                          SET 段赋值须为占位符
// #非 secreton 列的字面量赋值允许存在(无需引擎处理);secreton 列字面量/表达式 → 不可信。

package UniEngine

import (
	"strconv"
	"strings"
)

// hookSecretPlan 解析产物:Idx 为待加密实参下标,已**保序去重**。
// 展开顺序为**列优先**(外层遍历列清单、内层遍历 VALUES 组):同一编号占位符服务多个
// 列位时只出现一次。顺序不影响结果——调用方按集合就地加密每个下标。
// ArgCount 为 SQL 声明的实参布局:位置式=占位符总数,编号式=最大编号(与 lib/pq·pgx
// 的 NumInput 同口径,同编号复用不增加实参);调用方以其与 len(args) 比对,
// 防止"一组 VALUES 配多行实参"之类的错位。
type hookSecretPlan struct {
	Idx      []int
	ArgCount int
}

// resolveHookSecretArgs 解析写钩子 SQL,Ok=false 表示形态无法可信解析。
func resolveHookSecretArgs(UniTable *TUniTable, SqlQuery string) (hookSecretPlan, bool) {
	Masked := maskSQLLiterals(SqlQuery)
	Plcs := scanPlaceholders(Masked)

	secretOf := func(ColKey string) bool {
		if ColKey == "" {
			return false
		}
		UniField, Valid := UniTable.HashField[ColKey]
		return Valid && UniTable.secretColumn(UniField)
	}

	if ValuesPos := findKeyword(Masked, "values", 0); ValuesPos >= 0 {
		Plan, Ok := resolveInsertSecretArgs(SqlQuery, Masked, Plcs, ValuesPos, secretOf)
		if !Ok {
			return hookSecretPlan{}, false
		}
		Plan.Idx = dedupeInts(Plan.Idx)
		Plan.ArgCount = placeholderArgCount(Plcs)
		return Plan, true
	}
	if SetPos := findKeyword(Masked, "set", 0); SetPos >= 0 {
		Plan, Ok := resolveUpdateSecretArgs(SqlQuery, Masked, Plcs, SetPos, secretOf)
		if !Ok {
			return hookSecretPlan{}, false
		}
		Plan.Idx = dedupeInts(Plan.Idx)
		Plan.ArgCount = placeholderArgCount(Plcs)
		return Plan, true
	}
	return hookSecretPlan{}, false
}

// dedupeInts 保序去重:编号式占位符可被多个 secreton 列位复用($1,$1 / 多行 $1,$2,$1,$2),
// 同一实参只加密一次——重复加密会产出双层密文,读取侧解一次得到密文文本(静默错值)。
func dedupeInts(List []int) []int {
	if len(List) < 2 {
		return List
	}
	Seen := make(map[int]bool, len(List))
	Out := List[:0]
	for _, Item := range List {
		if Seen[Item] {
			continue
		}
		Seen[Item] = true
		Out = append(Out, Item)
	}
	return Out
}

// placeholderArgCount SQL 声明的实参布局:编号式($N/:N)以最大编号为准
// (同编号复用不增加实参,如 values ($1,$1) 只需 1 个实参);纯位置式=占位符总数。
func placeholderArgCount(Plcs []sqlPlace) int {
	MaxArg := -1
	Positional := 0
	for _, Pl := range Plcs {
		if Pl.Num {
			if Pl.Arg+1 > MaxArg {
				MaxArg = Pl.Arg + 1
			}
		} else {
			Positional++
		}
	}
	if MaxArg >= 0 {
		return MaxArg
	}
	return Positional
}

// resolveInsertSecretArgs INSERT 形态:values 前最后一个括号组为列清单,其后为
// 若干纯占位符组 `( ?, ? ), ...`;占位符总数即实参布局。
func resolveInsertSecretArgs(SqlQuery, Masked string, Plcs []sqlPlace, ValuesPos int, secretOf func(string) bool) (hookSecretPlan, bool) {
	//#values 之前出现占位符(如表名里掺占位符)→ 形态不可信
	for _, Pl := range Plcs {
		if Pl.Pos < ValuesPos || Pl.Arg < 0 {
			return hookSecretPlan{}, false
		}
	}

	Open := strings.LastIndex(Masked[:ValuesPos], "(")
	if Open < 0 {
		return hookSecretPlan{}, false
	}
	Close := matchParen(Masked, Open)
	if Close < 0 || Close > ValuesPos {
		return hookSecretPlan{}, false
	}
	var Cols []string
	for _, Seg := range splitTopLevel(Masked[Open+1 : Close]) {
		ColKey := hookColKey(SqlQuery[Open+1+Seg[0] : Open+1+Seg[1]])
		if ColKey == "" {
			return hookSecretPlan{}, false
		}
		Cols = append(Cols, ColKey)
	}
	if len(Cols) == 0 {
		return hookSecretPlan{}, false
	}

	//#values 之后:若干括号组,组内仅占位符;组外仅空白
	var Groups [][]sqlPlace
	//#组扫描从 values 关键字之后开始(列清单闭括号与 values 之间还有关键字本身)
	Pos := ValuesPos + len("values")
	for Pos < len(Masked) {
		if isBlank(Masked[Pos]) || Masked[Pos] == ',' {
			Pos++
			continue
		}
		if Masked[Pos] != '(' {
			return hookSecretPlan{}, false
		}
		GroupClose := matchParen(Masked, Pos)
		if GroupClose < 0 {
			return hookSecretPlan{}, false
		}
		var Row []sqlPlace
		var Spans [][2]int
		for _, Pl := range Plcs {
			if Pl.Pos > Pos && Pl.Pos < GroupClose {
				Row = append(Row, Pl)
				Spans = append(Spans, [2]int{Pl.Pos, tokenEnd(Masked, Pl.Pos)})
			}
		}
		//#组内占位符 token 之外不得有其它内容(字面量/表达式 → 不可信)
		Cur := Pos + 1
		for _, Span := range Spans {
			for i := Cur; i < Span[0]; i++ {
				if !isBlank(Masked[i]) && Masked[i] != ',' {
					return hookSecretPlan{}, false
				}
			}
			Cur = Span[1]
		}
		for i := Cur; i < GroupClose; i++ {
			if !isBlank(Masked[i]) && Masked[i] != ',' {
				return hookSecretPlan{}, false
			}
		}
		if len(Row) != len(Cols) {
			return hookSecretPlan{}, false
		}
		Groups = append(Groups, Row)
		Pos = GroupClose + 1
	}
	if len(Groups) == 0 {
		return hookSecretPlan{}, false
	}

	//#ArgCount 由调用方按 placeholderArgCount 统一赋值(本函数只产出 Idx)
	Plan := hookSecretPlan{}
	for j, ColKey := range Cols {
		if !secretOf(ColKey) {
			continue
		}
		for _, Row := range Groups {
			if Row[j].Arg < 0 {
				return hookSecretPlan{}, false
			}
			Plan.Idx = append(Plan.Idx, Row[j].Arg)
		}
	}
	return Plan, true
}

// resolveUpdateSecretArgs UPDATE 形态:SET 段按深度 0 逗号切赋值,每段须为
// `col = ?`;非 secreton 列的字面量赋值跳过,secreton 列字面量/表达式 → 不可信。
func resolveUpdateSecretArgs(SqlQuery, Masked string, Plcs []sqlPlace, SetPos int, secretOf func(string) bool) (hookSecretPlan, bool) {
	End := len(Masked)
	if WherePos := findKeyword(Masked, "where", SetPos+3); WherePos >= 0 {
		End = WherePos
	}

	Plan := hookSecretPlan{}
	for _, Seg := range splitTopLevel(Masked[SetPos+3 : End]) {
		SegStart := SetPos + 3 + Seg[0]
		SegMasked := Masked[SegStart : SetPos+3+Seg[1]]
		Eq := indexAssign(SegMasked)
		if Eq < 0 {
			return hookSecretPlan{}, false
		}
		ColKey := hookColKey(SqlQuery[SegStart : SegStart+Eq])
		if ColKey == "" {
			return hookSecretPlan{}, false
		}

		//#右值(掩码上)去空白后必须是单个占位符 token
		RightMasked := SegMasked[Eq+1:]
		Lead := len(RightMasked) - len(strings.TrimLeft(RightMasked, " \t\r\n"))
		Abs := SegStart + Eq + 1 + Lead
		TrimmedLen := len(strings.TrimSpace(RightMasked))
		Pl, Found := placeAt(Plcs, Abs)
		IsPlace := Found && tokenEnd(Masked, Abs)-Abs == TrimmedLen && TrimmedLen > 0

		if !secretOf(ColKey) {
			continue //#非 secreton 列:字面量/表达式无需引擎处理
		}
		if !IsPlace || Pl.Arg < 0 {
			return hookSecretPlan{}, false
		}
		Plan.Idx = append(Plan.Idx, Pl.Arg)
	}
	return Plan, true
}

// maskSQLLiterals 返回掩码副本:字符串/双引号标识符/反引号标识符的内部替换为
// 空格(定界符保留),注释整段(含标记)替换为空格——位置长度不变,内容定位后
// 回切原串取列名。
func maskSQLLiterals(SqlQuery string) string {
	Buf := []byte(SqlQuery)
	Size := len(SqlQuery)
	Index := 0
	for Index < Size {
		switch Char := SqlQuery[Index]; Char {
		case '\'', '"', '`':
			Next := scanQuoted(SqlQuery, Index, Char)
			//#闭合:内容止于闭定界符之前;未闭合(scanQuoted 到串尾未遇定界符):
			//#开口之后全部为内容,多掩一位——否则末字符残留会被误认占位符/括号
			End := Next - 1
			if Next >= Size && (Next-1 <= Index || SqlQuery[Next-1] != Char) {
				End = Next
			}
			for i := Index + 1; i < End && i < Size; i++ {
				Buf[i] = ' '
			}
			Index = Next
		case '-':
			if isLineComment(SqlQuery, Index) {
				Next := Index
				for Next < Size && SqlQuery[Next] != '\n' {
					Next++
				}
				for i := Index; i < Next; i++ {
					Buf[i] = ' '
				}
				Index = Next
			} else {
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
					Next = Size
				}
				for i := Index; i < Next; i++ {
					Buf[i] = ' '
				}
				Index = Next
			} else {
				Index++
			}
		default:
			Index++
		}
	}
	return string(Buf)
}

// isLineComment 判断 Pos 处是否开启行注释。**两遍扫描共用本判定**——掩码副本
// (`maskSQLLiterals`)与列名剥离(`stripSQLComments`)必须对同一输入给出同一解释,
// 否则"掩码认为不是注释、剥离认为是注释"会造成边界与内容不一致。
//
// 口径取 MySQL 语义:`--` 之后须为空白或行尾才算注释(ANSI/PG 无此要求)。
// 取该方向是为了**保守**:`a--b` 在 MySQL 里是算术表达式,若当注释掩掉,
// 其后可能是活代码(含占位符)会被一并掩掉 ⇒ 可能漏加密或漏映射;
// 反之把真注释当代码,只会让形态判定更严并 fail-fast(不静默)。
func isLineComment(Text string, Pos int) bool {
	if Pos+1 >= len(Text) || Text[Pos] != '-' || Text[Pos+1] != '-' {
		return false
	}
	return Pos+2 >= len(Text) || isBlank(Text[Pos+2])
}

// isIdentChar SQL 标识符字符(字母/数字/下划线)。
func isIdentChar(Char byte) bool {
	return Char == '_' || (Char >= '0' && Char <= '9') || (Char >= 'a' && Char <= 'z') || (Char >= 'A' && Char <= 'Z')
}

// findKeyword 掩码副本上的整词关键词定位(大小写不敏感),前后邻接标识符字符
// (如 offset 里的 set)不算命中;无则 -1。
func findKeyword(Masked string, Word string, From int) int {
	Lower := strings.ToLower(Masked)
	Off := From
	for Off <= len(Lower)-len(Word) {
		Idx := strings.Index(Lower[Off:], Word)
		if Idx < 0 {
			return -1
		}
		Idx += Off
		End := Idx + len(Word)
		Before := Idx == 0 || !isIdentChar(Masked[Idx-1])
		After := End == len(Masked) || !isIdentChar(Masked[End])
		if Before && After {
			return Idx
		}
		Off = Idx + 1
	}
	return -1
}

// matchParen 返回与 Open 配对的 ')' 下标;字符串/注释内的括号已被掩码,不干扰配对。
func matchParen(Masked string, Open int) int {
	Depth := 0
	for i := Open; i < len(Masked); i++ {
		switch Masked[i] {
		case '(':
			Depth++
		case ')':
			Depth--
			if Depth == 0 {
				return i
			}
		}
	}
	return -1
}

// splitTopLevel 按深度 0 的逗号切段,返回各段 [起,止) 区间(相对入参)。
func splitTopLevel(Segment string) [][2]int {
	var Out [][2]int
	Depth := 0
	Start := 0
	for i := 0; i < len(Segment); i++ {
		switch Segment[i] {
		case '(':
			Depth++
		case ')':
			Depth--
		case ',':
			if Depth == 0 {
				Out = append(Out, [2]int{Start, i})
				Start = i + 1
			}
		}
	}
	return append(Out, [2]int{Start, len(Segment)})
}

// sqlPlace 一个占位符:Arg 为对应实参下标($N/:N → N-1,? → 出现序),Pos 为掩码副本
// 位置,Num 标记编号式(实参布局以最大编号计,复用同编号不增加实参)。
type sqlPlace struct {
	Arg int
	Pos int
	Num bool
}

// scanPlaceholders 收集掩码副本代码区全部占位符(? 按出现序,$N/:N 按编号-1)。
func scanPlaceholders(Masked string) []sqlPlace {
	var List []sqlPlace
	Counter := 0
	for i := 0; i < len(Masked); i++ {
		switch Masked[i] {
		case '?':
			List = append(List, sqlPlace{Arg: Counter, Pos: i})
			Counter++
		case '$', ':':
			End := i + 1
			for End < len(Masked) && Masked[End] >= '0' && Masked[End] <= '9' {
				End++
			}
			if End == i+1 {
				continue
			}
			Number, eror := strconv.Atoi(Masked[i+1 : End])
			if eror != nil {
				continue
			}
			List = append(List, sqlPlace{Arg: Number - 1, Pos: i, Num: true})
			i = End - 1
		}
	}
	return List
}

// tokenEnd 占位符 token 的终点(? 为 1 字符,$N/:N 含编号数字)。
func tokenEnd(Masked string, Pos int) int {
	if Masked[Pos] == '?' {
		return Pos + 1
	}
	End := Pos + 1
	for End < len(Masked) && Masked[End] >= '0' && Masked[End] <= '9' {
		End++
	}
	return End
}

// placeAt 按位置取占位符。
func placeAt(Plcs []sqlPlace, Pos int) (sqlPlace, bool) {
	for _, Pl := range Plcs {
		if Pl.Pos == Pos {
			return Pl, true
		}
	}
	return sqlPlace{}, false
}

// hookColKey 列段原文归一化为注册表键:去注释(引号感知的块/行注释剥离),去引号
// 包裹,校验纯标识符字符,转小写;含其它字符(点/括号/运算符等)返回空串。
// 注:纯注释段(剥离后为空)仍返回空串,由调用方按不可信形态拒绝。
func hookColKey(Raw string) string {
	Text := strings.TrimSpace(stripSQLComments(Raw))
	if Text == "" {
		return ""
	}
	if (Text[0] == '"' && Text[len(Text)-1] == '"' && len(Text) > 1) ||
		(Text[0] == '`' && Text[len(Text)-1] == '`' && len(Text) > 1) {
		Text = Text[1 : len(Text)-1]
	}
	for i := 0; i < len(Text); i++ {
		if !isIdentChar(Text[i]) {
			return ""
		}
	}
	return strings.ToLower(Text)
}

// stripSQLComments 去除片段中的块注释(/*..*/,不嵌套)与行注释(--..行尾),
// 引号串/标识符内部原样保留(复用 unilex 的 scanQuoted 语义)。
func stripSQLComments(Raw string) string {
	var Buf strings.Builder
	Size := len(Raw)
	for i := 0; i < Size; {
		Char := Raw[i]
		switch {
		case Char == '\'' || Char == '"' || Char == '`':
			Next := scanQuoted(Raw, i, Char)
			Buf.WriteString(Raw[i:Next])
			i = Next
		case Char == '/' && i+1 < Size && Raw[i+1] == '*':
			End := strings.Index(Raw[i+2:], "*/")
			if End < 0 {
				i = Size //#未闭合块注释:剩余全部为注释
			} else {
				i = i + End + 4
			}
		case isLineComment(Raw, i): //#与掩码同口径:-- 后须空白或行尾
			End := strings.IndexByte(Raw[i:], '\n')
			if End < 0 {
				i = Size
			} else {
				i += End //#换行符保留,行注释止于行尾
			}
		default:
			Buf.WriteByte(Char)
			i++
		}
	}
	return Buf.String()
}

// indexAssign 赋值段中独立 '=' 的下标(前后邻接 ><!= 的比较运算符不算)。
func indexAssign(Text string) int {
	for i := 0; i < len(Text); i++ {
		if Text[i] != '=' {
			continue
		}
		if i > 0 && (Text[i-1] == '>' || Text[i-1] == '<' || Text[i-1] == '!' || Text[i-1] == '=') {
			continue
		}
		if i+1 < len(Text) && Text[i+1] == '>' {
			continue
		}
		return i
	}
	return -1
}
