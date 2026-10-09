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

func (this *TUniEngine) Execute(SqlQuery string, args ...interface{}) error {
	return this.ExecuteCtx(context.Background(), SqlQuery, args...)
}

func (this *TUniEngine) ExecuteCtx(ctx context.Context, SqlQuery string, args ...interface{}) error {

	SqlQuery = this.getSqlQuery(SqlQuery, args...)

	this.debugSQL("execute", SqlQuery, args)

	st, eror := this.prepareCtx(ctx, SqlQuery)
	if eror != nil {
		return eror
	}
	defer st.Close()

	if _, eror := st.ExecContext(ctx, args...); eror != nil {
		return fmt.Errorf("UniEngine: execute fail: %w", eror)
	}

	return nil
}

func (this *TUniEngine) ExecuteMust(SqlQuery string, args ...interface{}) error {
	return this.ExecuteMustCtx(context.Background(), SqlQuery, args...)
}

func (this *TUniEngine) ExecuteMustCtx(ctx context.Context, SqlQuery string, args ...interface{}) error {

	SqlQuery = this.getSqlQuery(SqlQuery, args...)

	this.debugSQL("execute", SqlQuery, args)

	st, eror := this.prepareCtx(ctx, SqlQuery)
	if eror != nil {
		return eror
	}
	defer st.Close()

	result, eror := st.ExecContext(ctx, args...)
	if eror != nil {
		return fmt.Errorf("UniEngine: execute fail: %w", eror)
	}

	size, eror := result.RowsAffected()
	if eror != nil {
		return eror
	}

	if size == 0 {
		return errors.New("UniEngine: the row count of affected is zero")
	}

	return nil
}

func (this *TUniEngine) IfDropView(TableName string) (bool, error) {
	return this.IfDropViewCtx(context.Background(), TableName)
}

func (this *TUniEngine) IfDropViewCtx(ctx context.Context, TableName string) (bool, error) {

	if !validIdent(TableName) {
		return false, fmt.Errorf("%w: %s", ErrInvalidTableName, TableName)
	}

	mrok, eror := this.ExistViewsCtx(ctx, TableName)
	if eror != nil {
		return false, eror
	}

	if mrok {
		cSQL := fmt.Sprintf("DROP VIEW %s", TableName)
		if eror = this.ExecuteCtx(ctx, cSQL); eror != nil {
			return false, eror
		}
	}

	return true, nil
}

// existCount 四个 Exist* 探测的公共收尾:执行计数 SQL 并转换为布尔结果。

// existCount 四个 Exist* 探测的公共收尾:执行计数 SQL 并转换为布尔结果。
func (this *TUniEngine) existCount(ctx context.Context, Kind string, cSQL string) (bool, error) {

	this.debugSQL(Kind, cSQL, nil)

	if cSQL == "" {
		return false, fmt.Errorf("UniEngine: no sql for %s", Kind)
	}

	Size, eror := this.SelectDCtx(ctx, cSQL)
	if eror != nil {
		return false, eror
	}

	return Size > 0, nil
}

func (this *TUniEngine) ExistTable(TableName string) (bool, error) {
	return this.ExistTableCtx(context.Background(), TableName)
}

func (this *TUniEngine) ExistTableCtx(ctx context.Context, TableName string) (bool, error) {

	if !validIdent(TableName) {
		return false, fmt.Errorf("%w: %s", ErrInvalidTableName, TableName)
	}

	var cSQL string

	switch this.dbFamily() {
	case FmPOSTGR:
		cSQL = TExistTable4POSTGR{}.GetSqlExistTable(this, TableName)
	case FmSQLSRV:
		cSQL = TExistTable4SQLSRV{}.GetSqlExistTable(this, TableName)
	case FmORACLE:
		cSQL = TExistTable4ORACLE{}.GetSqlExistTable(this, TableName)
	case FmMYSQLN:
		if this.DataBase == "" {
			return false, errors.New("UniEngine: database is not specified")
		}
		cSQL = TExistTable4MYSQLN{}.GetSqlExistTable(this, TableName, this.DataBase)
	}

	return this.existCount(ctx, "existtable", cSQL)
}

func (this *TUniEngine) ExistViews(TableName string) (bool, error) {
	return this.ExistViewsCtx(context.Background(), TableName)
}

func (this *TUniEngine) ExistViewsCtx(ctx context.Context, TableName string) (bool, error) {

	if !validIdent(TableName) {
		return false, fmt.Errorf("%w: %s", ErrInvalidTableName, TableName)
	}

	var cSQL string

	switch this.dbFamily() {
	case FmPOSTGR:
		cSQL = TExistTable4POSTGR{}.GetSqlExistViews(this, TableName)
	case FmSQLSRV:
		cSQL = TExistTable4SQLSRV{}.GetSqlExistViews(this, TableName)
	case FmORACLE:
		cSQL = TExistTable4ORACLE{}.GetSqlExistViews(this, TableName)
	case FmMYSQLN:
		if this.DataBase == "" {
			return false, errors.New("UniEngine: database is not specified")
		}
		cSQL = TExistTable4MYSQLN{}.GetSqlExistViews(this, TableName, this.DataBase)
	}

	return this.existCount(ctx, "existviews", cSQL)
}

func (this *TUniEngine) ExistField(TableName, FieldName string) (bool, error) {
	return this.ExistFieldCtx(context.Background(), TableName, FieldName)
}

func (this *TUniEngine) ExistFieldCtx(ctx context.Context, TableName, FieldName string) (bool, error) {

	if !validIdent(TableName) || !validIdent(FieldName) {
		return false, errors.New("UniEngine: invalid table/field name: " + TableName + "." + FieldName)
	}

	var cSQL string

	switch this.dbFamily() {
	case FmPOSTGR:
		cSQL = TExistField4POSTGR{}.GetSqlExistField(this, TableName, FieldName)
	case FmSQLSRV:
		cSQL = TExistField4SQLSRV{}.GetSqlExistField(this, TableName, FieldName)
	case FmORACLE:
		cSQL = TExistField4ORACLE{}.GetSqlExistField(this, TableName, FieldName)
	case FmMYSQLN:
		if this.DataBase == "" {
			return false, errors.New("UniEngine: database is not specified")
		}
		cSQL = TExistField4MYSQLN{}.GetSqlExistField(this, TableName, FieldName, this.DataBase)
	}

	return this.existCount(ctx, "existfield", cSQL)
}

// #ExistConst 判断指定类型的约束是否存在(按约束名/列名匹配)

// #ExistConst 判断指定类型的约束是否存在(按约束名/列名匹配)
func (this *TUniEngine) ExistConst(aConstType TConstType, aConstName string) (bool, error) {
	return this.ExistConstCtx(context.Background(), aConstType, aConstName)
}

func (this *TUniEngine) ExistConstCtx(ctx context.Context, aConstType TConstType, aConstName string) (bool, error) {

	if !validIdent(aConstName) {
		return false, fmt.Errorf("%w: %s", ErrInvalidConstraintName, aConstName)
	}

	var cSQL string

	switch this.dbFamily() {
	case FmPOSTGR:
		cSQL = TExistConst4POSTGR{}.GetSqlExistConst(this, aConstType, aConstName)
	case FmSQLSRV:
		cSQL = TExistConst4SQLSRV{}.GetSqlExistConst(this, aConstType, aConstName)
	case FmORACLE:
		cSQL = TExistConst4ORACLE{}.GetSqlExistConst(this, aConstType, aConstName)
	case FmMYSQLN:
		if this.DataBase == "" {
			return false, errors.New("UniEngine: database is not specified")
		}
		cSQL = TExistConst4MYSQLN{}.GetSqlExistConst(this, aConstType, aConstName, this.DataBase)
	}

	return this.existCount(ctx, "existconst", cSQL)
}

// ---------------------------------------------------------------------------
// 语句/事务管理
// ---------------------------------------------------------------------------

// prepareCtx 在当前事务(若有)或连接池上预备语句。
// 语句是局部变量,不再保存到引擎——这是并发安全的关键:查询路径不共享可变状态。
// 返回的语句由调用方负责 Close。
