#### 0.项目简介

UniEngine 是一个基于 `database/sql` 的多数据库 ORM-like 引擎,支持 PostgreSQL / SQLServer / Oracle / MySQL,以及金仓 / 达梦 / openGauss / PolarDB / Taurus 等。

> 注意:`TUniEngine` **查询/写入路径并发安全**——预备语句为局部变量,查询仅持读锁做注册表/事务查找,`Register*` 可与查询并发执行。
> 两个例外:**事务期间**(Begin 与 Commit/Cancel 之间)调用须串行(底层 `*sql.Tx` 非并发安全);配置字段(ColLabel/ColParam/Provider/SecretOn 等)与表结构变更(SetKeys/SetSecret/AutoKeys/PrepareTables/PrepareRunSQL)应在并发查询开始前完成。
> 本模块**零第三方依赖**(COPY 语句由包内自实现,不依赖 lib/pq)。

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

//#注册数据库操作类(AutoKeys 接收 *TUniEngine,返回 error,需处理)
if eror := UniEngineEx.RegisterClass(mock.TMAIN{}, "mock_main").AutoKeys(&UniEngineEx, AutoKeys); eror != nil {
  panic(eror)
}
if eror := UniEngineEx.RegisterClass(mock.TDATA{}, "mock_data").AutoKeys(&UniEngineEx, AutoKeys); eror != nil {
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

##### 3.应用加密(敏感字段)

UniEngine 提供应用层加密 hook,敏感字段(密码/证件号/手机号等)写入时自动加密,读取时自动解密。

```go
//#1.注册类时,给敏感字段打上 encrypt 标记(结构体 tag)
UniEngineEx.RegisterClass(mock.TUSER{}, "mock_user")
//# 字段声明示例:Password string `db:"password,encrypt"`

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

- 写入:Insert / Update / InsertL / CopyInL 对标记字段自动加密
- 读取:Select / SelectL / SelectM / SelectH 对标记字段自动解密
- SecretOn=0(默认)时一切直通,不改变原有行为
- 手工注册的字段可用 `.SetSecret("password")` 标记加密
- 主键字段请勿标记 encrypt(密文含随机nonce,无法用于匹配)
- **encrypt 仅支持 string 类型字段**:非 string 字段写入时直接报错(fast-fail),避免"写入密文/读取不解密"的不对称
- **SecretBy 请使用高熵随机密钥**(如 32 字节随机串):内置实现以 SHA-256(SecretBy) 派生 AES 密钥,无盐无迭代,人类口令可被离线暴力破解
- 密文格式:`ENC:` + base64( 随机nonce + AES-256-GCM密文 )

##### 3.1.存量数据鉴别与迁移

开启加密后,库中会存在两类数据:加密前的存量明文 + 加密后的新密文。
密文统一带明文前缀标签 `ENC:`,读取时自动鉴别:

- 带 `ENC:` 标签 → 密文,解密后返回
- 无标签 → 存量明文,直通返回(不报错、不解密)

```go
//# 迁移脚本:逐行读出存量明文,重新保存后即落为加密密文
UniEngineEx.IsEncrypted(value) //# true=密文 false=存量明文

//# 示例:读取全量用户,让 SaveIt/Update 自动把存量明文加密
var users []mock.TUSER
UniEngineEx.SelectL(&users, "select * from mock_user")
for i := range users {
    UniEngineEx.SaveIt(&users[i], "mock_user") //# 或 Update
}
```

注意:存量明文若恰好以 `ENC:` 开头会被误判为密文(真实敏感数据概率极低);
带标签但密钥错误的密文会解密报错(不静默,便于发现密钥轮换问题)。

##### 4.从旧版本迁移(SpecialInsert → CopyIn)

`SpecialInsert*` 系列方法更名为 `CopyIn*`(语义即 PostgreSQL 的 COPY IN 协议):

| 旧名称              | 新名称    |
| ------------------- | --------- |
| SpecialInsertL      | CopyInL   |
| SpecialInsertLCtx   | CopyInLCtx|
| SpecialInsertP      | CopyInP   |
| SpecialInsertPCtx   | CopyInPCtx|
| HasSpecialGetSqlInsertL | HasCopyInGetSqlInsertL |
| HasSpecialSetSqlValuesL | HasCopyInSetSqlValuesL |

- 旧方法名保留为**废弃包装**,直接委托到新实现,源码兼容无需改动;
- 旧接口仍被引擎探测:已实现 `SpecialGetSqlInsertL` / `SpecialSetSqlValuesL` 的类**无需改动即可继续工作**,建议尽快迁移到新接口。

同版本其他行为变化:

- **SaveIt / SaveItWhenNotExist 默认走方言原生 UPSERT**(PG `on conflict`、Oracle/SQLServer `merge`),消除 count 与写入两步之间的并发窗口;实现自定义 SQL 钩子的类与 MySQL(唯一键语义不等价)自动回退旧的 count-then-dispatch;
- **查询补 `rows.Err()` 检查**:读取中途出错不再被静默吞掉(此前表现为"正常返回但数据截断");
- **SQL 生成确定性**:CRUD 列序按类声明序(手工注册字段按字段名排序补齐),同一输入不再产生不同 SQL 文本,利于数据库端语句缓存;
- **表名/字段名白名单校验**:所有拼入 SQL 的表名/字段名仅允许字母/数字/下划线/点,非法字符直接报错(防注入);
- **ExistViews 修正**:PostgreSQL(补 `relkind='v'`)/ SQLServer(补 `xtype='V'`)/ MySQL(改查 `information_schema.views`)不再把同名普通表误判为视图;
- **SQLServer 主键探测**改用 `sys.indexes` 目录视图(替代老旧的 syscolumns/sysindexes 联查);
- **RegisterClass 双 key 注册**:类同时以"小写表名"和"类全名"注册(统一 GetTable 与 SaveIt 路径的可见性)。注意:两个类注册同一表名时,小写表名 key 以后注册者为准;
- Oracle 下 `$` 替换收窄到参数占位符(`$1`),不再误伤 SQL 文本中其它 `$` 字符;
- **CopyInL 保持表名原大小写**,不再整句转小写;全部字段只读时写入方法返回明确错误而非 panic;
- **(第二阶段)引擎并发安全**:预备语句移出结构体,查询/注册可并发;事务期间仍须串行;
- **(第二阶段)写方法表名参数类型安全化**:`args ...interface{}` → `TableName ...string`,传非字符串由运行期报错变为编译期报错,已有调用点语法不变;
- **(第二阶段)钩子接口引擎参数改 `*TUniEngine`**(消除逐行拷贝);`AutoKeys` 同步改指针;实现方需同步修改签名;
- **(第二阶段)`Select` 多行时报错**(旧版静默保留最后一行),多行请用 `SelectL`;
- **(第二阶段)移除 `github.com/lib/pq` 依赖**,COPY 语句由包内 `copyInStmt` 自实现,输出逐字一致;
- **(第二阶段)`CopyInL` 仅 PG 协议族可用**(DtPOSTGR/DtKINGES/DtOPENGS/DtPOLODB),其余方言直接报错。
