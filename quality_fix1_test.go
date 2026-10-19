// quality_fix_test
// #评审报告 docs/reviews/CODE_QUALITY_REVIEW_2026-10-14.md 第一批修复的回归测试(Q3/Q4/Q7)。
// #报告附录探针以"复现缺陷"为 PASS;本文件改为断言修复后的目标行为。

package UniEngine

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

// ----------------------------------------
// Q3:事务终态后引擎必须恢复可用
// ----------------------------------------

// BeginCtx 的 context 取消后 database/sql 自动回滚;Cancel 返回原始 ErrTxDone,
// 但引擎必须回收事务状态——后续 Begin 可重新开始,Execute 恢复普通执行。
func TestQ3CancelledTransactionRecovers(t *testing.T) {
	d := &mockDriver{}
	e := newMockEngine(d)
	defer e.Db.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if eror := e.BeginCtx(ctx); eror != nil {
		t.Fatal(eror)
	}
	cancel()

	//#轮询等待 database/sql 观察 ctx 取消并完成自动回滚
	deadline := time.Now().Add(2 * time.Second)
	for {
		stmt, err := e.currentTx().PrepareContext(context.Background(), "select 1")
		if stmt != nil {
			stmt.Close()
		}
		if errors.Is(err, sql.ErrTxDone) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("auto-rollback was not observed: %v", err)
		}
		time.Sleep(time.Millisecond)
	}

	cancelErr := e.Cancel()
	if !errors.Is(cancelErr, sql.ErrTxDone) {
		t.Fatalf("Cancel should surface the original ErrTxDone, got %v", cancelErr)
	}
	if eror := e.Begin(); eror != nil {
		t.Fatalf("engine should accept a new transaction, got %v", eror)
	}
	if eror := e.Execute("select 1"); eror != nil {
		t.Fatalf("engine should execute normally after recovery, got %v", eror)
	}
	if eror := e.Commit(); eror != nil {
		t.Fatalf("commit of the new transaction failed: %v", eror)
	}
}

// 驱动层提交失败:Commit 返回驱动错误,但引擎事务状态必须回收,实例可继续使用。
func TestQ3CommitFailureReleasesEngine(t *testing.T) {
	d := &mockDriver{commitErr: errors.New("mock commit failure")}
	e := newMockEngine(d)
	defer e.Db.Close()

	if eror := e.Begin(); eror != nil {
		t.Fatal(eror)
	}
	eror := e.Commit()
	if eror == nil || !strings.Contains(eror.Error(), "mock commit failure") {
		t.Fatalf("Commit should surface the driver error, got %v", eror)
	}
	if eror := e.Begin(); eror != nil {
		t.Fatalf("engine should accept a new transaction after failed commit, got %v", eror)
	}
	if eror := e.Cancel(); eror != nil {
		t.Fatalf("rollback of the new transaction failed: %v", eror)
	}
}

// 驱动层回滚失败:同上,引擎状态回收且原始错误保留。
func TestQ3RollbackFailureReleasesEngine(t *testing.T) {
	d := &mockDriver{rollbackErr: errors.New("mock rollback failure")}
	e := newMockEngine(d)
	defer e.Db.Close()

	if eror := e.Begin(); eror != nil {
		t.Fatal(eror)
	}
	eror := e.Cancel()
	if eror == nil || !strings.Contains(eror.Error(), "mock rollback failure") {
		t.Fatalf("Cancel should surface the driver error, got %v", eror)
	}
	if eror := e.Begin(); eror != nil {
		t.Fatalf("engine should accept a new transaction after failed rollback, got %v", eror)
	}
	if eror := e.Execute("select 1"); eror != nil {
		t.Fatalf("engine should execute normally after recovery, got %v", eror)
	}
}

// ----------------------------------------
// Q4:验签回填遇不可写行返回错误而非 panic
// ----------------------------------------

// 开启完整性校验后按值传入结构体:四个写入口必须返回普通错误(失败先于 SQL 执行)。
func TestQ4VerifyValueParamReturnsErrorNotPanic(t *testing.T) {
	for _, method := range []string{"Insert", "Update", "SaveIt", "SaveItWhenNotExist"} {
		t.Run(method, func(t *testing.T) {
			d := &mockDriver{columns: verifyRowColumns()}
			e := newVerifyTestEngine(d)
			defer e.Db.Close()
			e.VerificationMode = 2

			r := TVerifyRow{WhoBuild: 1, UserIndx: 2, DataStat: 1, MastName: "ok"}
			var eror error
			switch method {
			case "Insert":
				eror = e.Insert(r)
			case "Update":
				eror = e.Update(r)
			case "SaveIt":
				eror = e.SaveIt(r)
			case "SaveItWhenNotExist":
				eror = e.SaveItWhenNotExist(r)
			}
			if eror == nil {
				t.Fatal("expected an error for value param with verify on, got nil")
			}
			if !strings.Contains(eror.Error(), "not addressable") {
				t.Fatalf("unexpected error: %v", eror)
			}
			//#错误必须先于 SQL 执行
			if q := d.snapshotQueries(); len(q) != 0 {
				t.Fatalf("no SQL should run before the error, got %v", q)
			}
		})
	}
}

// 合法指针参数不受影响:写入成功且 MAC 回填调用方结构体(既有契约)。
func TestQ4VerifyPointerParamStillBackfills(t *testing.T) {
	d := &mockDriver{columns: verifyRowColumns()}
	e := newVerifyTestEngine(d)
	defer e.Db.Close()
	e.VerificationMode = 2

	r := TVerifyRow{WhoBuild: 1, UserIndx: 2, DataStat: 1, MastName: "ok"}
	if eror := e.Insert(&r); eror != nil {
		t.Fatal(eror)
	}
	if !strings.HasPrefix(r.MacText, UniVerifyTag) {
		t.Fatalf("MAC should be backfilled into the caller's struct, got %q", r.MacText)
	}
}

// ----------------------------------------
// Q7:重复列 tag 登记期拒绝
// ----------------------------------------

// 完全相同的 tag 被拒绝,报错包含两个 Go 字段名。
func TestQ7DuplicateTagRejected(t *testing.T) {
	type dupRow struct {
		A string `db:"name"`
		B string `db:"name"`
	}
	e := newTestEngine()
	tbl := e.RegisterClass(dupRow{}, "dup_row")
	if tbl.LastError == nil {
		t.Fatal("expected duplicate tag to be rejected at registration")
	}
	if !strings.Contains(tbl.LastError.Error(), "[A]") || !strings.Contains(tbl.LastError.Error(), "[B]") {
		t.Fatalf("error should name both fields, got %v", tbl.LastError)
	}
}

// 仅大小写不同、归一化后相同的 tag 同样被拒绝。
func TestQ7DuplicateTagCaseInsensitive(t *testing.T) {
	type caseRow struct {
		A string `db:"Name"`
		B string `db:"name"`
	}
	e := newTestEngine()
	tbl := e.RegisterClass(caseRow{}, "case_row")
	if tbl.LastError == nil {
		t.Fatal("expected case-insensitive duplicate tag to be rejected")
	}
}

// 多个忽略/只读标记字段(-/_)是合法用法,不得误拒。
func TestQ7IgnoreMarkersAllowed(t *testing.T) {
	type ignoreRow struct {
		A string `db:"-"`
		B string `db:"-"`
		C string `db:"real"`
	}
	e := newTestEngine()
	tbl := e.RegisterClass(ignoreRow{}, "ignore_row")
	if tbl.LastError != nil {
		t.Fatalf("ignored-marker fields must not be rejected: %v", tbl.LastError)
	}
	if _, ok := tbl.HashField["real"]; !ok {
		t.Fatal("real column should be registered")
	}
}
