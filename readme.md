#### 0.项目简介

UniEngine 是一个 Go 编写的多数据库 ORM 框架，通过 struct tag 声明字段与数据库列的映射，运行时自动生成参数化 SQL，支持 PostgreSQL / SQL Server / Oracle / MySQL（并预留金仓、达梦、高斯、PolarDB、华为 Taurus）。

##### 0.0.驱动安装（Oracle 客户端）

```bash
cp /Users/kazarus/ORACLE/instantclient_11_2/{libclntsh.dylib.11.1,libnnz11.dylib,libociei.dylib}   /usr/local/lib
```

##### 0.1.驱动标识

| 驱动标识           | 数据库     | 连接符 | 字段连接符 |
| ------------------ | ---------- | ------ | ---------- |
| UniEngine.DtPOSTGR | PostgreSQL | $      | $1         |
| UniEngine.DtSQLSRV | SQLServer  | $      | $1         |
| UniEngine.DtMYSQLN | MySQL      | ?      | ?          |
| UniEngine.DtORACLE | Oracle     | :      | :1         |
| UniEngine.DtDAMENG | 达梦       | :      | :1         |
| UniEngine.DtPOLODB | PolarDB    | $      | $1         |

##### 0.2.并发约定

`TUniEngine` 实例**非并发安全**：`tx`/`st`/`HashTabl` 为共享可变状态。多 goroutine 使用时应各自创建实例（底层 `*sql.DB` 连接池仍复用连接）。注册（`RegisterClass`/`SetKeys` 等）完成后再进入只读使用期，不要在查询期间并发修改注册表。

#### 1.安装方式

```bash
go get github.com/kazarus/UniEngine
```

#### 2.使用方法

##### 2.1.MySQL

```go
import _ "github.com/go-sql-driver/mysql"

DbSource := fmt.Sprintf("%s:%s@tcp(%s:3306)/%s", "<user>", "<pswd>", "<server>", "<database>")

db, err := sql.Open("mysql", DbSource)
if err != nil {
  fmt.Println(err.Error())
}

db.SetConnMaxLifetime(time.Minute * 3)
db.SetMaxOpenConns(100)
db.SetMaxIdleConns(10)

//#初始化（注册 TUniField，供 AutoKeys 内部查询使用）
UniEngineEx := UniEngine.TUniEngine{Db: db, ColLabel: "db", ColParam: "?", Provider: UniEngine.DtMYSQLN}
UniEngineEx.Initialize()

//#根据数据库元数据,主动获取主键
var AutoKeys = UniEngine.TAutoKeys4MYSQLN{}
AutoKeys.DataBase = "kz2020_gcgl_demo"

//#注册数据库操作类（RegisterClass 返回的 *TUniTable 与引擎内注册表共享同一对象，
//# 通过它 SetKeys/AutoKeys 设置的主键对后续 SaveIt/Delete 等生效）
UniEngineEx.RegisterClass(mock.TMAIN{}, "mock_main").AutoKeys(UniEngineEx, AutoKeys)
UniEngineEx.RegisterClass(mock.TDATA{}, "mock_data").AutoKeys(UniEngineEx, AutoKeys)
```

##### 2.2.PostgreSQL

```go
import _ "github.com/lib/pq"

db, err := sql.Open("postgres", "postgres://user:pswd@server:5432/dbname?sslmode=disable")
if err != nil {
  fmt.Println(err.Error())
}

UniEngineEx := UniEngine.TUniEngine{Db: db, ColLabel: "db", ColParam: "$", Provider: UniEngine.DtPOSTGR}
UniEngineEx.Initialize()

// PostgreSQL 下 AutoKeys 无需额外配置（默认查 pg_attribute/pg_constraint）
UniEngineEx.RegisterClass(mock.TMAIN{}, "mock_main").AutoKeys(UniEngineEx)
```

##### 2.3.Oracle

```go
import _ "github.com/godror/godror"

// DSN 示例：oracle://user:pswd@server:1521/service
db, err := sql.Open("godror", dsn)

UniEngineEx := UniEngine.TUniEngine{Db: db, ColLabel: "db", ColParam: ":", Provider: UniEngine.DtORACLE}
UniEngineEx.Initialize()
```

##### 2.4.字段标签

- `db:"column_name"`：映射列名
- `db:"column_name,readonly"`：只读列，插入/更新时跳过（如自增主键、数据库默认值）

##### 2.5.事务

```go
UniEngineEx.Begin()            // 开启事务
defer func() {
  if r := recover(); r != nil {
    UniEngineEx.Cancel()       // 回滚
  }
}()
// ... 在此执行 Insert / Update / Delete / Execute ...
UniEngineEx.Commit()           // 提交
```

注意：事务期间同一引擎实例不应被多个 goroutine 同时使用。

##### 2.6.批量插入

```go
// 普通批量（分页，默认每页 999 条）
UniEngineEx.InsertP(list, 500)

// PostgreSQL/PolarDB 专用 COPY 协议批量（走 pq.CopyIn，需注册对应字段）
UniEngineEx.SpecialInsertP(list, 500)
```

##### 2.7.错误处理约定

- 所有公开方法返回 `error`，不再使用 `panic` 表达常规错误；
- `ExistConst` 暂未实现，调用返回明确错误；
- 传入的表名/字段名仅允许字母、数字、下划线与点（防 SQL 注入），非法输入返回错误。
