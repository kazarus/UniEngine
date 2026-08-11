#### 0.项目简介

UniEngine 是一个基于 `database/sql` 的多数据库 ORM-like 引擎,支持 PostgreSQL / SQLServer / Oracle / MySQL,以及金仓 / 达梦 / openGauss / PolarDB / Taurus 等。

> 注意:`TUniEngine` 持有预备语句、事务等可变状态,**非并发安全**。请每 goroutine 独立实例,或串行调用。底层 `*sql.DB` 本身并发安全。

##### 0.0.驱动安装

```sh
cp /Users/kazarus/ORACLE/instantclient_11_2/{libclntsh.dylib.11.1,libnnz11.dylib,libociei.dylib}   /usr/local/lib
```

##### 0.1.驱动标识

| 驱动标识           | 数据库     | 连接符 | 字段连接符 |
| ------------------ | ---------- | ------ | ---------- |
| UniEngine.DtPOSTGR | PostgreSQL | $      | $1         |
| UniEngine.DtSQLSRV | SQLServer  | $      | $1         |
| UniEngine.DtMYSQLN | MySQL      | ?      | ?          |
| UniEngine.DtORACLE | Oracle     | :      | :1         |

#### 1.安装方式

```sh
go get github.com/kazarus/UniEngine
```

#### 2.使用方法

##### 1.mysql 下使用

```go
import _ "github.com/go-sql-driver/mysql"

DbSource := fmt.Sprintf("%s:%s@tcp(%s:3306)/%s", "<user>", "<pswd>", "<server>", "<database>")

db, eror := sql.Open("mysql", DbSource)
if eror != nil {
  fmt.Println(eror.Error())
}

db.SetConnMaxLifetime(time.Minute * 3)
db.SetMaxOpenConns(100)
db.SetMaxIdleConns(10)

//#初始化
UniEngineEx := UniEngine.TUniEngine{Db: db, ColLabel: "db", ColParam: "?", Provider: UniEngine.DtMYSQLN}
UniEngineEx.Initialize()

//#根据数据库元数据,主动获取主键
var AutoKeys = UniEngine.TAutoKeys4MYSQLN{}
AutoKeys.DataBase = "kz2020_gcgl_demo"

//#注册数据库操作类(AutoKeys 返回 error,需处理)
if eror := UniEngineEx.RegisterClass(mock.TMAIN{}, "mock_main").AutoKeys(UniEngineEx, AutoKeys); eror != nil {
  panic(eror)
}
if eror := UniEngineEx.RegisterClass(mock.TDATA{}, "mock_data").AutoKeys(UniEngineEx, AutoKeys); eror != nil {
  panic(eror)
}
```

##### 2.context.Context 支持

每个查询/写入方法都有对应的 `*Ctx` 变体(如 `SelectCtx` / `InsertCtx` / `ExecuteCtx` / `BeginCtx`),接受 `context.Context` 作为首参,可用于超时/取消。原方法(`Select` / `Insert` / ...)等价于以 `context.Background()` 调用对应 `*Ctx` 方法。

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

eror = UniEngineEx.InsertCtx(ctx, &row)
```
