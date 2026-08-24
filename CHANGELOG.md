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

### 新增

- **`context.Context` 支持**：每个查询/写入方法新增 `*Ctx` 变体（`SelectCtx`/`InsertCtx`/`ExecuteCtx`/`BeginCtx` 等），首参接收 `context.Context`；原方法等价于以 `context.Background()` 调用对应 `*Ctx`。
- **应用层加密**：
  - 结构体 tag `db:"field,encrypt"` 或 `.SetSecret("field")` 标记敏感字段；
  - `TUniEngine.SecretOn` / `SecretBy` 控制开关与密钥；
  - `SecretHook` 自定义加密钩子，默认内置 AES-256-GCM（密文带 `ENC:` 标签）；
  - 读取自动解密、存量明文（无标签）自动鉴别直通；
  - 非 `string` 字段标记 `encrypt` 时写入 `fast-fail` 报错。
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

### 已知限制

- `TUniEngine` 的 `st` / `tx` 仍是结构体上的共享可变状态，**非并发安全**（`HashTabl` 注册已加锁，但单实例仍建议每 goroutine 独立或串行调用）。
- `CopyIn*` 的 COPY 协议依赖 `github.com/lib/pq`（官方已进入维护模式），长期建议迁移 `pgx`。
