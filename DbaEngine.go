// DbaEngine
package UniEngine

type HasGetMapUnique interface {
	GetMapUnique() string
}

type GetMapUnique func(u interface{}) string

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

type HasGetSqlValues interface {
	GetSqlValues(TExeccuteType) []interface{}
}

type HasSetSqlValues interface {
	SetSqlValues(TExeccuteType, *[]interface{})
}

type HasSetSqlResult interface {
	//#SetSqlResult(reflect.Value, []interface{}, []string)
	SetSqlResult(interface{}, []interface{}, []string)
}

type HasGetSqlResult interface {
	GetSqlResult([]interface{}, []string) interface{}
}
