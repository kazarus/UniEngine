// UniMeta
// #执行与元数据探测:Execute/Exist*/IfDropView

package UniEngine

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

// mysqlDataBase 校验并返回 MySQL 元数据探测所需的库名。
// 库名会进入 information_schema 查询的单引号字面量,必须与表名/字段名同口径白名单校验。
func (this *TUniEngine) mysqlDataBase() (string, error) {
	if this.DataBase == "" {
		return "", errors.New("UniEngine: database is not specified")
	}
	if !validIdent(this.DataBase) {
		return "", fmt.Errorf("%w: %s", ErrInvalidDataBase, this.DataBase)
	}

	return this.DataBase, nil
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
		DataBase, eror := this.mysqlDataBase()
		if eror != nil {
			return false, eror
		}
		SQL = TExistTable4MYSQLN{}.GetSqlExistTable(this, TableName, DataBase)
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
		DataBase, eror := this.mysqlDataBase()
		if eror != nil {
			return false, eror
		}
		SQL = TExistTable4MYSQLN{}.GetSqlExistViews(this, TableName, DataBase)
	}

	return this.existCount(Ctx, "existviews", SQL)
}

func (this *TUniEngine) ExistField(TableName, FieldName string) (bool, error) {
	return this.ExistFieldCtx(context.Background(), TableName, FieldName)
}

func (this *TUniEngine) ExistFieldCtx(Ctx context.Context, TableName, FieldName string) (bool, error) {
	if !validIdent(TableName) {
		return false, fmt.Errorf("%w: %s", ErrInvalidTableName, TableName)
	}
	if !validColumnName(FieldName) {
		return false, fmt.Errorf("%w: %s", ErrInvalidFieldName, FieldName)
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
		DataBase, eror := this.mysqlDataBase()
		if eror != nil {
			return false, eror
		}
		SQL = TExistField4MYSQLN{}.GetSqlExistField(this, TableName, FieldName, DataBase)
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
		DataBase, eror := this.mysqlDataBase()
		if eror != nil {
			return false, eror
		}
		SQL = TExistConst4MYSQLN{}.GetSqlExistConst(this, ConstType, ConstName, DataBase)
	}

	return this.existCount(Ctx, "existconst", SQL)
}

// ExistConstOn 判断“指定表上的列默认值”等需要表名限定的约束。
// 对 CtDF,ConstName 为列名;其它约束类型仍按 ConstName 判断(表名参数被忽略),
// 因为主键/外键/唯一约束名通常已在 schema 内唯一。
// 该入口解决了 ExistConst(CtDF,列名) 只按列名匹配、无法区分同名列的问题。
func (this *TUniEngine) ExistConstOn(TableName string, ConstType TConstType, ConstName string) (bool, error) {
	return this.ExistConstOnCtx(context.Background(), TableName, ConstType, ConstName)
}

func (this *TUniEngine) ExistConstOnCtx(Ctx context.Context, TableName string, ConstType TConstType, ConstName string) (bool, error) {
	if !validIdent(TableName) {
		return false, fmt.Errorf("%w: %s", ErrInvalidTableName, TableName)
	}
	if !validIdent(ConstName) {
		return false, fmt.Errorf("%w: %s", ErrInvalidConstraintName, ConstName)
	}
	if ConstType != CtDF {
		return this.ExistConstCtx(Ctx, ConstType, ConstName)
	}

	var SQL string
	switch this.dbFamily() {
	case FmPOSTGR:
		SQL = fmt.Sprintf("select count(*) from pg_attrdef d join pg_class c on c.oid=d.adrelid join pg_attribute a on a.attrelid=c.oid and a.attnum=d.adnum where c.relname='%s' and a.attname='%s'", strings.ToLower(TableName), strings.ToLower(ConstName))
	case FmSQLSRV:
		SQL = fmt.Sprintf("select count(*) from sys.columns c join sys.tables t on c.object_id=t.object_id where t.name='%s' and c.name='%s' and c.default_object_id<>0", TableName, ConstName)
	case FmORACLE:
		SQL = fmt.Sprintf("select count(*) from user_tab_cols where table_name=upper('%s') and column_name=upper('%s') and data_default is not null", TableName, ConstName)
	case FmMYSQLN:
		DataBase, eror := this.mysqlDataBase()
		if eror != nil {
			return false, eror
		}
		SQL = fmt.Sprintf("select count(*) from information_schema.columns where table_schema='%s' and table_name='%s' and column_name='%s' and column_default is not null", DataBase, TableName, ConstName)
	}
	if SQL == "" {
		return false, fmt.Errorf("UniEngine: ExistConstOn has no SQL for provider %v", this.Provider)
	}
	return this.existCount(Ctx, "existconston", SQL)
}
