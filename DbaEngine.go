// DbaEngine
package UniEngine

import "reflect"

type TExeccuteType int

const (
	EtSelect TExeccuteType = 1 + iota
	EtInsert
	EtUpdate
	EtDelele
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
	SetSqlValues(TExeccuteType, *[]interface{})
}

type HasSetSqlResult interface {
	SetSqlResult(interface{}, []string, []interface{})
}
