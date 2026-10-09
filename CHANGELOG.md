# 更新日志

本文档记录相对旧版（master 基线）的**破坏性变更**与主要新增，供升级参考。

## ai-dev-20260904

三线融合：`context.Context` 支持 + 应用层加密 + 加固（第一阶段/第二阶段）。

### 模块路径

`module github.com/kazarus/UniEngine`（`go get github.com/kazarus/UniEngine`，包名 `UniEngine`）。

> 注：该线上一度改为 `github.com/kazarus/UniEngine/v2` 并打过 `v2.0.0` / `v2.0.1` tag，现已改回**无主版本后缀**。无后缀模块在 Go modules 中只接受 `v0` / `v1` 版本号，故 `v2.x.y` 形式的 tag **不再适用于本模块**；后续发布请使用 `v1.x.y`。

### 协议族归一化（provider family）

新增 `TDbFamily`（`FmPOSTGR`/`FmSQLSRV`/`FmORACLE`/`FmMYSQLN`/`FmUNKNOWN`）与 `dbFamilyOf`，引擎所有按方言分支的行为（占位符风格、标识符引用、元数据探测、UPSERT、INSERT ALL、COPY 白名单、分页缺省）统一按协议族判断。由此国产兼容库获得完整能力：

| 驱动 | 族 | 行为变化 |
|------|----|----------|
| DtKINGES/DtOPENGS/DtPOLODB | PG | 新增元数据探测（pg 目录）、原生 UPSERT、CopyIn 可用（此前仅 DtPOSTGR 有） |
| DtDAMENG | Oracle | 新增元数据探测（oracle 目录）、多行插入改走 `INSERT ALL`（此前生成 Oracle 不支持的多 values 语法）、标识符改裸名（Oracle 风格） |
| DtTAURUS | MySQL | 占位符 `$N`→`?`、标识符改裸名（此前按 PG 风格生成，MySQL 协议无法执行）、新增元数据探测 |

`ProviderName()` 补齐 kingbase/dameng/opengauss/polardb/taurus。

### 哨兵错误

新增 `errors.go`：`ErrUnregisteredClass`/`ErrNoPkeys`/`ErrNoTransaction`/`ErrAlreadyInTransaction`/`ErrInvalidTableName`/`ErrInvalidConstraintName`，调用方改用 `errors.Is` 判别；报错点以 `%w` 包装，**消息文本与旧版一致**。

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
| `HasSpecialGetSqlInsertL` / `SpecialGetSqlInsertL` | `HasGetSqlCopyInL` / `GetSqlCopyInL` | 旧接口仍被引擎探测（向后兼容） |
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

### 行为变更与修复（第三阶段加固）

- **标识符引用修正**：`quoteIdent` 改为按 `.` 分段逐段加引号（`schema.table` → `"schema"."table"`），修复 COPY 路径把限定名整体加引号当成单个标识符的问题；`getColParam` 复用 `quoteIdent`，列名引用与 COPY 路径一致（CRUD 表名仍保持裸插值，因其对限定名本就正确且 MySQL 不适用双引号）。
- **注册过滤**：`RegisterClass` 跳过**未导出字段**（避免 `FieldByName().Interface()` 反射 panic）与**无 `db` tag 字段**（避免以 `""` 键污染注册表并生成空列名 SQL）。
- **`Begin` 重复守卫**：重复 `Begin` 返回新增哨兵 `ErrAlreadyInTransaction`，不再覆盖并泄漏上一个事务；检查与设置改为持锁原子。
- **内嵌结构体防御**：行扫描预计算下，字段解析到提升字段（`reflect.Index` 长度 > 1）时返回明确错误，而非按 `[0]` 错绑到内嵌结构体本身。
- **主键值一律明文（encrypt×主键互斥）**：主键是 WHERE 定位行的标识，值保持明文才能与库中存量匹配。`SetSecret` 对已登记主键的字段返回错误，`SetKeys`/`AutoKeys` 对已标记 `encrypt` 的字段返回错误（双向守卫）；写入路径（`Insert`/`InsertL`/`CopyInL`/`saveUpsert`）经 `secretColumn` 对主键列跳过应用加密，兜底防御绕过守卫直接改注册表的用法——`SaveIt` 与 `Update` 对同一行的定位语义从此一致。
- **写方法 nil 指针守卫**：`SaveIt`/`Update`/`Insert`/`Delete`/`SaveItWhenNotExist`（含 `*Ctx` 变体）传入 typed nil 指针时返回明确错误（此前反射对零值 Value 取 `Interface()` 直接 panic）。

### 密钥派生加固（评审加固）

- **内置加密的密钥派生从单次 SHA-256 升级为 PBKDF2-HMAC-SHA256**（标准库 `crypto/pbkdf2`，`go.mod` 最低版本 1.21 → 1.24，仍零第三方依赖）：低熵密钥不再可直接离线暴力破解，缺省 600000 次迭代（OWASP 2023 建议值），新增 `TUniEngine.SecretIter` 可调。
- **现行密文格式 `ENC2:`** + base64( 盐(16) + 迭代数(4,大端) + 随机nonce + GCM密文 )：盐与迭代数内嵌密文，读取自包含（跨进程/跨密文可直接解密）；密文内嵌迭代数做合法性校验（≤0 或超上限拒绝），防损坏或恶意密文触发超高开销派生。
- **派生密钥缓存**：按"盐+迭代数"缓存在引擎内（指针字段，按值拷贝引擎时共享），每进程每份盐只付一次 PBKDF2 开销；首个到达的调用持锁派生，并发等待者直接命中缓存。行级加解密本身仍是纯 AES-GCM，读写吞吐不受影响。
- **旧格式 `ENC:` 读兼容**：存量密文（单次 SHA-256 派生时代写入）仍可解密读取，重新保存后自动落为 `ENC2:`；`IsEncrypted` 识别新旧两种标签。
- `SecretHook` 自定义钩子路径不受影响（钩子完全接管加解密时，格式由应用自定）。

### 基准测试（benchmark_test.go）

`go test -bench . -benchmem -run '^$'` 可复现，覆盖行扫描热路径与内置加解密：

- **`BenchmarkSelectLScan` vs `BenchmarkSelectLScanLegacy`**：现行"列→字段下标预解析"路径与旧算法复刻（每行每列 `FieldByName` + `HashField` 查找）走同一 mock 传输，差值即该优化的净收益（Apple M3 Ultra 实测 1000 行×10 列：320µs vs 777µs，**约 2.4×**，分配次数少 1/3）。
- **`BenchmarkSelectLScanDecrypt`**：行扫描+每行解密 1 个加密列的联合路径，解密增量与独立解密基准吻合（约 0.4µs/值）。
- **`BenchmarkSecretEncrypt/Decrypt`**：ENC2 稳定态（派生密钥缓存命中）按 16B/256B/4KB 分档；4KB 档 GCM 吞吐约 0.8-1.2 GB/s。
- **`BenchmarkSecretDecryptLegacyENC` / `BenchmarkSecretDecryptPlaintext`**：旧格式读取与存量明文直通（后者零分配，约 2ns）。
- **`BenchmarkSecretPBKDF2DeriveCold`**：一次性冷派生开销（缺省 600000 次迭代，实测约 53ms）——生产中每进程每份盐只付一次，行级吞吐不受影响。

### CI（.github/workflows/ci.yml）

push / pull_request 触发，Go 版本矩阵（`1.24.x`/`1.25.x`/`1.26.x`/`1.27.x`，1.24 为 go.mod 下限）× ubuntu-latest，依次执行 build → vet → gofmt 检查 → `go test -race -count=1` → 基准冒烟（`-benchtime=1x` 编译并各执行一次，防基准代码随重构腐化）。新 Go 版本发布后在矩阵追加一行即可。仓库零第三方依赖（无 go.sum），故关闭 setup-go 依赖缓存。

### PostgreSQL 真实驱动集成测试（integration/ 独立模块）

新增 `integration/` 目录为**独立 Go 模块**（自带 go.mod，`replace` 指向库本体），引入 `github.com/jackc/pgx/v5/stdlib` 作为**仅测试依赖**——库本体（根模块）保持零第三方依赖、go 1.24 下限不变，pgx 的传递版本要求被隔离在集成模块内。此前所有测试基于自实现 mock driver，真实驱动路径（COPY 协议、UPSERT 语句、加密密文落库）从未被验证过，本组测试补上这一层：

- **CRUD**：Insert/SelectL/Select/SelectS/SelectD/Update/Delete 全链路；
- **原生 UPSERT**：SaveIt 的插入/更新两分支、SaveItWhenNotExist 的插入/跳过两分支（`on conflict` 真实执行）；
- **COPY 协议**：CopyInL 批量 120 行、CopyInP 按 PageSize 分页（自实现 `copyInStmt` 对 pgx 的兼容性由此验证）；
- **应用加密**：Insert/Update/SaveIt 后库中为 `ENC2:` 密文（同明文两次加密密文不同）、SelectL/Select 自动还原（含中文）、存量明文直通、COPY 路径加密；
- **元数据探测**：AutoKeys 发现 bigserial 主键、ExistTable/ExistField/ExistConst（pg_catalog 真实查询）；
- **事务**：Begin→Insert→Cancel 回滚、Begin→Insert→Commit 提交、事务期间重复 Begin 返回 `ErrAlreadyInTransaction`。

运行方式：`UNIENGINE_PG_DSN=... go test ./integration/ -v`（连不上默认跳过，`UNIENGINE_PG_REQUIRED=1` 强制失败）；CI 由 `integration-pg` job 提供 `postgres:16-alpine` service 容器并设 `UNIENGINE_PG_REQUIRED=1` 自动执行。

### CopyInHook 与 contrib/pgxcopy（集成测试发现的真实缺陷及修复）

集成测试首次在真实 pgx 驱动下验证 COPY 协议时即抓到缺陷：**内置的 pq 风格逐行 COPY 协议在 pgx 下不可用**——pgx 的 stdlib 适配层不模拟 lib/pq 的 COPY 约定（COPY 语句 `NumInput()=0`，逐行传参被 `database/sql` 以 "expected 0 arguments" 拒绝），而 pgx 是当前主流 PG 驱动。修复为"引擎钩子 + 官方适配模块"，库本体保持零第三方依赖：

- **`TUniEngine.CopyInHook`**（`TCopyInHook` 类型）：COPY 协议执行钩子，接管 `CopyInL`/`CopyInP` 的批量写入；返回 `handled=false` 回退内置 pq 风格协议（lib/pq 兼容驱动不变）。`Rows` 为最终写入行（已完成应用加密）。**事务守卫**：钩子经由 `*sql.DB` 连接池取连接、无法路由到 `*sql.Tx`，事务期间引擎显式拒绝（避免数据静默落到事务外）。
- **`contrib/pgxcopy`（独立模块）**：经 `*sql.Conn.Raw` 取 pgx 原生连接执行 `CopyFrom`，`UniEngineEx.CopyInHook = pgxcopy.Hook` 一行接入；支持 `schema.table` 限定名；非 pgx 驱动返回未接管自动回退。
- 集成测试同步钉住两种行为：接钩子后 `CopyInL`（120 行）/`CopyInP`（PageSize 分页）真实写入且内容正确；**不接钩子**时 pgx 下报 `expected 0 arguments` 且零写入（文档化限制，防未来静默漂移）。

### 文件重组（纯物理移动，无行为变化）

`UniEngine.go`（2012 行）按既有逻辑分节拆为 7 个同包文件，**未改动任何签名、标识符与注释**，公开 API 与调用方零感知：

| 文件 | 行数 | 内容 |
|------|-----:|------|
| `UniEngine.go` | ~395 | 引擎核心：`TUniEngine` 定义与并发契约、锁/注册表查找、`currentTx`、`validIdent`、SQL 记号生成、`prepareCtx`（语句路由）、事务/Initialize |
| `UniRegister.go` | ~271 | RegisterClass/Table/Field/Pkeys、PrepareTables、PrepareRunSQL |
| `UniQuery.go` | ~332 | queryScalarCtx/queryRowsCtx 行扫描核心 + SelectD/F/S/L/M/H 全家 |
| `UniSave.go` | ~333 | 原生 UPSERT 机制 + SaveIt/SaveItWhenNotExist |
| `UniWrite.go` | ~358 | resolveTarget + Update/Insert/InsertL/Delete |
| `UniCopyIn.go` | ~245 | CopyInL/CopyInP、废弃包装、insertPage 方言路由 |
| `UniMeta.go` | ~240 | Execute/ExecuteMust/IfDropView + Exist* 四兄弟 |

等价性验证：拆分前后顶层声明签名集合完全一致（93 个），函数数一致（89），全量测试/`-race`/真实 PG 集成/基准冒烟全绿。

### 已知限制

- **pgx 驱动下 `CopyIn*` 需接 `CopyInHook`**（`contrib/pgxcopy.Hook`）：pgx stdlib 不模拟 lib/pq 逐行 COPY 约定，不接钩子报 `expected 0 arguments`；lib/pq 兼容驱动不受影响。事务期间 `CopyIn*`（钩子路径）被显式拒绝。
- **事务期间调用须串行**（`Begin` 与 `Commit`/`Cancel` 之间）：底层 `*sql.Tx` 非并发安全，期间所有语句都路由到该事务。
- 配置字段（`ColLabel`/`ColParam`/`Provider`/`SecretOn`/`SecretBy` 等）与表结构变更（`SetKeys`/`SetSecret`/`AutoKeys`/`PrepareTables`/`PrepareRunSQL`）应在并发查询开始前完成。
- **`encrypt` 不适用于主键字段**：主键值必须明文才能参与 WHERE 定位与冲突匹配；`SetSecret`/`SetKeys`/`AutoKeys` 会显式拒绝该组合，请勿对主键字段标记 `encrypt`。
- `CopyIn*` 的 COPY 协议由包内 `copyInStmt` 自实现，**不再依赖已停维护的 `github.com/lib/pq`**。
