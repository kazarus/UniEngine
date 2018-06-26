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

type HasGetSqlExistTable interface {
	GetSqlExistTable() string
}

type HasGetSqlExistField interface {
	GetSqlExistField() string
}

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
		"    left join pg_class on  pg_attribute.attrelid = pg_class.oid " +
		"    where pg_class.relname = '%s'  and attstattarget=-1 " +
		"    and exists (select * from pg_constraint where  pg_constraint.conrelid = pg_class.oid  and pg_constraint.contype='p' and attnum=any(conkey))"
	return fmt.Sprintf(result, strings.ToLower(TableName))
}

type TAutoKeys4SQLSRV struct{}

func (self TAutoKeys4SQLSRV) GetSqlAutoKeys(TableName string) string {
	result := "select a.name as field_name from syscolumns a  inner join sysindexkeys b on a.id=b.id  and a.colid =b.colid where a.id = object_id('%s')"
	return fmt.Sprintf(result, TableName)
}

type TAutoKeys4ORACLE struct{}

func (self TAutoKeys4ORACLE) GetSqlAutoKeys(TableName string) string {
	result := "wait to do %s"
	return fmt.Sprintf(result, TableName)
}
