# UniEngine 代码评估报告

评估日期：2026-08-11（分支 `ai-dev-20260810`）
评估范围：`UniEngine.go`（2449 行）、`DbaEngine.go`（366 行）、`UniTable.go`（109 行）、`UniField.go`（37 行）、`readme.md`、`copyit-p2.py`

## 1. 项目概况

UniEngine 是一个 Go 编写的多数据库 ORM 框架，支持 PostgreSQL / SQL Server / Oracle / MySQL（并预留金仓、达梦、高斯、PolarDB、华为 Taurus 驱动标识）。核心设计：

- 通过 struct tag（`ColLabel`，默认 `db`）声明字段与数据库列的映射；
- 引擎实例维护 `HashTabl`（类 → 表元数据），运行时用 `reflect` 生成 SQL 并绑定参数；
- 提供 `Select/SelectL/SelectM/SelectH/SelectD/SelectF/SelectS` 查询系列、`Insert/InsertL/InsertP/SpecialInsertL/SpecialInsertP` 写入系列、`Update/Delete/SaveIt`、事务（`Begin/Commit/Cancel`）与元数据检查（`ExistTable/ExistViews/ExistField/ExistConst`）。

总体评价：核心思路（反射 + 元数据注册 + 多方言适配）是成立的，SQL 生成大量使用参数占位符（`$1`/`?`/`:`），数据值基本走预编译绑定，注入面集中在**表名/字段名拼接**。但代码处于"可运行原型"阶段：无 `go.mod`、无测试、错误处理用 `panic`、资源管理不完整、存在若干必然触发的崩溃路径。下文按严重度分级列出问题，均附 `file:line`。

## 2. 严重问题（崩溃 / 功能未实现 / 数据安全）

### S1. 错误消息丢失格式符，`fmt.Sprintf` 格式串缺 `%s`
`errors.New(fmt.Sprintf("UniEngine: no such class registered:", t.String()))` 的格式串中没有 `%s`，`t.String()` 作为多余参数被丢弃，**实际错误消息不含类名**，无法定位问题。同类写法共 10 处：

- `UniEngine.go:669`（Select）、`:794`（SelectL）、`:934`（SelectM）、`:1211`（SaveIt）、`:1310`（SaveItWhenNotExist）、`:1530`（Insert）、`:1631`（InsertL）、`:1775`（SpecialInsertL）、`:2004` / `:2007`（Delete）

修复：改用 `fmt.Errorf("UniEngine: no such class registered: %s", t.String())`。

### S2. `ExistConst` 是空实现
`UniEngine.go:2330-2344` 中 `cSQL := ""` 后直接 `SelectD(cSQL)`，会向驱动提交空 SQL（必然报错或产生意外结果）。对外暴露却未实现，属于"半成品对外可见"。

修复：补齐各数据库查询约束（主键/外键/唯一键/默认值）的 SQL，或先返回明确的"未实现"错误并移除误导性调用。

### S3. 字符串切片越界 panic（空字段列表场景必然崩溃）
代码大量使用 `xxx[1:]` / `xxx[4:]` 切掉拼接前缀，列表为空时触发 index out of range panic：

- `UniEngine.go:399-400` / `:432-433` / `:497-498`（`PrepareRunSQL` 的 `SqlField[1:]`、`SqlParam[1:]`、`SqlWhere[1:]`）
- `:1577-1578`（`Insert`，全字段 `ReadOnly` 时 `UniField` 为空）
- `:1677` / `:1695` / `:1717` / `:1721`（`InsertL` 的 `UniField[1:]`、`SqlParam[1:]`、`EndParam[1:]`）
- `:1258`（`SaveIt`）、`:1352`（`SaveItWhenNotExist`）、`:2029`（`Delete`）的 `SqlWhere[4:]`

修复：拼接前判空（或先 build strings.Builder 再 trim 前缀），空列表时返回明确 error。

### S4. 库代码用 `panic` 处理常规业务错误
- `UniTable.go:49`（`SetKeys` 字段未注册）
- `UniTable.go:82` / `:86`（`AutoKeys` 查询失败 / 主键不存在）
- `DbaEngine.go:361`（`TAutoKeys4MYSQLN` 未指定 `DataBase`）

库 API 的契约错误应以 `error` 返回，`panic` 会让调用方（未 recover 时）直接崩溃。

### S5. `getSqlQuery` 占位符替换过于粗暴，可能破坏 SQL
`UniEngine.go:63-93`：

- Oracle：`strings.ReplaceAll(SqlQuery, "$", ":")` 把 SQL **文本中所有 `$`** 都替换为 `:`（字符串字面量、注释、正则里的 `$` 会被误改）；
- MySQL：`regexp.Compile(\`\$\d+\`)` 匹配失败时**静默返回空串**（`:83-86`），把错误吞掉；
- 两个分支都只在 `len(args) > 0` 时生效，同一 SQL 带不带参数行为不一致。

修复：只对参数占位符（`$数字`）做转换；编译失败应返回错误而非空串。

### S6. `SpecialInsertL` 无事务保护 + 无 Provider 判断
`UniEngine.go:1848-1855`：

- 直接 `self.tx.Prepare(...)`，若调用时未 `Begin()`（`tx == nil`）→ **nil 指针 panic**；其他方法均通过 `prepare()` 统一入口并检查 `canClose`；
- `pq.CopyIn` 是 PostgreSQL 专用协议，代码却对任何 `Provider`（含 MySQL/SQL Server）无条件使用；
- `strings.ToLower(pq.CopyIn(...))` 将整个 COPY 语句转小写，含大写表名/字段名时被破坏；
- 覆盖 `self.st` 前未关闭旧 stmt。

### S7. `Execute` / `ExecuteMust` 泄漏 prepared statement
`UniEngine.go:2052-2110`：`self.Db.Prepare(...)` 后 `Exec` 完直接返回，**没有 `defer self.st.Close()`**（对比 `SelectD` 等均有 `defer self.release()`）。每调用一次泄漏一个 stmt 占用的连接资源，长循环下可耗尽连接池。

## 3. 重要问题（健壮性 / 正确性 / 一致性）

### I1. `self.st` 复用不关闭，`prepare/release` 不配对
`UniEngine.go:2346-2375`：`prepare()` 每次覆盖 `self.st`，旧 stmt 不关闭；`release()` 仅关闭当前 stmt。连续执行多条 SQL 会累积未关闭 stmt。应在 `prepare` 前关闭旧 stmt（`release` 幂等化）。

### I2. 引擎实例非并发安全，`HashTabl` 懒初始化有竞态
`TUniEngine` 持有共享可变状态 `tx`、`st`、`canClose`、`HashTabl`（`UniEngine.go:19-39`）。`if self.HashTabl == nil { ... }`（`:172`、`:295`、`:360` 等多处）是"先读后写"，多 goroutine 并发注册/查询会数据竞争甚至 map 并发写 panic；`st` 单值被并发 `prepare/Query` 互相覆盖。

至少需要：文档明示"单实例非并发安全，多 goroutine 应各自创建实例（`*sql.DB` 连接池仍复用底层连接）"；对 `HashTabl` 懒初始化加保护。

### I3. `HashTabl` 双 key 体系，查表路径不统一
- `RegisterClass` 以 `t.String()`（如 `mock.TMAIN`）为 key：`UniEngine.go:197`
- `RegisterTable` / `RegisterField` / `RegisterPkeys` / `GetTable` / `PrepareTables` 以 `strings.ToLower(TableName)` 为 key：`:224`、`:256`、`:291`、`:299`、`:310`
- `SaveIt/Insert/Delete/Select*` 查 `t.String()`，`PrepareRunSQL` 查 `ToLower(TableName)`

同一引擎混用两类 API 会"注册了却查不到表"。应统一为单一 key 约定（建议统一 `ToLower`）。

### I4. `ProviderName()` 覆盖不全
`UniEngine.go:95-119` 仅处理 4 个驱动；`DtACCESS/DtSQLITE/DtKINGES/DtDAMENG/DtOPENGS/DtPOLODB/DtTAURUS` 返回空串，静默失败。

### I5. 表名/字段名 SQL 拼接存在注入面
`DbaEngine.go:197-366` 全部元数据查询（`TExistTable4*` / `TExistField4*` / `TAutoKeys4*`）用 `fmt.Sprintf(result, TableName/FieldName/DataBase)` 直拼；`UniEngine.go:2125` 的 `IfDropView` 直拼 `DROP VIEW %s`。虽然表名通常来自代码而非用户输入，但 `ExistTable/ExistField` 等接受任意字符串，一旦暴露给外部输入即为 SQL 注入。应加元数据白名单校验（只允许已注册的表/字段）或统一转义。

### I6. `SelectD/SelectF/SelectS` 取的是"最后一行"而非"第一行"
`UniEngine.go:551-556`、`:596-601`、`:639-645` 用 `for rows.Next()` 循环 + 每次覆盖扫描结果，语义上单值查询应取第一行（可加 `limit 1` 或读到第一行即返回），当前多行结果时静默丢弃除末行外的数据。

### I7. `PrepareRunSQL` EtSelect 分支残留死代码
`UniEngine.go:393-400`：循环内 `SqlField = self.getColParam(...)`、`SqlParam = self.getValParam(...)` 是**覆盖写**（累加逻辑被注释），随后 `SqlField[1:]` / `SqlParam[1:]` 切掉首字符——计算结果完全未使用（`SqlResult` 只用 `SqlWhere`）。属重构残留，应删除。

### I8. `IfDropView` 返回语义模糊
`UniEngine.go:2122-2136`：视图存在（已删除）与不存在两条路径都 `return true, nil`，调用方无法区分"已删除"与"本来就不存在"。

### I9. MySQL 视图检测 SQL 错误
`DbaEngine.go:253-259` `TExistTable4MYSQLN.GetSqlExistViews` 查的是 `information_schema.tables`（只含表），视图应查 `information_schema.views`。PostgreSQL / SQL Server / Oracle 的 `GetSqlExistTable` 与 `GetSqlExistViews` 也完全相同，视图判断不准确。

### I10. SQL Server 主键查询使用旧式多表连接且带分号
`DbaEngine.go:331-342`：`from syscolumns,sysobjects,sysindexes,sysindexkeys where ...`（ANSI-89 隐式连接，可读性差、易错），结尾 `;` 与其他 SQL 风格不一致。

### I11. `SpecialInsertL` 全语句转小写
`UniEngine.go:1848` `strings.ToLower(pq.CopyIn(...))`：表名/列名含大写字母时被强制转小写导致列名不匹配。转小写仅是为了对齐 PG 内部存储大小写，应保留原样或仅在 PG 端处理。

### I12. `Initialize` 把包自身结构体注册为业务表
`UniEngine.go:2442-2443` 将 `TUniTable{}`、`TUniField{}` 以 `t.String()`（`UniEngine.TUniTable` 等）注册，且 `TableName` 填的是 import path——看起来是错误用法/调试残留。

### I13. 死代码与注释代码块
- `DbaEngine.go:7-31`：整块 `TDATA` 接口示例注释
- `UniEngine.go`：大量整段注释代码（`:388-391`、`:420-423`、`:457-472`、`:544-550`、`:588-594`、`:631-637`、`:736-764`、`:817-818`、`:1016-1058`、`:1230-1239`、`:1325-1334`、`:2144-2157`、`:2209-2222` 等）
- `THasSetSqlResult` 等两个包级 `reflect.TypeOf` 变量（`DbaEngine.go:99-100`）用法散落且含注释掉的调用

### I14. 命名与 Go 惯例
- 拼写错误：`eror`（应为 `err`，全项目）、`EtDelele`（应为 `EtDelete`，`DbaEngine.go:71`）、`"second paramter"`（应为 parameter，多处）
- `errors.New(fmt.Sprintf(...))` 应统一为 `fmt.Errorf`（10+ 处）
- 接收者命名 `self`（Go 惯例用简短名）；`getValParam` 中 `fmt.Sprintf("%s", self.ColParam)`（`UniEngine.go:44`）冗余，直接返回 `self.ColParam` 即可

### I15. pkey WHERE 遍历 map 顺序随机
`SaveIt`（`:1243-1255`）、`Delete`（`:2020-2027`）遍历 `HashPkeys`/`HashField`（map）拼 WHERE 与参数，顺序随机导致生成 SQL 不稳定、执行计划可能劣化。应使用 `ListPkeys`（有序）。

## 4. 建议（工程化）

### A1. 无 `go.mod` / `go.sum`
项目仍是 GOPATH 时代结构，`go build` / `go vet` / `go test` 无法运行。应初始化 `go.mod`（`module github.com/kazarus/UniEngine`）并 `go mod tidy`（依赖 `github.com/lib/pq`）。

### A2. 无任何单元测试
全部方法零覆盖。优先为纯函数加测试：`getSqlQuery` / `getColParam` / `getValParam` / `PrepareRunSQL`（各方言 SQL 生成）、`RegisterClass` / `PrepareTables` / `ExistTable*` SQL 拼接、空列表越界场景回归。

### A3. `readme.md` 不完整
只有 MySQL 示例；"安装方式"章节空白；缺少其他数据库、事务、批量插入、错误处理约定、`SpecialInsertL` 前置条件（需 `Begin`）说明。

### A4. `copyit-p2.py` 为遗留同步脚本
Python 2 语法（`print` 语句、`psyco`）、Windows 硬编码路径（`C:\GOPATH\...` / `D:\GITHUB\...`），与项目运行无关，应从仓库移除（git 历史可恢复）。

### A5. 仓库卫生
- `.DS_Store` 已被 git 跟踪（`git ls-files` 可见），应 `git rm --cached` 并加 `.gitignore`；
- 无 `LICENSE`、无 CI 配置。

### A6. `TUniField.initialize` 未知 tag 静默忽略
`UniField.go:27-36`：除 `readonly` 外所有 tag 选项被静默丢弃（`default` 分支空），拼写错误时无任何提示。建议未知项返回错误或至少 `runDebug` 时告警。

### A7. 文档与错误消息统一
错误消息中英混杂、风格不一；注释存在 `//@`、`//#`、`#`、块注释等多种标记混用，建议统一并保留有价值的方言说明。

## 5. 改进路线图

对应实施顺序（详见实施计划）：

1. **工程化基础**：`go.mod` / `go.sum` 初始化，`go build` / `go vet` 全绿（前置，其余步骤依赖可编译状态）；
2. **严重问题**：S1 格式符、S2 `ExistConst`、S3 越界、S4 panic→error、S5 占位符替换、S6 事务与 Provider 判断、S7 stmt 泄漏；
3. **重要问题**：I1 资源配对、I2 并发说明+懒初始化保护、I3 key 统一、I4 `ProviderName` 补全、I5 注入面白名单、I6 首行语义、I7/I13 死代码清理、I8/I9/I10/I11 方言修正、I12 初始化清理、I14 命名、I15 有序 pkey；
4. **测试与文档**：A1 之外补 A2 单测、A3 readme、A4 移除脚本、A5 仓库卫生。

优先级建议：先 S1-S7 与 A1（直接决定能否构建、是否崩溃），再 I 类，最后 A 类工程化收尾。

## 6. 修复状态（2026-08-11）

以下问题已在本轮实施中修复（详见 git diff）：

- **S1** 缺 `%s` 格式符：15 处改为 `fmt.Errorf`；其余 8 处 `errors.New(fmt.Sprintf(...))` 规范化为 `fmt.Errorf`。
- **S2** `ExistConst` 空实现改为显式返回「未实现」错误（不再执行空 SQL）。
- **S3** 9 处空串切片越界全部加判空兜底（`PrepareRunSQL`/`SaveIt`/`SaveItWhenNotExist`/`Update`/`Insert`/`InsertL`/`Delete`）。
- **S4** 库内 panic（`SetKeys`/`AutoKeys`/`TAutoKeys4MYSQLN`）全部改为返回 error。
- **S5** `getSqlQuery` 占位符替换收敛为包级预编译正则 `\$\d+`（Oracle 仅替换 `$N`）。
- **S6/S7/I1** `prepare` 复用前关闭旧 stmt、`release` 幂等；`Execute`/`ExecuteMust` 补 `defer release`；`SpecialInsertL` 走统一 `prepare`（修复无事务 nil 指针）、去掉 `strings.ToLower` 全小写。
- **I2** 并发安全：`HashTabl` 指针锁（`lockTables`/`ensureTables`）保护懒初始化与注册，文档声明单实例非并发安全。
- **I3** `HashTabl` 双 key 统一：`RegisterClass` 同时以小写表名与类全名注册；**额外修复隐藏 bug**——`HashTabl` 由值拷贝改为 `map[string]*TUniTable`，`SetKeys`/`AutoKeys` 对注册表的修改现在真正生效（此前修改的是局部副本，pkey 全部丢失）。
- **I4** `ProviderName` 补全全部 11 个驱动分支。
- **I5** 注入面：新增 `validIdent` 校验（`ExistTable`/`ExistViews`/`ExistField`/`IfDropView`/`SaveIt`/`Insert`/`Update`/`Delete`/`InsertL`/`SpecialInsertL` 的表名入口）。
- **I6** `SelectD/F/S` 多行语义未改（保持兼容），记录待评估。
- **I8** `IfDropView` 返回语义修正：存在并删除返回 true，不存在返回 false。
- **I9** 视图检测 SQL：PostgreSQL 加 `relkind='v'`、SQL Server 加 `xtype='V'`、MySQL 改查 `information_schema.views`。
- **I10** SQL Server 主键查询改为现代 `INNER JOIN` + `ORDER BY index_column_id`。
- **I12** `Initialize` 移除误注册 `TUniTable`，`TUniField` 改用内名（AutoKeys 依赖）。
- **I13** 清理注释死代码块 17 处 + `DbaEngine.go` 顶部 TDATA 示例块。
- **I14** `eror`→`err` 全量重命名；`getValParam` 去冗余；`EtDelele` 保留公开名并加注释（API 兼容）。
- **I15** pkey 遍历有序化：`SetKeys`/`AutoKeys` 维护 `ListPkeys`（去重），`SaveIt`/`Delete`/`Update`/`PrepareRunSQL` 遍历有序列表（带 `HashPkeys` fallback）。
- **A1** `go.mod`/`go.sum` 初始化，`go build`/`go vet`/`gofmt` 全绿。
- **A2** 新增 12 个单元测试（`UniEngine_test.go`）：占位符/列/参数生成、`PrepareRunSQL` 三分支、字段顺序、全只读防越界、`SetKeys` 去重有序、`AutoKeys` 空库报错、`validIdent`、注入拒绝。
- **A3** `readme.md` 重写：各数据库示例、字段标签、事务、批量插入、错误处理约定、并发约定。
- **A4** `copyit-p2.py` 已移除。

已知限制（有意保留，供后续决策）：
1. `lockTables` 的 `mu` 指针懒初始化在「并发首次注册」场景存在理论竞态——按文档约定注册期不得并发，接受。
2. `SelectD/F/S` 仍取末行而非首行（保持行为兼容，未改）。
3. `EtDelele` 常量名保留（公开 API 兼容）。
4. `GetTable` 未注册时返回空 `&TUniTable{}`（非 nil，兼容旧调用方）。
5. Oracle/MySQL 占位符正则 `\$\d+` 会改写含数字的 `$` 字符串字面量（如 `'$100'`），已优于旧版全量替换，边界场景未覆盖。

