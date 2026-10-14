// UniQuery_test
// #单 struct Select 合并语义回归测试(修整结构体替换清空未选列)

package UniEngine

import (
	"database/sql"
	"database/sql/driver"
	"testing"
)

type TSelectMergeUser struct {
	WhoBuild     int64  `db:"who_build"`
	UserIndx     int64  `db:"user_indx"`
	FromWhoBuild int64  `db:"from_who_build"`
	UnitNamf     string `db:"unit_namf"`
}

func newSelectMergeEngine(d *mockDriver) *TUniEngine {
	db := sql.OpenDB(mockConnector{d: d})
	eng := &TUniEngine{Db: db, ColLabel: "db", ColParam: "$", Provider: DtPOSTGR}
	eng.Initialize()
	eng.RegisterClass(TSelectMergeUser{}, "tbl_merge_user")
	return eng
}

// TestSelectMergeKeepsUnselectedFields 单 struct 目标只覆盖 SELECT 列,
// 未出现在结果列里的字段保留调用方原值(回归:VerifyDX 的 FROM_WHO_BUILD 被清零)。
func TestSelectMergeKeepsUnselectedFields(t *testing.T) {
	d := &mockDriver{columns: []string{"unit_namf"}, queryRows: [][]driver.Value{{"测试单位"}}}
	eng := newSelectMergeEngine(d)

	var RealUser = TSelectMergeUser{WhoBuild: 7, UserIndx: 9, FromWhoBuild: 3, UnitNamf: "旧值"}
	if eror := eng.Select(&RealUser, "SELECT A.UNIT_NAMF FROM TBL_UNIT A"); eror != nil {
		t.Fatalf("Select 失败: %v", eror)
	}
	if RealUser.UnitNamf != "测试单位" {
		t.Errorf("SELECT 列应覆盖: unit_namf=%q", RealUser.UnitNamf)
	}
	if RealUser.WhoBuild != 7 || RealUser.UserIndx != 9 || RealUser.FromWhoBuild != 3 {
		t.Errorf("未选列被清零: %+v", RealUser)
	}
}

// TestSelectZeroRowsKeepsDestination 零行结果不触发写回,目标原样保留。
func TestSelectZeroRowsKeepsDestination(t *testing.T) {
	d := &mockDriver{columns: []string{"unit_namf"}, queryRows: nil}
	eng := newSelectMergeEngine(d)

	var RealUser = TSelectMergeUser{WhoBuild: 7, FromWhoBuild: 3}
	if eror := eng.Select(&RealUser, "SELECT A.UNIT_NAMF FROM TBL_UNIT A WHERE 1=0"); eror != nil {
		t.Fatalf("Select 失败: %v", eror)
	}
	if RealUser.WhoBuild != 7 || RealUser.FromWhoBuild != 3 || RealUser.UnitNamf != "" {
		t.Errorf("零行结果不应改动目标: %+v", RealUser)
	}
}

// TestSelectFreshStructUnchanged 传零值结构体的常规用法行为不变(选列写入)。
func TestSelectFreshStructUnchanged(t *testing.T) {
	d := &mockDriver{columns: []string{"who_build", "user_indx"}, queryRows: [][]driver.Value{{int64(1), int64(2)}}}
	eng := newSelectMergeEngine(d)

	var User = TSelectMergeUser{}
	if eror := eng.Select(&User, "SELECT WHO_BUILD,USER_INDX FROM tbl_merge_user"); eror != nil {
		t.Fatalf("Select 失败: %v", eror)
	}
	if User.WhoBuild != 1 || User.UserIndx != 2 {
		t.Errorf("选列未写入: %+v", User)
	}
}
