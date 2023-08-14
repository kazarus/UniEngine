#### 0.项目简介

##### 0.0.驱动安装

```json
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

//#注册数据库操作类
UniEngineEx.RegisterClass(mock.TMAIN{}, "mock_main").AutoKeys(UniEngineEx, AutoKeys)
UniEngineEx.RegisterClass(mock.TDATA{}, "mock_data").AutoKeys(UniEngineEx, AutoKeys)
```
