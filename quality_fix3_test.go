package UniEngine

import (
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestSQLServerInitializeChecksActualBinding(t *testing.T) {
	d := &mockDriver{probeResult: "1.00"} // 模拟 $1 被服务端当作 money 字面量
	e := &TUniEngine{Db: sql.OpenDB(mockConnector{d: d}), ColLabel: "db", ColParam: "$", Provider: DtSQLSRV}
	defer e.Db.Close()

	if eror := e.Initialize(); !errors.Is(eror, ErrInvalidPlaceholderBinding) {
		t.Fatalf("Initialize should reject a non-binding placeholder, got %v", eror)
	}
	if len(e.HashTabl) != 0 {
		t.Fatal("failed Initialize must not publish registry entries")
	}
	if e.inTx {
		t.Fatal("failed Initialize must not start a transaction")
	}

	d.mu.Lock()
	d.probeResult = ""
	d.mu.Unlock()
	if eror := e.Initialize(); eror != nil {
		t.Fatalf("Initialize should succeed after fixing binding: %v", eror)
	}
	if eror := e.Execute("select 1"); eror != nil {
		t.Fatal(eror)
	}
	d.mu.Lock()
	probes := d.probeCount
	d.mu.Unlock()
	if probes != 2 { // 首次失败 + 修正后成功; Execute 复用成功结果
		t.Fatalf("want two binding probes, got %d", probes)
	}
}

func TestSQLServerBindingRechecksChangedConfig(t *testing.T) {
	d := &mockDriver{}
	e := &TUniEngine{Db: sql.OpenDB(mockConnector{d: d}), ColLabel: "db", ColParam: ":", Provider: DtSQLSRV}
	defer e.Db.Close()
	if eror := e.Initialize(); eror != nil {
		t.Fatal(eror)
	}
	e.ColParam = "bad"
	if eror := e.Execute("select 1"); !errors.Is(eror, ErrInvalidPlaceholderBinding) {
		t.Fatalf("changed invalid ColParam must fail before business SQL: %v", eror)
	}
	if got := len(d.snapshotQueries()); got != 0 {
		t.Fatalf("invalid configuration reached Prepare: %d statements", got)
	}
}

func TestConcurrentSameTableRegistrationUsesSnapshots(t *testing.T) {
	d := &mockDriver{columns: []string{"id", "name"}, queryRows: [][]driver.Value{{int64(7), "alice"}}}
	e := newMockEngine(d)
	defer e.Db.Close()
	if eror := e.RegisterClass(mockRow{}, "t_live").SetKeys("id").LastError; eror != nil {
		t.Fatal(eror)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			for _, tab := range []*TUniTable{
				e.RegisterTable("t_live", int64(i)),
				e.RegisterField("t_live", "extra"),
				e.RegisterPkeys("t_live", "id"),
			} {
				if tab.LastError != nil {
					errCh <- tab.LastError
					return
				}
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			var rows []mockRow
			if eror := e.SelectL(&rows, "select id,name from t_live"); eror != nil {
				errCh <- eror
				return
			}
			if len(rows) != 1 || rows[0].Name != "alice" {
				errCh <- fmt.Errorf("unexpected rows: %+v", rows)
				return
			}
			if eror := e.Insert(&mockRow{ID: i, Name: "bob"}); eror != nil {
				errCh <- eror
				return
			}
		}
	}()
	wg.Wait()
	close(errCh)
	for eror := range errCh {
		t.Fatal(eror)
	}

	byName := e.GetTable("t_live")
	byType := e.HashTabl["UniEngine.mockRow"]
	if byName != byType {
		t.Fatal("table and class aliases must publish the same snapshot")
	}
	if byName.HashPkeys["id"].AttriName != "ID" || byName.HashField["name"].AttriName != "Name" {
		t.Fatal("dynamic registration must preserve class field mappings")
	}
}

func TestStrictVerifyRejectsIncompleteProjection(t *testing.T) {
	d := &mockDriver{columns: []string{"who_build", "user_indx"}, queryRows: [][]driver.Value{{int64(1), int64(2)}}}
	e := newVerifyTestEngine(d)
	defer e.Db.Close()
	e.VerifyMode = 2
	var rows []TVerifyRow
	if eror := e.SelectL(&rows, "select who_build,user_indx from tbl_verify_row"); !errors.Is(eror, ErrIncompleteVerification) {
		t.Fatalf("strict mode must reject a partial projection: %v", eror)
	}
	if len(rows) != 0 {
		t.Fatal("partial results must not escape failed verification")
	}
}

func TestDefaultMACWritesV2AndReadsLegacyV1(t *testing.T) {
	d := &mockDriver{columns: []string{"id", "a", "b", "mac"}}
	e := newMockEngine(d)
	defer e.Db.Close()
	e.VerifyOn, e.VerifyMode, e.VerifyBy = 1, 2, "mac-default-key"
	if eror := e.RegisterClass(reviewMACRow{}, "review_mac").SetKeys("id").SetVerify("mac", "id", "a", "b").LastError; eror != nil {
		t.Fatal(eror)
	}
	r := reviewMACRow{ID: 1, A: "admin\x1f0", B: "user"}
	if eror := e.Insert(&r); eror != nil {
		t.Fatal(eror)
	}
	if !strings.HasPrefix(r.MAC, "MAC2:") {
		t.Fatalf("zero-value VerifyMacVer must write v2, got %q", r.MAC)
	}
	d.queryRows = [][]driver.Value{{int64(1), r.A, r.B, r.MAC}}
	var got reviewMACRow
	if eror := e.Select(&got, "select id,a,b,mac from review_mac"); eror != nil {
		t.Fatalf("default v2 round trip: %v", eror)
	}

	legacy := &TUniEngine{Db: e.Db, ColLabel: "db", ColParam: "$", Provider: DtPOSTGR,
		VerifyOn: 1, VerifyMode: 2, VerifyBy: "mac-default-key", VerifyMacVer: 1}
	legacy.RegisterClass(reviewMACRow{}, "review_mac").SetKeys("id").SetVerify("mac", "id", "a", "b")
	old := reviewMACRow{ID: 2, A: "old", B: "row"}
	if eror := legacy.Insert(&old); eror != nil {
		t.Fatal(eror)
	}
	if !strings.HasPrefix(old.MAC, "MAC:") {
		t.Fatalf("explicit legacy write must retain v1 tag, got %q", old.MAC)
	}
	d.queryRows = [][]driver.Value{{int64(2), old.A, old.B, old.MAC}}
	if eror := e.Select(&got, "select id,a,b,mac from review_mac"); eror != nil {
		t.Fatalf("default reader must verify stored v1 row: %v", eror)
	}
}

func TestStrictVerifyRejectsCustomResultHook(t *testing.T) {
	e := newMockEngine(&mockDriver{})
	defer e.Db.Close()
	e.VerifyOn, e.VerifyMode, e.VerifyBy = 1, 2, "key"
	if eror := e.RegisterClass(setSqlResultRow{}, "hook_verify").SetKeys("user_name").SetVerify("password", "user_name").LastError; eror != nil {
		t.Fatal(eror)
	}
	var rows []setSqlResultRow
	if eror := e.SelectL(&rows, "select user_name,password from hook_verify"); !errors.Is(eror, ErrIncompleteVerification) {
		t.Fatalf("custom result hook bypass must fail in strict mode: %v", eror)
	}
}

func TestSecretCacheRejectsInvalidCiphertextsAndEvicts(t *testing.T) {
	reader := &TUniEngine{SecretBy: "cache-test-key"}
	for i := 0; i < 150; i++ {
		payload := make([]byte, secretHeadLen+16)
		binary.BigEndian.PutUint64(payload[8:16], uint64(i+1))
		binary.BigEndian.PutUint32(payload[secretSaltLen:secretSaltLen+4], 1)
		ciphertext := UniSecretTag + base64.StdEncoding.EncodeToString(payload)
		if _, eror := reader.SecretDefault(ciphertext, false); eror == nil {
			t.Fatal("invalid authentication tag must fail")
		}
	}
	st := reader.secretStateOf()
	st.mu.Lock()
	invalidCached := len(st.Keys)
	st.mu.Unlock()
	if invalidCached != 0 {
		t.Fatalf("invalid ciphertexts cached %d derived keys", invalidCached)
	}

	for i := 0; i < secretCacheMax+20; i++ {
		writer := &TUniEngine{SecretBy: "cache-test-key", SecretIter: 1}
		ciphertext, eror := writer.SecretDefault("value", true)
		if eror != nil {
			t.Fatal(eror)
		}
		plain, eror := reader.SecretDefault(ciphertext, false)
		if eror != nil || plain != "value" {
			t.Fatalf("valid cross-engine ciphertext failed: %q, %v", plain, eror)
		}
	}
	st.mu.Lock()
	validCached, orderLen := len(st.Keys), len(st.Order)
	st.mu.Unlock()
	if validCached != secretCacheMax || orderLen != secretCacheMax {
		t.Fatalf("cache must stay bounded: keys=%d order=%d max=%d", validCached, orderLen, secretCacheMax)
	}
}
