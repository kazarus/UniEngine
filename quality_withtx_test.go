// quality_withtx_test
// #WithTx 是 Begin/Commit/Cancel 的闭包封装:成功提交、错误回滚、panic 回滚、
// #嵌套/混用拒绝,且不改变现有事务状态机。
package UniEngine

import (
	"errors"
	"testing"
)

type withTxRow struct {
	ID int64 `db:"id"`
}

func TestWithTxCommitsOnSuccess(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.RegisterClass(withTxRow{}, "t_withtx")

	if eror := eng.WithTx(func() error {
		return eng.Insert(&withTxRow{ID: 1}, "t_withtx")
	}); eror != nil {
		t.Fatalf("WithTx: %v", eror)
	}
	if eng.inTx || eng.tx != nil {
		t.Fatal("transaction state must be cleared after commit")
	}
	if got := len(d.snapshotQueries()); got != 1 {
		t.Fatalf("queries=%d want 1", got)
	}
}

func TestWithTxRollsBackOnError(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.RegisterClass(withTxRow{}, "t_withtx")
	sentinel := errors.New("business error")

	if eror := eng.WithTx(func() error {
		if eror := eng.Insert(&withTxRow{ID: 1}, "t_withtx"); eror != nil {
			return eror
		}
		return sentinel
	}); !errors.Is(eror, sentinel) {
		t.Fatalf("WithTx error=%v want sentinel", eror)
	}
	if eng.inTx || eng.tx != nil {
		t.Fatal("transaction state must be cleared after rollback")
	}
}

func TestWithTxRollsBackOnPanicAndRepanics(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.RegisterClass(withTxRow{}, "t_withtx")

	func() {
		defer func() {
			if rec := recover(); rec == nil {
				t.Fatal("panic must be rethrown")
			}
		}()
		_ = eng.WithTx(func() error {
			panic("boom")
		})
	}()
	if eng.inTx || eng.tx != nil {
		t.Fatal("transaction state must be cleared after panic rollback")
	}
}

func TestWithTxRejectsNested(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.RegisterClass(withTxRow{}, "t_withtx")

	eror := eng.WithTx(func() error {
		return eng.WithTx(func() error { return nil })
	})
	if !errors.Is(eror, ErrAlreadyInTransaction) {
		t.Fatalf("nested WithTx=%v want ErrAlreadyInTransaction", eror)
	}
	if eng.inTx || eng.tx != nil {
		t.Fatal("outer WithTx must roll back after nested failure")
	}
}

func TestWithTxRejectsManualCommitInside(t *testing.T) {
	d := &mockDriver{}
	eng := newMockEngine(d)
	eng.RegisterClass(withTxRow{}, "t_withtx")

	eror := eng.WithTx(func() error {
		return eng.Commit()
	})
	if !errors.Is(eror, ErrNoTransaction) {
		t.Fatalf("manual Commit inside WithTx=%v want ErrNoTransaction", eror)
	}
	if eng.inTx || eng.tx != nil {
		t.Fatal("transaction state must be cleared")
	}
}

func TestWithTxCommitErrorClearsState(t *testing.T) {
	d := &mockDriver{commitErr: errors.New("commit failed")}
	eng := newMockEngine(d)
	eng.RegisterClass(withTxRow{}, "t_withtx")

	if eror := eng.WithTx(func() error { return nil }); eror == nil {
		t.Fatal("commit error must be returned")
	}
	if eng.inTx || eng.tx != nil {
		t.Fatal("commit error must still clear transaction state")
	}
}
