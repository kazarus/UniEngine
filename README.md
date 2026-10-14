# UniEngine

> **English summary** — UniEngine is a **zero-dependency**, ORM-like engine built
> on Go's `database/sql`. It maps struct tags to columns and drives SQL
> generation from a registry, targeting **PostgreSQL / SQL Server / Oracle /
> MySQL** plus compatible forks (KingbaseES, DM, openGauss, PolarDB, Taurus).
>
> - **Dialects by protocol family** (`TDbFamily`), not per driver: placeholders,
>   identifier quoting, metadata catalogues, native UPSERT (`ON CONFLICT` /
>   `MERGE`), `INSERT ALL` and `COPY` all branch on the family.
> - **Context everywhere**: every query/write method has a `...Ctx` variant.
> - **Application-layer encryption**: `secreton` columns are encrypted on write
>   and decrypted on read (PBKDF2-HMAC-SHA256 → AES-256-GCM, self-contained
>   ciphertext); `SecretHook` lets you plug in your own KMS or SM-crypto.
>   Optional **storage-integrity verification** (`SetVerify`) signs protected
>   columns into an HMAC companion column and verifies them per row on read.
> - **Concurrency**: query/write paths are safe for concurrent use; calls must be
>   serialized only while a transaction is open. The engine must always be used
>   as `*TUniEngine` — by-value copies are rejected by `go vet`.
> - **SQL Server binding check**: the placeholder must match the *driver name*.
>   `Initialize()` probes the live connection and returns
>   `ErrInvalidPlaceholderBinding` for a mismatch before registering tables.
>   Check its error; see §0.2 for the driver matrix.
> - **Nullable columns**: the driver scans straight into your struct fields, so a
>   `NULL` in a column mapped to a non-nullable type (`string`/`int64`/`bool`/`time.Time`)
>   fails the read — and for `SelectL` it discards the **whole** batch. Use
>   `sql.NullString` / `sql.NullInt64` / pointers, or `[]byte`. The scalar helpers
>   (`SelectS`/`SelectD`/`SelectF`) are the opposite: they scan into `sql.NullXxx`
>   and return a zero value on `NULL`. See §3 (Chinese) for the measured matrix.
> - **Tests**: 170+ unit tests (~88% statement coverage, mock driver) plus
>   real-database suites for PostgreSQL, SQL Server, Oracle and MySQL under
>   `integration/` (separate module, skipped when no server is reachable).
>
> Contributing guide: **[CONTRIBUTING.md](CONTRIBUTING.md)** (English, with a
> Chinese summary at the end). The rest of this README is in Chinese.

#### 0.项目简介

UniEngine 是一个基于 `database/sql` 的多数据库 ORM-like 引擎,支持 PostgreSQL / SQLServer / Oracle / MySQL,以及金仓 / 达梦 / openGauss / PolarDB / Taurus 等。

> **编码风格**:库代码标识符采用 **PascalCase**(每个单词首字母大写,如 `SqlQuery`/`Args`/`ColIndex`/`LowerTxt`),局部变量与参数同此风格,接收者固定为 `this`;方法名后缀 `...Ctx` 表示 context.Context 变体。测试文件(`*_test.go`/`integration`/`contrib`)沿用 Go 社区 camelCase。

> 注意:`TUniEngine` **查询/写入路径并发安全**——预备语句为局部变量,查询仅持读锁做注册表/事务查找,`Register*` 可与查询并发执行。
> 两个例外:**事务期间**(Begin 与 Commit/Cancel 之间)调用须串行(底层 `*sql.Tx` 非并发安全);配置字段(ColLabel/ColParam/Provider/SecretOn 等)与表结构变更(SetKeys/SetSecret/AutoKeys/PrepareTables/PrepareRunSQL)应在并发查询开始前完成。
> 同表 `RegisterTable/RegisterField/RegisterPkeys` 会发布新的元数据快照;已经开始的调用使用原快照,后续调用读取新快照。不要在并发使用期间直接修改已返回的 `*TUniTable` 或其 map/slice。
> 另:**引擎不得按值拷贝**——请一律使用 `*TUniEngine`。拷贝会复制 `tx`/`inTx` 等值字段却共享 `mu`,副本 Begin 之后原件仍认为无事务,原件上的写入会绕过该事务直接落到连接池。该约束已由 `noCopy` + `go vet`(copylocks 分析器)在静态检查阶段强制,CI 有门禁。
> 本模块**零第三方依赖**(COPY 语句由包内自实现,不依赖 lib/pq)。

##### 0.0.驱动安装

Oracle 系需要 Oracle Instant Client:将 `libclntsh`/`libnnz`/`libociei` 等动态库放入系统库路径(如 `/usr/local/lib`)。

##### 0.1.驱动标识与协议族

引擎行为(占位符/标识符引用/元数据目录/UPSERT/INSERT ALL/COPY)按**协议族**判断,新增兼容数据库按族归入:

| 协议族   | 驱动标识                                                  | 连接符 | 占位符 | 标识符引用 |
| -------- | --------------------------------------------------------- | ------ | ------ | ---------- |
| Postgre  | DtPOSTGR / DtKINGES(金仓) / DtOPENGS / DtPOLODB | $      | $1     | "name"     |
| Oracle   | DtORACLE / DtDAMENG(达梦)                                 | :      | :1     | name(裸)   |
| MySQL    | DtMYSQLN / DtTAURUS(华为)                                 | ?      | ?      | name(裸)   |
| SQLServer| DtSQLSRV                                                  | :      | :1     | "name"     |

> **SQLServer 注意**:上表取值以驱动名 **`mssql`** 为前提;驱动名不同,占位符结论完全不同——见 0.2。

##### 0.2.SQL Server 驱动名与占位符

SQL Server 的占位符**不只看 `ColParam`,还取决于 `sql.Open` 用的驱动名**。go-mssqldb 注册了两个名字,预处理行为相反。下表描述原始驱动行为;引擎在 `Initialize()` 时拦截错误组合:

| sql.Open 驱动名 | 引擎 ColParam | 实际效果 |
| --------------- | ------------- | -------- |
| `mssql`(官方标记 deprecated,但仍注册) | `:` / `$` / `?` | ✅ 三者皆可:驱动内部统一改写为 `@p1` 再发往服务端 |
| `mssql` | `@p` | ❌ `sql: expected 0 arguments, got N` |
| `sqlserver`(现代名) | `@p` | ✅ 唯一可用 |
| `sqlserver` | `:` / `?` | ❌ 服务端报语法错误 |
| `sqlserver` | `$` | ⚠️ **静默写错数据**,见下 |

```go
//#驱动名 mssql:内部把 ? / :N / $N 预处理为 @pN
db, _ := sql.Open("mssql", DbSource)
UniEngineEx := UniEngine.TUniEngine{Db: db, ColLabel: "db", ColParam: ":", Provider: UniEngine.DtSQLSRV}

//#驱动名 sqlserver:不预处理,仅认 @pN
db, _ := sql.Open("sqlserver", DbSource)
UniEngineEx := UniEngine.TUniEngine{Db: db, ColLabel: "db", ColParam: "@p", Provider: UniEngine.DtSQLSRV}
```

> **初始化校验**:`UniEngineEx.Initialize()` 在 SQL Server 族上用实际连接执行只读参数回显。占位符未真正绑定时返回 `ErrInvalidPlaceholderBinding`,且不登记内置表。务必处理初始化错误;后续执行入口也会在配置变动或跳过初始化时检查。`$N` 在 T-SQL 中是 **money 字面量**;旧版本使用 `sqlserver` + `$` 的部署可能已有错误数据,建议审计历史数据(可疑值形如 `"1.00"`/`"2.00"` 的数值型字符串)。
>
> 两种驱动名下**没有通用取值**:`@p` 在 `mssql` 下报 `expected 0 arguments`,而 `:`/`$`/`?` 在 `sqlserver` 下不能正确绑定——请按驱动名选择 `ColParam`。
>
> 现代写法与 `mssql` 同语义(同样只接受 `:`/`$`/`?`):`sql.OpenDB(mssql.NewConnectorWithProcessQueryText(cfg))`。

#### 1.安装方式

```sh
go get github.com/kazarus/UniEngine
```

```go
import "github.com/kazarus/UniEngine" //包名仍为 UniEngine
```

> 模块路径为 `github.com/kazarus/UniEngine`;相对旧版(master 基线)的破坏性变更与新增见 CHANGELOG。

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
//#MySQL 族走 information_schema:ExistTable/ExistViews/ExistField/ExistConst/IfDropView 与 AutoKeys 均需库名,
//#缺省报 `UniEngine: database is not specified`(其余族由连接或库自身元数据获得,无需该字段)
UniEngineEx.DataBase = "kz2020_gcgl_demo"
if eror := UniEngineEx.Initialize(); eror != nil {
  return eror
}

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

##### 3.可空列与 NULL(读路径语义)

引擎**不做 NULL 转换**:每一列直接扫进结构体对应字段,行为完全遵循 `database/sql`。真机实测(MySQL 8.4.6):

| 结构体字段类型 | 库中该列为 NULL 时 |
| --- | --- |
| `string` / `int64` / `bool` / `time.Time` 等**不可空类型** | ❌ 报错;`SelectL` **整批丢弃**(不是跳过那一行) |
| `[]byte` | ✅ 置为 `nil` |
| `sql.NullString` / `sql.NullInt64` / `sql.NullFloat64` / `sql.NullTime` | ✅ `Valid=false` |
| `*string` / `*int64` 等指针 | ✅ 置为 `nil` |

- 结构体入口 `Select` / `SelectL` / `SelectM` / `SelectH` 遇 NULL 即失败,错误形如
  `sql: Scan error on column index 1, name "v": converting NULL to string is unsupported`。
  该错误**只给列下标与列名、不含行号**,多行结果时无法据此定位是哪一行;`SelectL` 会**整批返回空**(已扫过的行一并丢弃)。
- **标量入口相反、是宽容的**:`SelectS`(内部 `sql.NullString`)、`SelectD`(`sql.NullInt64`)、
  `SelectF`(`sql.NullFloat64`)遇 NULL **不报错**,分别返回 `""` / `0` / `0`。
- 结论:**列可空时请把结构体字段声明为 `sql.NullXxx` 或指针**(推荐前者,语义最明确)。
  否则一条 NULL 就会让整次 `SelectL` 失败——这是"查不出数据"最常见的原因。
- 引擎**不提供**"NULL 自动降级为空值 / 跳过该行":前者会把 NULL 静默变成 `""`/`0`(判空与统计语义出错),
  后者会让调用方无法区分"本来没有这行"与"这行读不动"。需要部分成功语义时,请在应用层按可空类型读入后自行判定。

##### 4.应用加密(敏感字段)

UniEngine 提供应用层加密 hook,敏感字段(密码/证件号/手机号等)写入时自动加密,读取时自动解密。

```go
//#1.注册类时,给敏感字段打上 secreton 标记(结构体 tag)
UniEngineEx.RegisterClass(mock.TUSER{}, "mock_user")
//# 字段声明示例:Password string `db:"password,secreton"`

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
- 主键字段请勿标记 secreton(密文含随机nonce,无法用于匹配)
- **secreton 仅支持 string 类型字段**:非 string 字段写入时直接报错(fast-fail),避免"写入密文/读取不解密"的不对称
- **密钥派生**:内置实现以 PBKDF2-HMAC-SHA256(SecretBy, 盐, 迭代数) 派生 AES-256 密钥,缺省 600000 次迭代(OWASP 2023 建议值),`SecretIter` 可调(上限 10000000,越界时**写路径直接报错**而不是产出读不回的密文);派生密钥按"盐+迭代数+密钥指纹"缓存在引擎内,最多保留 128 项,按先入先出淘汰。冷解密仅在 GCM 认证成功后缓存派生结果;认证失败的密文不会增加缓存项。**仍建议 SecretBy 使用高熵随机密钥**(KDF 拉伸不能替代密钥熵度,人类口令依旧不推荐)
- 密文格式:`ENC:` + base64( 盐(16) + 迭代数(4) + 随机nonce + AES-256-GCM密文 );盐与迭代数内嵌密文,读取自包含,跨进程可直接解密;同一明文两次加密结果不同(随机nonce+随机盐)
- **防密文搬移请配合 `SetVerify`**(设计边界):GCM 只保证「单个密文未被篡改」,不绑定「密文属于哪一行哪一列」——库内把 A 行的密文贴到 B 行,纯加密路径读取时不会发现。行级绑定防护由存储完整性校验承担:`SetVerify` 的 HMAC 载荷域分隔含表名、覆盖**登记顺序的保护列**,且**必须覆盖全部已登记主键**(复合主键缺任何一列都会在登记期报 `LastError`;主键须先于验签登记——先 `SetKeys`/`AutoKeys` 再 `SetVerify`;确有理由不绑定行标识时可先置 `TUniTable.VerifyUnkeyed=true` 自行承担风险)。**`SetVerify` 成功即冻结主键**:此后 `SetKeys`/`AutoKeys`/`RegisterPkeys` 一律被拒并返回 `ErrPkeysFrozenAfterVerify`(**不写入任何主键**,`AutoKeys` 连元数据查询都不发起),需变更主键请重新 `RegisterClass` 后按 `[SetKeys/AutoKeys → SetVerify]` 顺序登记。保护列覆盖全部主键时,把 A 行的数据连同 MAC 一起搬到 B 行、或改写任一主键列,读取时验签必失败。加密 + SetVerify 同时启用即闭合该攻击面;纯加密场景请知晓此边界
- **MAC 载荷版本与防碰撞迁移**(评审 Q1):v1 载荷以 `0x1f` 直接拼接保护列值,字符串值本身含 `0x1f` 时不同数据可产出相同载荷(编码歧义,非 HMAC 碰撞)。缺省 `VerifyMacVer=0` 现在写 v2 无歧义载荷(`MAC2:`);`VerifyMacVer=2` 同样写 v2,只有显式设置 `VerifyMacVer=1` 才写旧 v1(`MAC:`)。读取按标签识别版本,存量 v1 行仍可验签。升级后新写入和更新的行自动写 v2;存量行需分批读出并更新重签,全表转为 v2 后才获得完整防歧义保证。
- **严格验签读取**:`VerifyMode=2` 且表启用验签时,结构体查询若缺少任一保护列或 MAC 列会返回 `ErrIncompleteVerification`;自定义结果钩子无法由引擎验签时也返回该错误。查询应投影完整受保护列及 MAC 列。

##### 4.1.存量数据鉴别与迁移

开启加密后,库中会存在两类数据:加密前的存量明文、加密后的密文。
读取时自动鉴别:

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
带标签但密钥不匹配的密文会解密报错(**不静默**,便于发现密钥轮换问题)。

**关于密钥轮换**:派生密钥以「盐 + 迭代数 + 密钥指纹」为缓存键,故运行时更换 `SecretBy`
**立即生效**(不会静默沿用旧派生密钥)。但密文格式只内嵌「盐 + 迭代数」、**不含密钥标识**,
因此轮换后**旧密钥写出的存量密文将无法再解密**——轮换必须配套迁移(用旧密钥读出、
用新密钥重写),或自行以 `SecretHook` 接管以实现多密钥共存。

##### 5.从旧版本迁移(SpecialInsert → CopyIn)

`SpecialInsert*` 系列方法更名为 `CopyIn*`(语义即 PostgreSQL 的 COPY IN 协议):

| 旧名称              | 新名称    |
| ------------------- | --------- |
| SpecialInsertL      | CopyInL   |
| SpecialInsertLCtx   | CopyInLCtx|
| SpecialInsertP      | CopyInP   |
| SpecialInsertPCtx   | CopyInPCtx|
| HasSpecialGetSqlInsertL | HasGetSqlCopyInL |
| HasSpecialSetSqlValuesL | HasCopyInSetSqlValuesL |

- 旧方法名保留为**废弃包装**,直接委托到新实现,源码兼容无需改动;
- 旧接口仍被引擎探测:已实现 `SpecialGetSqlInsertL` / `SpecialSetSqlValuesL` 的类**无需改动即可继续工作**,建议尽快迁移到新接口。

同版本其他行为变化:

- **SaveIt / SaveItWhenNotExist 默认走方言原生 UPSERT**(PG `on conflict`、Oracle/SQLServer `merge`),消除 count 与写入两步之间的并发窗口;实现自定义 SQL 钩子的类与 MySQL(唯一键语义不等价)自动回退旧的 count-then-dispatch;
- **查询补 `rows.Err()` 检查**:读取中途出错不再被静默吞掉(此前表现为"正常返回但数据截断");
- **SQL 生成确定性**:CRUD 列序按类声明序(手工注册字段按字段名排序补齐),同一输入不再产生不同 SQL 文本,利于数据库端语句缓存;
- **表名/字段名白名单校验**:所有**运行时传入**的表名/字段名(`RegisterClass`/`RegisterTable`/`RegisterField`/`RegisterPkeys` 的参数、写方法的 `TableName`、`Exist*`)与 **struct tag 列名**(`db:"..."`,只读标记 `-`/`_` 豁免)仅允许字母/数字/下划线/点,非法字符直接报错(`ErrInvalidTableName`/`ErrInvalidFieldName`,防注入);注册类 API 遇非法名返回**带 `LastError` 的未注册表**(链式短路,签名不变,非法名不进注册表);
- **ExistViews 修正**:PostgreSQL(补 `relkind='v'`)/ SQLServer(补 `xtype='V'`)/ MySQL(改查 `information_schema.views`)不再把同名普通表误判为视图;
- **SQLServer 主键探测**改用 `sys.indexes` 目录视图(替代老旧的 syscolumns/sysindexes 联查);
- **RegisterClass 双 key 注册**:类同时以"小写表名"和"类全名"注册(统一 GetTable 与 SaveIt 路径的可见性)。注意:两个类注册同一表名时,小写表名 key 以后注册者为准;
- Oracle 下 `$` 替换收窄到参数占位符(`$1`),不再误伤 SQL 文本中其它 `$` 字符;
- **占位符转换经 SQL 词法扫描**(评审 Q5):`Execute/Select*` 的 `$N` 方言转换只作用于**代码区**——字符串常量(`'...'`,仅 `''` 双写转义)、双引号/反引号串与注释(`-- ` 行注释、`/* */` 块注释)里的 `$N` 原样保留;MySQL 分支按占位符**出现序**重排/复制实参(`$2 ... $1` → `?,?` + `[a2,a1]`,`$1 $2 $1` → `?,?,?` + `[a1,a2,a1]`),编号越界(`$0`/超实参数)与实参多余时**显式报错**(此前全部替换为 `?` 却不重排,乱序编号会错绑实参)。已知边界:不处理反斜杠转义(MySQL `'don\'t'` 写法),请用 `''` 写法或方言原生占位符;
- **CopyInL 保持表名原大小写**,不再整句转小写;全部字段只读时写入方法返回明确错误而非 panic;
- **(第二阶段)引擎并发安全**:预备语句移出结构体,查询/注册可并发;事务期间仍须串行;
- **(第二阶段)写方法表名参数类型安全化**:`args ...interface{}` → `TableName ...string`,传非字符串由运行期报错变为编译期报错,已有调用点语法不变;
- **(第二阶段)钩子接口引擎参数改 `*TUniEngine`**(消除逐行拷贝);`AutoKeys` 同步改指针;实现方需同步修改签名;
- **(第二阶段)`Select` 多行时报错**(旧版静默保留最后一行),多行请用 `SelectL`;
- **(第二阶段)移除 `github.com/lib/pq` 依赖**,COPY 语句由包内 `copyInStmt` 自实现,输出逐字一致;
- **(第二阶段)`CopyInL` 仅 PG 协议族可用**(DtPOSTGR/DtKINGES/DtOPENGS/DtPOLODB),其余方言直接报错。

#### 5.性能基准

```sh
go test -bench . -benchmem -run '^$'
```

- **行扫描热路径**:`BenchmarkSelectLScan`(列→字段下标预解析)对比 `BenchmarkSelectLScanLegacy`(旧算法复刻:每行每列 FieldByName + HashField 查找),两者走同一 mock 传输,差值即优化净收益(Apple M3 Ultra 实测 1000 行×10 列:约 320µs vs 777µs,2.4×,分配次数少 1/3);
- **加解密**:`BenchmarkSecretEncrypt/Decrypt`(稳定态,按 16B/256B/4KB 分档);**4KB 档实测吞吐:加密约 0.75 GB/s、解密约 1.17 GB/s**(此前文档写作 0.8-1.2 GB/s,加密档偏乐观,已按实测更正);小尺寸以固定开销为主,16B 档约 23 / 39 MB/s。另有 `BenchmarkSelectLScanDecrypt`(行扫描+逐行解密联合路径,约 0.4µs/值)、`BenchmarkSecretDecryptPlaintext`(存量明文直通,零分配);
- **密钥派生**:`BenchmarkSecretPBKDF2DeriveCold`(600000 次迭代冷派生约 53ms,每进程每份盐只付一次)。

#### 6.真实数据库集成测试

`integration/` 是**独立 Go 模块**(自带 go.mod),用真实数据库验证各协议族的真实驱动行为——库本体保持零第三方依赖不受影响。覆盖 **PostgreSQL / SQL Server / Oracle / MySQL** 四族:

| 文件 | 驱动 | 覆盖内容 |
| ---- | ---- | -------- |
| `pg_test.go` | `pgx` | COPY 协议(on conflict UPSERT / CopyInL+CopyInP / ENC 密文落库与还原 / 存量明文直通 / AutoKeys+Exist* 元数据 / 事务提交回滚) |
| `mssql_test.go` | `go-mssqldb` | **驱动名 × ColParam 绑定矩阵**(`mssql` 预处理 `?`/`:N`/`$N` → `@pN`;`sqlserver` 仅认 `@pN`)+ 四组合六步 CRUD + `sqlserver`+`$` 静默写错数据的复现用例(默认跳过) |
| `oracle_test.go` | `go-ora`(纯 Go;`godror` 亦可) | `:N` 占位符契约(错误风格**硬报错**,无静默写错)+ CRUD / `INSERT ALL` 多行 / `MERGE` 两条分支 / `AutoKeys`+`Exist*` 元数据 / ENC 加密落库与还原 |
| `mysql_test.go` | `go-sql-driver/mysql` | `?` 占位符契约(三种错误风格**均不绑定实参**且均非静默写错)+ 六步 CRUD(含 `SaveIt` 的 count-then-dispatch 回退)+ `information_schema` 元数据 / ENC 加密落库与还原 |

```sh
# PostgreSQL(连不上时自动跳过;REQUIRED=1 则强制失败,CI 即如此)
UNIENGINE_PG_DSN="postgres://user:pass@127.0.0.1:5432/db?sslmode=disable" go test ./integration/ -v

# SQL Server:分量式(口令含 @/#/$/% 时自动 URL 转义)或整串 DSN
UNIENGINE_MSSQL_HOST=127.0.0.1 UNIENGINE_MSSQL_USER=sa UNIENGINE_MSSQL_PASS='...' \
UNIENGINE_MSSQL_DB=demo go test ./integration/ -run MSSQL -v

# Oracle:分量式或整串 DSN;驱动默认纯 Go 的 go-ora,用 godror 时设 UNIENGINE_ORACLE_DRIVER=godror
UNIENGINE_ORACLE_HOST=127.0.0.1 UNIENGINE_ORACLE_USER=scott UNIENGINE_ORACLE_PASS='tiger' \
UNIENGINE_ORACLE_SERVICE=ORCL go test ./integration/ -run Oracle -v

# MySQL:分量式或整串 DSN;元数据用例(Exist*/AutoKeys)需要库名,故分量模式下须给 UNIENGINE_MYSQL_DB
UNIENGINE_MYSQL_HOST=127.0.0.1 UNIENGINE_MYSQL_USER=root UNIENGINE_MYSQL_PASS='...' \
UNIENGINE_MYSQL_DB=demo go test ./integration/ -run MySQL -v
```

四族各有 `UNIENGINE_{PG,MSSQL,ORACLE,MYSQL}_REQUIRED=1` 开关(不可达时失败而非跳过),口径一致。

**CI 覆盖**(`.github/workflows/ci.yml`):

| job | 族 | 触发 | 说明 |
| --- | -- | ---- | ---- |
| `build-test` | — | 每次 push/PR | Go 1.24–1.27 矩阵:`build`/`vet`/`gofmt`/`test -race` + **覆盖率门槛 80%** + 基准 smoke + 10s fuzz smoke |
| `integration-pg` | PostgreSQL 16 | 每次 push/PR | `postgres:16-alpine` service 容器 |
| `integration-mysql` | MySQL 8.4 | 每次 push/PR | `mysql:8.4` service 容器 |
| `integration-heavy` | SQL Server 2022 + Oracle Free 23 | **每日定时 + 手动** | 重容器(各 1–2 GB / 30–90s 就绪),不阻塞 PR,次日暴露腐化;失败时打印容器日志 |

所有集成用例**只操作自建临时表**(`it_*` / `uni_it_*`)并在结束时删除,不触碰既有业务对象。
SQL Server 与 Oracle 用手动/定时方式而非每次 push,是针对容器体量的有意取舍;若 runner 资源充足,
放宽 `integration-heavy` 的 `if` 即可转为常态。

#### 7.pgx 驱动与 COPY 协议

pgx(当前主流 PG 驱动)的 `database/sql` 适配层**不模拟** lib/pq 的逐行 COPY 约定
( COPY 语句 `NumInput()=0`,逐行传参被 `database/sql` 拒绝),因此不接钩子时
`CopyInL/CopyInP` 在 pgx 下会报 `expected 0 arguments`。解决方式——接 `CopyInHook`
走 pgx 原生 `CopyFrom`:

```go
import "github.com/kazarus/UniEngine/contrib/pgxcopy"

UniEngineEx.CopyInHook = pgxcopy.Hook // 一次性设置,之后 CopyInL/CopyInP 正常可用
```

`contrib/pgxcopy` 为独立模块(自带 go.mod),不引入则库本体保持零第三方依赖;
非 pgx 驱动(如 lib/pq)下钩子返回未接管,自动回退内置协议。
注意:钩子经由连接池取连接,无法路由到 `*sql.Tx`,**事务期间** `CopyInL` 会被引擎显式拒绝。
