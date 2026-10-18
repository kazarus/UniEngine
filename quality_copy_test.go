// quality_copy_test
// #历史兼容:部分调用方按值复制 TUniEngine(每请求 c.Set("DB", ...)),
// #副本必须共享注册表锁;否则运行时 RegisterClass/PrepareTables 会与查询并发写同一张 map。
package UniEngine

import (
	"sync"
	"testing"
)

type copyCompatRow struct {
	ID int64 `db:"id"`
}

func copyEngineForTest(e *TUniEngine) *TUniEngine {
	//#显式复制历史调用方依赖的共享字段,避免 go vet 的 copylocks 把测试文件本身拦下;
	//#这里验证的是“副本必须共享 mu/txGate/HashTabl”这一兼容契约,不是鼓励新代码复制。
	return &TUniEngine{
		mu:       e.mu,
		txGate:   e.txGate,
		Db:       e.Db,
		ColLabel: e.ColLabel,
		ColParam: e.ColParam,
		HashTabl: e.HashTabl,
		Provider: e.Provider,
	}
}

func TestEngineCopySharesRegistryLocks(t *testing.T) {
	eng := newTestEngine()
	clone := copyEngineForTest(eng)

	if clone.mu != eng.mu || clone.txGate != eng.txGate {
		t.Fatal("copied engine must share mu/txGate pointers")
	}

	const table = "t_copy_shared"
	if tbl := eng.RegisterClass(copyCompatRow{}, table); tbl.LastError != nil {
		t.Fatalf("seed RegisterClass: %v", tbl.LastError)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			tbl := clone.RegisterClass(copyCompatRow{}, table)
			if tbl.LastError != nil {
				t.Errorf("clone RegisterClass: %v", tbl.LastError)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = eng.GetTable(table)
		}
	}()
	wg.Wait()

	if got := eng.GetTable(table); got == nil || got.ClassType == nil {
		t.Fatalf("registry did not observe class registration: %+v", got)
	}
}
