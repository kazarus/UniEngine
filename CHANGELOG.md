# 更新日志

本文档记录相对旧版（master 基线）的**破坏性变更**与主要新增，供升级参考。

## Unreleased — ai-dev-20260824

三线融合：`context.Context` 支持 + 应用层加密 + 加固。

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

- **移除 `github.com/lib/pq` 依赖**（模块归零第三方运行时依赖）：新增 `quoteIdent`（标识符双引号转义）与 `copyInStmt`（自实现 COPY IN 协议语句，输出与 `pq.CopyIn` 逐字一致）；`go.mod` 清空 `require`、删除 `go.sum`。
- **目录/钩子接口首参指针化**（实现方需同步改动）：`Has*` 系列接口与目录/钩子生成器首参由 `TUniEngine` 改为 `*TUniEngine`，涉及 `GetSqlExistTable` / `GetSqlExistViews` / `GetSqlExistField` / `GetSqlExistConst` / `GetSqlAutoKeys` / `GetSqlUpdate` / `GetSqlInsert` / `GetSqlInsertL` / `SetSqlValues` / `SetSqlValuesL` / `CopyInGetSqlInsertL` / `CopyInSetSqlValuesL` / `SpecialGetSqlInsertL` / `SpecialSetSqlValuesL` / `SetSqlResult` 等。
- **移除未使用的生命周期钩子接口**：`HasStartSelect`/`HasEndedSelect`/`HasStartUpdate`/`HasEndedUpdate`/`HasStartInsert`/`HasEndedInsert`/`HasStartDelete`/`HasEndedDelete`/`HasGetSqlDelete` 删除。

### 已知限制

- `TUniEngine` 的 `st` / `tx` 仍是结构体上的共享可变状态，**非并发安全**（`HashTabl` 注册已加锁，但单实例仍建议每 goroutine 独立或串行调用）。
- `CopyIn*` 的 COPY 协议由包内 `copyInStmt` 自实现，**不再依赖已停维护的 `github.com/lib/pq`**。
