package UniEngine

import (
	"fmt"
	"reflect"
	"strings"
)

const CONST_PROVIDER_NAME_TO_POSTGR = "PostgreSQL"
const CONST_PROVIDER_NAME_TO_SQLSRV = "SQL Server"
const CONST_PROVIDER_NAME_TO_ORACLE = "Oracle"
const CONST_PROVIDER_NAME_TO_ACCESS = "Access"
const CONST_PROVIDER_NAME_TO_SQLITE = "SQLite"
const CONST_PROVIDER_NAME_TO_MYSQLN = "MySQL"
const CONST_PROVIDER_NAME_TO_KINGES = "kb"
const CONST_PROVIDER_NAME_TO_DAMENG = "dm"
const CONST_PROVIDER_NAME_TO_OPENGS = "opengauss"
const CONST_PROVIDER_NAME_TO_POLODB = "PolorDB"

const CONST_PROVIDER_CODE_TO_POSTGR = "postgres"
const CONST_PROVIDER_CODE_TO_SQLSRV = "mssql"
const CONST_PROVIDER_CODE_TO_ORACLE = "godror"
const CONST_PROVIDER_CODE_TO_ACCESS = "Access"
const CONST_PROVIDER_CODE_TO_SQLITE = "SQLite"
const CONST_PROVIDER_CODE_TO_MYSQLN = "mysql"
const CONST_PROVIDER_CODE_TO_KINGES = "kb"
const CONST_PROVIDER_CODE_TO_DAMENG = "dm"
const CONST_PROVIDER_CODE_TO_OPENGS = "opengauss"

type TFieldType int

const (
	FtVarchar TFieldType = 1 + iota
	FtFloat
	FtInteger
	FtBigint
	FtNumeric
	FtText
)

type TQueryType int

const (
	EtSelect TQueryType = 1 + iota
	EtInsert
	EtUpdate
	EtDelete
)

type TConstType int

const (
	CtPK TConstType = 1 + iota
	CtFK
	CtUK
	CtDF
)

type TDriveType int

const (
	DtPOSTGR TDriveType = 1 + iota
	DtSQLSRV
	DtORACLE
	DtACCESS
	DtSQLITE
	DtMYSQLN
	DtKINGES //#金仓(og)
	DtDAMENG //#达梦(ora)
	DtOPENGS //#高斯(pg)
	DtPOLODB //#阿里(pg)
	DtTAURUS //#华为(mysql)
)

var THasSetSqlResult = reflect.TypeOf(new(HasSetSqlResult)).Elem()
var THasGetMapUnique = reflect.TypeOf(new(HasGetMapUnique)).Elem()

type GetMapUnique func(u interface{}) string

type HasGetMapUnique interface {
	GetMapUnique() string
}

type HasStartSelect interface {
	StartSelect(TUniEngine) error
}

type HasEndedSelect interface {
	EndedSelect(TUniEngine) error
}

type HasStartUpdate interface {
	StartUpdate(TUniEngine) error
}

type HasEndedUpdate interface {
	EndedUpdate(TUniEngine) error
}

type HasStartDelete interface {
	StartDelete(TUniEngine) error
}

type HasEndedDelete interface {
	EndedDelete(TUniEngine) error
}

type HasStartInsert interface {
	StartInsert(TUniEngine) error
}

type HasEndedInsert interface {
	EndedInsert(TUniEngine) error
}

// #单笔更新SQL
type HasGetSqlUpdate interface {
	GetSqlUpdate(TUniEngine, string) string
}

// #单笔插入SQL
type HasGetSqlInsert interface {
	GetSqlInsert(TUniEngine, string) string
}

// #批量插入SQL
type HasGetSqlInsertL interface {
	GetSqlInsertL(TUniEngine, string, int64) string
}

// #批量插入SQL-copy协议
type HasCopyInGetSqlInsertL interface {
	CopyInGetSqlInsertL(TUniEngine, string, int64) []string
}

// #批量插入SQL-copy协议#已废弃:旧版命名,新代码请实现 HasCopyInGetSqlInsertL;引擎仍会探测本接口
type HasSpecialGetSqlInsertL interface {
	SpecialGetSqlInsertL(TUniEngine, string, int64) []string
}

// #单笔删除SQL
type HasGetSqlDelete interface {
	GetSqlDelete(TUniEngine, string) string
}

// #单笔赋值
type HasSetSqlValues interface {
	SetSqlValues(TUniEngine, TQueryType, *[]interface{})
}

// #批量赋值L
type HasSetSqlValuesL interface {
	SetSqlValuesL(TUniEngine, TQueryType, reflect.Value, *[]interface{})
}

// #批量赋值L-copy协议
type HasCopyInSetSqlValuesL interface {
	CopyInSetSqlValuesL(TUniEngine, TQueryType, reflect.Value, *[][]interface{})
}

// #批量赋值L-copy协议#已废弃:旧版命名,新代码请实现 HasCopyInSetSqlValuesL;引擎仍会探测本接口
type HasSpecialSetSqlValuesL interface {
	SpecialSetSqlValuesL(TUniEngine, TQueryType, reflect.Value, *[][]interface{})
}

// #读取数据
type HasSetSqlResult interface {
	SetSqlResult(TUniEngine, interface{}, []string, []interface{})
}

// #for tuniengine get exist table
type HasGetSqlExistTable interface {
	GetSqlExistTable(TUniEngine, string) string
}

// #for tuniengine get exist table
type HasGetSqlExistViews interface {
	GetSqlExistViews(TUniEngine, string) string
}

type TExistTable4POSTGR struct{}

func (self TExistTable4POSTGR) GetSqlExistTable(UniEngineEx TUniEngine, TableName string) string {

	result := "select count(relname) as value from pg_class where relname='%s'"

	return fmt.Sprintf(result, strings.ToLower(TableName))
}

func (self TExistTable4POSTGR) GetSqlExistViews(UniEngineEx TUniEngine, TableName string) string {

	result := "select count(relname) as value from pg_class where relname='%s' and relkind='v'"

	return fmt.Sprintf(result, strings.ToLower(TableName))
}

type TExistTable4SQLSRV struct{}

func (self TExistTable4SQLSRV) GetSqlExistTable(UniEngineEx TUniEngine, TableName string) string {

	result := "select count(*) from sysobjects where 1=1 and name='%s'"

	return fmt.Sprintf(result, strings.ToLower(TableName))
}

func (self TExistTable4SQLSRV) GetSqlExistViews(UniEngineEx TUniEngine, TableName string) string {

	result := "select count(*) from sysobjects where 1=1 and name='%s' and xtype='V'"

	return fmt.Sprintf(result, strings.ToLower(TableName))
}

type TExistTable4ORACLE struct{}

func (self TExistTable4ORACLE) GetSqlExistTable(UniEngineEx TUniEngine, TableName string) string {

	result := "select count(*) from all_tables where table_name=upper('%s')"

	return fmt.Sprintf(result, TableName)
}

func (self TExistTable4ORACLE) GetSqlExistViews(UniEngineEx TUniEngine, TableName string) string {

	result := "select count(*) from user_views where view_name=upper('%s')"

	return fmt.Sprintf(result, TableName)
}

type TExistTable4MYSQLN struct{}

func (self TExistTable4MYSQLN) GetSqlExistTable(UniEngineEx TUniEngine, TableName string, DataBase string) string {

	result := "select count(*) from information_schema.tables t where lower(table_name)='%s' and table_schema='%s'"

	//#全部换成小写
	return strings.ToLower(fmt.Sprintf(result, TableName, DataBase))
}

func (self TExistTable4MYSQLN) GetSqlExistViews(UniEngineEx TUniEngine, TableName string, DataBase string) string {

	result := "select count(*) from information_schema.views t where lower(table_name)='%s' and table_schema='%s'"

	//#全部换成小写
	return strings.ToLower(fmt.Sprintf(result, TableName, DataBase))
}

// #for tuniengine get exist field
type HasGetSqlExistField interface {
	GetSqlExistField(TUniEngine, string, string) string
}

type TExistField4POSTGR struct{}

func (self TExistField4POSTGR) GetSqlExistField(UniEngineEx TUniEngine, TableName string, FieldName string) string {

	result := "select count(a.attname) as value from pg_attribute a" +
		"    left join pg_class b on a.attrelid=b.oid where b.relname='%s' and a.attname='%s' and attnum>0"

	return fmt.Sprintf(result, strings.ToLower(TableName), strings.ToLower(FieldName))
}

type TExistField4SQLSRV struct{}

func (self TExistField4SQLSRV) GetSqlExistField(UniEngineEx TUniEngine, TableName string, FieldName string) string {

	result := "select count(*) as value from syscolumns where 1=1 and id=object_id('%s') and  name='%s'"

	return fmt.Sprintf(result, strings.ToLower(TableName), strings.ToLower(FieldName))
}

type TExistField4ORACLE struct{}

func (self TExistField4ORACLE) GetSqlExistField(UniEngineEx TUniEngine, TableName string, FieldName string) string {

	result := "select count(*) as value from user_tab_columns where table_name=upper('%s') and column_name=upper('%s')"

	return fmt.Sprintf(result, strings.ToUpper(TableName), strings.ToUpper(FieldName))
}

type TExistField4MYSQLN struct{}

func (self TExistField4MYSQLN) GetSqlExistField(UniEngineEx TUniEngine, TableName string, FieldName string, DataBase string) string {

	result := "select count(*) as value from information_schema.columns where 1=1 and table_schema='%s' and table_name='%s' and column_name='%s'"

	return fmt.Sprintf(result, DataBase, TableName, FieldName)
}

// #for tuniengine get exist const
type HasGetSqlExistConst interface {
	GetSqlExistConst(TUniEngine, TConstType, string) string
}

type TExistConst4POSTGR struct{}

// #PG:主键/外键/唯一 走 pg_constraint(contype:p/f/u);默认值挂在 pg_attrdef,按列名查
func (self TExistConst4POSTGR) GetSqlExistConst(UniEngineEx TUniEngine, ConstType TConstType, aConstName string) string {

	switch ConstType {
	case CtPK:
		return fmt.Sprintf("select count(*) from pg_constraint where conname='%s' and contype='p'", strings.ToLower(aConstName))
	case CtFK:
		return fmt.Sprintf("select count(*) from pg_constraint where conname='%s' and contype='f'", strings.ToLower(aConstName))
	case CtUK:
		return fmt.Sprintf("select count(*) from pg_constraint where conname='%s' and contype='u'", strings.ToLower(aConstName))
	case CtDF:
		return fmt.Sprintf("select count(*) from pg_attrdef d join pg_class c on c.oid=d.adrelid join pg_attribute a on a.attrelid=c.oid and a.attnum=d.adnum where a.attname='%s'", strings.ToLower(aConstName))
	}

	return ""
}

type TExistConst4SQLSRV struct{}

// #SQLServer:约束对象都在 sys.objects,type:PK/F/UQ/D
func (self TExistConst4SQLSRV) GetSqlExistConst(UniEngineEx TUniEngine, ConstType TConstType, aConstName string) string {

	switch ConstType {
	case CtPK:
		return fmt.Sprintf("select count(*) from sys.objects where name='%s' and type='PK'", strings.ToLower(aConstName))
	case CtFK:
		return fmt.Sprintf("select count(*) from sys.objects where name='%s' and type='F'", strings.ToLower(aConstName))
	case CtUK:
		return fmt.Sprintf("select count(*) from sys.objects where name='%s' and type='UQ'", strings.ToLower(aConstName))
	case CtDF:
		return fmt.Sprintf("select count(*) from sys.objects where name='%s' and type='D'", strings.ToLower(aConstName))
	}

	return ""
}

type TExistConst4ORACLE struct{}

// #Oracle:主键 P、外键 R(引用)、唯一 U 在 user_constraints;默认值在 user_tab_cols.data_default,按列名查
func (self TExistConst4ORACLE) GetSqlExistConst(UniEngineEx TUniEngine, ConstType TConstType, aConstName string) string {

	switch ConstType {
	case CtPK:
		return fmt.Sprintf("select count(*) from user_constraints where constraint_name=upper('%s') and constraint_type='P'", aConstName)
	case CtFK:
		return fmt.Sprintf("select count(*) from user_constraints where constraint_name=upper('%s') and constraint_type='R'", aConstName)
	case CtUK:
		return fmt.Sprintf("select count(*) from user_constraints where constraint_name=upper('%s') and constraint_type='U'", aConstName)
	case CtDF:
		return fmt.Sprintf("select count(*) from user_tab_cols where column_name=upper('%s') and data_default is not null", aConstName)
	}

	return ""
}

type TExistConst4MYSQLN struct{}

// #MySQL:命名约束在 information_schema.table_constraints;默认值在 information_schema.columns,按列名查
func (self TExistConst4MYSQLN) GetSqlExistConst(UniEngineEx TUniEngine, ConstType TConstType, aConstName string, DataBase string) string {

	switch ConstType {
	case CtPK:
		return fmt.Sprintf("select count(*) from information_schema.table_constraints where constraint_name='%s' and constraint_type='PRIMARY KEY' and table_schema='%s'", aConstName, DataBase)
	case CtFK:
		return fmt.Sprintf("select count(*) from information_schema.table_constraints where constraint_name='%s' and constraint_type='FOREIGN KEY' and table_schema='%s'", aConstName, DataBase)
	case CtUK:
		return fmt.Sprintf("select count(*) from information_schema.table_constraints where constraint_name='%s' and constraint_type='UNIQUE' and table_schema='%s'", aConstName, DataBase)
	case CtDF:
		return fmt.Sprintf("select count(*) from information_schema.columns where column_name='%s' and table_schema='%s' and column_default is not null", aConstName, DataBase)
	}

	return ""
}

// #for tuniengine get primary keys
type HasGetSqlAutoKeys interface {
	GetSqlAutoKeys(TUniEngine, string) (string, error)
}

type TAutoKeys4POSTGR struct{}

func (self TAutoKeys4POSTGR) GetSqlAutoKeys(UniEngineEx TUniEngine, TableName string) (string, error) {

	result := "select attname as field_name from pg_attribute" +
		"    left join pg_class on pg_attribute.attrelid=pg_class.oid" +
		"    where pg_class.relname='%s' and attnum>0" + // and attstattarget=-1
		"    and exists (select * from pg_constraint where pg_constraint.conrelid=pg_class.oid and pg_constraint.contype='p' and attnum=any(conkey))"

	return fmt.Sprintf(result, strings.ToLower(TableName)), nil
}

type TAutoKeys4SQLSRV struct{}

func (self TAutoKeys4SQLSRV) GetSqlAutoKeys(UniEngineEx TUniEngine, TableName string) (string, error) {

	result := "select c.name as field_name" +
		"    from sys.indexes i" +
		"    inner join sys.index_columns ic on ic.object_id = i.object_id and ic.index_id = i.index_id" +
		"    inner join sys.columns c on c.object_id = ic.object_id and c.column_id = ic.column_id" +
		"    where i.object_id = object_id('%s') and i.is_primary_key = 1" +
		"    order by ic.index_column_id"

	return fmt.Sprintf(result, TableName), nil
}

type TAutoKeys4ORACLE struct{}

func (self TAutoKeys4ORACLE) GetSqlAutoKeys(UniEngineEx TUniEngine, TableName string) (string, error) {

	result := "select cu.column_name as field_name from user_cons_columns cu, user_constraints au where cu.constraint_name=au.constraint_name and au.constraint_type=upper('p') and au.table_name =upper('%s')"

	return fmt.Sprintf(result, TableName), nil
}

type TAutoKeys4MYSQLN struct {
	DataBase string
}

func (self TAutoKeys4MYSQLN) GetSqlAutoKeys(UniEngineEx TUniEngine, TableName string) (string, error) {

	if self.DataBase == "" {
		return "", fmt.Errorf("UniEngine: you should specify attribute [database] when using mysql.")
	}
	result := "select column_name as field_name from information_schema.columns where 1=1 and table_schema='%s' and table_name='%s' and column_key='PRI'"

	return fmt.Sprintf(result, self.DataBase, TableName), nil
}
