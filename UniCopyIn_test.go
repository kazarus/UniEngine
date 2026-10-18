// UniCopyIn_test
// #批量写入的分页路径:InsertP(多行 INSERT)与 CopyInP(COPY)的页切分、方言路由与废弃别名

package UniEngine

import (
	"context"
	"math"
	"strings"
	"testing"
)

type pageRow struct {
	Id   int64  `db:"id"`
	Name string `db:"name"`
}

func newPageEngine(d *mockDriver) *TUniEngine {
	e := newMockEngine(d)
	e.Provider, e.ColParam = DtPOSTGR, "$"
	return e
}

// #测试1:InsertP 按 PageSize 切页,页数与参数总量守恒(含 0/-1/负值/超额等边界)
func TestInsertPPagesAndRowCount(t *testing.T) {
	cases := []struct {
		page      int64
		wantPages int
	}{
		{2, 3},   //5 行按 2 切 → (2,2,1)
		{5, 1},   //整除 → 1 页,且尾页为空不再发包
		{100, 1}, //大于行数 → 1 页
		{0, 1},   //0 → 缺省 999 → 1 页
		{-1, 1},  //-1 → 缺省 999 → 1 页
		{-5, 1},  //负值 clamp 到 999;5 行仍为 1 页
	}

	for _, c := range cases {
		d := &mockDriver{}
		e := newPageEngine(d)
		e.RegisterClass(pageRow{}, "t_page").SetKeys("id")

		rows := []pageRow{{1, "a"}, {2, "b"}, {3, "c"}, {4, "d"}, {5, "e"}}
		if eror := e.InsertP(&rows, c.page, "t_page"); eror != nil {
			t.Fatalf("PageSize=%d: %v", c.page, eror)
		}

		if got := len(d.insertValues); got != c.wantPages {
			t.Fatalf("PageSize=%d 发包 %d 次, want %d", c.page, got, c.wantPages)
		}

		total := 0
		for _, Args := range d.insertValues {
			total += len(Args)
		}
		if total != len(rows)*2 {
			t.Fatalf("PageSize=%d 参数总数=%d, want %d(5 行 × 2 列)", c.page, total, len(rows)*2)
		}
	}
}

// #测试2:CopyInP 恒走 COPY(与 Supplier 无关);非 PG 族显式报错
func TestCopyInPRouting(t *testing.T) {
	d := &mockDriver{}
	e := newPageEngine(d)
	e.RegisterClass(pageRow{}, "t_copy").SetKeys("id")

	rows := []pageRow{{1, "a"}, {2, "b"}}
	if eror := e.CopyInP(&rows, 1, "t_copy"); eror != nil {
		t.Fatalf("CopyInP: %v", eror)
	}
	Q := d.snapshotQueries()
	if len(Q) == 0 || !strings.HasPrefix(strings.ToUpper(Q[0]), "COPY") {
		t.Fatalf("CopyInP 应产出 COPY 语句, got %v", Q)
	}

	//#非 PG 族:CopyInL 有方言守卫,报错而非发出 PG 语法
	dm := &mockDriver{}
	em := newMockEngine(dm)
	em.Provider, em.ColParam = DtMYSQLN, "?"
	em.RegisterClass(pageRow{}, "t_copy2").SetKeys("id")
	if eror := em.CopyInP(&rows, 1, "t_copy2"); eror == nil || !strings.Contains(eror.Error(), "requires a PostgreSQL-family provider") {
		t.Fatalf("MySQL 族 CopyInP 应报方言错误, got %v", eror)
	}
}

// #测试3:insertPage 的 PolarDB 分支——InsertP 在 Supplier=DtPOLODB 时改走 COPY
func TestInsertPPolarSupplierRoutesToCopy(t *testing.T) {
	d := &mockDriver{}
	e := newPageEngine(d)
	e.Supplier = DtPOLODB //#唯一影响该分支的字段(Provider 仍是 PG 族)
	e.RegisterClass(pageRow{}, "t_polo").SetKeys("id")

	rows := []pageRow{{1, "a"}, {2, "b"}}
	if eror := e.InsertP(&rows, 100, "t_polo"); eror != nil {
		t.Fatalf("InsertP: %v", eror)
	}
	Q := d.snapshotQueries()
	if len(Q) == 0 || !strings.HasPrefix(strings.ToUpper(Q[0]), "COPY") {
		t.Fatalf("Supplier=DtPOLODB 时 InsertP 应走 COPY, got %v", Q)
	}

	//#对照:Supplier 未设时同一调用走多行 INSERT
	d2 := &mockDriver{}
	e2 := newPageEngine(d2)
	e2.RegisterClass(pageRow{}, "t_polo2").SetKeys("id")
	if eror := e2.InsertP(&rows, 100, "t_polo2"); eror != nil {
		t.Fatalf("InsertP(对照): %v", eror)
	}
	Q2 := d2.snapshotQueries()
	if len(Q2) == 0 || !strings.HasPrefix(strings.ToUpper(Q2[0]), "INSERT") {
		t.Fatalf("Supplier 未设时 InsertP 应走 INSERT, got %v", Q2)
	}
}

// #测试4:废弃别名(SpecialInsertL/LCtx、SpecialInsertP/PCtx)与现行命名等价
func TestDeprecatedPagingWrappers(t *testing.T) {
	d := &mockDriver{}
	e := newPageEngine(d)
	e.RegisterClass(pageRow{}, "t_dep").SetKeys("id")

	rows := []pageRow{{1, "a"}, {2, "b"}}
	Ctx := context.Background()

	if eror := e.SpecialInsertL(&rows, "t_dep"); eror != nil {
		t.Fatalf("SpecialInsertL: %v", eror)
	}
	if eror := e.SpecialInsertLCtx(Ctx, &rows, "t_dep"); eror != nil {
		t.Fatalf("SpecialInsertLCtx: %v", eror)
	}
	if eror := e.SpecialInsertP(&rows, 1, "t_dep"); eror != nil {
		t.Fatalf("SpecialInsertP: %v", eror)
	}
	if eror := e.SpecialInsertPCtx(Ctx, &rows, 1, "t_dep"); eror != nil {
		t.Fatalf("SpecialInsertPCtx: %v", eror)
	}

	//#四个入口在 PG 族下都应落到 COPY
	for _, Q := range d.snapshotQueries() {
		if !strings.HasPrefix(strings.ToUpper(Q), "COPY") {
			t.Fatalf("废弃别名应等价于 CopyIn*, got %q", Q)
		}
	}
}

type pageWideRow struct {
	C01 int64 `db:"c01"`
	C02 int64 `db:"c02"`
	C03 int64 `db:"c03"`
	C04 int64 `db:"c04"`
	C05 int64 `db:"c05"`
	C06 int64 `db:"c06"`
	C07 int64 `db:"c07"`
	C08 int64 `db:"c08"`
	C09 int64 `db:"c09"`
	C10 int64 `db:"c10"`
}

func TestNormalizeBatchPageSize(t *testing.T) {
	e := newTestEngine()
	cases := []struct {
		provider TDriveType
		request  int64
		columns  int
		want     int64
	}{
		{DtPOSTGR, 0, 1, 999},
		{DtPOSTGR, -5, 1, 999},
		{DtPOSTGR, 1000, 1, 999},
		{DtPOSTGR, 999, 100, 600}, // 60000/100
		{DtPOSTGR, 999, 1000, 60},
		{DtORACLE, 999, 100, 100},
		{DtORACLE, 999, 3, 100},
		{DtORACLE, 50, 3, 50},
		{DtDAMENG, 999, 3, 666}, // 2000/3
		{DtDAMENG, 999, 47, 42},
		{DtDAMENG, 999, 1, 999},
		{DtSQLSRV, 999, 100, 20}, // 2000/100
		{DtMYSQLN, 999, 0, 999},
	}

	for _, C := range cases {
		e.Provider = C.provider
		if got := e.normalizeBatchPageSize(C.request, C.columns); got != C.want {
			t.Errorf("provider=%v request=%d cols=%d: got %d want %d", C.provider, C.request, C.columns, got, C.want)
		}
	}
}

func TestInsertPPageSizeByColumnBudget(t *testing.T) {
	rows := make([]pageWideRow, 1000)
	for i := range rows {
		rows[i] = pageWideRow{C01: int64(i), C02: 2, C03: 3, C04: 4, C05: 5, C06: 6, C07: 7, C08: 8, C09: 9, C10: 10}
	}

	cases := []struct {
		provider TDriveType
		param    string
		name     string
		wantPage int
	}{
		{DtPOSTGR, "$", "", 2},      // 999 行/页
		{DtORACLE, ":", "", 10},     // 100 行/页(INSERT ALL 性能拐点)
		{DtDAMENG, ":", "", 5},      // 2000/10 = 200 行/页
		{DtSQLSRV, "$", "mssql", 5}, // 2000/10 = 200 行/页
	}

	for _, C := range cases {
		d := &mockDriver{}
		e := newMockEngine(d)
		e.Provider, e.ColParam, e.ProvideName = C.provider, C.param, C.name
		e.RegisterClass(pageWideRow{}, "t_wide")

		if eror := e.InsertP(&rows, 999, "t_wide"); eror != nil {
			t.Fatalf("provider=%v InsertP: %v", C.provider, eror)
		}
		if got := len(d.insertValues); got != C.wantPage {
			t.Fatalf("provider=%v pages=%d want %d", C.provider, got, C.wantPage)
		}
	}
}

func TestInsertPClampsNegativeAndHugePageSize(t *testing.T) {
	for _, page := range []int64{-5, 1000, math.MaxInt64} {
		d := &mockDriver{}
		e := newPageEngine(d)
		e.RegisterClass(pageRow{}, "t_page")

		rows := make([]pageRow, 2000)
		for i := range rows {
			rows[i] = pageRow{Id: int64(i), Name: "n"}
		}
		if eror := e.InsertP(&rows, page, "t_page"); eror != nil {
			t.Fatalf("page=%d: %v", page, eror)
		}
		//#999 行/页:2000 → 999 + 999 + 2
		if got := len(d.insertValues); got != 3 {
			t.Fatalf("page=%d pages=%d want 3", page, got)
		}
		total := 0
		for _, Args := range d.insertValues {
			total += len(Args)
		}
		if total != len(rows)*2 {
			t.Fatalf("page=%d total args=%d want %d", page, total, len(rows)*2)
		}
	}
}

func TestCopyInPClampsPageSize(t *testing.T) {
	d := &mockDriver{}
	e := newPageEngine(d)
	e.RegisterClass(pageRow{}, "t_page")

	rows := make([]pageRow, 2000)
	for i := range rows {
		rows[i] = pageRow{Id: int64(i), Name: "n"}
	}
	if eror := e.CopyInP(&rows, math.MaxInt64, "t_page"); eror != nil {
		t.Fatal(eror)
	}
	if got := len(d.snapshotQueries()); got != 3 {
		t.Fatalf("CopyInP pages=%d want 3", got)
	}
}

// #达梦经 Oracle 兼容协议接入:Provider 保持 DtORACLE(应用 SQL 分支不变),
// #Supplier=DtDAMENG 只把批量预算切到 2000/列数,不套 Oracle 100 行性能上限。
func TestDamengSupplierUsesDamengBatchBudget(t *testing.T) {
	e := newTestEngine()
	e.Provider = DtORACLE
	e.Supplier = DtDAMENG

	if got := e.batchDialect(); got != DtDAMENG {
		t.Fatalf("batchDialect=%v want DtDAMENG", got)
	}
	if got := e.normalizeBatchPageSize(999, 47); got != 42 {
		t.Fatalf("DM 47 cols page=%d want 42 (2000/47)", got)
	}
	if got := e.normalizeBatchPageSize(999, 10); got != 200 {
		t.Fatalf("DM 10 cols page=%d want 200 (2000/10), Oracle 100 cap must not apply", got)
	}

	//#不带 Supplier 时仍是 Oracle:100 行上限。
	e.Supplier = 0
	if got := e.normalizeBatchPageSize(999, 10); got != 100 {
		t.Fatalf("Oracle page=%d want 100", got)
	}
}

func TestInsertPDamengSupplierBudget(t *testing.T) {
	d := &mockDriver{}
	e := newMockEngine(d)
	e.Provider, e.ColParam, e.Supplier = DtORACLE, ":", DtDAMENG
	e.RegisterClass(pageWideRow{}, "t_wide")

	rows := make([]pageWideRow, 1000)
	for i := range rows {
		rows[i] = pageWideRow{C01: int64(i), C02: 2, C03: 3, C04: 4, C05: 5, C06: 6, C07: 7, C08: 8, C09: 9, C10: 10}
	}
	if eror := e.InsertP(&rows, 999, "t_wide"); eror != nil {
		t.Fatalf("InsertP: %v", eror)
	}
	if got := len(d.insertValues); got != 5 {
		t.Fatalf("DM 10 cols pages=%d want 5 (2000/10=200 rows/page)", got)
	}
}
