package UniEngine

import "fmt"
import "strings"
import "reflect"

/*
func (self TDATA) GetSqlInsert(UniEngineEx UniEngine.TUniEngine, aTableName string) string {
}

func (self TDATA) GetSqlInsertL(UniEngineEx UniEngine.TUniEngine, aTableName string, aDataSize int64) string {
}

func (self TDATA) GetSqlUpdate(UniEngineEx UniEngine.TUniEngine, aTableName string) string {
}

func (self TDATA) GetSqlDelete(UniEngineEx UniEngine.TUniEngine, aTableName string) string {
}

func (self TDATA) SetSqlValues(UniEngineEx UniEngine.TUniEngine, e UniEngine.TQueryType, i *[]interface{}) {
}

func (self TDATA) SetSqlResult(UniEngineEx UniEngine.TUniEngine, result interface{}, column []string, fields []interface{}) {
}
*/

const CONST_PRIVIDER_NAME_TO_POSTGR = "PostgreSQL"
const CONST_PRIVIDER_NAME_TO_SQLSRV = "SQL Server"
const CONST_PRIVIDER_NAME_TO_ORACLE = "Oracle"
const CONST_PRIVIDER_NAME_TO_ACCESS = "Access"
const CONST_PRIVIDER_NAME_TO_SQLITE = ""
const CONST_PRIVIDER_NAME_TO_MYSQLN = "MySQL"
const CONST_PRIVIDER_NAME_TO_KINGES = ""

type TQueryType int

const (
	EtSelect TQueryType = 1 + iota
	EtInsert
	EtUpdate
	EtDelele
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
	DtKINGES //#人大金仓
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

// #单笔删除SQL
type HasGetSqlDelete interface {
	GetSqlDelete(TUniEngine, string) string
}

// #单笔取值
type HasSetSqlValues interface {
	SetSqlValues(TUniEngine, TQueryType, *[]interface{})
}

// #批量取值L
type HasSetSqlValuesL interface {
	SetSqlValuesL(TUniEngine, TQueryType, reflect.Value, *[]interface{})
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

	result := "select count(relname) as value from pg_class where relname = '%s'"

	return fmt.Sprintf(result, strings.ToLower(TableName))
}

func (self TExistTable4POSTGR) GetSqlExistViews(UniEngineEx TUniEngine, TableName string) string {

	result := "select count(relname) as value from pg_class where relname = '%s'"

	return fmt.Sprintf(result, strings.ToLower(TableName))
}

type TExistTable4SQLSRV struct{}

func (self TExistTable4SQLSRV) GetSqlExistTable(UniEngineEx TUniEngine, TableName string) string {

	result := "select count(*) from sysobjects where 1=1 and name = '%s'"

	return fmt.Sprintf(result, strings.ToLower(TableName))
}

func (self TExistTable4SQLSRV) GetSqlExistViews(UniEngineEx TUniEngine, TableName string) string {

	result := "select count(*) from sysobjects where 1=1 and name = '%s'"

	return fmt.Sprintf(result, strings.ToLower(TableName))
}

type TExistTable4ORACLE struct{}

func (self TExistTable4ORACLE) GetSqlExistTable(UniEngineEx TUniEngine, TableName string) string {

	result := "select count(*) from all_tables where table_name = upper('%s')"

	return fmt.Sprintf(result, TableName)
}

func (self TExistTable4ORACLE) GetSqlExistViews(UniEngineEx TUniEngine, TableName string) string {

	result := "select count(*) from user_views where view_name = upper('%s')"

	return fmt.Sprintf(result, TableName)
}

type TExistTable4MYSQLN struct{}

func (self TExistTable4MYSQLN) GetSqlExistTable(UniEngineEx TUniEngine, TableName string, DataBase string) string {

	result := "select count(*) from information_schema.tables t where table_name  = '%s' and table_schema = '%s'"

	return fmt.Sprintf(result, TableName, DataBase)
}

func (self TExistTable4MYSQLN) GetSqlExistViews(UniEngineEx TUniEngine, TableName string, DataBase string) string {

	result := "select count(*) from information_schema.tables t where table_name  = '%s' and table_schema = '%s'"

	return fmt.Sprintf(result, TableName, DataBase)
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

	result := "select count(*) as value from syscolumns where 1=1 and id=object_id('%s') and  name = '%s'"

	return fmt.Sprintf(result, strings.ToLower(TableName), strings.ToLower(FieldName))
}

type TExistField4ORACLE struct{}

func (self TExistField4ORACLE) GetSqlExistField(UniEngineEx TUniEngine, TableName string, FieldName string) string {

	result := "select count(*) as value from user_tab_columns where table_name=upper('%s') and column_name=upper('%s')"

	return fmt.Sprintf(result, strings.ToUpper(TableName), strings.ToUpper(FieldName))
}

type TExistField4MYSQLN struct{}

func (self TExistField4MYSQLN) GetSqlExistField(UniEngineEx TUniEngine, TableName string, FieldName string, DataBase string) string {

	result := "select count(*) as value from information_schema.columns  where and table_schema = %s and table_name  = %s  and column_name =%s"

	return fmt.Sprintf(result, DataBase, TableName, FieldName)
}

// #for tuniengine get exist const
type HasGetSqlExistConst interface {
	GetSqlExistConst() string
}

// #for tuniengine get primary keys
type HasGetSqlAutoKeys interface {
	GetSqlAutoKeys(TUniEngine, string) string
}

type TAutoKeys4POSTGR struct{}

func (self TAutoKeys4POSTGR) GetSqlAutoKeys(UniEngineEx TUniEngine, TableName string) string {

	result := "select attname as field_name from pg_attribute" +
		"    left join pg_class on  pg_attribute.attrelid = pg_class.oid" +
		"    where pg_class.relname = '%s'  and attstattarget=-1 and attnum>0" +
		"    and exists (select * from pg_constraint where  pg_constraint.conrelid = pg_class.oid  and pg_constraint.contype='p' and attnum=any(conkey))"

	return fmt.Sprintf(result, strings.ToLower(TableName))
}

type TAutoKeys4SQLSRV struct{}

func (self TAutoKeys4SQLSRV) GetSqlAutoKeys(UniEngineEx TUniEngine, TableName string) string {

	//@result := "select a.name as field_name from syscolumns a  inner join sysindexkeys b on a.id=b.id  and a.colid =b.colid where a.id = object_id('%s')"

	result := "select syscolumns.name as field_name" +
		"    from syscolumns,sysobjects,sysindexes,sysindexkeys" +
		"    where syscolumns.id = object_id('%s')" +
		"    and sysobjects.xtype = 'pk'" +
		"    and sysobjects.parent_obj = syscolumns.id" +
		"    and sysindexes.id = syscolumns.id" +
		"    and sysobjects.name = sysindexes.name" +
		"    and sysindexkeys.id = syscolumns.id" +
		"    and sysindexkeys.indid = sysindexes.indid" +
		"    and syscolumns.colid = sysindexkeys.colid;"

	return fmt.Sprintf(result, TableName)
}

type TAutoKeys4ORACLE struct{}

func (self TAutoKeys4ORACLE) GetSqlAutoKeys(UniEngineEx TUniEngine, TableName string) string {

	result := "select cu.column_name as field_name from user_cons_columns cu, user_constraints au where cu.constraint_name = au.constraint_name and au.constraint_type = upper('p') and au.table_name =upper('%s')"

	return fmt.Sprintf(result, TableName)
}

type TAutoKeys4MYSQLN struct {
	DataBase string
}

func (self TAutoKeys4MYSQLN) GetSqlAutoKeys(UniEngineEx TUniEngine, TableName string) string {

	if self.DataBase == "" {
		panic("UniEngine: you shoule be specify attribute [database] when using mysql.")
	}
	result := "select column_name as field_name from information_schema.columns where 1=1 and table_schema = '%s' and table_name = '%s' and column_key = 'PRI'"

	return fmt.Sprintf(result, self.DataBase, TableName)
}
