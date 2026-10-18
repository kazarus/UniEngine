// quality_fix5_test
// #2026-10-18 评审:ORACLE 系元数据判定口径统一

package UniEngine

import (
	"strings"
	"testing"
)

// Oracle 的 ExistTable 曾用 all_tables,而 ExistViews/ExistField/ExistConst/AutoKeys
// 全部用 user_*。其它 schema 存在同名表时,ExistTable=true 但 AutoKeys 查不到主键,
// 迁移/建档流程会进入错误分支。本测试锁定当前用户 schema 口径。
func TestOracleMetadataUsesCurrentSchema(t *testing.T) {
	eng := &TUniEngine{}

	Cases := []struct {
		Name string
		SQL  string
		Want string
	}{
		{"ExistTable", TExistTable4ORACLE{}.GetSqlExistTable(eng, "t_ora"), "user_tables"},
		{"ExistViews", TExistTable4ORACLE{}.GetSqlExistViews(eng, "v_ora"), "user_views"},
		{"ExistField", TExistField4ORACLE{}.GetSqlExistField(eng, "t_ora", "name"), "user_tab_columns"},
		{"ExistConst PK", TExistConst4ORACLE{}.GetSqlExistConst(eng, CtPK, "pk_ora"), "user_constraints"},
		{"ExistConst FK", TExistConst4ORACLE{}.GetSqlExistConst(eng, CtFK, "fk_ora"), "user_constraints"},
		{"ExistConst UK", TExistConst4ORACLE{}.GetSqlExistConst(eng, CtUK, "uk_ora"), "user_constraints"},
		{"ExistConst DF", TExistConst4ORACLE{}.GetSqlExistConst(eng, CtDF, "col_ora"), "user_tab_cols"},
	}

	for _, C := range Cases {
		if !strings.Contains(C.SQL, C.Want) {
			t.Errorf("%s should use %s, got %q", C.Name, C.Want, C.SQL)
		}
		if strings.Contains(strings.ToLower(C.SQL), "all_tables") || strings.Contains(strings.ToLower(C.SQL), "all_views") {
			t.Errorf("%s must not use ALL_* catalogues (cross-schema false positive), got %q", C.Name, C.SQL)
		}
	}

	AutoSQL, eror := TAutoKeys4ORACLE{}.GetSqlAutoKeys(eng, "t_ora")
	if eror != nil {
		t.Fatal(eror)
	}
	if !strings.Contains(AutoSQL, "user_cons_columns") || !strings.Contains(AutoSQL, "user_constraints") {
		t.Fatalf("AutoKeys should use current-schema catalogues, got %q", AutoSQL)
	}
	if !strings.Contains(AutoSQL, "order by cu.position") {
		t.Fatalf("AutoKeys must preserve composite PK column order, got %q", AutoSQL)
	}
}

// Oracle 的 ExistTable 当前只覆盖当前 schema;显式 schema.table 访问不在元数据探测范围内,
// 由文档说明,避免与 CRUD 裸表名路径混为一谈。
func TestOracleExistTableIsCaseAndScopeExplicit(t *testing.T) {
	SQL := TExistTable4ORACLE{}.GetSqlExistTable(&TUniEngine{}, "MixedCase")
	if !strings.Contains(SQL, "upper('MixedCase')") {
		t.Fatalf("Oracle unquoted identifiers are upper-cased; got %q", SQL)
	}
}
