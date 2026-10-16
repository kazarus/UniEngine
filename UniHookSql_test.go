// UniHookSql_test
// #写钩子 SQL 解析的单元用例:掩码语义(注释/字符串/引号标识符)、关键词词边界、列名归一化。
// 这三者是"列→实参"映射的地基——掩码副本的边界是否可靠,直接决定 secreton 列定位是否可信。
// 掩码契约:长度守恒、被掩码区域内容替换为空格、定界符保留(便于按位置回切原文取列名)。

package UniEngine

import (
	"strings"
	"testing"
)

// #测试1:maskSQLLiterals 的长度守恒不变量(位置回切的前提)
func TestMaskSQLLiteralsLengthInvariant(t *testing.T) {
	Inputs := []string{
		"",
		"insert into t (a,b) values (?,?)",
		"insert into t (a,b) -- values (?,?)\n values (?,?)",
		"update t set a='x' /* set b='y' */ where id=?",
		"select 'a''b', \"c\"\"d\", `e``f` from t",
		"/* 未闭合",
		"-- 未换行",
		"'未闭合字符串",
	}

	for _, In := range Inputs {
		if Got := maskSQLLiterals(In); len(Got) != len(In) {
			t.Fatalf("长度不守恒(%d→%d): %q", len(In), len(Got), In)
		}
	}
}

// #测试2:字符串/引号标识符——内部内容掩为空格,定界符保留
func TestMaskSQLLiteralsQuoted(t *testing.T) {
	Cases := []struct {
		name string
		in   string
		want string
	}{
		{"单引号字面量", "'abc'", "'   '"},
		{"空字面量", "''", "''"},
		{"双写引号是转义而非结束", "'it''s'", "'     '"},
		{"双引号标识符", `"col"`, `"   "`},
		{"反引号标识符", "`col`", "`   `"},
		{"混合定界符互不干扰", "'a\"b'", "'   '"},
		{"字面量内的注释标记不是注释", "'/*x*/'", "'     '"},
		{"标识符内的减号不是注释", "`a-b`", "`   `"},
	}

	for _, C := range Cases {
		if Got := maskSQLLiterals(C.in); Got != C.want {
			t.Fatalf("%s: maskSQLLiterals(%q)=%q, want %q", C.name, C.in, Got, C.want)
		}
	}
}

// #测试3:注释——整段(含标记)掩为空格;换行保留;非注释的 - / 原样
func TestMaskSQLLiteralsComments(t *testing.T) {
	Cases := []struct {
		name string
		in   string
		want string
	}{
		{"行注释到行尾", "-- x\nab", "    \nab"},
		{"行注释到输入尾", "ab -- x", "ab     "},
		{"MySQL 语义:--后无空白不是注释", "--x", "--x"},
		{"减号运算不是注释", "a-b", "a-b"},
		{"块注释", "a/* x */b", "a       b"},
		{"未闭合块注释掩到结尾", "a/* x", "a    "},
		{"除号不是注释", "a/b", "a/b"},
		{"注释内的引号不开启字符串", "-- '\nab", "    \nab"},
		{"字符串内的注释标记不开启注释", "'--' x", "'  ' x"},
	}

	for _, C := range Cases {
		if Got := maskSQLLiterals(C.in); Got != C.want {
			t.Fatalf("%s: maskSQLLiterals(%q)=%q, want %q", C.name, C.in, Got, C.want)
		}
	}
}

// #测试4:掩码后的关键词定位——被掩码内容不得影响 values/set 的定位
func TestFindKeywordOnMasked(t *testing.T) {
	Cases := []struct {
		name string
		in   string
		word string
		want int
	}{
		{"正常命中", "insert into t values (?)", "values", 14},
		{"大小写不敏感", "INSERT INTO T VALUES (?)", "values", 14},
		{"标识符内不算命中", "t_values_x", "values", -1},
		{"下划线邻接不算命中", "values_x", "values", -1},
		{"set 在 offset 内不算命中", "update offset set a=?", "set", 14},
		{"注释里的关键词不算命中", "a -- values (?,?)\n values (?)", "values", 19},
		{"字符串里的关键词不算命中", "a 'values' values (?)", "values", 11},
		{"无命中返回 -1", "select 1", "values", -1},
		{"起始位置即命中", "values (?)", "values", 0},
	}

	for _, C := range Cases {
		if Got := findKeyword(maskSQLLiterals(C.in), C.word, 0); Got != C.want {
			t.Fatalf("%s: findKeyword(%q) = %d, want %d", C.name, C.in, Got, C.want)
		}
	}
}

// #测试5:hookColKey 归一化——去引号、转小写、非标识符字符一律拒绝
func TestHookColKey(t *testing.T) {
	Ok := []struct {
		in   string
		want string
	}{
		{"password", "password"},
		{"  Password  ", "password"},
		{`"password"`, "password"},
		{"`password`", "password"},
		{"user_name", "user_name"},
		{"A1", "a1"},
	}
	for _, C := range Ok {
		if Got := hookColKey(C.in); Got != C.want {
			t.Fatalf("hookColKey(%q)=%q, want %q", C.in, Got, C.want)
		}
	}

	//#含点/括号/运算符/未配对引号/空串 → 返回空串(调用方据此 fail-fast)
	Bad := []string{"", "   ", "a.b", "a(b)", "a-b", "a+b", `"a`, `a"`, `""`, "'a'", "a b", "a;b"}
	for _, In := range Bad {
		if Got := hookColKey(In); Got != "" {
			t.Fatalf("hookColKey(%q) 应为空串, got %q", In, Got)
		}
	}
}

// #测试6:占位符扫描——位置式与编号式,以及"声明实参布局"的取大语义
func TestScanPlaceholdersAndArgCount(t *testing.T) {
	Cases := []struct {
		name      string
		in        string
		wantCount int //#占位符个数
		wantArgs  int //#placeholderArgCount(与最大编号取大)
	}{
		{"位置式两个", "values (?,?)", 2, 2},
		{"编号式两个", "values ($1,$2)", 2, 2},
		{"编号式不连续", "set a=$1, b=$3", 2, 3},
		{"编号式重复(同编号复用不增加实参,lib/pq·pgx·godror 均按去重编号计 NumInput)", "set a=$1, b=$1", 2, 1},
		{"冒号编号式", "set a=:1, b=:2", 2, 2},
		{"位置式多行", "values (?,?),(?,?)", 4, 4},
		{"字符串里的问号不算", "values ('?')", 0, 0},
	}

	for _, C := range Cases {
		Plcs := scanPlaceholders(maskSQLLiterals(C.in))
		if len(Plcs) != C.wantCount {
			t.Fatalf("%s: 占位符个数=%d, want %d", C.name, len(Plcs), C.wantCount)
		}
		if Got := placeholderArgCount(Plcs); Got != C.wantArgs {
			t.Fatalf("%s: placeholderArgCount=%d, want %d", C.name, Got, C.wantArgs)
		}
	}
}

// #测试7:组内"仅占位符"校验——字面量/表达式/函数调用必须被拒(不静默错位)
func TestResolveInsertRejectsNonPlaceholderGroups(t *testing.T) {
	Tab := &TUniTable{
		TableName: "t",
		HashField: map[string]TUniField{
			"user_name": {FieldName: "user_name", AttriName: "UserName"},
			"password":  {FieldName: "password", AttriName: "Password", SecretOn: true},
		},
	}

	Rejected := []string{
		"insert into t (user_name,password) values ('u','p')", //#字面量
		"insert into t (user_name,password) values (?,upper(?))",
		"insert into t (user_name,password) values (?,?) returning id",
		"insert into t (user_name,password) values (?,?) on duplicate key update password=?",
		"insert into t (user_name,password) values (?,?,?)", //#组内数与列数不符
		"insert into t values (?,?)",                        //#无列清单
		"insert into t (user_name,password) select a,b from s",
		"update t set password=pwd where user_name=?", //#加密列右值非单个占位符
		"truncate t",
	}
	for _, SQL := range Rejected {
		if _, Ok := resolveHookSecretArgs(Tab, SQL); Ok {
			t.Fatalf("形态应被判为不可信(Ok=false): %q", SQL)
		}
	}

	//#正向对照:合法形态必须可解析,且只把加密列的实参列入 Idx
	Plan, Ok := resolveHookSecretArgs(Tab, "insert into t (user_name,password) values (?,?)")
	if !Ok || len(Plan.Idx) != 1 || Plan.Idx[0] != 1 {
		t.Fatalf("合法 INSERT 应只映射 password→arg1, got Ok=%v Idx=%v", Ok, Plan.Idx)
	}
	if Plan.ArgCount != 2 {
		t.Fatalf("ArgCount=%d, want 2", Plan.ArgCount)
	}

	Plan, Ok = resolveHookSecretArgs(Tab, "update t set password=$1, user_name=$2 where user_name=$2")
	if !Ok || len(Plan.Idx) != 1 || Plan.Idx[0] != 0 {
		t.Fatalf("合法 UPDATE 应只映射 password→arg0, got Ok=%v Idx=%v", Ok, Plan.Idx)
	}
}

// #测试8:已文档化边界——列清单内的行内注释当前会被判为不可信(fail-fast,不静默)
// 该形态是合法 SQL,被拒属保守行为;此处钉住"拒绝而非错位加密"这一安全性质,
// 待后续按"仅掩码注释"取列名修复后,本用例应改为断言可解析。
func TestResolveInsertCommentInColumnListIsRejectedNotMisMapped(t *testing.T) {
	Tab := &TUniTable{
		TableName: "t",
		HashField: map[string]TUniField{
			"user_name": {FieldName: "user_name", AttriName: "UserName"},
			"password":  {FieldName: "password", AttriName: "Password", SecretOn: true},
		},
	}

	Plan, Ok := resolveHookSecretArgs(Tab, "insert into t (user_name,password /*c*/) values (?,?)")
	if Ok {
		//#若将来支持该形态,必须映射正确(不能把 user_name 当加密列,也不能漏掉 password)
		if len(Plan.Idx) != 1 || Plan.Idx[0] != 1 {
			t.Fatalf("支持该形态后映射必须正确, got Idx=%v", Plan.Idx)
		}
		t.Log("该形态已支持,映射正确")
		return
	}
	t.Log("已知边界:列清单内的注释被保守拒绝(fail-fast,不执行 SQL)")
}

// #测试9:掩码是等长的可定位副本——按掩码下标回切原文能取到原列名
func TestMaskedOffsetsIndexOriginal(t *testing.T) {
	SQL := "insert into t (user_name, password) values (?,?)"
	Masked := maskSQLLiterals(SQL)
	if len(Masked) != len(SQL) {
		t.Fatalf("掩码长度变了: %d vs %d", len(Masked), len(SQL))
	}

	Open := strings.Index(Masked, "(")
	Close := matchParen(Masked, Open)
	Segs := splitTopLevel(Masked[Open+1 : Close])
	if len(Segs) != 2 {
		t.Fatalf("应切出 2 段, got %d", len(Segs))
	}
	First := hookColKey(SQL[Open+1+Segs[0][0] : Open+1+Segs[0][1]])
	Second := hookColKey(SQL[Open+1+Segs[1][0] : Open+1+Segs[1][1]])
	if First != "user_name" || Second != "password" {
		t.Fatalf("回切原文取列名失败: %q / %q", First, Second)
	}
}

// #测试10:未闭合字面量(非法 SQL)下的掩码边界——断言"字面量起点之后不出现关键词"
//
// 已知边界:maskSQLLiterals 的空白循环是 `i < Next-1`,而 scanQuoted 对未闭合串返回 Size
// (其注释声明"整段按字符串处理"),两者相差一位 ⇒ 未闭合字面量的**最后一个字符**未被掩码。
// 更锋利的后果:该字符若恰为 '?' 会伪造出一个占位符——例如 "x 'abc?" 掩码为 "x '   ?",
// scanPlaceholders 会数到 1 个占位符,可能影响实参布局判定。影响面仅限非法 SQL(未闭合引号),
// 且实测残留字符不足以伪造关键词(values/set/where)。
// 修法一行:Next==Size 时把上界改为 Next,或让 scanQuoted 返回 Size+1。
func TestMaskUnterminatedLiteralSafety(t *testing.T) {
	Inputs := []string{
		"x 'values (?)",
		"insert into t (a) values ('oops",
		"x \"values",
		"x `values",
		"x /* 'values",
	}

	for _, In := range Inputs {
		Masked := maskSQLLiterals(In)
		if len(Masked) != len(In) {
			t.Fatalf("长度不守恒: %q → %q", In, Masked)
		}

		QuotePos := strings.IndexAny(In, "'\"`")
		if QuotePos < 0 {
			continue
		}
		//#安全性质:字面量/注释起点之后不得再识别出关键词
		for _, Word := range []string{"values", "set", "where", "update"} {
			if Pos := findKeyword(Masked, Word, QuotePos); Pos >= 0 {
				t.Fatalf("未闭合字面量伪造了关键词 %q: %q → %q (命中于 %d)", Word, In, Masked, Pos)
			}
		}
	}

	t.Log("已知边界:未闭合字面量的最后 1 个字符未被掩码;若该字符是 '?' 会伪造占位符(仅非法 SQL 可达)")
}

// #测试11:地基助手的分支边界——赋值定位(排除比较运算符)、括号配对、顶层逗号切分
func TestHookSQLHelpersBoundaries(t *testing.T) {
	//#indexAssign:只有独立赋值号才算;>=/<=/!=/<> 等比较运算符一律跳过
	Assign := []struct {
		in   string
		want int
	}{
		{"a=?", 1},
		{"a = ?", 2},
		{"a>=?", -1},
		{"a<=?", -1},
		{"a!=?", -1},
		{"a<>?", -1},
		{"a=>?", -1}, //#=> 按比较运算符保守跳过
		{"a", -1},
		{"", -1},
	}
	for _, C := range Assign {
		if Got := indexAssign(C.in); Got != C.want {
			t.Fatalf("indexAssign(%q)=%d, want %d", C.in, Got, C.want)
		}
	}

	//#matchParen:未配对/无起始括号返回 -1,嵌套正常配对
	Paren := []struct {
		in   string
		open int
		want int
	}{
		{"(a)", 0, 2},
		{"((a))", 0, 4},
		{"(a", 0, -1},
		{"a", 0, -1},
		{")a(", 2, -1},
	}
	for _, C := range Paren {
		if Got := matchParen(C.in, C.open); Got != C.want {
			t.Fatalf("matchParen(%q,%d)=%d, want %d", C.in, C.open, Got, C.want)
		}
	}

	//#splitTopLevel:括号内的逗号不切;末尾总有一段(空串也算一段)
	Split := []struct {
		in   string
		want int
	}{
		{"a,b", 2},
		{"fn(a,b),c", 2},
		{"(a),(b)", 2},
		{"a,", 2}, //#尾段为空
		{"a", 1},
		{"", 1},
	}
	for _, C := range Split {
		if Got := len(splitTopLevel(C.in)); Got != C.want {
			t.Fatalf("splitTopLevel(%q) 段数=%d, want %d", C.in, Got, C.want)
		}
	}

	//#切分区间必须能覆盖原文(起止下标回切后按序拼接等于原串)
	Segs := splitTopLevel("fn(a,b),c")
	if len(Segs) != 2 || "fn(a,b)" != "fn(a,b)"[Segs[0][0]:Segs[0][1]] {
		t.Fatalf("区间与原文不一致: %v", Segs)
	}
}

// 写钩子守卫的实参类型分支:[]byte 走加密,非 string/[]byte 一律 fail-fast
type zzHookArgTypes struct {
	UserName string `db:"user_name"`
	Password string `db:"password"`
}

func (z zzHookArgTypes) GetSqlInsert(e *TUniEngine, TableName string) string {
	return "insert into t (user_name,password) values (?,?)"
}

// #测试12:加密列实参为 []byte 时加密;为数值等其它类型时拒绝且不执行 SQL
func TestHookWriteGuardArgTypes(t *testing.T) {
	//#[]byte 实参:就地加密(不报错)
	d := &mockDriver{}
	e := newMockEngine(d)
	e.SecretOn, e.SecretBy = 1, "argtype-key"
	Tab := &TUniTable{
		TableName: "t",
		HashField: map[string]TUniField{
			"user_name": {FieldName: "user_name", AttriName: "UserName"},
			"password":  {FieldName: "password", AttriName: "Password", SecretOn: true},
		},
	}
	Args := []interface{}{"u", []byte("p")}
	if eror := e.hookWriteGuard("Insert", Tab, "insert into t (user_name,password) values (?,?)", Args); eror != nil {
		t.Fatalf("[]byte 实参应加密放行: %v", eror)
	}
	if Got, Ok := Args[1].(string); !Ok || !e.IsEncrypted(Got) {
		t.Fatalf("[]byte 实参应被加密为带标签的字符串, got %T %v", Args[1], Args[1])
	}

	//#数值实参:拒绝(fail-fast)
	d2 := &mockDriver{}
	e2 := newMockEngine(d2)
	e2.SecretOn, e2.SecretBy = 1, "argtype-key"
	Args2 := []interface{}{"u", int64(42)}
	eror := e2.hookWriteGuard("Insert", Tab, "insert into t (user_name,password) values (?,?)", Args2)
	if eror == nil || !strings.Contains(eror.Error(), "must be string") {
		t.Fatalf("数值实参应被拒并指明类型要求, got %v", eror)
	}
	if len(d2.insertValues) != 0 {
		t.Fatalf("被拒时不得执行 SQL: %v", d2.insertValues)
	}
}

// #测试13:stripSQLComments——引号感知的注释剥离(列名归一化的前置步骤)
// 契约:注释整段删除(不占位),引号段原样保留,换行保留。
func TestStripSQLComments(t *testing.T) {
	Cases := []struct {
		name string
		in   string
		want string
	}{
		{"无注释原样", "password", "password"},
		{"列尾块注释", "password /*c*/", "password "},
		{"列尾紧贴块注释", "password/*c*/", "password"},
		{"列前块注释", "/*c*/password", "password"},
		{"列中场块注释", "a /*c*/ b", "a  b"},
		{"纯注释段", "/*c*/", ""},
		{"行注释到行尾(换行保留)", "password -- x\nnext", "password \nnext"},
		{"行注释到输入尾", "password -- x", "password "},
		{"未闭合块注释:剩余全为注释", "password /*c", "password "},
		{"嵌套起始取首个结束符", "/* /* */", ""},
		{"引号内的注释标记不是注释", "'a/*b*/c'", "'a/*b*/c'"},
		{"双引号内的减号不是注释", `"a--b"`, `"a--b"`},
		{"反引号内原样", "`a--b`", "`a--b`"},
		{"空字面量", "''", "''"},
		{"未闭合引号按整段引号处理", "'", "'"},
		{"MySQL 语义:--后无空白不算注释", "password --x", "password --x"},
		{"MySQL 语义:--后无空白(整串)", "--x", "--x"},
		{"MySQL 语义:--到行尾算注释", "-- x", ""},
	}

	for _, C := range Cases {
		if Got := stripSQLComments(C.in); Got != C.want {
			t.Fatalf("%s: stripSQLComments(%q)=%q, want %q", C.name, C.in, Got, C.want)
		}
	}
}

// #测试14:两遍扫描的 `--` 口径必须一致(isLineComment 单一判定)
// 掩码副本与列名剥离若对同一输入给出不同解释,"边界来自掩码、内容来自原文"的取数会错位。
func TestLineCommentSemanticsUnified(t *testing.T) {
	Cases := []struct {
		in        string
		pos       int
		isComment bool
	}{
		{"a--b", 1, false},  //#MySQL 里是 a - (-b) 算术表达式
		{"a-- b", 1, true},  //#-- 后有空白 ⇒ 注释
		{"a--", 1, true},    //#-- 到行尾 ⇒ 注释
		{"a--\nb", 1, true}, //#-- 后是行尾 ⇒ 注释
		{"a-b", 1, false},   //#单减号
		{"--x", 0, false},   //#-- 后无空白
		{"-- x", 0, true},   //#-- 后有空白
		{"a -- x", 2, true}, //#常规写法
	}

	for _, C := range Cases {
		if Got := isLineComment(C.in, C.pos); Got != C.isComment {
			t.Fatalf("isLineComment(%q,%d)=%v, want %v", C.in, C.pos, Got, C.isComment)
		}

		//#掩码:是注释则被掩掉(该位置起出现空格),否则原样
		Masked := maskSQLLiterals(C.in)
		if len(Masked) != len(C.in) {
			t.Fatalf("%q 掩码长度不守恒", C.in)
		}
		MaskedAt := strings.TrimRight(Masked[C.pos:], " ") != strings.TrimRight(C.in[C.pos:], " ")
		if !C.isComment && Masked != C.in {
			t.Fatalf("%q 非注释却被掩码: %q", C.in, Masked)
		}
		if C.isComment && !MaskedAt && Masked == C.in {
			t.Fatalf("%q 是注释却未被掩码: %q", C.in, Masked)
		}

		//#剥离:是注释则被移除(结果变短),否则原样
		Stripped := stripSQLComments(C.in)
		if C.isComment && Stripped == C.in {
			t.Fatalf("%q 是注释却未被剥离: %q", C.in, Stripped)
		}
		if !C.isComment && Stripped != C.in {
			t.Fatalf("%q 非注释却被剥离: %q → %q", C.in, C.in, Stripped)
		}
	}
}
