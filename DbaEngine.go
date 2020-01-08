// DbaEngine
package UniEngine

import "fmt"
import "strings"
import "reflect"

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

type HasGetSqlUpdate interface {
	GetSqlUpdate() string
}

type HasGetSqlInsert interface {
	GetSqlInsert() string
}

type HasGetSqlDelete interface {
	GetSqlDelete() string
}

type HasSetSqlValues interface {
	SetSqlValues(TQueryType, *[]interface{})
}

type HasSetSqlResult interface {
	SetSqlResult(interface{}, []string, []interface{})
}

//#for tuniengine get exist table
type HasGetSqlExistTable interface {
	GetSqlExistTable(string) string
}

type TExistTable4POSTGR struct{}

func (self TExistTable4POSTGR) GetSqlExistTable(TableName string) string {

	result := "select count(relname) as value from pg_class where relname = '%s'"

	return fmt.Sprintf(result, strings.ToLower(TableName))
}

//#for tuniengine get exist field
type HasGetSqlExistField interface {
	GetSqlExistField(string, string) string
}

type TExistField4POSTGR struct{}

func (self TExistField4POSTGR) GetSqlExistField(TableName string, FieldName string) string {

	result := "select count(a.attname) as value from pg_attribute a" +
		"    left join pg_class b on a.attrelid=b.oid where b.relname='%s' and a.attname='%s' and attnum>0"

	return fmt.Sprintf(result, strings.ToLower(TableName), strings.ToLower(FieldName))
}

//#for tuniengine get exist const
type HasGetSqlExistConst interface {
	GetSqlExistConst() string
}

//#for tuniengine get primary keys
type HasGetSqlAutoKeys interface {
	GetSqlAutoKeys(string) string
}

type TAutoKeys4POSTGR struct{}

func (self TAutoKeys4POSTGR) GetSqlAutoKeys(TableName string) string {

	result := "select attname as field_name from pg_attribute" +
		"    left join pg_class on  pg_attribute.attrelid = pg_class.oid" +
		"    where pg_class.relname = '%s'  and attstattarget=-1 and attnum>0" +
		"    and exists (select * from pg_constraint where  pg_constraint.conrelid = pg_class.oid  and pg_constraint.contype='p' and attnum=any(conkey))"

	return fmt.Sprintf(result, strings.ToLower(TableName))
}

type TAutoKeys4SQLSRV struct{}

func (self TAutoKeys4SQLSRV) GetSqlAutoKeys(TableName string) string {

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

func (self TAutoKeys4ORACLE) GetSqlAutoKeys(TableName string) string {

	result := "select cu.column_name as field_name from user_cons_columns cu, user_constraints au where cu.constraint_name = au.constraint_name and au.constraint_type = upper('p') and au.table_name =upper('%s')"

	return fmt.Sprintf(result, TableName)
}
