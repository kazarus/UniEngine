// UniMeta
// #执行与元数据探测:Execute/Exist*/IfDropView

package UniEngine

import (
	"context"
	"errors"
	"fmt"
)

// ---------------------------------------------------------------------------
// Execute / 元数据探测
// ---------------------------------------------------------------------------

func (this *TUniEngine) Execute(SqlQuery string, Args ...interface{}) error {
	return this.ExecuteCtx(context.Background(), SqlQuery, Args...)
}

func (this *TUniEngine) ExecuteCtx(Ctx context.Context, SqlQuery string, Args ...interface{}) error {
	SqlQuery, Args, eror := this.getSqlQuery(SqlQuery, Args...)
	if eror != nil {
		return eror
	}

	this.sqlDebug("execute", SqlQuery, Args)

	St, eror := this.prepareCtx(Ctx, SqlQuery)
	if eror != nil {
		return eror
	}
	defer St.Close()

	if _, eror := St.ExecContext(Ctx, Args...); eror != nil {
		return fmt.Errorf("UniEngine: execute fail: %w", eror)
	}

	return nil
}

func (this *TUniEngine) ExecuteMust(SqlQuery string, Args ...interface{}) error {
	return this.ExecuteMustCtx(context.Background(), SqlQuery, Args...)
}

func (this *TUniEngine) ExecuteMustCtx(Ctx context.Context, SqlQuery string, Args ...interface{}) error {
	SqlQuery, Args, eror := this.getSqlQuery(SqlQuery, Args...)
	if eror != nil {
		return eror
	}

	this.sqlDebug("execute", SqlQuery, Args)

	St, eror := this.prepareCtx(Ctx, SqlQuery)
	if eror != nil {
		return eror
	}
	defer St.Close()

	Result, eror := St.ExecContext(Ctx, Args...)
	if eror != nil {
		return fmt.Errorf("UniEngine: execute fail: %w", eror)
	}

	Size, eror := Result.RowsAffected()
	if eror != nil {
		return eror
	}

	if Size == 0 {
		return errors.New("UniEngine: the row count of affected is zero")
	}

	return nil
}

func (this *TUniEngine) IfDropView(TableName string) (bool, error) {
	return this.IfDropViewCtx(context.Background(), TableName)
}

func (this *TUniEngine) IfDropViewCtx(Ctx context.Context, TableName string) (bool, error) {
	if !validIdent(TableName) {
		return false, fmt.Errorf("%w: %s", ErrInvalidTableName, TableName)
	}

	mrok, eror := this.ExistViewsCtx(Ctx, TableName)
	if eror != nil {
		return false, eror
	}

	if mrok {
		SQL := fmt.Sprintf("DROP VIEW %s", TableName)
		if eror = this.ExecuteCtx(Ctx, SQL); eror != nil {
			return false, eror
		}
	}

	return true, nil
}

// existCount 四个 Exist* 探测的公共收尾:执行计数 SQL 并转换为布尔结果。
func (this *TUniEngine) existCount(Ctx context.Context, Kind string, SQL string) (bool, error) {
	this.sqlDebug(Kind, SQL, nil)

	if SQL == "" {
		return false, fmt.Errorf("UniEngine: no sql for %s", Kind)
	}

	Size, eror := this.SelectDCtx(Ctx, SQL)
	if eror != nil {
		return false, eror
	}

	return Size > 0, nil
}

func (this *TUniEngine) ExistTable(TableName string) (bool, error) {
	return this.ExistTableCtx(context.Background(), TableName)
}

func (this *TUniEngine) ExistTableCtx(Ctx context.Context, TableName string) (bool, error) {
	if !validIdent(TableName) {
		return false, fmt.Errorf("%w: %s", ErrInvalidTableName, TableName)
	}

	var SQL string

	switch this.dbFamily() {
	case FmPOSTGR:
		SQL = TExistTable4POSTGR{}.GetSqlExistTable(this, TableName)
	case FmSQLSRV:
		SQL = TExistTable4SQLSRV{}.GetSqlExistTable(this, TableName)
	case FmORACLE:
		SQL = TExistTable4ORACLE{}.GetSqlExistTable(this, TableName)
	case FmMYSQLN:
		if this.DataBase == "" {
			return false, errors.New("UniEngine: database is not specified")
		}
		SQL = TExistTable4MYSQLN{}.GetSqlExistTable(this, TableName, this.DataBase)
	}

	return this.existCount(Ctx, "existtable", SQL)
}

func (this *TUniEngine) ExistViews(TableName string) (bool, error) {
	return this.ExistViewsCtx(context.Background(), TableName)
}

func (this *TUniEngine) ExistViewsCtx(Ctx context.Context, TableName string) (bool, error) {
	if !validIdent(TableName) {
		return false, fmt.Errorf("%w: %s", ErrInvalidTableName, TableName)
	}

	var SQL string

	switch this.dbFamily() {
	case FmPOSTGR:
		SQL = TExistTable4POSTGR{}.GetSqlExistViews(this, TableName)
	case FmSQLSRV:
		SQL = TExistTable4SQLSRV{}.GetSqlExistViews(this, TableName)
	case FmORACLE:
		SQL = TExistTable4ORACLE{}.GetSqlExistViews(this, TableName)
	case FmMYSQLN:
		if this.DataBase == "" {
			return false, errors.New("UniEngine: database is not specified")
		}
		SQL = TExistTable4MYSQLN{}.GetSqlExistViews(this, TableName, this.DataBase)
	}

	return this.existCount(Ctx, "existviews", SQL)
}

func (this *TUniEngine) ExistField(TableName, FieldName string) (bool, error) {
	return this.ExistFieldCtx(context.Background(), TableName, FieldName)
}

func (this *TUniEngine) ExistFieldCtx(Ctx context.Context, TableName, FieldName string) (bool, error) {
	if !validIdent(TableName) || !validIdent(FieldName) {
		return false, errors.New("UniEngine: invalid table/field name: " + TableName + "." + FieldName)
	}

	var SQL string

	switch this.dbFamily() {
	case FmPOSTGR:
		SQL = TExistField4POSTGR{}.GetSqlExistField(this, TableName, FieldName)
	case FmSQLSRV:
		SQL = TExistField4SQLSRV{}.GetSqlExistField(this, TableName, FieldName)
	case FmORACLE:
		SQL = TExistField4ORACLE{}.GetSqlExistField(this, TableName, FieldName)
	case FmMYSQLN:
		if this.DataBase == "" {
			return false, errors.New("UniEngine: database is not specified")
		}
		SQL = TExistField4MYSQLN{}.GetSqlExistField(this, TableName, FieldName, this.DataBase)
	}

	return this.existCount(Ctx, "existfield", SQL)
}

// #ExistConst 判断指定类型的约束是否存在(按约束名/列名匹配)
func (this *TUniEngine) ExistConst(ConstType TConstType, ConstName string) (bool, error) {
	return this.ExistConstCtx(context.Background(), ConstType, ConstName)
}

func (this *TUniEngine) ExistConstCtx(Ctx context.Context, ConstType TConstType, ConstName string) (bool, error) {
	if !validIdent(ConstName) {
		return false, fmt.Errorf("%w: %s", ErrInvalidConstraintName, ConstName)
	}

	var SQL string

	switch this.dbFamily() {
	case FmPOSTGR:
		SQL = TExistConst4POSTGR{}.GetSqlExistConst(this, ConstType, ConstName)
	case FmSQLSRV:
		SQL = TExistConst4SQLSRV{}.GetSqlExistConst(this, ConstType, ConstName)
	case FmORACLE:
		SQL = TExistConst4ORACLE{}.GetSqlExistConst(this, ConstType, ConstName)
	case FmMYSQLN:
		if this.DataBase == "" {
			return false, errors.New("UniEngine: database is not specified")
		}
		SQL = TExistConst4MYSQLN{}.GetSqlExistConst(this, ConstType, ConstName, this.DataBase)
	}

	return this.existCount(Ctx, "existconst", SQL)
}

// ---------------------------------------------------------------------------
// 语句/事务管理
// ---------------------------------------------------------------------------

// prepareCtx 在当前事务(若有)或连接池上预备语句。
// 语句是局部变量,不再保存到引擎——这是并发安全的关键:查询路径不共享可变状态。
// 返回的语句由调用方负责 Close。
