# UniEngine 代码质量评估报告

> **当前复评：86 / 100（2026-10-14，基于 `60d0f7f` 及本轮工作区修复）。** 下方「当前销账登记」是本报告的现行结论。原始评审基于 `fe3b25d`，评分 68 / 100；其缺陷复现、覆盖率和 Windows 环境记录保留在「历史基线」中，不代表当前代码仍有这些缺陷。

## 当前销账登记（复评基线 `60d0f7f` 加本轮工作区修复）

### 复评范围与评分

本次核对 `60d0f7f` 加本轮工作区的生产代码、`quality_fix1_test.go`、`quality_fix2_test.go`、`quality_fix3_test.go`、集成测试及原评审 Q1–Q8。评分沿用原报告的六项权重；它是基于代码和可重复验证结果的工程判断，不是自动化评级。工作区修复尚未形成提交,不可把该分数直接归于 `60d0f7f` 本身。

| 维度 | 权重 | 原分 / 10 | 当前分 / 10 | 当前加权分 | 变化依据 |
| --- | ---: | ---: | ---: | ---: | --- |
| 正确性与可靠性 | 25% | 6.5 | 8.8 | 22.00 | 同表登记改为快照发布；SQL Server 初始化时校验实际绑定；事务与反射异常修复保留 |
| 安全性与数据完整性 | 25% | 5.5 | 8.2 | 20.50 | 新 MAC 缺省写 v2，严格模式拒绝不完整投影；旧 v1 行仍待重签，主键后改问题仍在 |
| 架构与可维护性 | 15% | 7.5 | 8.3 | 12.45 | 元数据快照集中发布；公开可变的表指针仍需要调用方遵守并发契约 |
| 测试与工程保障 | 15% | 7.5 | 9.2 | 13.80 | 增加同表并发、初始化错绑、验签与缓存回归；MySQL 与 SQL Server 真库验收已通过 |
| 性能与资源管理 | 10% | 7.0 | 8.0 | 8.00 | 派生密钥缓存容量限制为 128，认证失败不入缓存；分页路径覆盖仍薄弱 |
| 文档与易用性 | 10% | 8.0 | 9.0 | 9.00 | README/CHANGELOG/贡献指南说明新缺省值、错误和迁移；初始化错误需应用显式处理 |
| **合计** | **100%** | **68 / 100** | | **85.75 → 86 / 100** | 整数四舍五入；与原评审采用同一权重 |

本轮复评记录：根模块 `go test -race -count=1 ./...`、`go vet ./...`、`gofmt` 检查,以及独立 `integration/` 模块编译检查。`60d0f7f` 的设计记录载明本机 MySQL 26.7.0 的 6 项集成测试通过,含 Q5 最终存储值断言；本轮未重新连接 MySQL。**【更正 2026-10-14】**`26.7.0` 不是有效的 MySQL 版本号(MySQL 发行序列为 5.x / 8.0 / 8.1–8.4 / 9.x),该版本号无法核实。评估方以握手报文确认可达实例服务端为 **MySQL 8.4.6**,并在该实例上用同一用例 `TestMySQLPlaceholderRewriteStoredValues` 复跑**通过**(见下方 Q5 行);原「本机」实例的版本仍未经独立核验。用户提供的 SQL Server 实例上执行 `go test -count=1 -run '^TestMSSQL' .` 通过:8 个原始绑定矩阵子例、4 个有效组合读写子例、4 个错误组合初始化拒绝子例。读写用例使用随机独立表名,并把清理失败计为测试失败；未操作固定名称的已有表。连接信息未写入仓库。本轮未连接真实 PostgreSQL 或 Oracle,也未核验远端 CI 状态；82.4% 覆盖率是上一复评基线,并非本轮重测结果。

### Q1–Q8 销账台账

「已销账」表示本次核对到代码修复和正式回归断言；「有条件销账」表示原缺陷的核心代码路径已修复，但原验收条件或实际启用条件尚未全部满足。历史复现探针的 PASS 只说明旧缺陷曾存在，不能作为当前销账证据。

当前共 **6 项已销账、2 项有条件销账、0 项未销账**。有条件项在右列条件完成前仍需跟踪，不能视作风险完全消失。

| 编号 | 当前状态 | 修复及销账证据 | 尚需处理的边界 |
| --- | --- | --- | --- |
| Q1 MAC 载荷歧义 | **有条件销账** | `VerifyMacVer=0` 缺省写 `MAC2:` v2,显式 `=1` 才写 v1；新回归与旧行兼容测试通过 | 存量 v1 行仍须更新重签,全表转为 v2 后才获得完整防歧义保证。迁移步骤见 README「MAC 载荷版本与防碰撞迁移」 |
| Q2 复合主键绑定 | **已销账** | `UniTable.go:SetVerify` 拒绝遗漏已登记主键；两列、三列、缺主键和篡改回归通过。**【补记 2026-10-14】** 顺序变体亦已封住：`SetVerify` 成功后主键冻结，`SetKeys`/`AutoKeys`/`RegisterPkeys` 一律被拒并返回 `ErrPkeysFrozenAfterVerify`，且不写入任何主键（`RegisterPkeys` 不克隆不发布）；`VerifyOn=false` 时不冻结，正常登记顺序不受影响。回归用例 `TestPkeysFrozenAfterSetVerify`、`TestPkeysNotFrozenWhenSetVerifyFails` | 需变更主键须重新 `RegisterClass` 后按序登记；外部代码直接改已返回的 `*TUniTable` 仍受 Q6 快照语义约束 |
| Q3 事务取消后状态滞留 | **已销账** | `UniEngine.go:Cancel/Commit` 在底层返回错误后也清理 `tx/inTx`；`TestQ3CancelledTransactionRecovers`、提交失败和回滚失败测试通过 | 长期事务归属设计仍可改进，但不影响本项缺陷销账 |
| Q4 MAC 回填反射 panic | **已销账** | `UniVerify.go:verifySignCols` 在 `SetString` 前检查 `CanSet`；四个写入口的按值参数返回普通错误且 SQL 未执行，指针回填回归通过 | 后续扩展写入口时仍需沿用该参数契约 |
| Q5 SQL 占位符错改/错绑 | **已销账** | 词法扫描与 MySQL 参数重排已回归；MySQL 真库记录含最终存储值断言(原记录版本「26.7.0」非有效 MySQL 版本号,【更正 2026-10-14】;可达实例经握手实测 **8.4.6**,该用例已在其上复跑通过)；SQL Server 本轮真库 16 个子例通过,覆盖原始绑定、有效组合读写与 `Initialize()` 拒绝错绑 | 反斜杠转义仍是已文档化的词法边界；旧版 `sqlserver` + `$` 部署仍需审计历史数据 |
| Q6 同表动态注册并发 | **已销账** | `RegisterTable/RegisterField/RegisterPkeys` 在锁内复制表元数据并发布新快照；`TestConcurrentSameTableRegistrationUsesSnapshots` 与根模块 race 测试覆盖同表注册和查询 | 外部代码直接改已返回的 `*TUniTable` 不受快照机制保护,仍须遵守并发契约 |
| Q7 重复列 tag 映射不对称 | **已销账** | `RegisterClass` 拒绝归一化后重复的真实列名；同名、大小写及 `-`/`_` 标记回归通过 | 无本项遗留 |
| Q8 无效密文污染无界缓存 | **已销账** | 派生密钥缓存限制 128 项并淘汰旧项；冷解密在 GCM 认证通过后才缓存,无效密文回归通过 | 大量不同盐仍可产生 PBKDF2 计算负载,调用层需要限制输入规模和速率 |

第一批修复见提交 `4014962`（Q3/Q4/Q7）；第二批见提交 `93438d2`（Q1/Q2/Q5）；本轮代码基于 `60d0f7f` 加工作区改动（Q1/Q5/Q6/Q8 及严格验签边界）。以上状态以当前源码和回归结果为准,历史分数保留作对比。

### 当前待办与验收顺序

| 优先级 | 待办 | 完成标准 |
| --- | --- | --- |
| 中 | Q1 数据迁移、Q2 后续主键变更 | 全表 v1 行分批重签为 v2；`SetVerify` 后主键变更被拒绝或重新校验 |
| 中 | 严格验签的查询覆盖 | 已对结构体读取的部分投影及自定义结果钩子返回 `ErrIncompleteVerification`;继续评估标量读取及外部原始 SQL 的使用契约 |
| 持续 | 低覆盖与方言边界 | 优先覆盖分页、元数据、`verifyFormat`；复核非默认 schema、批量部分成功与日志脱敏 |

---

## 历史基线说明（原始评审，提交 `fe3b25d`）

以下 §1–§7 与附录保留原始评审的环境、复现证据和当时的实施计划。其中“当前”“本次”“需要修复”等表述均指原始提交 `fe3b25d`，请以上方销账台账判断本轮工作区代码的状态。

> **原始综合评分：68 / 100（6.8 / 10）。** 项目已具备清晰的功能拆分、较丰富的测试和多数据库适配基础；当时的主要短板在完整性校验、事务异常恢复、反射边界及 SQL 改写。

## 1. 评估基线与结论

| 项目 | 本次评估范围 |
| --- | --- |
| 评估日期 | 2026-10-14（Asia/Shanghai） |
| 仓库 | `D:\test\UniEngine` |
| 提交 | `fe3b25dfab40d0ff561a430264e0d14c96a6cb73`，分支 `master` |
| 工作区 | 评估开始时无未提交改动；本次交付仅新增本报告 |
| 语言与环境 | Go，`go1.24.9 windows/amd64` |
| 根模块规模 | 14 个非测试 Go 文件，3,884 行；包含注释、空行及 `doc.go` |
| 根模块测试 | 10 个测试文件，3,876 行；127 个 `Test` 函数、7 个 `Benchmark` 函数，另有 fuzz 与示例 |
| 审查内容 | 根模块生产代码、关键单元测试、四类数据库集成测试配置、pgx COPY 适配器、CI 与文档 |
| 核心发现 | **7 类问题已用临时探针复现，另有 1 类并发问题由静态代码路径确认，未完成 race 动态验证** |

这是一次针对上述历史提交的独立评估。仓库原有的 `UniEngine-评估报告.md` 保留不变，其中的历史分数、其他机器的测试结果和“panic 已清零”等结论，均不直接作为该次结论。

该历史版本适合在明确约束下继续使用和迭代，但还不足以单凭“测试全绿”证明所有对外承诺成立。尤其是当时 `SetVerify` 的防篡改保证存在可以实际绕过的输入组合；事务取消后，也可能让整个引擎实例持续不可用。

## 2. 分项评分

评分是基于本次证据的工程判断，不是自动化工具生成的客观评级。分档参考：90 分以上为边界保障成熟；80–89 分为整体良好；70–79 分为可用但有明显短板；60–69 分表示具备基础、仍有关键问题需要修复。

| 维度 | 权重 | 得分 / 10 | 加权分 | 主要依据 |
| --- | ---: | ---: | ---: | --- |
| 正确性与可靠性，含事务和并发 | 25% | 6.5 | 16.25 | 主流程和输入守卫较完整；仍有事务终态滞留、panic、参数错绑及注册并发问题 |
| 安全性与数据完整性 | 25% | 5.5 | 13.75 | 使用标准密码算法、参数绑定和标识符白名单；MAC 编码与复合主键绑定仍有缺口 |
| 架构与可维护性 | 15% | 7.5 | 11.25 | 查询、写入、注册、元数据、加密等已拆分；公开可变状态和多份字段元数据增加一致性成本 |
| 测试与工程保障 | 15% | 7.5 | 11.25 | 127 个单测、78.1% 覆盖率、fuzz、benchmark、四族集成测试配置；关键状态和组合边界欠缺 |
| 性能与资源管理 | 10% | 7.0 | 7.00 | 扫描下标预解析、批量接口和派生缓存有价值；全局初始化锁、无界缓存和重复反射仍可优化 |
| 文档与易用性 | 10% | 8.0 | 8.00 | README、贡献指南、迁移说明丰富；个别安全、并发描述过强，配置容易组合出错误状态 |
| **合计** | **100%** | | **67.50 → 68** | 四舍五入到整数 |

没有因为仓库有意采用 PascalCase、`this` 接收者等既有风格而单独扣分。提升优先级由行为风险决定。

## 3. 实际验证结果

### 3.1 本机执行记录

| 验证 | 结果 | 解释 |
| --- | --- | --- |
| `go build ./...` | 通过 | 根模块可构建 |
| `go vet ./...` | 通过 | 包括现有 `noCopy` 的静态检查 |
| `go test ./... -count=1`，同时采集覆盖率 | 通过 | 根模块语句覆盖率 **78.1%**；在加入临时探针前测得 |
| `go tool cover -func=coverage-review.out` | 成功 | 覆盖率不是全仓库所有嵌套模块的合并值 |
| `gofmt -l .` | 原始检查看到文件列表 | Git 为 LF、Windows 工作区为 CRLF，且 `core.autocrlf=true`；逐文件归一化为 LF 后与 gofmt 输出比较，**全部一致**，不作为格式缺陷 |
| `go test -race -count=1 ./...` | 未完成有效验证 | 测试进程以 `0xc0000139` 异常退出；未得到 race 检查结果，不能算通过，也不能据此归因于业务代码 |
| `go test -run '^$' -bench . -benchtime=1x -benchmem .` | 通过 | 仅作基准冒烟；单次样本不用于生产吞吐量或性能优势定量结论 |
| `FuzzValidIdent`，5 秒、2 workers | 通过 | 450,903 次执行；只覆盖标识符性质 |
| `FuzzUniFieldInitialize`，5 秒、2 workers | 通过 | 431,200 次执行；只覆盖 tag 解析性质 |
| 临时 `TestReview*` 探针 | 7 个顶层用例通过 | **通过表示缺陷被成功复现**，包括 4 个 panic 子用例，不表示实现符合期望 |
| `integration/` 内 `go test -run '^$' ./...` | 编译检查受阻 | 需要 Go 1.25；自动下载工具链时连接 `proxy.golang.org` 超时 |
| 四族真实数据库测试 | 本次未执行 | 未连接真实数据库；历史报告及 CI 配置不能替代本次执行证据 |
| `contrib/pgxcopy` | 静态审查 | 同样声明 Go 1.25；本次没有单独完成构建验证 |

根目录的 `go test ./...` 不会自动覆盖有独立 `go.mod` 的 `integration/` 和 `contrib/pgxcopy/`。CI 已分别配置集成检查，这是优点；本报告没有核验远端 Actions 是否实际执行成功。

### 3.2 覆盖率的实际薄弱处

| 代码位置 | 根模块语句覆盖率 | 值得补充的行为 |
| --- | ---: | --- |
| `UniMeta.go` | 25.0% | 执行失败、受影响行数为零、存在性与删除视图路径 |
| `UniCopyIn.go` | 54.9% | 分页边界、第二页失败、取消与部分成功 |
| `InsertPCtx` / `CopyInPCtx` | 各 11.1% | 默认页大小、负数、整除页数、尾页、驱动参数上限 |
| `verifyFormat` | 30.0% | 不同数值类型、浮点值及不支持类型的稳定行为 |
| `Cancel` / `Commit` | 各 88.9% | 本次发现的关键缺口恰恰位于终结事务的错误分支 |

以上仅是根模块单测覆盖率。某些路径另有集成用例，不能把本地 0% 等同于仓库完全没有测试；同样，总覆盖率较高也不能证明安全性质成立。

## 4. 原始问题清单（历史复现，当前状态见销账台账）

优先级定义：**P1** 为可能破坏数据、安全保证或服务可用性的明确问题，应优先修复；**P2** 为特定输入或负载下的可靠性问题，随后安排。本次没有将任何发现定为必须无条件阻断所有使用场景的 P0。

| 编号 | 优先级 | 问题 | 证据级别 |
| --- | --- | --- | --- |
| Q1 | P1 | MAC 载荷直接拼接分隔符，不同字段值可以得到相同签名载荷 | 严格模式下读取探针复现 |
| Q2 | P1 | 复合主键只需保护其中一列，无法保证绑定到唯一行 | 严格模式下读取探针复现 |
| Q3 | P1 | 事务自动回滚后引擎保留旧事务，不能重新开始或正常执行 | context 取消探针复现 |
| Q4 | P1 | 开启完整性校验后，按值传入结构体触发 4 个写入口 panic | 4 个公共入口复现 |
| Q5 | P1 | 正则改写 SQL 误改字符串常量，并在 MySQL 乱序占位符下错绑参数 | 实际生成 SQL 和参数捕获 |
| Q6 | P1 | 同一表的 `RegisterField/RegisterPkeys` 与查询、写入没有完整同步 | 静态路径确认；race 未验证 |
| Q7 | P2 | 重复列 tag 被接受，写入取第一个字段，读取填最后一个字段 | 写入参数和读取结果探针复现 |
| Q8 | P2 | 解密失败的密文也能增加无界派生密钥缓存 | 5 份无效密文探针复现 |

### Q1：MAC 载荷编码有歧义

**位置：** [UniVerify.go:98](D:/test/UniEngine/UniVerify.go:98)，`verifyPayload`，尤其是 102–109 行。

载荷形如 `表名 + 0x1f + 值1 + 0x1f + 值2 ...`，字符串值没有长度前缀、转义或控制字符校验。相同主键下，下列两组不同的保护字段会得到完全相同的载荷：

```text
原值：A = "admin\x1f0"，B = "user"
改值：A = "admin"，     B = "0\x1fuser"
```

探针先通过 `Insert(&row)` 取得正常签名，再把两列改成第二组值，保留原 MAC，最后通过公共 `Select` 读取；即使 `VerifyMode=2` 也返回成功。

**影响条件：** 攻击者能修改存储中的受保护字符串，且字段允许该控制字符；引擎本身没有约束此字符。这不是 HMAC 算法碰撞，而是不同数据被编码成同一载荷。

**建议：** 使用带版本的、无歧义的规范编码，例如逐字段编码“列名、类型、长度、字节内容”；也可临时拒绝分隔符，但必须同时处理读取和存量数据。载荷变更应升级 MAC 格式版本并安排重签迁移，不能直接使现有数据全部验签失败。

**验收：** 包含 `0x1f`、空串、Unicode 的不同字段元组不能共用签名；以上修改必须被严格模式拒绝。

### Q2：复合主键的行绑定不完整

**位置：** [UniTable.go:330](D:/test/UniEngine/UniTable.go:330)，`SetVerify` 的 `HasPkey` 检查。

当前条件是保护列里“至少有一个已登记主键列”。对于主键 `(who_build, user_indx)`，仅保护 `who_build` 也能登记成功。同一 `who_build` 下不同用户可以共享原 MAC 对应的数据组合。

探针登记两列主键，但只把 `who_build` 和业务列加入保护。把 `user_indx` 从 `2` 改为 `999`，其他受保护值与原 MAC 保持不变，完整列查询在严格模式下仍然通过。

**影响条件：** 使用复合主键，且保护清单遗漏其中部分列。README 中“把 A 行的数据连同 MAC 搬到 B 行，验签必失败”的表述在此条件下不成立。

**建议：** 默认强制覆盖完整主键，或自动将所有主键列纳入签名；如支持其他唯一键，应显式声明、验证其唯一性。调整 `SetKeys/AutoKeys` 后也要重新验证绑定条件。

**验收：** 两列、三列复合主键的任何一列变化都导致验签失败；未完整绑定时登记应失败，除非调用方明确选择退出行绑定保护。

### Q3：事务取消后无法恢复引擎状态

**位置：** [UniEngine.go:335](D:/test/UniEngine/UniEngine.go:335) 和 [UniEngine.go:353](D:/test/UniEngine/UniEngine.go:353)，`Cancel` / `Commit`。

两者都在底层返回错误时直接退出，仅在成功分支清理 `tx/inTx`。`BeginCtx` 的 context 取消后，`database/sql` 会自动回滚；这时 `Cancel()` 返回 `sql.ErrTxDone`，引擎却仍保存事务进行中的状态。

本次捕获到的结果：

```text
Cancel  -> sql: transaction has already been committed or rolled back
Begin   -> UniEngine: transaction already in progress
Execute -> sql: transaction has already been committed or rolled back
```

**影响：** 一次请求超时可能使共享引擎实例的后续请求持续失败。除了 context 自动回滚，提交/回滚终结时的错误分支也需要检查。

**建议：** 按 `sql.Tx` 的终态语义清理引擎引用，同时保留原始错误；不能只在成功时清理。长期建议引入局部事务对象或 `WithTx(ctx, fn)`，避免把请求事务挂在共享引擎上。不要用重新调用 `Initialize()` 作为事务恢复方式，它没有完整回收事务的语义。

**验收：** context 取消、`ErrTxDone`、提交失败、回滚失败后，都能按约定恢复普通执行并开始新事务，同时返回原始错误供调用方判断。

### Q4：完整性签名回填仍有反射 panic

**位置：** [UniVerify.go:169](D:/test/UniEngine/UniVerify.go:169)，`verifySignCols` 在第 174 行直接执行 `SetString`。

写接口接受结构体值；开启 `VerifyOn` 并成功 `SetVerify` 后，MAC 要写回该值的字段。按值传入时反射对象不可设置，当前没有 `CanSet()` 或指针要求检查。

已复现的入口：`Insert(row)`、`Update(row)`、`SaveIt(row)`、`SaveItWhenNotExist(row)`，错误均为：

```text
reflect: reflect.Value.SetString using unaddressable value
```

**建议：** 明确开启签名时的参数契约。可统一要求可写结构体指针并返回普通错误，或构造可寻址副本进行 SQL 参数组装；如果承诺 MAC 回填调用方，则应要求指针。

**验收：** 四个入口及其 context 变体在值参数、合法指针、typed nil、批量输入下均不得产生反射 panic；失败前不应执行 SQL。

### Q5：占位符转换没有理解 SQL 语法与编号

**位置：** [UniEngine.go:190](D:/test/UniEngine/UniEngine.go:190)，`getSqlQuery`。

当前正则会替换 SQL 任意位置的 `$N`，包含字符串常量和注释；MySQL 分支还把所有编号都替成 `?`，却原样保留实参数组。

本次通过 `Execute` 捕获：

```text
输入 SQL：update t set amount=$2, note='$1' where id=$1
输入参数：[7, 99]
输出 SQL：update t set amount=?, note='?' where id=?
输出参数：[7, 99]
```

字符串内容已从 `'$1'` 变为 `'?'`；按位置绑定时，`amount` 会接收 `7`，`id` 会接收 `99`，与原编号意图相反。重复使用 `$1` 也无法自动扩展参数。

**证据边界：** 本次确认的是实际 SQL 与实参生成错误；没有在真实 MySQL 上执行上述 UPDATE。Oracle 同样存在误改字符串常量的问题，但其 `:N` 绑定细节不能直接套用 MySQL 的结论。

**建议：** 优先要求调用者使用明确的方言原生占位符；如保留转换功能，应使用能识别常量、注释、标识符的词法扫描，并返回转换后的 SQL 与重排/重复后的参数。无法支持的编号形式应明确拒绝。

**验收：** 覆盖常量中的 `$1`、注释、乱序 `$2/$1`、重复 `$1`、参数缺失等情况，并在真实驱动中验证最终存储值。

### Q6：注册表锁只保护查找，没有保护表内 map 的后续访问

**位置：** [UniEngine.go:113](D:/test/UniEngine/UniEngine.go:113)、[UniRegister.go:132](D:/test/UniEngine/UniRegister.go:132)、[UniRegister.go:167](D:/test/UniEngine/UniRegister.go:167)、[UniQuery.go:93](D:/test/UniEngine/UniQuery.go:93)、[UniTable.go:56](D:/test/UniEngine/UniTable.go:56)。

`tableByType` 获取表指针后释放读锁；查询随后直接读取 `HashField`，写入会遍历 `HashField/HashPkeys`。同一时间 `RegisterField/RegisterPkeys` 在持有引擎写锁时直接修改现有表对象的 map，读端却没有继续持有对应锁。

**影响条件：** 向已经被查询或写入的同一张表调用这些注册接口。写端独自加锁无法保护未加锁的读端，存在数据竞争及并发 map 访问风险。

现有 [TestConcurrentQueryAndRegister](D:/test/UniEngine/UniEngine_test.go:744) 测试的是 `RegisterClass` 新建/替换表对象，不覆盖上述原地改 map 的路径。

**建议：** 优先采用不可变的表元数据快照，注册时构造新对象再发布；也可对表内部增加成对读写锁。若暂不支持动态扩展字段和主键，应收窄并发契约并在运行阶段显式拒绝这些变更。

**验收：** 在可运行 race detector 的环境中，并发执行同一表的 `RegisterField/RegisterPkeys` 与 `SelectL/Insert/Update`，确认无数据竞争且语义稳定。本次仅完成静态路径确认，没有声称得到 race 报告。

### Q7：重复列 tag 导致读写映射不对称

**位置：** [UniRegister.go:87](D:/test/UniEngine/UniRegister.go:87) 与 [UniTable.go:64](D:/test/UniEngine/UniTable.go:64)。

```go
type Row struct {
    A string `db:"name"`
    B string `db:"name"`
}
```

`HashField` 保留后出现的 B；`ListField` 保留两个字段，`orderedField` 去重后选择先出现的 A。探针结果为：写入参数来自 A，读取同一列却填到 B，登记阶段没有报错。

**建议：** 在注册阶段拒绝大小写归一化后重复的真实列名，并返回包含两个 Go 字段名的错误；`db:"-"`、`db:"_"` 等忽略/只读标记应单独处理，不能误拒多个标记字段。尽量让字段顺序与元数据只维护一份权威来源。

**验收：** 相同 tag 和仅大小写不同的重复 tag 均被拒绝；正常结构体往返映射保持一致。

### Q8：无效密文会污染无界派生缓存

**位置：** [UniSecret.go:95](D:/test/UniEngine/UniSecret.go:95) 以及 [UniSecret.go:239](D:/test/UniEngine/UniSecret.go:239)。

`deriveKey` 在 GCM 认证之前就把派生结果放入 `Keys`，缓存没有容量限制或淘汰策略。探针构造 5 个不同盐、迭代数为 1 的无效密文，解密全部失败，但缓存留下 5 个条目。

**影响条件：** 应用会读取不可信或被篡改的密文，或者长期运行中读到大量不同写入实例生成的盐。迭代上限能限制单次成本，但不能限制总计算量和缓存增长；冷派生还持有该引擎缓存锁。

**建议：** 增加缓存容量和淘汰机制，避免持久缓存认证失败的输入，合并同一派生任务，并限制密文大小及可接受的 KDF 参数。把攻击面与应用入口关联评估，不将它直接描述为无条件可远程利用的漏洞。

**验收：** 大量不同盐的无效密文不会造成缓存无界增长；并发冷/热解密有资源上限，合法跨实例密文仍能读取。

## 5. 其余值得提升的部分

### 5.1 API 与配置：让错误尽早暴露

- 新增 `NewEngine(Config) (*TUniEngine, error)` 或配置验证接口，检查 `Db`、支持的 provider、占位符和加密配置。保留现有初始化方式作为兼容入口。
- SQL Server 应显式记录驱动模式，并校验与 `ColParam` 的组合。仓库已详细记录 `mssql` 与 `sqlserver` 的差异，下一步应让错误配置在执行前失败；不能简单把全族统一改成 `@p`。
- 注册链的 `LastError` 需要调用方主动检查。建议 CRUD 遇到存在 `LastError` 的表时拒绝继续执行，避免半完成的主键/加密配置流入运行阶段。
- 为 `Update/Delete` 提供可选的受影响行数结果或严格版本，使“没有匹配任何行”可被业务区分。
- 对部分列查询、标量查询和自定义扫描钩子的校验边界给出统一说明。当前部分列读取会跳过验签，即使 `VerifyMode=2` 也不代表每次查询都验签；可新增严格读取接口，在缺少保护列时返回错误。

### 5.2 测试：优先提高断言质量和场景覆盖

- 修正 [UniEngine_test.go:210](D:/test/UniEngine/UniEngine_test.go:210) 的 `TestSetKeysUnregisteredFieldReturnsError`：它把 `tbl.SetKeys(...)` 返回的表指针命名为 `err` 并检查 `err == nil`，实际没有检查 `LastError`，不能证明未知字段被拒绝。其他测试中已有正确检查方式，应保持一致。
- 将本文探针转换为“正确行为断言”的正式回归测试。附录中的探针为了记录现状会把成功复现缺陷视为 PASS，**不能直接作为修复后的质量门禁**。
- 对 MAC 采用独立的不变量测试：不同字段元组编码不同、完整主键变化必失败、跨表不能移植、部分查询是否验签可观测。不要只复刻生产实现的拼接算法来计算期望值。
- 扩充可注入提交/回滚/Prepare/Exec 错误的 mock 驱动，并补充真实库的超时取消测试。
- 集成测试补充非默认 schema、混合大小写/保留字、重复 tag、复合主键以及 MySQL 参数重排等情况。
- 维持现有 75% 覆盖率门槛，并优先补低覆盖关键路径；之后再按稳定水位逐步提高门槛，不为数字编写无有效断言的测试。

### 5.3 方言与批处理语义

- 元数据查询应明确处理 `schema.table`。例如 [DbaEngine.go:225](D:/test/UniEngine/DbaEngine.go:225) 的 PostgreSQL 表探测只比较 `relname`，不能正确表达 schema 限定；还应区分表、视图及其他同名对象。这是静态审查建议，本次没有真库复现。
- `InsertP/CopyInP` 只特殊处理 `0/-1`，其他负值不会触发分页。统一页大小验证，并依据字段数及驱动参数限制计算安全批量。
- 分页写入应明确整体原子性及部分成功语义。pgx COPY hook 当前主动拒绝事务，限制本身已经明确，但需要让调用者能方便选择合适的批量事务方案。
- MySQL 与自定义 SQL hook 的 `SaveIt` 回退仍为 count-then-write。建议通过冲突处理/重试或受控事务表达原子语义，并在文档中区分不同路径的并发保证。

### 5.4 性能、观测与文档

- 在元数据冻结后缓存字段顺序、读写列索引和可复用 SQL 模板，减少每次调用的排序、map 构造和反射查找。先测实际工作负载，再引入复杂缓存。
- 当前每次查表/查事务都要经过包级 `muLazy`，加解密也经过包级状态初始化锁。已有 `noCopy` 约束后，可考虑值类型锁、`sync.Once` 等方案消除稳定态的全局初始化锁竞争。
- 评估一次性 `Prepare/Exec/Close` 相比直接 `ExecContext/QueryContext` 的驱动往返成本；语句缓存只在真实基准证明收益后引入，并明确生命周期。
- `sqlDebug` 直接打印实参，建议提供可注入日志接口、默认参数脱敏、耗时/影响行数/错误分类，避免调试开关将业务敏感值写入日志。
- 收敛文档中的绝对化描述。事务串行要求可作为引擎自身契约保留，但不宜简单归因于“`database/sql.Tx` 不支持并发”；关键风险是本引擎共享的事务归属与生命周期。
- 历史报告作为历史资料保存，新结论标注提交和实测环境；及时删除“全部 panic 已清零”“加入任一主键即完整行绑定”等过度保证。

## 6. 原始建议实施顺序与验收门槛（历史）

| 阶段 | 工作 | 规模估计 | 完成标准 |
| --- | --- | --- | --- |
| 第一批：可靠性 | Q3 事务恢复、Q4 可写性检查、Q7 重复 tag、错误断言修正 | 小至中 | 正常错误返回代替 panic；超时后实例可恢复；映射冲突登记即失败 |
| 第二批：完整性与数据正确性 | Q1 MAC 编码、Q2 完整行绑定、Q5 参数转换 | 中；包含格式兼容和迁移设计 | 篡改探针失败；旧格式有明确迁移方案；真实驱动验证绑定后的值 |
| 第三批：并发与资源边界 | Q6 元数据快照/同步、Q8 有界缓存、配置验证 | 中 | 同表动态注册 race 测试通过；坏输入资源消耗有上限；非法配置提前失败 |
| 持续改进 | 覆盖率薄弱路径、分页语义、schema 支持、日志与性能基准 | 持续 | CI 验证可重复，文档与实际支持范围一致 |

以上为相对工作量，不是工期承诺。若系统已经依赖 `SetVerify` 保护授权或状态数据，**第二批应与第一批并行安排或提前**。

建议把以下条件作为下一次提升到 80 分以上的复评门槛，而非修完就自动加分：

1. 本文所有 P1 有回归用例和明确处理结果。
2. 在支持的 Go/操作系统环境中完成 race 检查；至少补充同表动态注册和事务取消场景。
3. 四族真实驱动测试有可追溯的成功记录，关键校验/参数语义覆盖不能只靠 mock。
4. MAC 格式兼容、严格读取语义、事务归属与分页部分成功语义形成稳定契约。

## 7. 原始评审交付说明（历史）

本次未修改生产代码、现有测试或历史评估报告。临时探针与覆盖率文件在评估结束后清理；探针完整源码保留在下面的附录，便于复核。

附录探针依赖当前仓库测试夹具，在本报告所列提交上运行。复制为根目录的 `zz_quality_review_test.go` 后执行：

```powershell
go test -run '^TestReview' -v -count=1 .
```

**注意：这些探针的 PASS 意味着旧缺陷被复现；修复后应反转相应断言，改为验证安全、正确的目标行为。** 本次使用的只是内存 mock 数据库，不会连接或改写真实业务数据库。

## 附录：本次复现探针源码

```go
package UniEngine

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"time"
)

// Temporary evidence probes: PASS means the described defect was reproduced.
func TestReviewVerifyValuePanic(t *testing.T) {
	for _, method := range []string{"Insert", "Update", "SaveIt", "SaveItWhenNotExist"} {
		t.Run(method, func(t *testing.T) {
			e := newVerifyTestEngine(&mockDriver{})
			defer e.Db.Close()
			defer func() {
				if p := recover(); p != nil {
					t.Logf("reproduced panic: %v", p)
				} else {
					t.Error("expected reproduction of unaddressable MAC field panic")
				}
			}()
			r := TVerifyRow{WhoBuild: 1, UserIndx: 2}
			switch method {
			case "Insert":
				_ = e.Insert(r)
			case "Update":
				_ = e.Update(r)
			case "SaveIt":
				_ = e.SaveIt(r)
			case "SaveItWhenNotExist":
				_ = e.SaveItWhenNotExist(r)
			}
		})
	}
}

func TestReviewCancelledTransactionStuck(t *testing.T) {
	e := newMockEngine(&mockDriver{})
	defer e.Db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := e.BeginCtx(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	deadline := time.Now().Add(time.Second)
	for {
		stmt, err := e.currentTx().PrepareContext(context.Background(), "select 1")
		if stmt != nil {
			stmt.Close()
		}
		if errors.Is(err, sql.ErrTxDone) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("rollback was not observed: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	rollbackErr, beginErr, writeErr := e.Cancel(), e.Begin(), e.Execute("select 1")
	if !errors.Is(rollbackErr, sql.ErrTxDone) || !errors.Is(beginErr, ErrAlreadyInTransaction) || !errors.Is(writeErr, sql.ErrTxDone) {
		t.Fatalf("unexpected results: %v / %v / %v", rollbackErr, beginErr, writeErr)
	}
	t.Logf("Cancel=%v; Begin=%v; Execute=%v", rollbackErr, beginErr, writeErr)
}

type reviewMACRow struct {
	ID  int64  `db:"id"`
	A   string `db:"a"`
	B   string `db:"b"`
	MAC string `db:"mac"`
}

func TestReviewMACSeparatorCollision(t *testing.T) {
	d := &mockDriver{columns: []string{"id", "a", "b", "mac"}}
	e := newMockEngine(d)
	defer e.Db.Close()
	e.VerifyOn, e.VerifyMode, e.VerifyBy = 1, 2, "review-test-key"
	tbl := e.RegisterClass(reviewMACRow{}, "review_mac").SetKeys("id").SetVerify("mac", "id", "a", "b")
	if tbl.LastError != nil {
		t.Fatal(tbl.LastError)
	}
	r := reviewMACRow{ID: 1, A: "admin\x1f0", B: "user"}
	if err := e.Insert(&r); err != nil {
		t.Fatal(err)
	}
	d.queryRows = [][]driver.Value{{int64(1), "admin", "0\x1fuser", r.MAC}}
	var got reviewMACRow
	if err := e.Select(&got, "select id,a,b,mac from review_mac"); err != nil {
		t.Fatal(err)
	}
	if got.A == r.A || got.B == r.B {
		t.Fatal("probe did not change the fields")
	}
	t.Log("strict verification accepted changed A and B with the original MAC")
}

func TestReviewCompositeKeyPartialBinding(t *testing.T) {
	d := &mockDriver{columns: verifyRowColumns()}
	e := newVerifyTestEngine(d)
	defer e.Db.Close()
	e.VerifyMode = 2
	tbl := e.GetTable("tbl_verify_row").SetVerify("mac_text", "who_build", "data_stat", "mast_name")
	if tbl.LastError != nil {
		t.Fatal(tbl.LastError)
	}
	r := TVerifyRow{WhoBuild: 1, UserIndx: 2, DataStat: 1, MastName: "allowed"}
	if err := e.Insert(&r); err != nil {
		t.Fatal(err)
	}
	d.queryRows = [][]driver.Value{{int64(1), int64(999), int64(1), "allowed", r.MacText}}
	var got TVerifyRow
	if err := e.Select(&got, "select * from tbl_verify_row"); err != nil {
		t.Fatal(err)
	}
	t.Logf("strict verification accepted original MAC on changed composite key: user_indx=%d", got.UserIndx)
}

func TestReviewPlaceholderRewriting(t *testing.T) {
	d := &mockDriver{}
	e := newMockEngine(d)
	defer e.Db.Close()
	e.Provider, e.ColParam = DtMYSQLN, "?"
	if err := e.Execute("update t set amount=$2, note='$1' where id=$1", 7, 99); err != nil {
		t.Fatal(err)
	}
	queries := d.snapshotQueries()
	if !strings.Contains(queries[0], "note='?'") {
		t.Fatal(queries)
	}
	t.Logf("generated SQL=%s; arguments=%v (amount is bound to 7, id to 99)", queries[0], d.insertValues[0])
}

func TestReviewDuplicateTagAsymmetry(t *testing.T) {
	type row struct {
		A string `db:"name"`
		B string `db:"name"`
	}
	d := &mockDriver{columns: []string{"name"}, queryRows: [][]driver.Value{{"from-A"}}}
	e := newMockEngine(d)
	defer e.Db.Close()
	tbl := e.RegisterClass(row{}, "review_duplicate")
	if tbl.LastError != nil {
		t.Fatal(tbl.LastError)
	}
	r := row{A: "from-A", B: "from-B"}
	if err := e.Insert(&r); err != nil {
		t.Fatal(err)
	}
	var got row
	if err := e.Select(&got, "select name from review_duplicate"); err != nil {
		t.Fatal(err)
	}
	if d.insertValues[0][0] != "from-A" || got.B != "from-A" || got.A != "" {
		t.Fatal("unexpected probe result")
	}
	t.Logf("write arguments=%v; read result=%+v", d.insertValues[0], got)
}

func TestReviewInvalidCiphertextCache(t *testing.T) {
	e := &TUniEngine{SecretOn: 1, SecretBy: "review-test-key"}
	for i := 0; i < 5; i++ {
		data := make([]byte, secretHeadLen+16)
		data[0] = byte(i)
		binary.BigEndian.PutUint32(data[16:20], 1)
		if _, err := e.Secret(UniSecretTag+base64.StdEncoding.EncodeToString(data), false); err == nil {
			t.Fatal("expected authentication failure")
		}
	}
	if len(e.secret.Keys) != 5 {
		t.Fatalf("cache size=%d", len(e.secret.Keys))
	}
	t.Logf("five rejected ciphertexts left %d cached derived keys", len(e.secret.Keys))
}
```
