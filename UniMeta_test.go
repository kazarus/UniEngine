// UniMeta_test
// #元数据探测与 DDL 辅助的 mock 用例:Exist* 四个探测的公共收尾、IfDropView、ExecuteMust

package UniEngine

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
)

// #测试1:ExistTable/ExistViews/ExistField/ExistConst 共用 existCount 收尾,计数 >0 即 true
func TestExistProbesByCount(t *testing.T) {
	cases := []struct {
		count int64
		want  bool
	}{
		{1, true},
		{3, true},
		{0, false},
	}

	for _, c := range cases {
		d := &mockDriver{columns: []string{"value"}, queryRows: [][]driver.Value{{c.count}}}
		e := newMockEngine(d)

		if got, eror := e.ExistTable("t_x"); eror != nil || got != c.want {
			t.Fatalf("ExistTable(count=%d)=(%v,%v), want (%v,nil)", c.count, got, eror, c.want)
		}
		if got, eror := e.ExistViews("t_x"); eror != nil || got != c.want {
			t.Fatalf("ExistViews(count=%d)=(%v,%v), want (%v,nil)", c.count, got, eror, c.want)
		}
		if got, eror := e.ExistField("t_x", "id"); eror != nil || got != c.want {
			t.Fatalf("ExistField(count=%d)=(%v,%v), want (%v,nil)", c.count, got, eror, c.want)
		}
		if got, eror := e.ExistConst(CtPK, "pk_x"); eror != nil || got != c.want {
			t.Fatalf("ExistConst(count=%d)=(%v,%v), want (%v,nil)", c.count, got, eror, c.want)
		}
	}
}

// #测试2:探测前的标识符白名单——非法名不得拼进 SQL
func TestExistProbesRejectInvalidIdent(t *testing.T) {
	e := newMockEngine(&mockDriver{})

	if _, eror := e.ExistTable("t;drop table x"); !errors.Is(eror, ErrInvalidTableName) {
		t.Fatalf("ExistTable 非法表名应报 ErrInvalidTableName, got %v", eror)
	}
	if _, eror := e.ExistViews("t;drop table x"); !errors.Is(eror, ErrInvalidTableName) {
		t.Fatalf("ExistViews 非法表名应报 ErrInvalidTableName, got %v", eror)
	}
	if _, eror := e.ExistField("t_x", "id;drop"); eror == nil {
		t.Fatal("ExistField 非法字段名应报错")
	}
	if _, eror := e.ExistConst(CtPK, "pk;drop"); !errors.Is(eror, ErrInvalidConstraintName) {
		t.Fatalf("ExistConst 非法约束名应报 ErrInvalidConstraintName, got %v", eror)
	}
}

// #测试3:MySQL 族元数据探测缺 DataBase 时显式报错(而非拼出空库名 SQL)
func TestExistProbesMySQLNeedDataBase(t *testing.T) {
	e := newMockEngine(&mockDriver{})
	e.Provider = DtMYSQLN
	e.ColParam = "?"

	for _, Call := range []struct {
		name string
		fn   func() (bool, error)
	}{
		{"ExistTable", func() (bool, error) { return e.ExistTable("t_x") }},
		{"ExistViews", func() (bool, error) { return e.ExistViews("t_x") }},
		{"ExistField", func() (bool, error) { return e.ExistField("t_x", "id") }},
		{"ExistConst", func() (bool, error) { return e.ExistConst(CtPK, "pk_x") }},
	} {
		if _, eror := Call.fn(); eror == nil || !strings.Contains(eror.Error(), "database is not specified") {
			t.Fatalf("%s 缺 DataBase 应报 database is not specified, got %v", Call.name, eror)
		}
	}
}

// #测试4:IfDropView——存在则 DROP VIEW 后返回 (true,nil);不存在则不执行 DROP
func TestIfDropView(t *testing.T) {
	//#探测返回 1 → 视图存在
	d := &mockDriver{columns: []string{"value"}, queryRows: [][]driver.Value{{int64(1)}}}
	e := newMockEngine(d)

	ok, eror := e.IfDropView("v_x")
	if eror != nil || !ok {
		t.Fatalf("IfDropView(存在)=(%v,%v), want (true,nil)", ok, eror)
	}
	Q := d.snapshotQueries()
	if len(Q) == 0 || !strings.Contains(strings.ToUpper(Q[len(Q)-1]), "DROP VIEW") {
		t.Fatalf("视图存在时应执行 DROP VIEW, got %v", Q)
	}

	//#探测返回 0 → 视图不存在:不执行 DROP(仍返回 true,契约是"操作完成")
	d2 := &mockDriver{columns: []string{"value"}, queryRows: [][]driver.Value{{int64(0)}}}
	e2 := newMockEngine(d2)
	if ok, eror := e2.IfDropView("v_y"); eror != nil || !ok {
		t.Fatalf("IfDropView(不存在)=(%v,%v), want (true,nil)", ok, eror)
	}
	for _, One := range d2.snapshotQueries() {
		if strings.Contains(strings.ToUpper(One), "DROP VIEW") {
			t.Fatalf("视图不存在时不应执行 DROP VIEW, got %q", One)
		}
	}

	//#非法表名在探测前被拒
	if _, eror := e2.IfDropView("v;drop"); !errors.Is(eror, ErrInvalidTableName) {
		t.Fatalf("IfDropView 非法表名应报 ErrInvalidTableName, got %v", eror)
	}
}

// #测试5:ExecuteMust 影响 0 行报错、1 行放行、驱动错误原样包装
func TestExecuteMustAffectedRows(t *testing.T) {
	Zero := int64(0)
	d := &mockDriver{affected: &Zero}
	e := newMockEngine(d)
	if eror := e.ExecuteMust("update t_x set a=1"); eror == nil {
		t.Fatal("影响 0 行应报错")
	}
	if eror := e.ExecuteMustCtx(testCtx(), "update t_x set a=1"); eror == nil {
		t.Fatal("ExecuteMustCtx 影响 0 行应报错")
	}

	One := int64(1)
	d2 := &mockDriver{affected: &One}
	e2 := newMockEngine(d2)
	if eror := e2.ExecuteMust("update t_x set a=1"); eror != nil {
		t.Fatalf("影响 1 行不应报错, got %v", eror)
	}

	//#驱动层失败:Execute 与 ExecuteMust 都应把它包装上抛(errors.Is 可判别)
	Boom := errors.New("boom")
	d3 := &mockDriver{execErr: Boom}
	e3 := newMockEngine(d3)
	if eror := e3.Execute("update t_x set a=1"); !errors.Is(eror, Boom) {
		t.Fatalf("Execute 应包装驱动错误, got %v", eror)
	}
	if eror := e3.ExecuteMust("update t_x set a=1"); !errors.Is(eror, Boom) {
		t.Fatalf("ExecuteMust 应包装驱动错误, got %v", eror)
	}
}

// testCtx 返回一个后台 context(*Ctx 变体的公共实参)
func testCtx() context.Context { return context.Background() }
