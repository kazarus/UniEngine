# 更新日志

本文档记录相对旧版（master 基线）的**破坏性变更**与主要新增，供升级参考。

## master-20261018

### 行为修复

- **注册双键冲突不再静默覆盖**:`RegisterClass` 此前无条件写 `HashTabl[lower(table)]` 与 `HashTabl[class]`。现在发布前校验:
  - 类已登记到另一个表名 → `ErrClassConflict`;
  - 表名已由手工 `RegisterTable/RegisterField/RegisterPkeys` 登记 → `ErrTableConflict`;
  - 不同类共享同一表名 → **允许**(同一张表的多个投影/只读类),类名键各自保留,表名键指向最后一次注册;
  - 双键指向不同快照 / 类键存在但表键缺失 → `ErrRegistryInconsistent`;
  - 同一类、同一表名重复登记仍允许,按“重新注册”语义重建类元数据(手工 pkeys/Secret/Verify 需重新登记)。
  冲突时不修改注册表,返回带 `LastError` 的未注册载体;`Initialize()` 的内置表登记错误不再被吞掉。
- **`AutoKeys` 默认 SQL 按协议族选择**:不传生成器时,PG/SQLServer/Oracle/MySQL 分别使用各自的主键探测 SQL,不再写死 PostgreSQL;MySQL 缺 `DataBase` 或未知 Provider 明确报错。
- **达梦批量预算接入 `Supplier`**:新增 `batchDialect()`;`Provider=DtORACLE` + `Supplier=DtDAMENG` 时按达梦 2000 参数预算分页,不套 Oracle 100 行上限,SQL 生成仍走 Oracle 分支。生产无需改 `Provider`(避免 507 处 `case DtORACLE` 迁移)。
- **锁模型恢复共享指针**:`mu`/`txGate` 改回 `*sync.RWMutex`/`*sync.Mutex`,使历史“按值复制引擎”调用方的副本共享注册表锁;否则副本各自持锁却写同一张 `HashTabl`,运行时 `RegisterClass`/`PrepareTables` 与查询并发会触发 map 竞态。事务状态(`tx`/`inTx`)仍按值复制,新代码仍应使用 `*TUniEngine`。
- **新增 `RegisterClassForce`**:显式允许用类元数据替换**手工登记**的表(`ClassType==nil`);不会把已登记到其它表的类改绑(`ErrClassConflict`)。不同类共享同一表名本来就是允许的。
- **批量分页 clamp**:`InsertP`/`CopyInP` 的 `PageSize` 统一归一化:
  - `0`/负值 → 999 行;超过 999 → 999 行;
  - `InsertP` 按可写列数压到方言单语句绑定参数预算内:Oracle 60000、达梦 2000、SQLServer 2000、PG/MySQL 60000;至少 1 行;
  - Oracle 额外限制 **100 行/页**(真机测得 `INSERT ALL` 的 `INTO` 子句数存在性能拐点:999 行约 144s、500 行约 14s、200 行约 0.3s、100 行约 0.1s、50 行约 0.05s);
  - 先 clamp 再转 `int`,消除 32 位截断/超大值导致的不分页;`CopyInP` 走 COPY,只做行数上限。
  这修复了达梦 3 列以上、Oracle/SQLServer 宽表使用默认 999 行时超出参数上限的问题,并把 Oracle `InsertP` 从分钟级降到秒级。
- **Oracle 集成测试支持 godror**:`UNIENGINE_ORACLE_DRIVER=godror` + URL DSN 的 `libDir` 参数可走生产驱动;错误断言改为驱动无关(go-ora 抛 ORA-,godror 客户端直接拒绝),并新增 godror 真机 `InsertP` 用例。`integration/go.mod` 仅测试模块新增 godror 依赖。
- **新增 TiDB 真机集成测试**:`integration/tidb_test.go` 覆盖 MySQL 协议(端口 4000)的 CRUD、`SaveIt` 的 count-then-dispatch 回退、`InsertL`、`InsertP` 页大小 clamp、ENC 加密往返、`information_schema` 元数据与事务提交/回滚;通过 `UNIENGINE_TIDB_*` 配置,未纳入固定 CI 服务。
- **分页测试补全**:新增页大小归一化、按列预算(Oracle/达梦/SQLServer/PG)、负值/`MaxInt64`/COPY clamp 回归。

### 升级注意

- 同类注册到第二个表名现在返回 `ErrClassConflict`;不同类共享同一表名是允许的(表名键由最后一次注册覆盖)。确需替换手工表时用 `RegisterClassForce`。
- `PageSize=-2/-5` 等负值从“永不分页”变为按 999 行分页;宽表会按列数自动减少每页行数。
- 自定义写钩子下 `InsertP` 的列数按默认可写列估算;钩子改变列数时请自行控制批量大小。

## master-20261017

### 行为修复

- **COPY 自定义列/值不再绕过加密与验签**(安全收紧,曾静默写明文):`CopyInL` 走 `HasGetSqlCopyInL`/`HasCopyInSetSqlValuesL`(含废弃 `Special*`)时,钩子行曾完全跳过默认分支的 `secretEncrypt`;现按列名映射注册表,**就地加密** secreton 实参,列名不可映射、行长度不符、实参类型非 string/[]byte、返回值却无列清单时 fail-fast 不执行 COPY;验签激活时一律拒绝(钩子列清单无法保证包含 MAC 伴随列,读侧验签会静默失效)。`SecretOn=0` 且未验签时行为不变。
- **钩子接收者统一**:`SetSqlResult`/`GetSqlUpdate`/`GetSqlInsert`/`GetSqlInsertL`/`SetSqlValues*`/`GetMapUnique`/COPY 钩子此前仅在部分入口识别指针接收者,同一类在 `Select` 能走钩子、`SelectL`/`Insert`/`SaveIt` 却静默生成默认 SQL。现在值接收者与 `func (u *TUser) ...` 指针接收者全入口一致。
- **`SelectL` 整批语义落地**:查询中途失败(扫描错误/`rows.Err`)时回滚本次已追加的行,与 README/doc.go 的"整批丢弃"契约一致;调用方预置的元素保留。此前错误路径会把已扫过的行留在切片里。
- **标量查询多行显式报错**:`SelectS`/`SelectD`/`SelectF` 曾逐行覆写目标、静默返回最后一行;现在返回 `ErrMultipleRows`,与 `Select` 的单行契约对齐。
- **`SelectH` 拒绝 nil 键函数**:旧版把所有行折叠到键 `""`,map 静默只剩一条;现在在 map/键类型校验之后返回明确错误。
- **`Db` 未装配不再 panic**:`prepareCtx`/`BeginCtx`/`CopyInHook` 统一返回 `ErrNoDatabase`;`Initialize()` 仍允许只做注册(便于单测),执行入口兜底。
- **`Initialize()` 不再改写事务状态**:删除无锁的 `inTx=false`,避免 `inTx=false` 且 `tx!=nil` 的悬挂状态(Begin 报"已有事务"、Commit 报"无事务")。
- **`AutoKeys` 短路 `LastError`**:非法表名/字段名不再进入元数据 SQL 的单引号拼接;`AutoKeys` 对 nil 引擎也显式报错。MySQL 元数据探测的 `DataBase` 纳入标识符白名单(`ErrInvalidDataBase`),各 `TExist*4MYSQLN` 生成器对非法输入返回空串(由 `existCount` 拒绝执行)。
- **列名禁止点号**:struct tag 与 `RegisterField`/`RegisterPkeys`/`ExistField` 的字段名仅允许字母/数字/下划线(表名仍允许 `schema.table`),避免 `db:"a.b"` 被引成 `"a"."b"` 后静默指向另一 schema。
- **`SetVerify` 登记期校验保护列类型**:`time.Time`/`[]byte` 等不被 `verifyFormat` 支持的 kind 直接写 `LastError`,不再等到首次读写才失败;提升字段(内嵌结构体)同样在登记期拒绝。
- **调试输出脱敏**:`RunDebug(true)` 只打印 SQL 文本与实参个数,不打印实参值,避免明文密码/证件号进入日志。
- **链式登记改为快照 COW**(并发安全,行为变更):`SetKeys`/`SetSecret`/`SetVerify`/`AutoKeys` 此前直接改写 `RegisterClass` 已发布的 `*TUniTable`,与并发查询读取 `HashField/ListField/HashPkeys` 构成数据竞争(并发 map 读写可致进程 fatal)。现在这些方法在引擎写锁内克隆当前快照、修改副本并发布,返回**新的** `*TUniTable`;旧指针保持不变。链式写法 `tbl := eng.RegisterClass(...).SetKeys(...).SetSecret(...)` 不受影响;忽略返回值后再读旧指针属性会看到旧快照,`AutoKeys` 之后请用 `eng.GetTable(name)` 重新取表。
- **`PrepareTables`/`PrepareRunSQL` 改为快照 COW**:两者原在持锁状态下原地改写已发布快照的 `ListField/ListPkeys/SqlSelect~SqlDelete`,读者取到指针后不持锁访问,构成数据竞争。现均克隆后发布新快照,旧指针保持只读。
- **`ExistTable` 按对象种类过滤**:PostgreSQL 补 `relkind in ('r','p')`(排除视图/索引/序列),SQL Server 补 `xtype='U'`(排除视图/系统对象),MySQL 补 `table_type='BASE TABLE'`(排除视图);Oracle 改为 `user_tables`,与 `ExistViews/ExistField/ExistConst/AutoKeys` 统一为当前用户 schema 口径,避免其它 schema 同名表让 `ExistTable=true` 而 `AutoKeys` 查不到主键。
- **Oracle `AutoKeys` 复合主键顺序**:`user_cons_columns` 查询补 `order by cu.position`,复合主键的登记顺序不再依赖数据库返回序。
- **锁窗口收敛**:`mu` 改为内嵌 `sync.RWMutex`,删除包级 `muLazy` 懒初始化锁(每次查询不再抢全局锁);新增 `txGate` 串行化事务生命周期,`BeginTx`/`Commit`/`Rollback` 的驱动网络等待不再持注册表写锁。

### 升级注意

- `SelectS`/`SelectD`/`SelectF` 多行从"取最后一行"变为 `ErrMultipleRows`;条件写漏会显式失败,请补唯一性条件或改用 `SelectL`。
- `SelectH` 的 `f` 不能为 nil;需要固定键时请传显式函数。
- `Db` 未装配时执行入口返回 `ErrNoDatabase`(不再 panic);`Initialize()` 不再终结进行中的事务。
- 自定义 COPY 钩子在加密/验签激活时可能被拒;请改用默认列/值路径,或先关闭该表的加密/验签。
- **链式登记返回新快照**:`SetKeys`/`SetSecret`/`SetVerify`/`AutoKeys` 不再原地修改接收者。请始终使用返回值(链式调用天然满足);忽略返回值后继续读旧 `*TUniTable` 会看到旧元数据,`AutoKeys` 后请 `eng.GetTable(name)` 取新表。
- 调试输出不再包含实参值;若排障确需值,请在应用侧脱敏后记录。

## master-20261015

### 新增

- **`VerifySignRow` 应用侧存量回填入口**：对已注册结构体按当前字段值重算 MAC 并回填伴随字段，不组装 SQL、不产生任何写库动作。与写路径(Insert/Update/InsertL)共用同一签名实现，载荷版本(`VerifyMacVer` v1/v2)与 `VerifyHook` 口径天然一致——存量 `MAC:` v1 行的批量重签可按"读侧取行 → `VerifySignRow` 内存签名 → 应用自行批量落库 MAC 列"完成(评审 Q1 条件销账的迁移路径)。行须传指针:值传入时 MAC 无法回填，返回普通错误;未登记类/非结构体/`nil` 均前置报错;引擎未启用验签时直通返回且不改动 MAC 字段。

### 行为修复

- **写钩子与加密/验签**(行为收紧，曾为静默绕过):`Update`/`Insert`/`InsertL` 走自定义 SQL 写钩子(`HasGetSqlUpdate`/`HasGetSqlInsert`/`HasGetSqlInsertL`/`HasSetSqlValues*`)时，钩子自产的语句与实参曾完全绕过 `secretEncrypt`(secreton 列明文写库)/`verifySignRow`。现引擎解析钩子 SQL 的"列→实参"映射(`UniHookSql.go`,支持位置式 `?` 与编号式 `$N`/`:N`、单行与多行 VALUES):**加密侧**——激活的 secreton 列实参在执行前自动加密，钩子按列序给实参即与默认路径同口径，非 secreton 列字面量不受影响，实参数与 SQL 声明不符时维持 fail-fast;**验签侧**——激活时(宽容/严格一并)维持 fail-fast 且不执行 SQL(钩子语句无法附带 MAC 伴随列，读侧钩子行本就无法验签);**形态不可信解析**(secreton 列字面量/表达式赋值、未知结构)同样 fail-fast 不执行 SQL。`SaveIt`/`SaveItWhenNotExist` 的钩子类经 count-then-dispatch 回退走同一守卫。功能未激活时(`SecretOn=0` 且未验签)钩子路径放行不变。
- **读钩子与加密/验签**(行为收紧，与写钩子同批):`Select`/`SelectL`/`SelectM`/`SelectH` 走 `SetSqlResult` 结果钩子时——**验签侧**:激活验签的表(宽容/严格模式一并)返回 `ErrIncompleteVerification` 且不执行 SQL(此前仅严格模式拒绝，宽容模式下钩子行一律静默免验签，连 MAC 列齐全的有效行也不验)；**加密侧**:secreton 结果列在交接钩子前由引擎解密(曾静默交原始密文)，钩子与默认解码路径拿同一口径的明文数据，解密失败报错中断整次查询，`SecretOn=0` 时与默认路径同口径不解密。

## master-20261014

> **结构说明（2026-10-14）**：本段原为**三个同名的 `## master-20261014` 标题**（质量评审后续修复 / 代码质量评审修复 / 早期批次），读者无法判断哪个是当前段。现合并为**一个发布段**：原三组降为 `###` 主题，组内子标题相应降一级。**内容与顺序未改动。**

### 质量评审后续修复

- **SQL Server 配置校验变更**:开发者须填写 `ProvideName="mssql"` 或 `ProvideName="sqlserver"`,与 `sql.Open` 或连接器的实际驱动模式一致。`ProviderName()` 在 SQL Server 族返回该声明；`Initialize()` 按 `ProvideName × ColParam` 检查配置并在错误时返回 `ErrInvalidPlaceholderBinding`,不登记内置表。执行入口也检查当前配置。旧版的真实连接只读回显已移除,因此声明与实际驱动不一致无法被引擎发现；调用方必须保证配置一致。
- SQL Server 的 16 个真库子例覆盖原始绑定矩阵、有效组合读写和错误组合初始化拒绝。
- 同表 `RegisterTable/RegisterField/RegisterPkeys` 改为复制并发布元数据快照,允许与读取并发;已取得的旧指针继续使用旧快照。直接修改返回的 `*TUniTable` 仍须遵守并发契约。
- 缺省 `VerifyMacVer=0` 改为写 `MAC2:` v2 载荷;显式 `VerifyMacVer=1` 保留旧 v1 写法。读取仍按标签兼容 v1 行,但存量数据需要重签才能全面获得 v2 防歧义保证。
- `VerifyMode=2` 的结构体读取若缺保护列或 MAC 列,或使用无法由引擎验签的结果钩子,现在返回 `ErrIncompleteVerification`。
- 内置派生密钥缓存最多 128 项;冷解密仅在 GCM 认证通过后缓存派生结果。
- **`SetVerify` 之后主键冻结**(评审 Q2 的顺序变体,安全收紧):此前可在 `SetVerify` 成功后继续用 `SetKeys`/`AutoKeys`/`RegisterPkeys` 追加主键,使「主键必须全部纳入 MAC 载荷」这一保证失效(实测:事后追加的主键列被改写,原 MAC 仍通过验签)。现在这三种调用一律被拒并返回 `ErrPkeysFrozenAfterVerify`,且**不写入任何主键**;`AutoKeys` 的守卫置于最前,连元数据查询都不发起;`RegisterPkeys` 直接返回未注册表,**不克隆也不发布**,已发布快照的主键集合不变。被拒只影响后续登记,数据面读写与既有绑定不受影响。需变更主键请重新 `RegisterClass` 后按 `[SetKeys/AutoKeys → SetVerify]` 顺序登记;`VerifyOn=false`(含 `SetVerify` 失败)时主键不冻结,正常登记顺序不受影响。
- **`SetKeys` 拒绝只读标记字段作主键**(评估发现,行为收紧):`db:"-"` / `db:"_"` 的 `FieldName` 就是标记本身、**没有真实列名**,作主键会生成 `-=?` 这类非法 WHERE;而 `pkeyFields` 只过滤 `AttriName` 为空、**不过滤 `ReadOnly`**,故 `Update`/`Delete`/`SaveIt`/`SaveItWhenNotExist` 的 WHERE 会一并语法错误(真机实测 `Error 1064 (42000)`),`PrepareRunSQL(EtDelete)` 同理。现在登记期直接拒绝且不写入主键。**只拦标记字段**:`db:"real_col,readonly"` 有真实列名,作 WHERE 定位键依然合法(回归用例 `TestSetKeysRejectsReadonlyMarker` 含该正向对照)。其余入口本已封住——`RegisterPkeys("-")` 被标识符白名单拒绝,`AutoKeys` 取库内真实列名。
- **文档:可空列与 NULL 的读路径语义**(评估发现,仅文档):README 新增 `3.可空列与 NULL`,写明真机实测矩阵——不可空字段(`string`/`int64`/`bool`/`time.Time`)遇 NULL 会**报错且 `SelectL` 整批丢弃**(0 行,错误只给列下标与列名、不含行号);`[]byte`、`sql.NullXxx`、指针则正常。同时记明**引擎自身的两种相反策略**:结构体入口报错,而标量入口 `SelectS`/`SelectD`/`SelectF` 内部用 `sql.NullXxx`、NULL 时返回零值不报错。`doc.go` 同步补充。引擎不做静默降级(会把 NULL 变成 `""`/`0` 或让调用方无法区分"没有这行"与"读不动"),需部分成功语义时请在应用层按可空类型读入后判定。
- **回归测试补全与覆盖率门槛上调**(评估 T1 批次):新增 **14 条**用例——`PrepareRunSQLEtDelete` 四情形(正常/复合主键按登记序/无主键拒绝/未注册表)、分页路径(`InsertP` 页数与参数守恒、`CopyInP` 方言路由、`insertPage` 的 `Supplier=DtPOLODB` 分支、四个废弃别名)、元数据探测(`Exist*` 共用收尾的真/假分支、非法标识符、MySQL 缺 `DataBase`、`IfDropView` 两种情形、`ExecuteMust` 的 0 行/1 行/驱动错误)、以及 `verifyFormat` 的类型全覆盖与不支持 kind 的稳定报错。根模块语句覆盖率 **82.4% → 88.0%**：`UniMeta.go` 41.9%→91.9%、`UniCopyIn.go` 45.6%→97.1%、`insertPage` 0%→100%、`InsertPCtx`/`CopyInPCtx` 8.7%→91.3%、`verifyFormat` 30%→100%。CI 覆盖率门槛由 **75% 上调至 80%**。
- **清理与口径对齐**(评估 T1 批次):`TUniEngine.Instance`/`.DataUser` 与 `TUniTable.ParentTab` 标注 `Deprecated`(引擎内部零引用,设置无效);`ProvideName` 当时也曾标为无效,上方配置变更已重新启用它。`Supplier` 的注释改为其真实用途(`InsertP` 的 PolarDB→COPY 路由,且引擎从不赋值),`SqlSelect~SqlDelete` 注明是 `PrepareRunSQL` 的结果留痕;**`PrepareRunSQL(EtSelect)` 的 WHERE 改为只跳过只读标记字段**(经新增的 `isPkeyUsable`),使 `db:"col,readonly"` 这类**有真实列名的只读列**能作定位键,与 `Delete/Update/SaveIt` 的 `pkeyFields()` 口径一致(此前会被跳过,导致 WHERE 漏掉该主键)。
- **文档:钩子 SQL 形态契约**(README §4.2,仅文档):把原先"形态不可信解析 → fail-fast"一句话展开为精确清单——3 种场景行为(验签激活一律报错/加密未激活不干预/加密激活自动加密)、2 种**可解析**形态(`INSERT ... VALUES` 单行或多行、`UPDATE ... SET` 每段右值为单占位符;占位符 `?`/`$N`/`:N` 均可,列名可加引号、大小写不敏感、`WHERE` 段主键保持明文)、9 类**不可解析**形态(加密列字面量/表达式、VALUES 组含非占位符、组内数与列数不符、无列清单、实参数不符、**尾部子句 `ON DUPLICATE KEY UPDATE`/`RETURNING`/`ON CONFLICT`**、`MERGE`/`INSERT...SELECT`、列清单内注释、加密列实参非 string/[]byte)。并如实记录两处**已知边界**(列清单内注释被保守拒绝;未闭合字面量最后一个字符未掩码,仅非法 SQL 可达),修法各一行,三处(含编号式复用双层加密)已于同批修复,见下条。
- **测试:钩子解析地基补全**(评估 P1):新增 `UniHookSql_test.go`(12 条用例)——掩码的长度守恒不变量、字符串/双引号/反引号内部掩码与定界符保留、`''` 转义、**注释边界**(`--` 后无空白不算注释属 MySQL 语义、块注释、未闭合块注释、注释内引号不开启字符串、字符串内注释标记不开启注释)、掩码后关键词定位的**词边界**(`offset` 里的 `set`、`t_values_x` 里的 `values`、注释/字符串里的关键词不算命中)、`hookColKey` 归一化(去引号/转小写/点括号运算符一律拒绝)、占位符扫描与"声明实参布局取大"语义、**组内仅占位符**与 9 类不可信形态的拒绝、以及"按掩码下标回切原文取列名"的可定位性。`maskSQLLiterals` 覆盖率 **36.7% → 100%**、`hookColKey` **66.7% → 100%**、`findKeyword` **85.7% → 100%**、`indexAssign`/`matchParen`/`splitTopLevel`/`placeholderArgCount` 全部 **100%**、`hookWriteGuard` **69.6% → 87.0%**(补齐 `[]byte` 加密分支与"非 string/[]byte"拒绝分支);根模块语句覆盖率 **87.3% → 89.1%**。
- **统一两遍扫描的 `--` 注释口径 + 补解析地基用例**:抽取单一判定 `isLineComment`(`--` 之后须空白或行尾,MySQL 语义),由掩码副本(`maskSQLLiterals`)与列名剥离(`stripSQLComments`)共用——此前两者口径不同(掩码按 MySQL、剥离按 ANSI),同一输入会被作出不同解释,而"边界取自掩码、内容取自原文"的取数方式依赖两者一致。方向取 MySQL 是**保守**选择:`a--b` 在 MySQL 里是算术表达式,若当注释掩掉,其后可能是含占位符的活代码被一并掩掉(漏加密/漏映射);反之把真注释当代码只会让形态判定更严并 fail-fast(不静默)。新增 `TestStripSQLComments`(18 情形:块/行注释各位置、未闭合块注释、首个结束符、引号感知、`--` 后无空白的三种形态)与 `TestLineCommentSemanticsUnified`(8 情形,断言两遍扫描对同一输入的判定与结果一致):`stripSQLComments` 覆盖率 **73.7% → 100%**、新增 `isLineComment` 100%、根模块 **89.1% → 89.3%**。另把 `hookSecretPlan.Idx` 的注释改为与实现一致(实为**列优先**展开,顺序不影响结果,已保序去重),并去掉 `resolveInsertSecretArgs` 中恒被调用方覆盖的 `ArgCount` 初始化(旧语义残留)。

- **修复:钩子 SQL 解析三处边界**(评审复查确认后修复):①编号式占位符被多个加密列位复用(`values ($1,$1)`、多行 `$1,$2,$1,$2`)曾按重复下标逐次加密产出**双层密文**(读取侧解一次得到密文文本,静默错值)——现保序去重,同一实参只加密一次,实参布局按去重编号计(与 lib/pq·pgx·godror 的 NumInput 同口径);②列清单/SET 段内的注释曾令解析整体不可信而被拒——现取列名前按引号感知语义剥离注释,合法 SQL 正常解析;③未闭合字面量的最后一个字符未被掩码(`scanQuoted` 返回串长与掩码上界相差一位),残留字符恰为 `?` 时伪造占位符——现未闭合区间整体掩码。回归:三修复用例 + 12 条地基用例全绿。

### 代码质量评审修复（第一批 + 第二批）

依据 `docs/reviews/CODE_QUALITY_REVIEW_2026-10-14.md`(评分 68/100)分两批修复 6 项 P1;修复设计见 `docs/reviews/FIX_DESIGN_BATCH2_2026-10-14.md`。

#### 第二批(行为变更,升级须读)

- **`SetVerify` 强制覆盖全部已登记主键**(评审 Q2,破坏性收紧):此前复合主键只需保护其中一列即可登记,未保护的主键列被改写仍能通过严格验签。现在缺任何主键列都报 `LastError` 并列出缺失列;主键须先于验签登记(先 `SetKeys`/`AutoKeys` 再 `SetVerify`,顺序颠倒显式报错)。逃生门 `VerifyUnkeyed=true` 语义不变。
- **MAC 载荷 v2(评审 Q1,当时的第二批行为)**:该批引入 `VerifyMacVer=2` 以启用无歧义载荷;上方后续修复已将缺省写入改为 v2。读取仍按标签兼容旧 v1 行。迁移步骤见 README「MAC 载荷版本与防碰撞迁移」。
- **占位符转换改为 SQL 词法扫描**(评审 Q5,含破坏性报错):`$N` 转换只作用于代码区,字符串常量与注释里的 `$N` 不再被误改;MySQL 分支按占位符出现序**重排/复制实参**(乱序 `$2/$1`、重复 `$1` 均正确绑定),`$0`/编号超实参数/实参多余从"静默错绑"变为**显式报错**。内部方法 `getSqlQuery` 签名变更(非导出,不影响公开 API)。已知边界:不处理反斜杠转义(`'don\'t'`),请用 `''` 或原生占位符。

#### 第一批(可靠性,commit 4014962)

- `Cancel/Commit` 按事务终态语义**无条件**回收引擎事务引用:context 取消自动回滚、提交失败、回滚失败后实例可恢复并重新 `Begin`,原始错误保留(评审 Q3;此前一次超时即令共享实例永久滞留"事务中");
- 验签回填前检查可写性,按值传行时四个写入口(`Insert/Update/SaveIt/SaveItWhenNotExist`)返回普通错误而非反射 panic,失败先于 SQL 执行(评审 Q4);
- `RegisterClass` 登记期拒绝重复列 tag(含大小写归一化),报错列出冲突的两个 Go 字段名;`-`/`_` 标记豁免(评审 Q7;此前写入取第一个字段、读取填最后一个字段,读写不对称);
- 修正 `TestSetKeysUnregisteredFieldReturnsError` 假断言(检查表指针而非 `LastError`)。

### SQL Server 占位符：正确性取决于「驱动名 × ColParam」（文档澄清 + 真库实测）

在真实 SQL Server 2017 上对 `{mssql, sqlserver} × {":", "$", "?", "@p"}` 共 8 个组合做六步 CRUD 端到端验证后，澄清并补齐 SQL Server 占位符文档。**此前 README 只给 `ColParam` 取值、未说明它还取决于驱动名，是把使用者引向静默数据损坏的缺口。**

- **`README.md` 驱动表 SQLServer 行**：占位符由 `$` / `$1` 改为 `:` / `:1`（与生产环境所用驱动名 `mssql` 一致）。
- **新增 `README.md` `0.2.SQL Server 驱动名与占位符`**：给出对照表、两种驱动名的配置示例与危险组合警示：

| `sql.Open` 驱动名 | 引擎 `ColParam` | 实测结果 |
| --- | --- | --- |
| `mssql`（官方标记 deprecated，但仍注册） | `:` / `$` / `?` | ✅ 三者皆可：驱动内部经 `internal/querytext.ParseParams` 统一改写为 `@pN` |
| `mssql` | `@p` | ❌ `sql: expected 0 arguments, got N` |
| `sqlserver`（现代名） | `@p` | ✅ 唯一可用 |
| `sqlserver` | `:` / `?` | ❌ 服务端报语法错误 |
| `sqlserver` | `$` | ⚠️ **静默写错数据**（见「已知限制」） |

- **两种驱动名下不存在通用取值**：`@p` 在 `mssql` 下报 `expected 0 arguments`，而 `:`/`$`/`?` 在 `sqlserver` 下报语法错误或静默写错。故处置方式只能是「文档说明（本次）」+「按驱动名选择 `ColParam`」，**不能改为一个新的占位符常量**。
- 现代等价写法（与 `mssql` 同语义，同样只接受 `:`/`$`/`?`）：`sql.OpenDB(mssql.NewConnectorWithProcessQueryText(cfg))`。

### 禁止引擎按值拷贝（破坏性变更）

`TUniEngine` 新增内部类型 `noCopy` 的**具名字段**，使按值拷贝在 `go vet`（copylocks 分析器）阶段即被拦下，而非留到运行期：

- **原因**：`mu`/`secret` 是指针字段（拷贝时共享），而 `Db`/`ColLabel`/`ColParam`/`Provider`/`tx`/`inTx` 是值字段（拷贝时独立）。副本与原件因此出现「锁共享、事务状态不共享」的错配——副本 `Begin` 之后原件仍认为无事务，原件上的写入会**绕过该事务**直接落到连接池。
- **表现**：`passes lock by value: UniEngine.TUniEngine contains UniEngine.noCopy`；import 本包的外部调用方在各自 `go vet` 时同样会被提示。
- **破坏面**：新增导出结构体字段会使**非具名（unkeyed）复合字面量**编译失败；使用具名字段（`TUniEngine{Db: db, ColLabel: "db", ...}`）或 `*TUniEngine` 不受影响。本仓库已核查无 unkeyed 用法。
- **注意**：这是 vet 期约束而非编译错误；`go vet ./...` 已在本仓库 CI 门禁中。`README.md` 并发说明已同步补充该契约。
- 包内唯一按值拷贝点（测试助手 `newTestEngine()`）已改为返回 `*TUniEngine`。

### SQL Server 真驱动集成测试（integration/mssql_test.go）

新增 `integration/mssql_test.go`，把上述结论固化为可回归资产（库本体保持零第三方依赖，驱动只进独立模块）：

- `TestMSSQLPlaceholderContract`：8 个子用例固化「驱动名 × 占位符风格」绑定矩阵（如 `$1` 在 `sqlserver` 下解析为 money 字面量 `"1.0000"`、`@p1` 在 `mssql` 下报 `expected 0 arguments`）。
- `TestMSSQLRoundTripMatrix`：4 个可用组合各跑六步 CRUD（Insert / Update / SaveIt 插入 / SaveIt 更新 / InsertL / Delete）+ 绕过引擎的原生读回校验。
- 当时的 `TestMSSQLDefectSqlserverNoAt` 用于复现 `sqlserver` + 非 `@p` 的失效；后续修复已用常规用例 `TestMSSQLInitializeRejectsMismatchedBinding` 取代,断言初始化阶段拒绝错误组合。
- **环境口径**与 `pg_test.go` 一致：`UNIENGINE_MSSQL_DSN`，或分量 `UNIENGINE_MSSQL_HOST/PORT/USER/PASS/DB`（口令含 `@`/`#`/`$`/`%` 时自动 URL 转义）；不可达默认跳过，`UNIENGINE_MSSQL_REQUIRED=1` 则失败。
- `integration/go.mod` 新增 `github.com/microsoft/go-mssqldb v1.11.2`（连带 `golang.org/x/sync`、`golang.org/x/text` 上移）。
- **测试连接生命周期统一为 `t.Cleanup`**（在 `newEngine` / `openMSSQL` 内登记）：关闭登记在最早位置，按 LIFO 必定晚于建表后的 `DROP` 执行；此前用 `defer db.Close()` 会让 `DROP` 落在已关闭的连接上而静默失败（表泄漏）。

### 注册 API 标识符白名单（防注入）+ `RegisterPkeys` 返回 nil 缺陷

遗留注册表 API 此前完全不做标识符校验，而它们的参数会被**直接拼进生成的 SQL**（CRUD 表名不引号；MySQL/Oracle 族的列名亦不引号），构成一处与写路径/`Exist*` 不一致的注入面。本批补齐：

- **新增校验**：`RegisterClass`（表名 + struct tag 列名）、`RegisterTable`、`RegisterField`、`RegisterPkeys`（表名与字段名）统一走 `validIdent`；`db:"-"` / `db:"_"` 是只读标记而非列名，**豁免**校验。
- **新增哨兵 `ErrInvalidFieldName`**（表名沿用 `ErrInvalidTableName`），便于调用方 `errors.Is` 区分表名/字段名违规。
- **零破坏修法**：非法输入不再注册，改为返回**带 `LastError` 的未注册表**（沿用 `6f771bf` 为 `RegisterClass` 引入的链式约定），四个方法的签名与调用点均不变；链式调用（`.SetKeys()` 等）在 `LastError` 上短路。
- **`RegisterPkeys` 表未登记时不再返回 `nil`**：旧实现在表不存在时返回 `nil`，链式 `.SetKeys()` 会 **nil 解引用 panic**（实测复现）。现返回带 `ErrUnregisteredClass` 的未注册表。
- **`Initialize()` 内部登记名改为合法标识符**：旧值 `github.com/kazarus/uniengine/unitable`（含 `/`）会被新的白名单拒绝，改为 `unitable` / `unifield`。该键仅作小写别名（另以类全名 `t.String()` 注册），不影响任何查找路径。
- **新增回归测试**：`TestRegisterRejectsInvalidIdentifiers`（6 类非法输入逐个断言哨兵 + 非法名不进注册表 + `PrepareRunSQL` 不产出可注入 SQL + 只读标记不被误杀 + 合法注册正向对照）、`TestRegisterPkeysMissingTableNoPanic`。

### 文档更正（README）

- **表名/字段名白名单校验的范围**：原文「**所有**拼入 SQL 的表名/字段名……」在遗留注册表路径上不成立。补齐校验后重写为准确表述：运行时传入的名字（`Register*` 参数、写方法 `TableName`、`Exist*`）与 struct tag 列名均在覆盖范围内。
- **加解密吞吐**：原文「4KB 档 GCM 吞吐约 0.8-1.2 GB/s」对加密档偏乐观。按实测更正为**加密约 0.75 GB/s、解密约 1.17 GB/s**（16B 档约 23 / 39 MB/s）。

### 代码风格清理与包级 godoc

- **清除工具化残留（零语义变化）**：删除 **30 处**「注释块 + 空行 + 同一注释块」的重复文档注释（合计 65 行冗余——此前批量改写脚本的产物），以及 **200 处**「开括号后紧跟空行」。库本体、`integration/`、`contrib/pgxcopy/` 全部归零，`gofmt` 稳定。
- **新增包级文档 `doc.go`**：此前 17 个源文件的头注释（`// UniQuery`、`// #查询:...`）紧贴 `package` 子句，被 go/doc 视为 17 段包文档**拼接**在一起，`go doc .` 输出为无结构的碎片。现：
  - 新增唯一 `doc.go` 承载正式包文档，覆盖**快速开始 / 方言与占位符（含 SQL Server 驱动名陷阱）/ 并发契约（含禁止按值拷贝）/ 错误处理（哨兵清单）/ 应用加密与存储完整性 / 模块布局**；
  - 其余 17 个文件的头注释与 `package` 子句**解耦**（插入空行），不再污染包文档。`go doc .` 现只输出 `doc.go` 一段。
- **新增用法示例 `example_test.go`**：`Example`（装配 → 登记 → 写入/查询/事务）与 `ExampleTUniEngine_Secret`（应用加密与 `SecretHook` 接管）。均为**编译期校验**（不带 `Output:` 注释，故 `go test` 只编译不执行，无需数据库）。
- 验证：`gofmt -l` 空、`go vet ./...` 通过、全量单测 + `-race` + 真库 MSSQL 集成测试全绿。

### Oracle 真驱动集成测试（integration/oracle_test.go）

补齐 Oracle 协议族的真库验证——此前该族只有「SQL 文本比对」级覆盖，是测试维度最大的结构性缺口。在真实 **Oracle Database 11g Enterprise Edition 11.2.0.1.0** 上全部通过：

- **驱动**：`github.com/sijms/go-ora/v2`（**纯 Go**，无需 Oracle Instant Client）；README 记录的生产驱动 `godror` 亦支持（`UNIENGINE_ORACLE_DRIVER=godror`）。引擎对 Oracle 族生成 `:N` + 裸标识符，两驱动一致。
- `TestOraclePlaceholderContract`：`:1` 真绑定；`?` / `$1` → `ORA-00911`，`@p1` → `ORA-00936`。
  - **重要对照**：Oracle 上用错 `ColParam` 是**硬报错**，**不存在** SQL Server 那种「`$N` 被当字面量静默写错」的形态。
- `TestOracleRoundTrip`：Insert / Update / Delete / `SaveIt`（`MERGE` 的 matched 与 not matched 两条分支）/ `SaveItWhenNotExist`（已存在时不变）/ `InsertL`（**`INSERT ALL` 多行语法**）/ `SelectL`，均逐行绕过引擎读回校验。
- `TestOracleWrongColParamFailsLoudly`：配错 `ColParam` 时引擎报错且**无任何行被写入**（显式对照 SQL Server 的静默写入）。
- `TestOracleMetadata`：`ExistTable` / `ExistViews`（视图与表互不误判）/ `ExistField` / `ExistConst`（显式命名主键）/ `AutoKeys(TAutoKeys4ORACLE)`，覆盖引擎使用的全部 Oracle 数据字典视图（`all_tables` / `user_views` / `user_tab_columns` / `user_constraints` / `user_cons_columns` / `user_tab_cols`）。
- `TestOracleSecretRoundTrip`：`secreton` 列在 `varchar2(1024)` 中落库为 `ENC:` 密文，经引擎读回还原为明文。
- 环境口径与 pg/mssql 一致（`UNIENGINE_ORACLE_*` 分量或整串 DSN + `_REQUIRED=1`）；只操作自建表 `uni_it_ora_probe` 与视图 `uni_it_ora_probe_v`（含显式命名主键），结束时 DROP——已复查确认无残留对象。
- `integration/go.mod` 新增 `github.com/sijms/go-ora/v2 v2.9.0`；库本体仍零第三方依赖。README 第 6 节改为按族列出三个集成测试文件与各自的运行方式。

### 贡献指南与英文摘要（面向外部贡献者）

- 新增 **`CONTRIBUTING.md`**（英文主体，文末附中文要点）：项目布局与「库本体零依赖」硬约束；构建/测试/CI 门禁命令；**有意非常规的编码风格**及其处置原则（要改就单独一次机械式提交并配 `gofumpt`/`revive`，不要零散改名）；**6 条不得回归的不变量**（公共入口不 panic / 形态校验用可赋值性而非 `Kind` / 注册 API 不返回 `nil` / 标识符白名单 / 引擎不得按值拷贝 / 缓存键须覆盖全部输入）；**新增方言的 5 个接入点**；**占位符与驱动名规则**（含 SQL Server 静默写错陷阱，明确要求真库验证）；集成测试的环境变量矩阵与两条清理规则（`t.Cleanup` 登记顺序、只动自建 `uni_it_*` 对象）；提交与 PR 检查清单；报 bug 需附的信息。
- **README 增加英文摘要**：补齐缺失的 H1 标题，并以引用块给出英文概览（定位与零依赖、协议族、context 变体、应用加密与存储完整性、并发契约与禁止按值拷贝、**SQL Server 占位符陷阱**、测试规模），链接到 `CONTRIBUTING.md`。

### MySQL 真驱动集成测试（integration/mysql_test.go）

- 新增 `integration/mysql_test.go`：
  - `TestMySQLPlaceholderContract`：`?` 真绑定；`:1` / `$1` / `@p1` **不得等于实参**——注意 `@p1` 在 MySQL 里是**用户变量**（句法合法、值为 NULL），故不能断言「必然报错」，只能断言「绝不绑定」；这与 SQL Server 的 `$N`（money 字面量）同属「句法合法的错误占位符」，值得固化。
  - `TestMySQLRoundTrip`：六步 CRUD，含 `SaveIt` 的 **count-then-dispatch 回退路径**（MySQL 不在 `upsertStmt` 内，因为任一唯一键都会触发 `ON DUPLICATE KEY`，与主键语义不等价）与 `SaveItWhenNotExist` 不覆盖已存在行。
  - `TestMySQLMetadata`：`information_schema` 探测（`ExistTable`/`ExistViews` 走 `views` 而非 `tables`/`ExistField`/`ExistConst(CtPK,"PRIMARY")`/`AutoKeys(TAutoKeys4MYSQLN)`），需引擎 `DataBase` 有值。
  - `TestMySQLSecretRoundTrip`：`secreton` 列落库为 `ENC:` 密文并经引擎还原。
- 连接超时设为 15s（覆盖「DNS 解析 + TCP 连接」；公网域名解析偶发偏慢）。
- `integration/go.mod` 新增 `github.com/go-sql-driver/mysql`；库本体仍零第三方依赖。
- **已在真实 MySQL 上全部通过**（**MySQL 8.4.6**，默认认证插件 `caching_sha2_password`；4 用例 / `-race`）。实测的占位符行为，与 SQL Server 恰成对照：
  | 写法 | 实测结果 |
  | ---- | -------- |
  | `?` | 真绑定 |
  | `:1` | `Error 1064` 语法错误 |
  | `$1` | `Error 1054 Unknown column '$1'` |
  | `@p1` | `sql: expected 0 arguments, got 1`（`@p1` 是**用户变量**，句法合法故服务端不报错，由驱动按「0 个占位符」拒绝实参） |
  即 MySQL 上三种错误风格**都不绑定实参**，但失败层次不同（服务端语法错 / 服务端列不存在 / 客户端参数计数）——**均非静默写错**。至此 **PostgreSQL / SQL Server / Oracle / MySQL 四族的真驱动集成测试全部就位**（PG 由 CI 的 service 容器执行）。

### CI：MySQL 纳入每次 push + 覆盖率门槛 + fuzz + 重容器定时/手动

- **新增 `integration-mysql` job**（`mysql:8.4` service 容器，与实测过的 8.4.x 对齐）：`UNIENGINE_MYSQL_REQUIRED=1` 保证不可静默跳过；同时设 `UNIENGINE_MYSQL_DB`（`Exist*`/`AutoKeys` 走 `information_schema` 需要库名）。PG job 保持原样（轻量容器，每次 push）。
- **新增覆盖率门槛**：`build-test` 的测试步骤产出 `-coverprofile`，门禁为 **75%**（当前 80.7%，留 5 个点回撤空间），防止静默劣化；`-race` 下使用 `-covermode=atomic`。
- **新增 fuzz 冒烟**（见下节）：矩阵中每个 Go 版本各跑 10s/目标；seed 语料则随每次 `go test` 执行。
- **新增 `integration-heavy` job**（SQL Server 2022 + Oracle Free 23）：容器各需 1–2 GB 内存、30–90s 就绪，故走**每日定时 + 手动触发**而非每次 push——不阻塞 PR，但次日即可暴露腐化。实现要点：用 `docker run` 而非 `services:`，以便在 shell 内轮询就绪并在超时/失败时**打印容器日志**；`sqlcmd` 路径按镜像版本（`tools18`/`tools`）探测；Oracle 使用镜像自带的 `/opt/oracle/healthcheck.sh`。若 runner 资源充足，放宽该 job 的 `if` 即可转为常态。
- 校验方式：YAML 经解析校验（4 job / 4 触发器）；**CI 引用的 `UNIENGINE_*` 变量与测试代码实际读取的变量做集合比对，无多余、无拼错**；覆盖率门槛命令、fuzz 命令、基准 smoke、以及 CI 走的「整串 DSN」代码路径均已在本机对真实库跑通。**SQL Server/Oracle 两个重容器 job 无法在本机验证**（本机无 Docker），首次定时运行可能需要按 runner 环境微调。

### 模糊测试（fuzz_test.go）

- `FuzzValidIdent`：把「防注入白名单」的性质写成可自动探索的不变量——**凡 `validIdent` 放行，必不含任何危险字符**（引号/分号/空白/注释起手/反斜杠/`$`/`?`/`:`/`@`/反引号等），且长度非 0、仅由白名单字符构成。反向不变量（白名单外一律拒绝）由既有 `TestValidIdent` 用例表覆盖。
- `FuzzUniFieldInitialize`：tag 解析的两个不变量——`FieldName` 永不含逗号；首段为 `-`/`_` 时必置只读。
- seed 语料随 `go test` 执行；随机探索需显式 `-fuzz`（CI 中 10s/目标）。**本机 10s 实测各约 750 万次执行、未发现反例**。

### 密文搬移防护的设计边界（文档化）

外部评估指出：GCM 密文未绑定 AAD（表名/主键/列名），库内把 A 行的密文贴到 B 行，纯加密读取路径不会发现。经设计评估，该防护**由已实现的 `SetVerify` 存储完整性校验承担**而非在加密层重做——其 HMAC 载荷域分隔含表名、覆盖主键与保护列，正是行级绑定所需，且两者叠加即闭合该攻击面。加密层保持无上下文（`SelectS` 标量自动解密依赖此特性）。README 加密章节已写明该边界与配合方式。

### 加密写路径迭代数越界 fail-fast（评估 §4.7）

- 缺陷：写路径只做「≤0 取缺省」**不设上限**，而读路径强制 `Iter ≤ secretIterMax`（1e7）。配置越界时引擎会**成功加密却永远读不回**自己的密文（数据丢失级，降回旧配置也无效）；`SecretIter ≥ 2³²` 时内嵌迭代数还被 `uint32` 静默截断；写入侧另有 PBKDF2 线性空转（2³² 次迭代约 6.4 分钟）。既有测试 `TestSecretRejectsAbsurdIterations` 只覆盖**读**侧，从未测写侧。
- 修复：`SecretDefault` 写分支在派生前校验上界，越界返回 `UniEngine: SecretIter %d exceeds max %d; ciphertext would be unreadable`（**fail-fast**，不产出坏数据）。
- 回归测试 `TestSecretWriteRejectsOversizedIter`：边界值 `secretIterMax` 必须可往返；越界必须报错且不得返回密文。**该测试在修复前失败**（已双向验证）。

### 派生密钥缓存键并入 SecretBy（评估 §4.6 / §4.9）

- 缺陷：缓存键原为「盐 + 迭代数」，**漏了 `SecretBy`**，导致运行时更换密钥仍命中旧派生密钥——新旧数据全线继续用旧密钥，轮换既不生效也不报错。
- 修复：键改为「盐 + 迭代数 + **密钥指纹**（`sha256(SecretBy)[:8]`）」。以指纹而非明文入键，避免在缓存映射里额外留存一份密钥明文副本；缓存项数仍以「每份盐 + 迭代数 + 密钥」为界，有界。
- 回归测试 `TestSecretRotateKeyTakesEffect`：同进程轮换后，新密文必须只能由新密钥解开，且旧密文必须报错。
- **文档更正**：README 原称「带标签但密钥错误的密文会解密报错（不静默）」——该断言在修复前**同进程轮换场景下不成立**。现补充「轮换立即生效」与「密文不含密钥标识，轮换后旧密文需配套迁移（旧密钥读出、新密钥重写）」两段。

### SetVerify 行级绑定改为登记期强制（评估 §4.8）

- 缺陷：MAC 载荷只覆盖**显式列出的**保护列，主键不会自动并入。此前若只登记非键列，把 A 行的数据连同 MAC 一起搬到 B 行仍能通过验签——**行级绑定失效**，而 README/CHANGELOG 称其「覆盖主键」。
- 修复：`TUniTable.SetVerify` 登记期要求保护列中至少含一个已登记主键，否则记入 `LastError`（`needs at least one pkey among protected fields for row binding`）。新增逃生口 `TUniTable.VerifyUnkeyed`（在 `SetVerify` 之前置 `true`）供确有理由不绑定行标识的场景。
- 选择「登记期拒绝」而非「自动并入主键」，是为了**不改变载荷构成**——已按合规方式登记的表，其存量 MAC 仍然有效，避免升级瞬间全表验签失败。
- 测试：`TestSetVerifyRequiresPkeyForRowBinding`（拒绝 / 含主键放行 / 逃生口三条路径）；`integration/mysql_test.go` 新增 `TestMySQLVerifyRowBinding`，在真库上验证「整行搬运被检出」与「篡改保护列被检出」。

### 仓库卫生门禁（style_test.go）

- 把两类机械残留（行尾 `{` 后紧跟空行、注释块 + 空行 + 同一注释块）固化为可执行断言，随 `go test` 在本地与 CI 矩阵中执行；遍历库本体与 `integration/`、`contrib/`。
- 背景：这两类残留曾被批量清理（v9），随后又被新代码引入 **19 处**——约定只写在文档里会漂移。
- 判据排除原始字符串与块注释内部以避免误报；已用临时构造的违规文件验证两处门禁**确实能拦住**。

### 文档：MySQL 元数据 API 的前置要求（评估 §4.10）

- README 的 MySQL 初始化示例补上 `UniEngineEx.DataBase`：`ExistTable`/`ExistViews`/`ExistField`/`ExistConst`/`IfDropView` 与 `AutoKeys` 在 MySQL 族走 `information_schema`，需库名，否则报 `UniEngine: database is not specified`（其余族由连接或库自身元数据获得，无需该字段）。

### 已知限制

> **注**：本节属早期条目，保留原样以便追溯。带 **【已被后续修复取代】** 标记者**不再代表当前行为**——当前行为以本文档靠前的修复段与代码为准。

- **【历史条目，当前行为见上文】SQL Server 占位符与驱动名强耦合** —— 中间版本曾在 `Initialize()` 用真实连接做只读参数回显；当前已按开发者指定的 `ProvideName × ColParam` 静态检查,不再连接数据库验证实际绑定。配置不一致返回 `ErrInvalidPlaceholderBinding`,但错误声明的 `ProvideName` 无法自动发现。**以下为原文留痕：**
  > ~~`TUniEngine` 只持有 `Db *sql.DB` 与 `ColParam`，而 `database/sql` 不暴露驱动名，故无法在 `Initialize()` 校验组合一致性。配置不匹配时：`sqlserver` + `$` **静默写错数据**（无任何错误提示），`sqlserver` + `:`/`?` 报语法错误，`mssql` + `@p` 报 `expected 0 arguments`。请按 `README.md` 0.2 节选择 `ColParam`。~~
- **`sqlserver` + `$` 的历史数据需审计（本条仍然有效）**：该组合下写入的值取自占位符序号（`$1`→1、`$2`→`"2.00"`），INSERT 写错值、UPDATE 改错行、DELETE 删错行，且引擎一律返回 `nil`。可疑值形如 `"1.00"`/`"2.00"` 的数值型字符串。

## master-20261013

### 存储完整性校验（UniVerify）

面向等保/密评"访问控制信息存储完整性"要求：对注册类的保护列做**写时签名、读时验签**（HMAC 伴随列方案）。

- **登记**：`RegisterClass(T, "tbl").SetKeys(...).SetVerify("mac_text", "col1", "col2", ...)`。`mac_text` 为 MAC 伴随列（string 字段，建议 `varchar(64) NOT NULL DEFAULT ''`），保护列按登记顺序参与载荷；载荷以注册表名做域分隔（跨表同值 MAC 互异），格式 `MAC:` + base64。
- **写路径**：`Insert`/`Update`/`InsertL`/`CopyInL`/`SaveIt`（含 `SaveItWhenNotExist`；`CopyInP`/`SpecialInsertL*`/PolarDB `InsertP` 分页均汇入 `CopyInLCtx`/`InsertLCtx`）组装 SQL/COPY 数据前按结构体当前值重算 MAC 回填伴随字段；幂等，`SaveIt` 回退链重复调用无副作用。注意 `Update` 是整行语义，更新前须先读后改（与本引擎既有契约一致）。
- **读路径**：`Select`/`SelectL`/`SelectM`/`SelectH` 在解密后逐行验签（secreton 列参与载荷的是明文）；SELECT 未覆盖全部保护列+MAC 列时,宽容模式沿用跳过验签,严格模式返回 `ErrIncompleteVerification`；比对用 `crypto/hmac.Equal` 常量时间比较，失败即报错（文案不含载荷与行值）。
- **算法**：内置 HMAC-SHA256（密钥 `VerifyBy`）；赋值 `VerifyHook` 可接管为 HMAC-SM3/密码机等应用实现。
- **模式**：`VerifyOn` 引擎级总开关（0=关）；`VerifyMode` 1=宽容（无标签存量行直通，回填期用）→ 2=严格（无标签行报错，回填完成后切换）；非空但无标签的值一律报错。
- **范围外**（与 secreton 同口径）：自定义 SQL 钩子类（`HasGetSqlUpdate`/`HasGetSqlInsert`/`HasSetSqlValues[L]`/`HasSetSqlResult`）与裸 `Execute` 语句不经签名/验签。

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
| `GetSqlAutoKeys(TUniEngine, string) string` | `GetSqlAutoKeys(TUniEngine, string) (string, error)` | `HasGetSqlAutoKeys` 接口及实现返回值增加 `error` |
| `SetKeys` / `SetSecret` 返回 `error` | 返回 `*TUniTable`（链式） | 错误改记入 `TUniTable.LastError`（首错短路后续登记），链尾 `if tbl.LastError != nil` 检查：`RegisterClass(...).SetKeys(...).SetSecret(...)` |
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
  - 结构体 tag `db:"field,secreton"` 或 `.SetSecret("field")` 标记敏感字段；
  - `TUniEngine.SecretOn` / `SecretBy` 控制开关与密钥；
  - `SecretHook` 自定义加密钩子，默认内置 AES-256-GCM（密文带 `ENC:` 标签）；
  - 读取自动解密、存量明文（无标签）自动鉴别直通；
  - 非 `string` 字段标记 `secreton` 时写入 `fast-fail` 报错；
  - **`TUniTable.SecretOn`**（表级加密标记）：`SetSecret` 或 `RegisterClass` 检测到 tag 标记的加密列时置位，形成 `TUniEngine.SecretOn`（引擎级）→ `TUniTable.SecretOn`（表级）→ `TUniField.SecretOn`（列级）三级标记；`TUniTable.ColSecret []TUniField` 登记加密列清单（tag 路径按类声明序、SetSecret 按调用序，重复登记自动去重）。
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
- **主键值一律明文（secreton×主键互斥）**：主键是 WHERE 定位行的标识，值保持明文才能与库中存量匹配。`SetSecret` 对已登记主键的字段返回错误，`SetKeys`/`AutoKeys` 对已标记 `secreton` 的字段返回错误（双向守卫）；写入路径（`Insert`/`InsertL`/`CopyInL`/`saveUpsert`）经 `secretColumn` 对主键列跳过应用加密，兜底防御绕过守卫直接改注册表的用法——`SaveIt` 与 `Update` 对同一行的定位语义从此一致。
- **写方法 nil 指针守卫**：`SaveIt`/`Update`/`Insert`/`Delete`/`SaveItWhenNotExist`（含 `*Ctx` 变体）传入 typed nil 指针时返回明确错误（此前反射对零值 Value 取 `Interface()` 直接 panic）。

### 密钥派生加固（评审加固）

- **内置加密的密钥派生从单次 SHA-256 升级为 PBKDF2-HMAC-SHA256**（标准库 `crypto/pbkdf2`，`go.mod` 最低版本 1.21 → 1.24，仍零第三方依赖）：低熵密钥不再可直接离线暴力破解，缺省 600000 次迭代（OWASP 2023 建议值），新增 `TUniEngine.SecretIter` 可调。
- **密文格式 `ENC:`** + base64( 盐(16) + 迭代数(4,大端) + 随机nonce + GCM密文 )：盐与迭代数内嵌密文，读取自包含（跨进程/跨密文可直接解密）；密文内嵌迭代数做合法性校验（≤0 或超上限拒绝），防损坏或恶意密文触发超高开销派生。
- **派生密钥缓存**：按"盐+迭代数"缓存在引擎内（指针字段，按值拷贝引擎时共享），每进程每份盐只付一次 PBKDF2 开销；首个到达的调用持锁派生，并发等待者直接命中缓存。行级加解密本身仍是纯 AES-GCM，读写吞吐不受影响。
- **单标签 `ENC:`**：项目未投产、无存量密文兼容负担，双标签（`ENC2:`）与旧格式（单次 SHA-256 派生）读兼容路径已删除，加密格式只有一种。
- `SecretHook` 自定义钩子路径不受影响（钩子完全接管加解密时，格式由应用自定）。

### 基准测试（benchmark_test.go）

`go test -bench . -benchmem -run '^$'` 可复现，覆盖行扫描热路径与内置加解密：

- **`BenchmarkSelectLScan` vs `BenchmarkSelectLScanLegacy`**：现行"列→字段下标预解析"路径与旧算法复刻（每行每列 `FieldByName` + `HashField` 查找）走同一 mock 传输，差值即该优化的净收益（Apple M3 Ultra 实测 1000 行×10 列：320µs vs 777µs，**约 2.4×**，分配次数少 1/3）。
- **`BenchmarkSelectLScanDecrypt`**：行扫描+每行解密 1 个加密列的联合路径，解密增量与独立解密基准吻合（约 0.4µs/值）。
- **`BenchmarkSecretEncrypt/Decrypt`**：稳定态（派生密钥缓存命中）按 16B/256B/4KB 分档；4KB 档 GCM 吞吐约 0.8-1.2 GB/s。
- **`BenchmarkSecretDecryptPlaintext`**：存量明文直通（零分配，约 2ns）。
- **`BenchmarkSecretPBKDF2DeriveCold`**：一次性冷派生开销（缺省 600000 次迭代，实测约 53ms）——生产中每进程每份盐只付一次，行级吞吐不受影响。

### CI（.github/workflows/ci.yml）

push / pull_request 触发，Go 版本矩阵（`1.24.x`/`1.25.x`/`1.26.x`/`1.27.x`，1.24 为 go.mod 下限）× ubuntu-latest，依次执行 build → vet → gofmt 检查 → `go test -race -count=1` → 基准冒烟（`-benchtime=1x` 编译并各执行一次，防基准代码随重构腐化）。新 Go 版本发布后在矩阵追加一行即可。仓库零第三方依赖（无 go.sum），故关闭 setup-go 依赖缓存。

### PostgreSQL 真实驱动集成测试（integration/ 独立模块）

新增 `integration/` 目录为**独立 Go 模块**（自带 go.mod，`replace` 指向库本体），引入 `github.com/jackc/pgx/v5/stdlib` 作为**仅测试依赖**——库本体（根模块）保持零第三方依赖、go 1.24 下限不变，pgx 的传递版本要求被隔离在集成模块内。此前所有测试基于自实现 mock driver，真实驱动路径（COPY 协议、UPSERT 语句、加密密文落库）从未被验证过，本组测试补上这一层：

- **CRUD**：Insert/SelectL/Select/SelectS/SelectD/Update/Delete 全链路；
- **原生 UPSERT**：SaveIt 的插入/更新两分支、SaveItWhenNotExist 的插入/跳过两分支（`on conflict` 真实执行）；
- **COPY 协议**：CopyInL 批量 120 行、CopyInP 按 PageSize 分页（自实现 `copyInStmt` 对 pgx 的兼容性由此验证）；
- **应用加密**：Insert/Update/SaveIt 后库中为 `ENC:` 密文（同明文两次加密密文不同）、SelectL/Select 自动还原（含中文）、存量明文直通、COPY 路径加密；
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

### 行为修复（随 downstream 侧改动同步）

- **`SelectS` 标量路径自动解密**：返回前经 `Secret` 走与列路径同一套语义——`SecretOn=0` 直通、带 `ENC:` 标签的密文还原、存量明文（无标签）直通；查加密列的标量查询不再返回密文（存储形态断言请绕过引擎用原生 query）。`SelectD/F` 为数值不涉及。

- **单 struct `Select` 改为按列合并**：`queryRowsCtx` 的 sink 增加 `fieldIdx` 参数（列→字段下标预计算结果），非钩子类的单 struct 目标仅用 SELECT 返回列覆盖目标字段，**未出现在结果列里的字段保留调用方原值**——修复"SELECT 少量列灌回业务结构体把其余字段清零"的问题（如 VerifyDX 的 `FROM_WHO_BUILD` 被清零）；钩子类仍整结构体替换（填充范围由类自身 `SetSqlResult` 决定）；零行结果不触发 sink，目标原样保留。`SelectL/M/H` 行为不变（sink 忽略新增参数）。新增 `UniQuery_test.go` 回归测试（3 例：未选列保留/零行保留/全零基线）。

### 健壮性加固（评审 P0：消除全部反射 panic 路径）

外部评估报告（`docs/reviews/UniEngine-评估报告.md`）以探针实测确认 17 条可复现 panic 路径（13 个公共 CRUD 入口对裸 `nil`、4 处反射类型 panic），本批全部消除：

- **新增哨兵 `ErrNilParam`** 与统一校验助手 `nilParamOf`：裸 `nil` 与 typed nil 统一判别返回 `ErrNilParam` 包装错误。**判断先于任何 `reflect.TypeOf/Kind`**——裸 `nil` 时 `TypeOf` 返回 nil Type，此前写路径的 typed-nil 守卫排在 `Kind()` 之后永远执行不到（根因即判断顺序）。
- **覆盖全部 13 个入口**：写路径 7 个（经 `resolveTarget`）+ `Select`/`SelectL`/`SelectM`/`SelectH` + `InsertP`/`CopyInP`。
- **`Select` 传值（非指针）返回明确错误**（"needs a pointer param"），替代逐列回写时 `reflect.Value.Set` 不可寻址 panic。
- **`RegisterClass` 类型守卫**：裸 `nil`（`ErrNilParam`）与非结构体（map/指针，明确 kind 报错）返回**带 `LastError` 的未注册表**（沿用链式登记约定，不破坏 `*TUniTable` 签名），非法类型不再进入注册表，`NumField` 反射 panic 消除。
- **测试扩面**：`TestAllEntriesRejectBareNil`（13 入口逐一断言 `errors.Is(ErrNilParam)` 且 recover 兜底不得 panic）、typed nil、`Select` 传值、`RegisterClass` 三类非法输入。既有 `TestWriteMethodsRejectNilPointer` 保留。
- **P0 收尾（剩余 7 条 panic 路径）**：`SelectL` 传值切片（Append 需可寻址）返回 "needs a pointer param"；`SelectM`/`SelectH` 对 **nil map**（直接传入与经指针间接两种形态）返回 "got a nil map"，对**非 string 键**（`GetMapUnique` 返回 string，键类型不符会 `SetMapIndex` panic）返回 "needs a map with string key"；正向对照测试确认合法形态（string 键 + 已初始化 map，直接与指针两种传参）行为不变。
- **键语义三分支（对抗性复查修正）**：`Kind()==String` 只是"类型种类"近似——命名字符串键（`type MyKey string`，Kind 仍为 String）此前守卫放行后在 `SetMapIndex` 处 panic（string 不可赋值给 MyKey）；`map[any]T` 则 string 本可赋值却被 Kind 误拒。修正为三分支：**string 可直接赋值（any/空接口）→ 直写；命名 string 类型 → 写时 `Convert`；其余 → 报错**（消息含 "assignable from string"）。新增 3 用例（命名 string 键 / `map[any]T` / 非 string 非命名类型），打破"测试用例与 Kind 判断同构"的覆盖盲区。

### 已知限制

- **pgx 驱动下 `CopyIn*` 需接 `CopyInHook`**（`contrib/pgxcopy.Hook`）：pgx stdlib 不模拟 lib/pq 逐行 COPY 约定，不接钩子报 `expected 0 arguments`；lib/pq 兼容驱动不受影响。事务期间 `CopyIn*`（钩子路径）被显式拒绝。
- **事务期间调用须串行**（`Begin` 与 `Commit`/`Cancel` 之间）：底层 `*sql.Tx` 非并发安全，期间所有语句都路由到该事务。
- 配置字段（`ColLabel`/`ColParam`/`Provider`/`SecretOn`/`SecretBy` 等）与表结构变更（`SetKeys`/`SetSecret`/`AutoKeys`/`PrepareTables`/`PrepareRunSQL`）应在并发查询开始前完成。
- **`secreton` 不适用于主键字段**：主键值必须明文才能参与 WHERE 定位与冲突匹配；`SetSecret`/`SetKeys`/`AutoKeys` 会显式拒绝该组合，请勿对主键字段标记 `secreton`。
- `CopyIn*` 的 COPY 协议由包内 `copyInStmt` 自实现，**不再依赖已停维护的 `github.com/lib/pq`**。
