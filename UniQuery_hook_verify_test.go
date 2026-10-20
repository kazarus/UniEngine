// UniQuery_hook_verify_test
// #结果钩子(SetSqlResult)路径的完整性验签测试

package UniEngine

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// THookVerifyRow 结果钩子类:自带列映射,不依赖引擎默认解码。
type THookVerifyRow struct {
	WhoBuild int64  `db:"who_build"`
	UserIndx int64  `db:"user_indx"`
	DataStat int64  `db:"data_stat"`
	MastName string `db:"mast_name"`
	MacText  string `db:"mac_text"`
}

func (this THookVerifyRow) SetSqlResult(eng *TUniEngine, result interface{}, column []string, fields []interface{}) {
	row := result.(*THookVerifyRow)
	for i, col := range column {
		switch strings.ToLower(col) {
		case "who_build":
			row.WhoBuild = fields[i].(int64)
		case "user_indx":
			row.UserIndx = fields[i].(int64)
		case "data_stat":
			row.DataStat = fields[i].(int64)
		case "mast_name":
			row.MastName = fields[i].(string)
		case "mac_text":
			row.MacText = fields[i].(string)
		}
	}
}

func newHookVerifyEngine(d *mockDriver, mode int64) *TUniEngine {
	eng := &TUniEngine{Db: sql.OpenDB(mockConnector{d: d}), ColLabel: "db", ColParam: "$",
		Provider: DtPOSTGR, VerifyOn: 1, VerificationMode: mode, VerifyBy: "test-verify-key"}
	eng.Initialize()
	eng.RegisterClass(THookVerifyRow{}, "tbl_hook_verify").
		SetKeys("who_build", "user_indx").
		SetVerify("mac_text", "who_build", "user_indx", "data_stat", "mast_name")
	return eng
}

func hookVerifyExpect(t *testing.T, eng *TUniEngine, row THookVerifyRow) string {
	t.Helper()
	payload, eror := verifyPayload("tbl_hook_verify",
		[]string{"who_build", "user_indx", "data_stat", "mast_name"},
		[]int{0, 1, 2, 3}, reflect.ValueOf(row))
	if eror != nil {
		t.Fatal(eror)
	}
	sum, eror := eng.verifySign(payload)
	if eror != nil {
		t.Fatal(eror)
	}
	return verifyTextOf(sum)
}

func hookVerifyColumns() []string {
	return []string{"who_build", "user_indx", "data_stat", "mast_name", "mac_text"}
}

// #钩子路径:列覆盖完整且 MAC 正确 -> 验签通过
func TestVerifyHookSelectPass(t *testing.T) {
	eng := newHookVerifyEngine(&mockDriver{}, 1)
	row := THookVerifyRow{WhoBuild: 1, UserIndx: 2, DataStat: 1, MastName: "mast"}

	d := &mockDriver{columns: hookVerifyColumns(),
		queryRows: [][]driver.Value{{int64(1), int64(2), int64(1), "mast", hookVerifyExpect(t, eng, row)}}}
	eng.Db = sql.OpenDB(mockConnector{d: d})

	var rows []THookVerifyRow
	if eror := eng.SelectL(&rows, "select who_build,user_indx,data_stat,mast_name,mac_text from tbl_hook_verify"); eror != nil {
		t.Fatal(eror)
	}
	if len(rows) != 1 || rows[0].MastName != "mast" {
		t.Fatalf("hook path should return verified row: %+v", rows)
	}
}

// #钩子路径:MAC 被篡改 -> 严格模式报错
func TestVerifyHookSelectTamperStrict(t *testing.T) {
	eng := newHookVerifyEngine(&mockDriver{}, 2)
	good := THookVerifyRow{WhoBuild: 1, UserIndx: 2, DataStat: 1, MastName: "mast"}
	bad := good
	bad.MastName = "other"

	d := &mockDriver{columns: hookVerifyColumns(),
		queryRows: [][]driver.Value{{int64(1), int64(2), int64(1), "mast", hookVerifyExpect(t, eng, bad)}}}
	eng.Db = sql.OpenDB(mockConnector{d: d})

	var rows []THookVerifyRow
	eror := eng.SelectL(&rows, "select who_build,user_indx,data_stat,mast_name,mac_text from tbl_hook_verify")
	if eror == nil {
		t.Fatal("strict mode must reject tampered mac on hook path")
	}
}

// #钩子路径:严格模式缺保护列 -> ErrIncompleteVerification
func TestVerifyHookSelectIncompleteStrict(t *testing.T) {
	eng := newHookVerifyEngine(&mockDriver{}, 2)
	cols := []string{"who_build", "user_indx", "data_stat", "mac_text"}
	d := &mockDriver{columns: cols,
		queryRows: [][]driver.Value{{int64(1), int64(2), int64(1), "MAC1:AAAA"}}}
	eng.Db = sql.OpenDB(mockConnector{d: d})

	var rows []THookVerifyRow
	eror := eng.SelectL(&rows, "select who_build,user_indx,data_stat,mac_text from tbl_hook_verify")
	if !errors.Is(eror, ErrIncompleteVerification) {
		t.Fatalf("strict incomplete hook select should fail with ErrIncompleteVerification, got %v", eror)
	}
}

// #钩子路径:宽容模式缺保护列 -> 跳过验签(与默认路径同语义)
func TestVerifyHookSelectIncompleteLenient(t *testing.T) {
	eng := newHookVerifyEngine(&mockDriver{}, 1)
	cols := []string{"who_build", "user_indx", "data_stat", "mac_text"}
	d := &mockDriver{columns: cols,
		queryRows: [][]driver.Value{{int64(1), int64(2), int64(1), "MAC1:AAAA"}}}
	eng.Db = sql.OpenDB(mockConnector{d: d})

	var rows []THookVerifyRow
	if eror := eng.SelectL(&rows, "select who_build,user_indx,data_stat,mac_text from tbl_hook_verify"); eror != nil {
		t.Fatalf("lenient incomplete hook select should pass, got %v", eror)
	}
}

// #钩子路径:允许钩子自取计算列(不在类中的列不影响验签)
func TestVerifyHookExtraColumnIgnored(t *testing.T) {
	eng := newHookVerifyEngine(&mockDriver{}, 2)
	row := THookVerifyRow{WhoBuild: 1, UserIndx: 2, DataStat: 1, MastName: "mast"}

	cols := append(hookVerifyColumns(), "computed")
	d := &mockDriver{columns: cols,
		queryRows: [][]driver.Value{{int64(1), int64(2), int64(1), "mast", hookVerifyExpect(t, eng, row), "x"}}}
	eng.Db = sql.OpenDB(mockConnector{d: d})

	var rows []THookVerifyRow
	if eror := eng.SelectL(&rows, "select who_build,user_indx,data_stat,mast_name,mac_text,computed from tbl_hook_verify"); eror != nil {
		t.Fatalf("extra computed column must not break hook verification: %v", eror)
	}
}
