module github.com/kazarus/UniEngine/integration

go 1.25.0

replace github.com/kazarus/UniEngine => ../

replace github.com/kazarus/UniEngine/contrib/pgxcopy => ../contrib/pgxcopy

require (
	gitee.com/chunanyong/dm v1.8.23
	github.com/go-sql-driver/mysql v1.10.1
	github.com/jackc/pgx/v5 v5.11.0
	github.com/kazarus/UniEngine v0.0.0-00010101000000-000000000000
	github.com/kazarus/UniEngine/contrib/pgxcopy v0.0.0-00010101000000-000000000000
	github.com/microsoft/go-mssqldb v1.11.2
	github.com/sijms/go-ora/v2 v2.9.0
)

require (
	github.com/VictoriaMetrics/easyproto v0.1.4 // indirect
	github.com/go-logfmt/logfmt v0.6.0 // indirect
	github.com/godror/knownpb v0.3.0 // indirect
	github.com/golang/snappy v0.0.4 // indirect
	golang.org/x/exp v0.0.0-20250506013437-ce4c2cf36ca6 // indirect
	google.golang.org/protobuf v1.36.6 // indirect
)

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/godror/godror v0.51.4
	github.com/golang-sql/civil v0.0.0-20220223132316-b832511892a9 // indirect
	github.com/golang-sql/sqlexp v0.1.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/text v0.41.0 // indirect
)
