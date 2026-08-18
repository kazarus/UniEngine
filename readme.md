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

##### 0.应用加密(敏感字段)

UniEngine 提供应用层加密 hook,敏感字段(密码/证件号/手机号等)写入时自动加密,读取时自动解密。

```go
//#1.注册类时,给敏感字段打上 encrypt 标记
UniEngineEx.RegisterClass(mock.TUSER{}, "mock_user")
//# 或使用结构体 tag:db:"password,encrypt"

//#2.开启加密,并设置密钥
UniEngineEx.SecretOn = 1
UniEngineEx.SecretBy = "your-secret-key"

//#3.可选:自定义加密钩子(默认内置 AES-256-GCM)
UniEngineEx.SecretHook = func(Value string, Encrypt bool) (string, error) {
    if Encrypt {
        return myEncrypt(Value) //#业务密钥/国密/加密机
    }
    return myDecrypt(Value)
}
```

- 写入:Insert / Update / InsertL / SpecialInsertL 对标记字段自动加密
- 读取:Select / SelectL / SelectM / SelectH 对标记字段自动解密
- SecretOn=0(默认)时一切直通,不改变原有行为
- 手工注册的字段可用 `.SetSecret("password")` 标记加密
- 主键字段请勿标记 encrypt(密文含随机nonce,无法用于匹配)
- 密文格式:base64( 随机nonce + AES-256-GCM密文 )

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
