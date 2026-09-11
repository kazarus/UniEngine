# 更新日志

本文档记录相对旧版（master 基线）的**破坏性变更**与主要新增，供升级参考。

## v2.0.0 — ai-dev-20260904

三线融合：`context.Context` 支持 + 应用层加密 + 加固（第一阶段/第二阶段），并以 **v2 module 路径**发布（钩子接口签名有破坏性修改，按 Go modules 惯例升主版本）。

### 模块路径（v2）

| 旧 | 新 |
|----|----|
| `module github.com/kazarus/UniEngine` | `module github.com/kazarus/UniEngine/v2`（`go get github.com/kazarus/UniEngine/v2`，包名仍为 `UniEngine`） |

v1（master 分支）继续可用且不再变更。

### 协议族归一化（provider family）

新增 `TDbFamily`（`FmPOSTGR`/`FmSQLSRV`/`FmORACLE`/`FmMYSQLN`/`FmUNKNOWN`）与 `dbFamilyOf`，引擎所有按方言分支的行为（占位符风格、标识符引用、元数据探测、UPSERT、INSERT ALL、COPY 白名单、分页缺省）统一按协议族判断。由此国产兼容库获得完整能力：

| 驱动 | 族 | 行为变化 |
|------|----|----------|
| DtKINGES/DtOPENGS/DtPOLODB | PG | 新增元数据探测（pg 目录）、原生 UPSERT、CopyIn 可用（此前仅 DtPOSTGR 有） |
| DtDAMENG | Oracle | 新增元数据探测（oracle 目录）、多行插入改走 `INSERT ALL`（此前生成 Oracle 不支持的多 values 语法）、标识符改裸名（Oracle 风格） |
| DtTAURUS | MySQL | 占位符 `$N`→`?`、标识符改裸名（此前按 PG 风格生成，MySQL 协议无法执行）、新增元数据探测 |

`ProviderName()` 补齐 kingbase/dameng/opengauss/polardb/taurus。

### 哨兵错误

新增 `errors.go`：`ErrUnregisteredClass`/`ErrNoPkeys`/`ErrNoTransaction`/`ErrInvalidTableName`/`ErrInvalidConstraintName`，调用方改用 `errors.Is` 判别；报错点以 `%w` 包装，**消息文本与旧版一致**。

### 性能

- **行扫描热路径**：`queryRowsCtx` 在行循环前一次性解析"列→字段下标"与加密列下标，循环内以 `Field(idx)` 取代每行每列的 `FieldByName` 线性扫描与 HashField 查找；解密走无查找的 `decryptRow`。公开 API `DecryptResult` 保持不变。

### 其它

- `AutoKeys` 的建议输出收敛到 `RunDebug(true)` 之后（库代码不再无条件打印到 stdout）。
- README 移除机器相关路径，驱动表补充协议族说明。

### 破坏性变更（Breaking Changes）

| 旧 | 新 | 说明 |
|----|----|------|
| `TUniEngine.HashTabl` 为 `map[string]TUniTable` | `map[string]*TUniTable` | 引擎注册表字段改为指针类型 |
| `EtDelele` | `EtDelete` | 常量拼写修正 |
| `CONST_PRIVIDER_NAME_TO_*` / `CONST_PRIVIDER_CODE_TO_*` | `CONST_PROVIDER_NAME_TO_*` / `CONST_PROVIDER_CODE_TO_*` | 常量拼写修正（`PRIVIDER` → `PROVIDER`） |
| `TUniField.DataLeng` | `TUniField.DataLen` | 字段改名 |
| `GetSqlAutoKeys(TUniEngine, string) string` | `GetSqlAutoKeys(TUniEngine, string) (string, error)` | `HasGetSqlAutoKeys` 接口及实现返回值增加 `error` |
| `SpecialInsertL` / `SpecialInsertLCtx` | `CopyInL` / `CopyInLCtx` | 旧名保留为 `deprecated` 转发 |
| `SpecialInsertP` / `SpecialInsertPCtx` | `CopyInP` / `CopyInPCtx` | 旧名保留为 `deprecated` 转发 |
| `HasSpecialGetSqlInsertL` / `SpecialGetSqlInsertL` | `HasCopyInGetSqlInsertL` / `CopyInGetSqlInsertL` | 旧接口仍被引擎探测（向后兼容） |
| `HasSpecialSetSqlValuesL` / `SpecialSetSqlValuesL` | `HasCopyInSetSqlValuesL` / `CopyInSetSqlValuesL` | 旧接口仍被引擎探测（向后兼容） |
| `TUniEngine.CanClose()` | （移除） | 自调试打印移除后一直是 no-op 死代码 |
| 写方法 `args ...interface{}`（首参字符串作表名） | `TableName ...string` | 类型安全：传非字符串由运行期报错变为**编译期报错**；已有调用点无需改动 |
| 钩子/生成器接口的 `TUniEngine` 首参 | `*TUniEngine` | 消除按值拷贝（`SelectL` 每行拷贝整个引擎）；实现方签名需同步修改 |
| `TUniTable.AutoKeys(TUniEngine, ...)` | `AutoKeys(*TUniEngine, ...)` | 同上 |
| `Select` 多行时静默保留最后一行 | 返回错误 | 与 sqlx `Get` 行为一致；多行请用 `SelectL` |
| `CopyInL` 任意方言都发送 COPY 语句 | 非 PG 协议族（`DtPOSTGR`/`DtKINGES`/`DtOPENGS`/`DtPOLODB`）直接报错 | 显式失败替代必然失败的 SQL |
| `HasStartSelect`/`HasEndedSelect`/`HasStartUpdate`/`HasEndedUpdate`/`HasStartInsert`/`HasEndedInsert`/`HasStartDelete`/`HasEndedDelete`/`HasGetSqlDelete` | （移除） | 引擎从不调用的死接口 |

### 新增

- **`context.Context` 支持**：每个查询/写入方法新增 `*Ctx` 变体（`SelectCtx`/`InsertCtx`/`ExecuteCtx`/`BeginCtx` 等），首参接收 `context.Context`；原方法等价于以 `context.Background()` 调用对应 `*Ctx`。
- **应用层加密**：
  - 结构体 tag `db:"field,encrypt"` 或 `.SetSecret("field")` 标记敏感字段；
  - `TUniEngine.SecretOn` / `SecretBy` 控制开关与密钥；
  - `SecretHook` 自定义加密钩子，默认内置 AES-256-GCM（密文带 `ENC:` 标签）；
  - 读取自动解密、存量明文（无标签）自动鉴别直通；
  - 非 `string` 字段标记 `encrypt` 时写入 `fast-fail` 报错。
- **`ExistConst` 约束存在性检查**：支持 `CtPK`/`CtFK`/`CtUK`/`CtDF` 四类约束，按数据库方言查询系统目录（PG `pg_constraint`/`pg_attrdef`、SQLServer `sys.objects`、Oracle `user_constraints`/`user_tab_cols`、MySQL `information_schema`），并对约束名做 `validIdent` 防注入校验。
- **加固**：
  - `validIdent` 表名/字段名防注入校验；
  - 注册表 `HashTabl` 增加 `sync.Mutex` 保护；
  - `RegisterClass` 双 key 注册（小写表名 + 类全名）并保留字段声明顺序；
  - `SetKeys` 类型校验 + 去重 + 顺序保留；
  - Oracle/MySQL 参数占位符改为包级预编译正则；Oracle 仅替换 `$N`（不再误改 SQL 文本中的 `$`）；
  - 视图存在性查询修正（PG `relkind='v'`、SQLServer `xtype='V'`、MySQL `information_schema.views`）；
  - SQLServer 主键查询改用现代系统目录（`sys.indexes` / `sys.index_columns`）。

### 其它

- `readme.md` 更名为 `README.md`，并补充 context 与加密说明。
- 新增 `LICENSE`、`.gitignore`、`go.mod`、`go.sum`。
- 删除遗留的 `copyit-p2.py`（Python 2 同步脚本）。

### 行为变更与修复（第一阶段加固）

- **SaveIt / SaveItWhenNotExist 默认走方言原生 UPSERT**（PG `on conflict (主键) do update / do nothing`、Oracle/SQLServer `merge`），消除原先 count 与写入两步之间的并发窗口，且参数只传一遍。回退旧的 count-then-dispatch 的情形：
  - 类实现了自定义 SQL 钩子（`GetSqlUpdate` / `GetSqlInsert` / `SetSqlValues`）——自定义语句无法转为 UPSERT；
  - MySQL——`ON DUPLICATE KEY UPDATE` 由任一唯一键触发而非仅主键，与 SaveIt 的主键语义不等价。
- **查询循环补充 `rows.Err()` 检查**：`rows.Next()` 因错误提前结束（而非读完）不再被静默吞掉——此前表现为"正常返回但数据截断"。
- **SQL 生成确定性**：CRUD 列序统一走确定序（类声明序优先，其余按字段名排序补齐，见 `TUniTable.orderedFields`/`orderedPkeys`），同一输入不再因 map 迭代产生不同 SQL 文本，利于数据库端语句缓存。
- **CopyInL 不再把 COPY 语句整体转小写**：混合大小写表名不再被改坏。
- **SpecialPageSize 尊重传入值**：Oracle/PG 不再强制 99；MySQL 缺省从 0 改为 10；非正值回退 `DefaultPageSize`。
- **空列防护**：全部字段只读 / 无主键属性映射时，`Insert`/`Update`/`Delete` 返回明确错误（此前为字符串切片越界 panic）。
- **PrepareRunSQL 对未注册表返回空结果**（此前对 nil 指针取 `ListPkeys` 会 panic）；`EtUpdate` 无可更新列时返回错误（此前 panic）。
- **错误包装**：写路径错误统一以 `%w` 包装保留错误链；错误消息拼写修正（`retun`→`returns`、`paramter`→`parameter`）。
- **大规模去重**（UniEngine.go 2463 → 1881 行）：`SelectD/F/S` 共享 `queryScalarCtx`；`Select/L/M/H` 共享 `queryRowsCtx` 行扫描核心；六个写方法共享 `resolveTarget` 前置；四个 `Exist*` 共享 `existCount`；`switch bool` 惯用法全部扁平化为 `if`。

### 行为变更与修复（第二阶段加固）

- **引擎并发安全**（旧版"每 goroutine 独立实例"的告诫作废）：
  - 预备语句 `st` 移出结构体，`prepareCtx` 返回局部语句——查询/写入路径不再共享可变状态；
  - 注册表锁升级为 `sync.RWMutex`：查询只持读锁做一次表查找，`Register*` 可与查询并发执行（`TestConcurrentQueryAndRegister` 在 `-race` 下验证）；
  - 事务状态 `tx`/`inTx` 由同一把锁保护；**事务期间（Begin 与 Commit/Cancel 之间）调用仍须串行**（`database/sql` 的 `*sql.Tx` 非并发安全）；
  - `RunDebug` 改原子写（`runDebug int32`），可在运行中并发切换。
- **写方法表名参数类型安全化**：`SaveIt`/`SaveItWhenNotExist`/`Update`/`Insert`/`InsertL`/`CopyInL`/`InsertP`/`CopyInP`（含废弃包装与 `*Ctx` 变体）的 `args ...interface{}` 改为 `TableName ...string`；空切片沿用注册表默认表名，已有调用点语法不变。
- **`Select` 多行报错**：查询返回多于一行时返回错误（旧版静默保留最后一行，掩盖查询缺陷）。
- **`CopyInL` 方言路由**：非 PG 协议族直接报错，不再发送必然失败的 COPY 语句。
- **移除 `github.com/lib/pq` 依赖**（模块归零第三方运行时依赖）：新增 `quoteIdent`（标识符双引号转义）与 `copyInStmt`（自实现 COPY IN 协议语句，输出与 `pq.CopyIn` 逐字一致）；`go.mod` 清空 `require`。
- **移除未使用的生命周期钩子接口**：`HasStartSelect`/`HasEndedSelect`/`HasStartUpdate`/`HasEndedUpdate`/`HasStartInsert`/`HasEndedInsert`/`HasStartDelete`/`HasEndedDelete`/`HasGetSqlDelete` 删除（引擎从不调用）。

### 已知限制

- **事务期间调用须串行**（`Begin` 与 `Commit`/`Cancel` 之间）：底层 `*sql.Tx` 非并发安全，期间所有语句都路由到该事务。
- 配置字段（`ColLabel`/`ColParam`/`Provider`/`SecretOn`/`SecretBy` 等）与表结构变更（`SetKeys`/`SetSecret`/`AutoKeys`/`PrepareTables`/`PrepareRunSQL`）应在并发查询开始前完成。
- `CopyIn*` 的 COPY 协议由包内 `copyInStmt` 自实现，**不再依赖已停维护的 `github.com/lib/pq`**。
