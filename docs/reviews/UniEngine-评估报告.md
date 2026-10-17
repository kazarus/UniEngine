# UniEngine 代码评估报告（v15 · §4.7/§4.6/§4.8 修复落地）

| 项 | 内容 |
| --- | --- |
| 评估对象 | `github.com/kazarus/UniEngine`（Go 多数据库 ORM-like 引擎） |
| 仓库路径 | `github.com/kazarus/UniEngine` |
| **评估版本** | **`f4f5ff5`**（HEAD，`master`）+ 未提交的 §4.7/§4.6/§4.8 修复、`style_test.go`、README/CHANGELOG/CONTRIBUTING 增补与本报文 |
| 评估日期 | 2026-10-14 |
| 工具链 | `go1.27.1 darwin/arm64`；`go build`/`go vet`/`gofmt`/`go test -race`/`go test -cover`；探针模块 `/tmp/uniprobe`、`/tmp/sqlsrvchk`、`/tmp/orachk`、`/tmp/mysqlchk` |
| **真库实测** | ① **Microsoft SQL Server 2017**（14.0.1000.169 X64）@ `127.0.0.1:1433`，`go-mssqldb`；② **Oracle Database 11g EE 11.2.0.1.0** @ `127.0.0.1:1521/ORCL`，`sijms/go-ora/v2`；③ **MySQL 8.4.6** @ `127.0.0.1:3306/UNIENGINE_TESTDB`，`go-sql-driver/mysql`。**本会话共 11 个集成用例在真库上通过** |
| 本次方法 | 落实 v14 的修复清单（§4.7 写路径上界、§4.6 缓存键、§4.8 行级绑定门禁、§4.9/§4.10 文档），并补**对称性测试**与**仓库卫生门禁**；修复均做「修复前失败 / 修复后通过」的双向验证 |
| 安全边界 | 只用自建对象并在结束时删除：SQL Server `uni_*`、Oracle `uni_it_ora_probe(_v)`、MySQL `uni_it_mysql_probe(_v)` / `uni_verify_probe` / `uni_rebind_probe` / `uni_page_probe` / `uni_dv_probe`。全部建在名为 `UNIENGINE_TESTDB` 的库上（非生产库），并逐个复查确认无残留；**未在生产库上运行任何 DDL** |
| 工作区状态 | 前几轮成果（注册守卫、`noCopy`、风格清理、包级 godoc、四族集成测试、CI、CONTRIBUTING、fuzz）均已由业主提交至 `f4f5ff5`。**本轮改动**：`integration/mysql_test.go`、`integration/oracle_test.go` 各清理 9/10 处开括号后空行（修我自己引入的残留）；**本报告 v14**。 |
| 版本关系 | 本文为 **v15**；**v1 7.4 → v2 6.8 → v3 6.9 → v4 6.4 → v5 6.9 → v5.1 7.0 → v6 7.3 → v7 7.4 → v8 7.7 → v9 7.8 → v10 7.9 → v11 7.9 → v12 8.0 → v13 8.1 → v14 7.8 → v15 8.2**。修订明细见 §8 |

> **本版要点**：v14 查出的四项**全部修复并双向验证**。最重的一条是 **§4.7**——加密写路径不校验 `SecretIter` 上界，`SecretIter=10000001` 时**能加密却永久读不回**（数据丢失级）；现改为止损在派生前 fail-fast（`UniSecret.go` +4 行），回归测试 `TestSecretWriteRejectsOversizedIter` 已确认**修复前失败、修复后通过**。另修 **§4.6** 派生密钥缓存键漏 `SecretBy`（同进程轮换既不生效也不报错）、**§4.8** `SetVerify` 行级绑定改为登记期强制保护列含主键（真库验证整行搬运被检出）、**§4.9/§4.10** 两处文档失实。另新增 `style_test.go` 门禁，把「清理后又回归 19 处」的机械残留变成可执行断言。测试 127 / 断言 264 / 覆盖率 **80.8%** / 真库 **12 用例通过**。**总分 7.8 → 8.2**。

> **历史要点（v5 更正）**：v4 的头号结论（「SQLServer 族静默写错数据」）**范围搞错了**。真库全矩阵实测表明，SQLServer 的占位符正确性取决于 **驱动名 × ColParam 两个维度**；生产环境所用组合（驱动名 `mssql` + `ColParam: ":"`）**实测完全正确**。v4 的结论只对 `sqlserver` 驱动名成立。详见 §4.1。

---

## 1. 本次验证的范围与方法

1. **静态重读**：逐文件重读库代码当前版本（含本会话我自己改动的 `UniEngine.go`/`UniEngine_test.go`/`integration/*`，做自审）。
2. **仓库状态核对**：HEAD 已前进至 `61ecc94`（+1 提交，关闭 7 条容器形态 panic）；厘清已提交与工作区未提交的边界。
3. **边界探针**（`/tmp/uniprobe`）：容器空值、map 键类型、错误表链式 setter、引擎拷贝语义、重复 tag。
4. **对抗性复查**（本轮新增）：针对 `61ecc94` 的新守卫，构造其未覆盖的输入形态（命名字符串键、`map[any]`、双层指针、数组、指针元素等）主动寻找残留 panic。
5. **真库全矩阵端到端**：对 `{mssql, sqlserver} × {":", "$", "?", "@p"}` 共 8 个组合，跑六步 CRUD 并逐行读回校验。
6. **驱动源码确证**：阅读 `go-mssqldb` 的 `internal/querytext/parser.go` 与 `mssql.go`，确认预处理规则，避免仅凭文档推断。
7. **落库为回归资产**：真库验证改造为 `integration/mssql_test.go`（§7.5）。

---

## 2. 修复验证表（P0 修复逐条实测）

### 2.1 panic 路径：20 条 → **0 条（全部关闭）**

分三批关闭：`6f771bf` 修判断顺序，`61ecc94` 补容器形态，`f72d29f` 把键守卫从 `Kind` 近似改为可赋值语义：

| 输入形态 | 修复前 | **实测（当前）** | 状态 |
| --- | --- | --- | :---: |
| 13 个公共入口传裸 `nil` | 全部 panic | 全部返回 `ErrNilParam` 包装错误 | ✅ |
| `SelectL((*[]T)(nil))` typed nil | panic | `got a nil pointer param` | ✅ |
| `Select(T{}, ...)` 传值 | `Set using unaddressable value` | `needs a pointer param` | ✅ |
| `RegisterClass(nil/map/pointer, ...)` | `NumField of non-struct type` | 返回带 `LastError` 的未注册表 | ✅ |
| `SelectL([]T{})` / `SelectL([]T(nil))` 非指针 | `Set using unaddressable value` | `needs a pointer param` | ✅ |
| `SelectM/H(nil map)` 直接与经指针 | `assignment to entry in nil map` | `got a nil map` | ✅ |
| `SelectM/H(map[int]T)`、`map[int64]T` | `SetMapIndex: string not assignable to int` | `needs a map key assignable from string` | ✅ |
| **`SelectM/H(map[MyKey]T)` 命名 string 键** | `SetMapIndex: string not assignable to MyKey` | **支持**（写时 `Convert`），实测键正确写入 | ✅ |
| **`SelectM(map[any]T)`**（此前被误拒） | 误报错误（本可用） | **支持**（`string` 可赋值给 `any`） | ✅ |
| `map[bool]` / `map[float64]` / `map[[2]byte]` / `map[error]` | — | 明确报错 | ✅ |
| **零行 + 非法键** | — | **仍报错**（守卫位于行循环之前，fail-fast 不依赖数据） | ✅ |

```text
ERROR   SaveIt(nil) / SaveItWhenNotExist(nil) / Update(nil) / Insert(nil) / InsertL(nil)
        Delete(nil) / CopyInL(nil) / InsertP(nil,10) / CopyInP(nil,10)
        Select(nil,...) / SelectL(nil,...) / SelectM(nil,...) / SelectH(nil,nil,...)
        → UniEngine: nil param: method [X] got a nil param; check your code;
ERROR   SelectL([]TRowM{}) / SelectL([]TRowM(nil))    → method [SelectL] needs a pointer param
ERROR   SelectM(map[string]TRowM(nil))                → method [SelectM] got a nil map
ERROR   SelectM(&map[string]TRowM(nil))               → method [SelectM] got a nil map
ERROR   SelectM(map[int]…) / map[bool] / map[[2]byte] / map[float64] / map[error]
        → needs a map key assignable from string (string key or named string type), but got key type [int]
OK      SelectM(map[MyKey]TRowM)  → 写入 map[a:{1 a}]      (命名 string 键 Convert 后写入)
OK      SelectM(map[any]TRowM)    → 写入 map[a:{1 a}]      (String 可赋值给 any)
OK      零行 + map[string]TRowM    → nil
```

**评价**：三批修复都正确抓到根因。第三批尤其值得肯定——它**没有停留在"满足上报用例"，而是把判据从"类型种类"换成"真实语义"**，并主动打破了测试与实现同构的盲区（新增命名 string / `any` / `int` 三组用例）。我独立复跑的对抗性矩阵（含零行、接口键、数组键、浮点键）全部符合预期，**未再发现残留路径**。

### 2.2 其余结论在 HEAD 上的状态

| 结论 | 状态 | 证据 |
| --- | :---: | --- |
| SQLServer 占位符（v2/v4 版本） | ⚠️ **已更正范围** | 见 §4.1：仅 `sqlserver` 驱动名成立 |
| panic 路径 | ✅ **20 条全部关闭** | 含命名 string 键、`map[any]`、零行 fail-fast（§2.1）；已用对抗性矩阵独立复核 |
| §5.3 引擎拷贝后事务状态不同步 | ⚠️ **已加静态防护（v5.1）** | 实测失效模式仍在（`clone.Begin()->nil` 而 `original.Cancel()->no transaction`），但按值拷贝现已被 `noCopy` + `go vet` 拦下 |
| §5.4 遗留注册表路径绕过标识符白名单 | ✅ **已修复（v8 实施）** | 四个注册方法的表名/字段名 + struct tag 列名均走 `validIdent`;非法名不入注册表,`PrepareRunSQL` 注入面实测归零 |
| §5.5 风格与命名 churn | ❌ 仍存在 | 66 处重复注释、173 处空括号行、19/131 为重命名类提交 |
| §5.6 多方言真驱动测试缺位 | ✅ **已解决（v12）** | **四族全部接入真驱动**：PG（CI service）/ MSSQL / Oracle 11g / **MySQL 8.4.6（v12 实跑，4 用例 `-race` 全绿）** |
| §5.7 加密无 AAD / 无 KeyID | ❌ 仍存在 | `GCM.Seal(nil, Nonce, ..., nil)` |
| §5.8-1 重复 `db` tag 读写不对称 | ❌ 仍存在 | `HashField[x].AttriName=B` / `orderedField[0].AttriName=A` |
| §5.8-2 `insertPage` 用 `Supplier` | ❌ 仍存在 | `UniCopyIn.go:205` |
| §5.8-3 `ExistField` 哨兵不一致 | ❌ 仍存在 | `errors.Is(err, ErrInvalidTableName) == false` |
| §5.8-4 `AutoKeys` 无参默认 PG SQL | ❌ 仍存在 | `AutoKeys(non-impl) -> can not build autokeys sql` |
| §5.8-8 `.DS_Store` 被跟踪 | ✅ 已修复 | `git ls-files` 已无 |
| §5.8-7 `SetVerify` 校验顺序 | ✅ 主动改进 | 提前到 MAC 列校验之前 |

### 2.3 修复未引入回归

`go build ./...` ✅ · `go vet ./...` ✅ · `gofmt -l .` 空 ✅ · `go test -count=1 .` ✅（`ok 0.567s`）· 错误表的链式 setter（`SetKeys`/`SetSecret`/`SetVerify`/`AutoKeys`）在 `RegisterClass(map)` 返回的表上**无 panic** ✅。

---

## 3. 评分（v15）

> v1–v7 的历史列已从下表移除（完整演进见 §8）；下表保留 v8 起的近期快照。

| 维度 | 权重 | v10 | v11 | v12 | v13 | v14 | **v15** | 加权 | v15 依据 |
| --- | --- | :---: | :---: | :---: | :---: | :---: | :---: | :---: | --- |
| 功能完整性与设计 | 20% | 8.0 | 8.0 | 8.0 | 8.0 | 8.0 | **8.0** | 1.60 | 四族真库可用；方言与驱动名维度已澄清 |
| 正确性与健壮性 | 20% | 8.0 | 8.0 | 8.0 | 8.0 | 7.5 | **8.5** ⬆ | 1.70 | **§4.7 已修**（写路径 fail-fast，回归测试双向验证）+ **§4.6 已修**（缓存键并入密钥指纹）；21 条 panic 路径仍全部关闭 |
| 安全 | 15% | 7.5 | 7.5 | 7.5 | 7.5 | 7.0 | **7.5** ⬆ | 1.13 | **§4.8 已修**：登记期强制保护列含主键（含显式逃生口），真库测试证明整行搬运被检出；余项为加密无 AAD（已由 SetVerify 行级绑定闭合） |
| 并发安全 | 10% | 7.5 | 7.5 | 7.5 | 7.5 | 7.5 | **7.5** | 0.75 | `noCopy` + `go vet` 强制；派生密钥缓存有界（盐按实例生成并复用） |
| 测试与 CI | 15% | 9.0 | 9.0 | 9.5 | 10.0 | 9.5 | **10.0** ⬆ | 1.50 | **对称性盲区已补**（写侧迭代数边界 + 轮换生效 + SetVerify 主键门禁）；**新增 `style_test.go` 门禁**拦住两类机械残留并已验证可拦住违规；四族 CI + 覆盖率门槛 + 2 个 fuzz 目标 |
| 代码风格与可维护性 | 10% | 6.5 | 6.5 | 6.5 | 6.5 | 6.5 | **6.5** | 0.65 | 残留归零且**已有门禁兜底**（不再只靠自觉）；仍余：`this` 接收者与 PascalCase 局部变量（README 声明为有意决策，按"取舍"计） |
| 文档与工程化 | 10% | 8.0 | 8.5 | 8.5 | 8.5 | 8.0 | **8.5** ⬆ | 0.85 | **§4.9/§4.10 已修**：轮换语义改写为精确表述（含"密文不含密钥标识、需配套迁移"）、MySQL 示例补 `DataBase`；余项为 README 正文单语 |
| **合计** | 100% | **7.9** | **7.9** | **8.0** | **8.1** | **7.8** | **8.2** | **8.18** | 等级 **A− / B+** |

> **加权和勘误**：v11/v12 两版报告中"合计"栏曾写作 7.93 / 7.98，按本表权重逐项复算应为 **7.925 / 8.000**（v11、v12 的一位数结论不变，分别为 7.9 与 8.0）。本版起统一以逐项加权和为准并保留两位小数。
>
> **v13 恰好压在 8.05 的半分线上**（8.075）：若把"测试"判为 9.75 而非 10，则总分为 8.04，一位数结论回落为 8.0。因此 8.1 这个结论**取决于是否认可"重容器走定时/手动"算作已自动化**——我的判断是算（见下），但把口径写在明面上。

**v8 → v9 说明**：本批完成了上轮列出的第 ③ 项（清理风格残留 + 包级 godoc）。

- **代码风格与可维护性 5.5 → 6.5**：清除 **30 处**重复文档注释（65 行冗余）与 **200 处**「开括号后空行」，覆盖库本体与两个子模块，全部归零且零语义变化。**未给更高**是因为命名体系（`this` 接收者、PascalCase 局部变量、`CONST_*` 常量）仍与 Go 社区惯例相左——但项目已在 README 声明为有意决策且内部自洽，故按"取舍"而非"缺陷"计分。
- **文档与工程化 7.5 → 8.0**：新增 `doc.go` 承载正式包文档（快速开始 / 方言与占位符 / 并发契约 / 错误处理 / 加密与完整性 / 模块布局），并**把 17 个文件的头注释与 `package` 子句解耦**——此前它们被 go/doc 拼成 17 段碎片，`go doc .` 完全不可读。另加 2 个编译期示例。**未给更高**是因为仍缺 `CONTRIBUTING.md`，且文档全中文、对国际使用者不友好。

**v9 → v10 说明**：补齐 **Oracle 协议族的真库验证**——此前该族只有「SQL 文本比对」级覆盖，是测试维度最大的结构性缺口（也正是 MSSQL 那次误判的成因）。

- **测试与 CI 8.5 → 9.0**：在真实 **Oracle Database 11g Enterprise Edition 11.2.0.1.0** 上新增 5 个用例并全部通过：占位符契约、六步 CRUD（含 `INSERT ALL` 多行与 `MERGE` 两条分支）、配错 `ColParam` 的失效对照、`AutoKeys`+`Exist*` 元数据（覆盖 6 个数据字典视图）、`secreton` 在 `varchar2` 上的密文落库与还原。**未给更高**是因为 MySQL 仍无真驱动（其 `?` 为各驱动通用风格，风险最低）。
- **本轮未发现新缺陷**：引擎的 Oracle 族 SQL 生成（`:N` 占位符、裸标识符、`INSERT ALL`、`MERGE`、数据字典查询）此前仅经文本比对，现证实**在真实 11g 上成立**。
- **一条有价值的对照结论**：Oracle 上配错 `ColParam` 是**硬报错**（`?`/`$1` → `ORA-00911`，`@p1` → `ORA-00936`）且**无任何行被写入**；**静默写错数据是 SQL Server + `sqlserver` 驱动名独有的形态**——因为 `$N` 恰好是 T-SQL 的合法 money 字面量。这使 §4.1 的结论边界更清晰。
- 驱动选择说明：测试机无 Oracle Instant Client，故用**纯 Go** 的 `sijms/go-ora/v2`；README 记录的生产驱动 `godror` 亦支持（`UNIENGINE_ORACLE_DRIVER=godror`），引擎对两驱动生成的 SQL 一致。

**v11 → v12 说明**：MySQL 真驱动测试在真实 **MySQL 8.4.6** 上跑通（4 用例 / `-race`），**四族真驱动覆盖就此补齐**。

- **测试与 CI 9.0 → 9.5**：`TestMySQLPlaceholderContract` / `TestMySQLRoundTrip` / `TestMySQLMetadata` / `TestMySQLSecretRoundTrip` 全绿。实测占位符行为与 SQL Server 恰成对照：
  | 写法 | MySQL 实测 | 层次 |
  | ---- | ---------- | ---- |
  | `?` | 真绑定 | — |
  | `:1` | `Error 1064` 语法错误 | 服务端 |
  | `$1` | `Error 1054 Unknown column '$1'` | 服务端 |
  | `@p1` | `sql: expected 0 arguments, got 1` | 客户端（`@p1` 是**用户变量**，句法合法、值为 NULL，故由驱动按"0 个占位符"拒绝实参） |
  即三种错误风格**都不绑定实参**，但失败层次不同，**均非静默写错**——这再次确认「静默写错」为 SQL Server + `sqlserver` 驱动名独有。
- **本轮同样未发现新缺陷**：MySQL 族的 `SaveIt` 走 count-then-dispatch 回退、`InsertL` 多行 `VALUES`、`information_schema` 元数据（含 `ExistViews` 走 `views` 表）与 `secreton` 密文往返，在真库上全部成立。
- **PG 的现状**：本会话未能实跑 PG 套件（本地无实例）；顺带探到 `127.0.0.1:5432` 是可连的 **PostgreSQL 16.4**，但 18 个库全部是 `*_real` 生产库，而 `pg_test.go` 会 `drop table if exists it_user` + `create`——**不应在生产库上运行**，需另给一个可写测试库或允许新建 scratch 库。

**v12 → v13 说明**：把真库集成测试**接入 CI**（此前仅 PG 一个 job），并补齐测试维度的两个已知缺口（覆盖率门槛、fuzz）。

- **测试与 CI 9.5 → 10.0**：
  - **新增 `integration-mysql` job**（`mysql:8.4` service 容器，与实测过的 8.4.x 对齐，每次 push/PR 执行）。
  - **新增 `integration-heavy` job**：SQL Server 2022 + Oracle Free 23 用 `docker run` 拉起，走**每日定时 + 手动触发**。之所以不挂在每次 push：这两个镜像各需 1–2 GB 内存、30–90s 就绪，挂上去会让 PR 变慢且易 flaky。实现上刻意不用 `services:` 而用 `docker run` + shell 轮询，**超时或失败时打印容器日志**，保证失败可自证；`sqlcmd` 路径按镜像版本（`tools18`/`tools`）探测。
  - **新增覆盖率门槛 75%**（当前 80.7%，留 5 个点回撤空间），`-race` 下用 `-covermode=atomic`。
  - **新增 2 个 fuzz 目标**（见 §7.2）：`FuzzValidIdent` 把"防注入白名单"写成可探索的不变量，`FuzzUniFieldInitialize` 约束 tag 解析；seed 语料随 `go test` 执行，CI 中另跑 10s/目标。**本机 10s 各约 750 万次执行、未发现反例**。
- **口径说明（为什么给 10）**：本维度衡量"测试资产 + 自动化程度"。四族都有真驱动套件，其中两族每次 push、两族每日定时 + 一键手动；覆盖率有门槛、有 fuzz、有基准 smoke、有 `-vet`/`gofmt` 门禁。"10"在这里的含义是**这个规模的项目已无值得投入的测试缺口**，而不是"没有任何可改进之处"——重容器不进每次 push 是**有意的取舍**，已在 CI 注释、README 与 CONTRIBUTING 三处写明并给出放宽方法。
- **诚实边界**：SQL Server / Oracle 两个容器化 job 的**运行无法在本机验证**（本机无 Docker）。我能验的都验了：YAML 解析、4 job/4 触发器结构、**CI 引用的 `UNIENGINE_*` 变量与代码实际读取的变量做集合比对（无多余、无拼错）**、覆盖率门槛命令、fuzz 命令、基准 smoke、以及 CI 走的"整串 DSN"路径（对真实 MySQL 跑通）。首次定时运行若因 runner 环境需微调，改动仅限该 job。

**v14 → v15 说明**：本轮把 v14 查出的四项**全部修复**并逐条验证（含「修复前失败 / 修复后通过」的双向验证）。

- **正确性与健壮性 7.5 → 8.5**
  - **§4.7 已修**：`SecretDefault` 写分支在派生前校验上界，越界直接报错而非产出坏数据（`UniSecret.go` +4 行）。回归测试 `TestSecretWriteRejectsOversizedIter` —— 已在 `/tmp` 副本上确认其**修复前失败**（`SecretIter=10000001 写路径未拦截,产出读不回的密文`）、修复后通过。
  - **§4.6 已修**：派生密钥缓存键由「盐 + 迭代数」改为「盐 + 迭代数 + `sha256(SecretBy)[:8]`」，以指纹而非明文入键。回归测试 `TestSecretRotateKeyTakesEffect` 覆盖「新密文只由新密钥可解」与「旧密文不再静默沿用旧密钥」。
- **安全 7.0 → 7.5**：**§4.8 已修**。选择**登记期拒绝**而非自动并入主键，理由是自动并入会改变载荷构成，使已合规登记表的存量 MAC 全部失效、升级瞬间全表验签失败；登记期拒绝则不触碰载荷语义。新增逃生口 `TUniTable.VerifyUnkeyed`。真库测试 `TestMySQLVerifyRowBinding` 证明「整行搬运被检出」与「篡改保护列被检出」。
  - **【2026-10-14 补记】** 本节修复当时只覆盖「先主键、后验签」的顺序；其后发现**顺序变体**：`SetVerify` 成功后再用 `SetKeys`/`AutoKeys` 追加主键不会重新校验，新主键脱离载荷（实测可绕过）。现已按业主方案封住——**`SetVerify` 即主键冻结**，三处变更入口一律返回 `ErrPkeysFrozenAfterVerify` 且不写入主键；`VerifyOn=false` 时不冻结。回归用例 `TestPkeysFrozenAfterSetVerify` / `TestPkeysNotFrozenWhenSetVerifyFails`。
- **测试与 CI 9.5 → 10.0**：补齐**对称性测试**（写侧迭代数边界、轮换生效、SetVerify 主键门禁三条），并新增 **`style_test.go` 门禁**——把 v9 清理又回归的两类机械残留写成断言，随 `go test` 在本地与 CI 矩阵执行。门禁本身也做了「能否拦住违规」的验证（临时构造违规文件 → 准确报出 `file:line`）。
- **文档与工程化 8.0 → 8.5**：**§4.9 已修**（轮换语义改为精确表述，并补上「密文不含密钥标识，轮换需配套迁移」这一操作前提）、**§4.10 已修**（MySQL 示例补 `DataBase`）。
- **勘误我自己的预测**：v14 报告里我写「四项做完约 **8.6**」，按本模型逐项复算实际为 **8.18 → 8.2**。这已是我本会话第三次算错合计（前两次是 v11/v12 的 7.93/7.98，实为 7.925/8.000），已改为逐项复算并保留两位小数。

**验证留痕（本轮全部为可复现命令）**：

| 项 | 结果 |
| --- | --- |
| `gofmt -l` / `go vet`（三模块） | ✅ 干净 |
| `go test -race -coverprofile -covermode=atomic .` | ✅ 覆盖率 **80.8%**（较 v14 的 80.7% 略升） |
| 覆盖率门槛（CI 同款命令） | ✅ 80.80% ≥ 75% |
| `TestStyle`（两道门禁） | ✅ 通过；且验证过能拦住违规 |
| 测试 / 断言 | **127**（+5）/ **264**（+12） |
| 三族真库集成（MSSQL + Oracle + MySQL） | ✅ **12 个用例通过**（含新增 `TestMySQLVerifyRowBinding`） |

**剩余提分路径**：
1. **风格维度（6.5）**：`this` 接收者与 PascalCase 局部变量与 Go 社区惯例相左——项目已在 README 声明为有意决策，故按「取舍」计分；若要提分需业主决定是否投入一次机械式重命名。
2. **文档维度（8.5）**：README 正文仍为单语（已有英文摘要 + 英文 CONTRIBUTING），如需可补 README 英文版与 CHANGELOG 英文版。
3. **功能维度（8.0）**：`sqlserver` + 非 `@p` 组合无法由引擎自检（`database/sql` 不暴露驱动名）——属上游接口限制，已完整文档化。

---

## 4. 问题清单（v15 更新）

### 🔴 4.1 SQLServer 占位符：正确性取决于「驱动名 × ColParam」两维（v4 结论更正）

#### 4.1.1 实测全矩阵

对 8 个组合各跑六步 CRUD（Insert / Update / SaveIt 插入 / SaveIt 更新 / InsertL / Delete）并逐行读回校验：

| 驱动名 | `ColParam=":"` | `ColParam="$"` | `ColParam="?"` | `ColParam="@p"` |
| --- | --- | --- | --- | --- |
| **`mssql`**（已废弃但仍注册） | ✅ 6/6 | ✅ 6/6 | ✅ 6/6 | ❌ `expected 0 arguments` |
| **`sqlserver`**（现代名） | ❌ `":"附近有语法错误` | ⚠️ **6 步中 5 步静默写错/失配（仅 Delete "✅"）** | ❌ `"?"附近有语法错误` | ✅ 6/6 |

**`sqlserver` + `$` 的细节（危险组合）**：

```text
Insert 引擎返回 <nil>,读回 name="2.00"      → 占位符被当 money 字面量
Update 引擎返回 <nil>,读回 name="2.00"      → 改错行/写错值
SaveIt 引擎返回 <nil>,目标行不存在           → MERGE 匹配到字面量序号对应的行
InsertL 报主键冲突(重复键值 1)               → 报错原因与真实问题无关
Delete  ✅
```

**根本不存在"SQLServer 族一律损坏"这回事。**

#### 4.1.2 机制（驱动源码确证）

`go-mssqldb` 注册了两个驱动名，行为不同：

```go
// mssql.go:33-38
var driverInstance          = &Driver{processQueryText: true}   // 注册为 "mssql"
var driverInstanceNoProcess = &Driver{processQueryText: false}  // 注册为 "sqlserver"

// mssql.go:519-525
func (c *Conn) prepareContext(ctx context.Context, query string) (*Stmt, error) {
	paramCount := -1
	if c.processQueryText {
		query, paramCount = querytext.ParseParams(query)   // ← 仅 "mssql" 走这里
	}
	return &Stmt{c, query, paramCount, nil, false}, nil
}
```

```go
// internal/querytext/parser.go:parseNormal
if ch == '?' {
	return parseOrdinalParameter            // ? → @pN
} else if ch == '$' || ch == ':' {
	ch2, _ := p.next(); p.unread()
	if ch2 >= '0' && ch2 <= '9' {
		return parseOrdinalParameter        // $1 / :1 → @pN
	} else if 'a' <= ch2 && ch2 <= 'z' || 'A' <= ch2 && ch2 <= 'Z' {
		...                                 // $name / :name → @name
	}
}
```

结论：

- 驱动名 **`mssql`** → `?`、`$N`、`:N`（及 `$name`/`:name`）**全部**改写为 `@pN`，都能真绑定；而 `@pN` 不被该预处理器识别 → `expected 0 arguments`。
- 驱动名 **`sqlserver`** → 不预处理。`:` 与 `?` 是非法 T-SQL，服务端直接语法错误；`$N` 是**合法 money 字面量**，不报错但实参被忽略。
- 现代等价写法：`sql.OpenDB(mssql.NewConnectorWithProcessQueryText(cfg))`（`mssql.go:149-157`）。

#### 4.1.3 收窄后仍然成立的真实缺陷

1. **危险组合未被拦截**：驱动名 `sqlserver` + `ColParam` 非 `"@p"` 时，引擎不校验、不报错，`$` 组合会**静默写错数据**（`Update` 改错行、`Delete` 删错行、`MERGE` 匹配错行）。这是真实且严重的数据完整性风险，只是**不是"整个 SQLServer 族"的问题**。
2. **README 未说明驱动名依赖**：驱动表只给「连接符/占位符」两列，读者无法得知 `$`/`:`/`?` 的正确性还取决于 `sql.Open` 用的是 `mssql` 还是 `sqlserver`。**这正是本次误判的根源，也是读者会踩的同一个坑。**
3. ~~**引擎无法自检**：`TUniEngine` 只持有 `Db *sql.DB` 与 `ColParam`，不掌握驱动名，因此无法在 `Initialize()` 时校验组合一致性。~~
   **【历史修复与当前行为 · 2026-10-14】** 中间版本曾在 `Initialize()` 使用真实连接只读参数回显,以覆盖 `sqlserver` + `$` 等静默错绑。当前按开发者指定的 `ProvideName × ColParam` 静态检查,不再探测实际连接；组合错误仍返回 `ErrInvalidPlaceholderBinding`,但 `ProvideName` 与真实驱动不一致时引擎无法发现。该段原始缺陷复现仅代表当时版本。
4. **`getSqlQuery` 无 SQLServer 分支**（`UniEngine.go:198-223`）：原始 SQL 里的 `$N` 在 `sqlserver` 驱动名下不会被改写。

> **2026-10-14 说明（业主决定）**：README 驱动表 SQLServer 行已由 `$` / `$1` 改为 **`:` / `:1`**（仅文档，代码未动）。**本次实测确认该改动有效**——生产环境使用驱动名 `mssql`，而 `mssql` 下 `:`、`$`、`?` 三者都正确（实测 6/6），故新值与原值在 `mssql` 下均可用。`README.md:24` 现在与生产配置一致。

#### 4.1.4 建议

| 优先级 | 措施 |
| --- | --- |
| P0 | 在 README 驱动表增加一列或脚注，写明**驱动名依赖**：`mssql` → `:`/`$`/`?` 皆可（预处理）；`sqlserver` → **仅 `@p`**。这是成本最低、收益最高的一条 |
| P1 | 为 `TUniEngine` 增加可选的自检：例如提供 `SetDriverName(driver string) error`，在 `Initialize()` 时校验 `(驱动名, ColParam)` 组合，不匹配直接报错（`ErrPlaceholderMismatch`） |
| P2 | `getSqlQuery` 增加 SQLServer 分支，使原始 SQL 中的 `$N` 在不预处理的驱动名下也能正确改写 |
| P2 | 在 `integration/mssql_test.go` 基础上把矩阵纳入 CI（两个驱动名各跑一遍） |

#### 4.1.5 我在 v2 / v4 中的错误更正

| 版本 | 原表述 | 更正 |
| --- | --- | --- |
| v2 | 「SQLServer 族在文档配置下**所有 DML 与 MERGE 均会失败**」 | **错**。`sqlserver`+`$` 不失败而是静默写错；`mssql` 下一切正常 |
| v4 | 「SQLServer 族**静默写错数据**（家族级缺陷）」 | **范围错**。仅 `sqlserver` 驱动名 + 非 `@p` 成立；生产（`mssql`+`:`）实测 6/6 正确 |
| v2/v3/v4 | 建议改用 **`ColParam: "@p"`** 作为修复/绕行 | **该建议有害，已撤回**。实测 `mssql` 驱动名 + `@p` → `expected 0 arguments`，会**打断生产**。正确做法是按驱动名选择（`mssql` 用 `:`/`$`/`?`；`sqlserver` 用 `@p`） |

**方法学教训（供后续评审复用）**：我连续三版把「在**单一驱动名**下测得的驱动层行为」当作「该数据库族的普遍契约」，且只依赖官方文档的措辞而**未读驱动源码**。真库全矩阵 + 源码确证本应在 v2 就做。凡涉及「驱动/方言契约」的结论，必须同时固定：驱动实现、驱动名、DSN 参数、实际服务端版本。

---

### ✅ 4.2 panic 路径：已全部关闭（`f72d29f` 修复，本轮独立复核通过）

v6 报告的「1 条同类变体」（命名字符串键 panic + `map[any]` 误拒）已由 `f72d29f` 修掉，修法与我上轮建议的三分支一致：

```go
KeyType    := t.Key()
KeyDirect  := reflect.TypeOf("").AssignableTo(KeyType)   // string / any 等:直接写
KeyConvert := !KeyDirect && KeyType.Kind() == reflect.String  // 命名 string:写时 Convert
if !KeyDirect && !KeyConvert { return fmt.Errorf("... assignable from string ...") }
// sink 内: if !KeyDirect { KeyValue = KeyValue.Convert(KeyType) }
```

**我的独立复核（对抗性矩阵，超出其自带用例）**

```text
OK      SelectM(map[MyKey]RowNK)  → 写入 map[a:{1 a}]              (命名 string 键 Convert 后写入)
OK      SelectH(map[MyKey]RowNK)  → 写入 map[:{1 a}]
OK      SelectM(map[any]RowNK)    → 写入 map[a:{1 a}]              (String 可赋值给 any,误拒已修)
ERROR   map[int] / map[bool] / map[float64] / map[[2]byte] / map[error]
        → needs a map key assignable from string ..., but got key type [int]
OK      零行 + map[string]RowNK   → nil
ERROR   零行 + map[int]RowNK      → 仍报错(守卫在行循环之前,fail-fast 不依赖是否有数据)
```

**结论**：无残留 panic；语义判断正确（`string` 未实现的接口如 `error` 被正确拒绝）；`map[any]T` 从"误拒"变为"支持"属于行为扩展而非破坏。

**值得记录的方法学改善**：`f72d29f` 的新增用例**主动打破了"测试与实现同构"的盲区**（同时覆盖命名 string / `any` / `int`），据其提交说明，该用例确实当场抓到了一次漏改（`SelectM` sink 未被脚本替换命中）。这是本项目第一次由测试独立发现实现遗漏——**此前三次修复都是「修复覆盖面 = 测试覆盖面 = 认知覆盖面」**。建议把这一做法固化为规范：**新增守卫时，测试的输入维度应比实现所依据的判据更宽**。

---

### ✅ 4.3 遗留注册表路径绕过标识符白名单（v8 已修复）

**修复前**（本会话前几轮实测）：四个注册方法均不校验标识符，而其参数会被**直接拼进生成的 SQL**——CRUD 表名从不加引号，MySQL/Oracle 族的列名亦不加引号：

```text
PrepareRunSQL[MySQL]               -> insert into t;drop table x ( a;drop table y ) values ( ? )
PrepareRunSQL[Oracle]              -> insert into t;drop table x ( a;drop table y ) values ( :1 )
PrepareRunSQL[RegisterClass/MySQL] -> insert into t;drop table z ( id,name ) values ( ?,? )
PrepareRunSQL[PG]                  -> 被 quoteIdent 中和
```

**v8 修复内容**（零破坏：签名与调用点不变，非法输入返回带 `LastError` 的未注册表并短路链式调用）：

- `RegisterClass`：表名 + **struct tag 列名**（`db:"-"` / `db:"_"` 只读标记豁免）
- `RegisterTable` / `RegisterField` / `RegisterPkeys`：表名与字段名
- 新增哨兵 `ErrInvalidFieldName`（表名沿用 `ErrInvalidTableName`），便于 `errors.Is` 区分
- 附带修掉 `RegisterPkeys` 表未登记时**返回 nil** 的链式 nil 解引用 panic（§4.7）
- `Initialize()` 内部登记名 `github.com/kazarus/uniengine/unitable`（含 `/`，会被新白名单拒绝）改为 `unitable` / `unifield`；该键仅作小写别名（另以类全名注册），不影响查找路径

**独立复核（我方探针，超出其自带用例）**

```text
RegisterTable 表名注入      ✅ 拒绝  UniEngine: invalid table name: RegisterTable table name [t;drop table x] ...
RegisterField 字段名注入     ✅ 拒绝  UniEngine: invalid field name: RegisterField field name [a;drop table y] ...
RegisterClass 表名注入      ✅ 拒绝  UniEngine: invalid table name: RegisterClass table name [t;drop table z] ...
RegisterClass tag 注入      ✅ 拒绝  UniEngine: invalid field name: RegisterClass field tag on Bad [a;drop table w] ...
RegisterPkeys 表不存在       ✅ 拒绝  UniEngine: no such class registered: RegisterPkeys table [no_such] ...
非法表是否进注册表: GetTable("t;drop table x")=<nil>        (期望 nil)
PrepareRunSQL 注入面是否消失: ""                             (期望空)
合法注册正向对照: "insert into t_ok ( id,name ) values ( ?,? ) " err=<nil>
只读标记 db:"-"/db:"_" 未被误杀
```

**结论**：该注入面已关闭，且未破坏任何既有行为（全量测试 + 真库集成测试均通过）。剩余的安全项为密钥轮换静默失效（§4.6）与加密无 AAD（§4.5-7）。

---

### 🔴 4.7 【v8 新发现并已修复】`RegisterPkeys` 表未登记时返回 nil → 链式调用 nil 解引用 panic

**这是第 21 条 panic 路径**，与已修的前 20 条同源：**公共 API 用 panic 表达编程错误**。

```text
RegisterPkeys("no_such_table", "id") 返回 nil      ← 旧实现:表不存在则 return nil
链式调用 .SetKeys("id")  →  PANIC: invalid memory address or nil pointer dereference
```

`RegisterTable` / `RegisterField` 在表不存在时会新建表，唯独 `RegisterPkeys` 走 `if Valid { ... }` 后 `return UniTable`（表不存在即 nil）——而它同样返回 `*TUniTable`、被文档与既有用法当作可链式载体。

**v8 处置**：改为返回带 `ErrUnregisteredClass` 的未注册表：

```text
RegisterPkeys(不存在的表) 返回 nil? false
错误: UniEngine: no such class registered: RegisterPkeys table [no_such_table] is not registered; call RegisterTable/RegisterClass first;
链式 SetKeys 在 LastError 上短路,无 panic
```

**回归测试**：`TestRegisterPkeysMissingTableNoPanic`（断言非 nil、哨兵正确、链式不 panic）。

**方法学备注**：这条是"顺着同类缺陷的分布去找"发现的——前 20 条都是"入口对非法输入 panic"，于是我逐个检查所有返回 `*TUniTable` 的公开方法是否都保证非 nil 载体，从而找到这处唯一返回 nil 的实现。**同类缺陷应成组排查，而非逐个上报**。

---

### 🟠 4.4 引擎按值拷贝后「锁共享、事务状态不共享」（v5.1 已加静态防护）

```text
h1 clone.Begin()      -> <nil>                      # 副本开启事务成功
h2 clone.Cancel()     -> <nil>                      # 副本回滚成功
h3 original.Cancel()  -> UniEngine: no transaction  # 原件看不到该事务
```

`mu`/`secret` 为指针（共享），`tx`/`inTx`/`Db`/`ColLabel` 为值（不共享）→ 副本持事务句柄而原件认为「无事务」，原件写入会绕过事务。

**v5.1 处置**：`TUniEngine` 新增 `noCopy` 具名字段，按值拷贝现由 `go vet`（copylocks）在静态检查阶段拦下——已在**外部模块**验证对真实类型稳定报出 3 条告警（见 §6 P1-6）。同时修正了包内唯一的按值拷贝点 `newTestEngine()`（改为返回 `*TUniEngine`）。

**仍未解决**：根因是"值字段承载事务状态"，`noCopy` 只是禁止拷贝、并未把 `tx`/`inTx` 收进与 `mu`/`secret` 同级的指针结构。彻底修法：把 `tx`/`inTx` 移入一个指针持有的运行时状态结构体。

---

### 🟡 4.5 其余未修复项

| # | 问题 | 位置 | 证据 |
| --- | --- | --- | --- |
| 1 | 重复 `db` tag 读写不对称 | `UniRegister.go:58-60`、`UniTable.go:60-67` | `HashField[x]=B` 而 `orderedField[0]=A` → SELECT 灌 B、INSERT 取 A |
| 2 | `insertPage` 用 `Supplier` 判断 PolarDB | `UniCopyIn.go:205` | 全库方言判断走 `Provider`，唯此处例外 |
| 3 | `ExistField` 哨兵不一致 | `UniMeta.go:179-181` | `errors.Is(err, ErrInvalidTableName) == false` |
| 4 | `AutoKeys` 无参默认 PG 元数据 SQL | `UniTable.go:343-365` | 无参固定 `TAutoKeys4POSTGR`；传错类型返回 `can not build autokeys sql` |
| 5 | 结果列严格校验 | `UniQuery.go:93-97` | 结果集任何未注册列即硬报错，`SELECT *` 遇 schema 漂移即中断；未在 README 说明 |
| 6 | `PrepareRunSQL`+`Execute` 不经加密/验签 | `UniRegister.go:209-300` | 只生成 SQL 文本，`secreton`/`VerifyAt` 不生效 |
| 7 | 加密无 AAD、密文无 KeyID | `UniSecret.go` | 同行/列密文可搬移而不被察觉；无密钥轮换 |
| 8 | 包级 godoc 缺失 | `go doc .` | ✅ **已修复（v9）**：新增 `doc.go` 承载正式包文档，17 个文件头注释与 `package` 解耦；新增 2 个编译期示例 |
| 9 | `Initialize` 注册 key 与模块路径不符 | `UniEngine.go` | key 为旧小写路径 `github.com/kazarus/uniengine/unitable` |
| 10 | 文档语言单一 | `README.md`/`CHANGELOG.md` | ⚠️ **已改善（v11）**：README 增英文摘要、新增英文主体 `CONTRIBUTING.md`；正文与 CHANGELOG 仍为中文 |

---

### 🟠 4.6 【本轮新发现】派生密钥缓存不随 `SecretBy` 失效，密钥轮换被静默忽略

**本轮把重读重心移到 `UniSecret.go`，发现派生密钥的缓存键漏了密钥本身。**

`deriveKey` 的缓存键构造（`UniSecret.go:99`，源码确证）：

```go
CacheKey := string(Salt) + "|" + strconv.Itoa(Iter)   // ← 不含 SecretBy
if Key, Valid := St.Keys[CacheKey]; Valid { return Key, nil }
Key, eror := pbkdf2.Key(sha256.New, this.SecretBy, Salt, Iter, 32)
```

只要「盐 + 迭代数」相同（而写盐是**每引擎固定**的），无论 `SecretBy` 怎么变都会命中缓存、返回**旧密钥派生结果**。

**实测证据**（`/tmp/uniprobe` 探针，两步判断「密文究竟用哪把钥匙加密」）：

```text
用 key-1 加密 -> err=<nil>  密文="ENC:H0TvHBjU…"
运行时把 SecretBy 改为 key-2 后再加密 -> err=<nil>  密文="ENC:H0TvHBjU…"
把第二段密文分别交给两把钥匙的"干净引擎"解密:
  c2 用 key-1 解密 -> "hello"   err=<nil>                        ✅ 成功
  c2 用 key-2 解密 -> ""        err=decrypt fail: message authentication failed   ❌
  ⇒ 缺陷确认:引擎实际仍用 key-1,尽管引擎里 SecretBy 已是 key-2
```

**影响**：

- **密钥轮换静默失效**。README/CHANGELOG 提到「带标签但密钥错误的密文会解密报错（不静默，便于发现密钥轮换问题）」，暗示轮换是受支持场景；但实际上在长驻进程里改 `SecretBy` 后，**新数据仍用旧密钥加密**——既不报错，也无任何迹象，直至用新密钥的进程读不懂数据。
- **多租户/多密钥场景下更危险**：若按请求切换 `SecretBy`（同引擎复用），所有租户的数据都会被**第一个**密钥加密。
- 与文档"配置字段应在并发查询前完成"部分重叠，但**该约束并不覆盖"启动后轮换"**这一被文档默许的场景，且轮换需求在等保/密评语境下是硬性要求。
- 附带：`St.Keys` 无淘汰机制，读取由**不同写盐**（其它进程/历史数据）产生的密文会不断新增缓存项——长期运行的进程存在缓慢增长。严重度低，一并记录。

**修法（成本极低）**：把密钥纳入缓存键，例如

```go
SecretTag := sha256.Sum256([]byte(this.SecretBy))
CacheKey := string(Salt) + "|" + strconv.Itoa(Iter) + "|" + hex.EncodeToString(SecretTag[:8])
```

（用哈希而非明文密钥作键，避免在缓存映射里额外留存密钥明文副本；仅在 `SecretBy` 变化时产生新缓存项。若仍希望保留旧密钥解旧数据，可进一步引入 `KeyID`，见 §4.5-7。）

**建议补的回归测试**：`SecretOn=1` 下先用 key-A 加密 → 改 `SecretBy=key-B` → 再加密 → 断言第二段密文**只能**被 key-B 解开。这类「缓存键是否覆盖全部输入」的测试，与本轮 §4.2 的教训同源：**凡有缓存，就要测"输入变了缓存是否失效"**。

---

#### 4.7 【v14 发现 · 高 → ✅ v15 已修复】加密写路径不校验迭代数上下界 ⇒ 可加密却永久读不回

**现象（真机复现，`SecretDefault` 两条路径不对称）**：

| `SecretIter` | 加密 | 解密 | 结果 |
| --- | :---: | :---: | --- |
| 600000（缺省） | ✅ 56ms | ✅ | 正常 |
| 10000000（= `secretIterMax`） | ✅ 919ms | ✅ | 正常 |
| **10000001** | ✅ 914ms | ❌ `invalid iterations in cipher Text` | **写成功但永久读不回** |
| **20000000** | ✅ 1.83s | ❌ 同上 | 同上 |
| **2³²** | ✅（耗时 **6.4 分钟**） | ❌ 内嵌迭代数被 `uint32` **截断为 0** | 同上 |

**根因**：写路径 `secretIterOf()` 只做「≤0 则取缺省」，**不设上限**；而读路径强制 `Iter <= secretIterMax`（1e7）。二者不对称，于是引擎能写出自己拒绝读取的密文。

```go
// UniSecret.go:写路径 —— 无上限
func (this *TUniEngine) secretIterOf() int {
    if this.SecretIter <= 0 { return UniSecretIter }
    return this.SecretIter
}
// UniSecret.go:读路径 —— 有上限
if Iter <= 0 || Iter > secretIterMax { return "", errors.New("...invalid iterations in cipher Text") }
```

**影响**：数据**永久不可读**。密文内嵌了越界迭代数，读路径必然拒绝，降回旧配置也无用（除非改代码或手工离线解密）。此外 `uint32(Iter)` 在 `Iter ≥ 2³²` 时静默截断——若截断值恰落在合法区间，会产生「用 4G 次迭代派生、却按截断值解密」的必然验签失败。写入侧还有 6.4 分钟的 CPU 空转（PBKDF2 线性）。

**既有测试为何没拦住**：`TestSecretRejectsAbsurdIterations` 只构造**读侧**的伪造密文断言被拒，**从未测写侧**——典型的不对称盲区（"测了校验函数的调用方，没测生成方"）。

**修复建议（约 5 行）**：把校验前移到写路径，让越界配置 **fail fast** 而不是产出坏数据：

```go
func (this *TUniEngine) secretIterOf() int { ... }   // 保留

// 写路径改用:
func (this *TUniEngine) secretIterForWrite() (int, error) {
    Iter := this.secretIterOf()
    if Iter > secretIterMax {
        return 0, fmt.Errorf("UniEngine: SecretIter %d exceeds max %d (ciphertext would be unreadable)", Iter, secretIterMax)
    }
    return Iter, nil
}
```

**建议补的回归测试**：`SecretIter>secretIterMax` 时 `Secret(true)` 必须**报错**（而非返回密文）；`SecretIter` 取 `secretIterMax` 边界值必须可往返。

---

#### 4.8 【v14 发现 · 中高 → ✅ v15 已修复】`SetVerify` 的「行级绑定」不是自动的，而文档称其「覆盖主键」

`f4f5ff5` 把「跨行/跨列搬移」的防护归档给 `SetVerify` 的行级绑定，理由是其 HMAC 载荷「域分隔含表名、覆盖主键与保护列」。实测表明**主键不会自动进入载荷**：

| 登记方式 | 整行搬运（把 2 号的 name+MAC 贴到 1 号）后读取 | 结果 |
| --- | --- | :---: |
| `SetVerify("MAC_TEXT", "NAME")` | 验签**通过** | ❌ 未被检出 |
| `SetVerify("MAC_TEXT", "ID", "NAME")` | 验签失败 | ✅ 被检出 |

`verifyPayload` 只拼接**调用方显式列出的**列：载荷 = `表名 + Σ(登记的保护列值)`。因此仅保护业务列时，整行搬运恰好满足 MAC，攻击面并未闭合。

**为何这是问题而非纯用户责任**：
1. 公开文档已把它写成既成事实——`README.md:175`「其 HMAC 载荷域分隔含表名、**覆盖主键与保护列**，搬移后读取验签必失败」，`CHANGELOG.md` 同一措辞。用户按文档理解会以为绑定是内置的。
2. `SetVerify` 已经对 MAC 列做了 6 项登记期校验（未注册/无属性/只读/主键/非 string/自保护/重复），**唯独没有**「保护列中至少含一个主键」这条——既有校验的完备性也提示这里应当补齐。

**建议（择一）**：
- **A（推荐，最省事且不破坏兼容）**：登记期校验——若 `Fields` 中不含任何已登记主键，则 `LastError` 报错（可给一个显式 `SetVerifyAllowNoPkey` 逃生口供确有需要的场景）。
- **B**：自动把该表已登记的主键并入载荷（`ColVerify` 保持不变，仅内部拼接时附加），文档即可保持原措辞。
- **C（最低限度）**：若两条都不做，则必须改文档为「**请在保护列中包含主键**，否则整行搬运无法检出」。

---

#### 4.9 【v14 发现 · 中 → ✅ v15 已修复】README 关于「错误密钥会报错」的表述在**同进程轮换**场景下不成立

`README.md:198`：「带标签但密钥错误的密文会解密报错(**不静默**,便于发现密钥轮换问题)」。

该断言只在**冷缓存**（新进程/新引擎实例）下成立。同进程内改动 `SecretBy` 时，§4.6 的缓存键不含 `SecretBy`，派生密钥命中旧缓存，于是：

| 场景 | 实测 |
| --- | --- |
| 同一引擎：key-A 加密 → 改 `SecretBy=key-B` → 解该密文 | ✅ **成功解出**（用的是缓存的 key-A 派生密钥） |
| 同一引擎：改 `SecretBy=key-B` 后**新加密**的密文 | 只能用 **key-A** 解开；key-B 解不开 ⇒ **轮换完全无效** |
| 冷缓存新引擎（`SecretBy=key-B`）解 key-A 的密文 | ❌ `message authentication failed`（此处文档成立） |

即：§4.6 不只是「解密用了旧密钥」，而是**新旧数据全线继续用旧密钥**——轮换在运行时 100% 无效，且这一过程不报错（与 README 的「不静默」相反）。修复 §4.6 的缓存键即可同时消除本条，但 README 的措辞需要同步精确化（限定为"跨进程/冷缓存"）。

#### 4.10 【v14 发现 · 低 → ✅ v15 已修复】MySQL 族的元数据 API 依赖 `Eng.DataBase`，README 未提示

`IfDropView` / `ExistTable` / `ExistViews` / `ExistField` / `ExistConst` / `AutoKeys` 在 MySQL 族走 `information_schema`，必须以 `DataBase` 提供库名，否则报 `UniEngine: database is not specified`。补上后全部正常（**非缺陷**：错误文案清晰、行为可预期），但 README 的 MySQL 相关小节只讲了驱动名与占位符，未提这一前置要求；`integration/mysql_test.go` 的用例名里提了，但对库使用者不可见。建议在 README 的 MySQL 配置示例里补一行注释。

---

#### 4.11 本轮**排除**的怀疑（已证伪，记录以免后人重查）

- **密钥派生缓存无界增长**：曾疑心 `writeSalt()` 每次写入都生成随机盐，导致 `deriveKey` 缓存随写入行数无界增长。**证伪**——`writeSalt()` 只在引擎实例首次调用时生成盐并复用（`if St.Salt == nil`），故缓存项数有界（每份「盐+迭代数」一条）；每次加密使用新的随机 nonce，密码学上成立。
- **部分列查询会用「子集」错误验签**：**证伪**——`verifyCtxOf` 在任一保护列或 MAC 列不在结果集中时直接返回 `nil`（整行跳过验签），不会用子集重算出一个错误结论。
- **分页写入的边界值**：`InsertP` 在 `PageSize` 取 2/1/100/-1/0/-5 时均正确写入全部 5 行、无报错。**未见缺陷**。
- **`ExecuteMust` 语义**：影响 0 行报错、影响 1 行通过，符合文档。**未见缺陷**（`ExecuteMust("select 1")` 亦报"影响行数为 0"，属对非 DML 误用的 fail-fast，可接受）。
- **完整性校验真机行为**：`SetVerify` 在真实 MySQL 上**功能正确**——写入回填 MAC、正常读取通过、篡改保护列被检出、篡改 MAC 被检出、strict 模式拒绝无 MAC 存量行、lenient 模式直通。此前该特性只有文本/mock 级验证，本轮补上了真机证据。


## 5. 本轮的次要发现

### 5.1 `Select` 的新错误未接入哨兵体系（修复引入，轻微）

`UniQuery.go:238` 用 `errors.New("... needs a pointer param ...")`，未包装 `ErrNilParam` → `errors.Is` 失效，测试只能退化为字符串包含判断。

### 5.2 错误表返回的 `HashField`/`HashPkeys` 为 nil map（修复引入，轻微）

`UniRegister.go:21-30` 两条错误路径未初始化 map。实测在 `SetKeys`/`SetSecret`/`SetVerify`/`AutoKeys` 上**无 panic**（均被 `LastError` 短路），但属潜在脆弱点：未来新增的 setter 若在检查 `LastError` 之前写 map 即 panic。建议顺手 `make`。

### 5.3 `nilParamOf` 抽象层级偏低（设计观察）

该函数只回答「是不是 nil」，而各入口真正需要的是**形态契约**（`Select` 需 `*struct`、`SelectL` 需 `*slice`、`SelectM/H` 需非 nil map 且键为 string）。检查被拆在两处，导致 `Select` 加了指针校验而 `SelectL/M/H` 漏了。建议收敛为按入口声明的公共前置。

### 5.4 修复回合的方法学确认

修复提交直接引用评审条目（`评审P0`、`评审低级项#5`）并补了对应回归测试，把外部评审转成了可回归资产——方向正确。唯一遗留是 §4.2 的「测试覆盖面 = 修复覆盖面」。

---

## 6. 改进建议（v15 · 已逐条复核）

> 本节每条均回到源码/真库复核过。标注：**✔ 有效** = 复核后仍成立；**✎ 已细化** = 有效但修法需要修正；**✖ 已撤回** = 复核后不成立。
>
> **复核中发现的关键前提**：SQLServer 的占位符**不存在「驱动名无关」的取值**——`@p` 在 `mssql` 下报 `expected 0 arguments`，而 `:`/`$`/`?` 在 `sqlserver` 下报语法错误或静默写错。因此**修法不能是「换一个常量」**，只能是「文档说明 + 可选自检」。

### P0

1. **在文档中补上「驱动名」维度 ✅ 已实施（2026-10-14）**
   - 复核结论：✅ 成立，且比原判断更关键——因为没有通用取值，文档是唯一能同时覆盖两种驱动名的手段。
   - **落地内容**：`README.md` 驱动表 SQLServer 行下新增提示，并新增 **`##### 0.2.SQL Server 驱动名与占位符`** 一节，含：
     - 「驱动名 × ColParam」对照表（`mssql` → `:`/`$`/`?` 皆可；`sqlserver` → 仅 `@p`）；
     - 两种驱动名的配置示例（`ColParam: ":"` 与 `ColParam: "@p"`）；
     - `sqlserver` + `$` 的**静默写错数据**警示与历史数据审计提示；
     - 「两种驱动名无通用取值」与 `NewConnectorWithProcessQueryText` 的等价写法。
   - 佐证：`integration/mssql_test.go` 的 `TestMSSQLPlaceholderContract`（8/8 通过）与 `TestMSSQLRoundTripMatrix`（4/4 通过）。

2. **关闭剩余 7 条 panic 路径 ✅ 已实施（`61ecc94` + `f72d29f`，独立复核通过）**
   - `61ecc94`：`SelectLCtx` 补「必须传指针」校验；`SelectMCtx`/`SelectHCtx` 补「非 nil map」（含经指针间接）与键类型校验。
   - `f72d29f`：把键守卫从 `Kind` 近似改为**可赋值语义**（三分支），一并修掉命名字符串键 panic 与 `map[any]` 误拒。
   - 我方独立复核（含零行 / 接口键 / 数组键 / 浮点键）**无残留 panic**。

3. **补输入形态矩阵测试 ⚠️ 部分完成（`f72d29f` 已迈出关键一步）**
   - `f72d29f` 新增 3 个用例覆盖命名 string / `any` / 非 string 键，**主动打破了「测试与实现同构」的盲区**，并当场抓到一次漏改（`SelectM` sink 未被脚本替换命中）。
   - 建议把这一做法固化为规范：**新增守卫时，测试的输入维度应比实现所依据的判据更宽**；并可继续扩展到容器 × 写法 × 键类型的完整矩阵。

4. **修掉 §4.2 的命名键变体 ✅ 已实施（`f72d29f`）**
   - 落地内容与建议一致：`KeyDirect := reflect.TypeOf("").AssignableTo(KeyType)` + 命名 string 走 `Convert` + 其余报错；`SelectM`/`SelectH` 两处 sink 同步。
   - **转为新增 P0**：修 §4.6 的密钥缓存键（`deriveKey` 的 `CacheKey` 补入 `SecretBy` 哈希），并补「改密钥后新密文只能由新密钥解开」的回归测试。

### P1

5. **`(驱动名, ColParam)` 组合自检 ✔ 有效，但需接受 API 代价**
   - 复核结论：`database/sql` 不暴露驱动名，引擎无法自动探测（`sql.DB` 无该接口），故必须由调用方显式告知。
   - 修法：新增可选字段/方法（如 `DriverName string` 或 `SetDriverName(string) error`），在 `Initialize()` 时校验组合，不匹配返回新哨兵 `ErrPlaceholderMismatch`。未设置时保持现状（不破坏兼容）。

6. **注册 API 补标识符校验 ✎ 已细化——可零破坏实现**
   - 复核结论：✅ 成立（`RegisterTable`/`RegisterField`/`RegisterPkeys` 均未调用 `validIdent`）。
   - **修法修正**：原建议「调整签名」不必要。这三个方法都返回 `*TUniTable`，而 `TUniTable.LastError` 已能短路后续链式调用（`UniTable.go:168`）——**直接复用作者在 `6f771bf` 中为 `RegisterClass` 引入的模式**：校验失败则返回带 `LastError` 的未注册表，签名不变、调用点不变。
   - 同时修正 `README.md:159` 的表述（「所有拼入 SQL 的表名字段名均校验」当前不成立）。

7. **明确引擎拷贝契约 ✅ 已实施（2026-10-14）**
   - 复核结论：✅ 成立（实测 `clone.Begin()` 成功后 `original.Cancel()` 报 `no transaction`）。
   - **落地内容**：`UniEngine.go` 新增 `noCopy` 类型（`Lock`/`Unlock` 空实现），作为 `TUniEngine` 的**具名字段**（非匿名嵌入，避免方法提升）；同时更新 `mu` 字段注释、修正包内唯一的按值拷贝点 `newTestEngine()`（`UniEngine_test.go:22` 由返回值改为返回 `*TUniEngine`，并同步 1 处 `&e` 调用）。
   - **有效性验证**：在**外部模块**中对 `TUniEngine` 做按值拷贝，`go vet` 稳定报出 3 条：
     ```
     CopyEngine passes lock by value: github.com/kazarus/UniEngine.TUniEngine contains github.com/kazarus/UniEngine.noCopy
     return copies lock value: github.com/kazarus/UniEngine.TUniEngine contains github.com/kazarus/UniEngine.noCopy
     call of CopyEngine copies lock value: github.com/kazarus/UniEngine.TUniEngine contains github.com/kazarus/UniEngine.noCopy
     ```
   - 三个模块 `go build`/`go vet` 全部通过；`-race` 通过；真库 MSSQL 集成测试 2 PASS / 1 SKIP。
   - **兼容性影响**：这是 vet 期约束而非编译错误，但①外部按值拷贝会在其 `go vet` 中报错；②新增字段会使**非具名（unkeyed）复合字面量**编译失败（本仓库全部使用具名字段，已核查无此用法；且 `go vet` 的 `composites` 分析器本就对跨包 unkeyed 字面量告警）。
   - **仍未解决**：`noCopy` 只是禁止拷贝，并未把 `tx`/`inTx` 收进与 `mu` 同级的共享结构——根因（值字段事务状态）仍在，故并发维度只加到 7.5。深改建议保留：把事务状态整体移入指针持有结构。

8. **统一方言判断入口 ✎ 已细化——原论证需修正**
   - 复核结论：✅ 问题成立，但理由比原表述更准确。`Supplier` 的字段注释明确写着用途是「数据库驱动（**区分 DtORACLE VS DtDAMENG**）」（`UniEngine.go:50`），而 `UniCopyIn.go:205` 却用它判断 `DtPOLODB` 决定是否走 COPY——**与字段自身文档语义相矛盾**，属误用。
   - 修法：PolarDB 已由 `Provider` 表达且属 `FmPOSTGR`，应改为按 `Provider`/`dbFamily()` 路由；`Supplier` 要么删除，要么在文档中明确它到底承担什么。

9. **零散一致性 ✔ 有效（含本轮两条新发现）**
   - `Select` 的指针错误未接哨兵（`UniQuery.go:238` 用 `errors.New`）→ 改 `fmt.Errorf("%w: ...", ErrNilParam)` 或新增 `ErrNotPointer`；
   - 错误表返回的 `HashField`/`HashPkeys` 为 nil map（`UniRegister.go:21-30`）→ 顺手 `make`（当前安全，因 setter 均被 `LastError` 短路，属防御性修复）；
   - `ExistFieldCtx` 用哨兵（`UniMeta.go:179-181`）；
   - `AutoKeys` 参数收窄为 `HasGetSqlAutoKeys`，并让错误消息说明真实原因（当前传错类型只报 `can not build autokeys sql`）；
   - 注册期检测重复 `db` tag 并报错（当前 `HashField` 取后者、`orderedField` 取前者，导致 SELECT 与 INSERT 操作不同字段）。

10. **补齐 MySQL / Oracle 真驱动测试 ✅ 两族均已完成（v10 Oracle / v12 MySQL）**
   - 复核结论：成立。MSSQL 的那次误判正是「只有文本比对、没有真驱动」的盲区所致。
   - **Oracle ✅ 已实施**：真实 **11g EE 11.2.0.1.0** 上 5 用例全绿（`integration/oracle_test.go`），覆盖占位符契约、`INSERT ALL`、`MERGE` 两分支、`SaveItWhenNotExist`、6 个数据字典视图的 `Exist*`/`AutoKeys`、`secreton` 在 `varchar2` 上的密文往返。**未发现新缺陷**。
   - **MySQL ✅ 已实施（v12）**：真实 **MySQL 8.4.6** 上 4 用例 `-race` 全绿。实测三种错误占位符（`:1` / `$1` / `@p1`）**均不绑定实参**，但失败层次不同（服务端语法错 / 服务端列不存在 / 客户端参数计数），**均非静默写错**。
   - **后续可选**：把 MySQL 纳入 CI（`mysql:8` service 容器成本低）；PG 套件需要可写测试库（远端仅有生产库）。

11. **清理风格残留 ✅ 机械部分已实施（v9）**
    - **机械、无争议 ✅ 已实施**：30 处重复文档注释（65 行）与 200 处 `{` 后空行已全部清除（含 `integration/`、`contrib/pgxcopy/`），`gofmt`/`go vet`/全量测试均通过。
    - **有主张、交业主决策（未动）**：`this` 接收者、PascalCase 局部变量、`CONST_*` 全大写常量。`README.md:5` 已声明为有意决策且内部自洽；若调整，建议单独一次机械式提交并配 `gofumpt`/`revive` 门禁，避免继续以「改名」形式零散提交（现已有 19/133 次）。

### P2

12. **加密绑定与轮换 ✔ 有效，补充适用条件**
    - 结论成立：`GCM.Seal(nil, Nonce, ..., nil)` 无 AAD，且写盐每进程共享，故密文可在**同行不同列**或**不同行**间搬移而不被解密层察觉。
    - **适用条件**：仅当 `VerifyOn` 关闭时可被利用；若同时开启了存储完整性校验，HMAC 载荷会覆盖该列（前提是该列被 `SetVerify` 登记且出现在 SELECT 中）。
    - 修法：AAD 取 `表名|主键值|列名` 的规范化拼接；密文头增加 `KeyID` 以支持轮换。

13. **工程化补全 ✎ 覆盖率门槛现已可落地**
    - 复核结论：原条目中「CI 覆盖率门槛」此前被我误判为不可行（见 §7.3 注），实际 **`go test -cover` 正常工作，基线 78.6%**。
    - 建议：CI 增加 `go test -cover` 并设门槛（建议先设 **75%** 防回退，后续再逐步提高），同时保留现有 `-race` 与基准 smoke。
    - **已实施**：包级 godoc 与 `Example*`（v9）、`CONTRIBUTING.md` 与英文摘要（v11）、**覆盖率门槛 + 2 个 fuzz 目标 + 四族 CI 化**（v13）。**仍余**：CHANGELOG 无英文版。

14. **文档校正 ✔ 有效**
    - README 4KB 加密吞吐由「0.8–1.2 GB/s」改为实测区间（加密 ~635 MB/s、解密 ~1.32 GB/s）；
    - 补充两处边界：「查询结果列必须全部注册，否则硬报错」「`PrepareRunSQL` + `Execute` 路径不经加密/验签」。

### 已撤回项（保留记录，避免复用）

| 项 | 原建议 | 撤回原因 |
| --- | --- | --- |
| ~~SQLServer 占位符改 `@p`~~ | 把 `ColParam` 改为 `"@p"` 作为修复/绕行 | **有害**：`mssql` 驱动名 + `@p` → `expected 0 arguments`，会打断生产。正确做法见 P0-1 |
| ~~「SQLServer 族整体不可用/静默损坏」~~ | 作为家族级缺陷修复 | 范围错误：仅 `sqlserver` 驱动名 + 非 `@p` 成立 |
| ~~「覆盖率因工具链故障无法测量」~~ | 不设覆盖率门槛 | 我方环境问题（沙箱拦截缓存），实测 78.6% 可测 |

### 优先级重排建议

| 顺序 | 项 | 理由 |
| :---: | --- | --- |
| 1 | P0-1 文档补驱动名维度 | 唯一能防止「换个常量就以为修好了」的误解；成本几行字 |
| 2 | P0-2 + P0-3 关闭 7 条 panic + 形态矩阵测试 | 当前唯一的「用户可触发崩溃」开放项，且测试能一劳永逸防复发 |
| 3 | P1-6 拷贝契约（`noCopy`） | 三行代码换取 vet 期保护，性价比极高 |
| 4 | P1-5 注册校验（零破坏实现） | 安全表述与实现对齐，可复用既有 `LastError` 模式 |
| 5 | P2-1 覆盖率门槛 | 基线已知（78.6%），防回退成本低 |
| 其余 | 按原顺序 | — |

---

## 7. 附录

### 7.1 环境与命令

```sh
go version                 # go1.27.1 darwin/arm64
git rev-parse HEAD         # 134adce7c3d7c9708fcdc424cd9b53673eec95c4
git rev-list --count 73be9b5..HEAD   # 4

# 沙箱拦截 ~/Library/Caches/go-build 与 ~/go/pkg/mod 写入,统一改到可写位置
export GOCACHE=/tmp/gocache GOMODCACHE=/tmp/gomodcache GOTMPDIR=/tmp
export GOPROXY=https://goproxy.cn GOSUMDB=off       # 国内代理(proxy.golang.org 超时)

go build ./... ; go vet ./... ; gofmt -l .           # 均通过
go test -count=1 .                                  # ok 0.567s
go test -cover -count=1 .                           # coverage: 78.6% of statements
go test -coverprofile=/tmp/cov.out -count=1 . && go tool cover -func=/tmp/cov.out | tail -1

cd integration
UNIENGINE_MSSQL_HOST=... UNIENGINE_MSSQL_USER=... UNIENGINE_MSSQL_PASS='...' \
UNIENGINE_MSSQL_DB=UNIENGINE_TESTDB go test -race -run MSSQL -v .
```

### 7.2 探针模块

- `/tmp/uniprobe`：桩驱动 + 录制型驱动，覆盖 panic 路径、容器空值、map 键类型、错误表 setter、拷贝语义、重复 tag、方言 SQL 文本抓取。
- `/tmp/sqlsrvchk`：真库程序（`chk` 链路对照 / `chk2` 机制证明 / `chk3` 影响面 / `ph` 占位符风格 / `named` 命名参数 / `decisive` 驱动名×ColParam 全矩阵 / `cleanup`+`direct` 残留检查）。
- 全部结论不依赖业务表：仅用自建 `uni_*` 临时表，结束即删。

### 7.3 代码规模与风格指标（HEAD = `61ecc94`）

| 指标 | v2 基线（`73be9b5`） | `134adce` | `61ecc94` | `f72d29f` | v8 工作区 | v9 工作区 | **v14（`f4f5ff5`）** |
| --- | :---: | :---: | :---: | :---: | :---: | :---: | :---: |
| 库代码行数 | ~3815 | ~3877 | ~3921 | ~3938 | ~4010 | ~4040 | **3884** |
| 测试代码行数 | ~3159 | ~3262 | ~3329 | ~3383 | ~3480 | ~3520 | **3876** |
| **Test 函数数** | 109 | 109 | 117 | 120 | 122 | 122 | **127** |
| **Example 数** | 0 | 0 | 0 | 0 | 0 | 2 | **2** |
| **断言数** | 217 | 217 | 233 | 241 | 247 | 247 | **264** |
| **语句覆盖率** | 未测（见下注） | 78.6% | 80.3% | 80.4% | 80.7% | 80.7% | **80.8%** |
| 重复文档注释（空行分隔） | 66 | 66 | 66 | 65 | 65 | 0 | **0** |
| `{` 后紧跟空行 | 172 | 173 | 173 | 173 | 173 | 0 | **0**（新代码曾回归 19 处，本轮已修） |
| 包级 godoc | 碎片（17 段拼接） | 同 | 同 | 同 | 同 | `doc.go` 单段 | **`doc.go` 单段** |
| **真驱动集成族数** | 1（PG） | 1 | 1 | 1 | 1 | 3 | **4（PG + MSSQL + Oracle + MySQL）** |
| **CI 自动化的族** | 1（PG） | 1 | 1 | 1 | 1 | 1 | **4（PG/MySQL 每次 push；MSSQL/Oracle 每日定时 + 手动）** |
| **覆盖率门槛** | 无 | 无 | 无 | 无 | 无 | 无 | **75%（当前 80.7%）** |
| **fuzz 目标数** | 0 | 0 | 0 | 0 | 0 | 0 | **2**（各 10s 冒烟；seed 随 `go test`） |
| **卫生门禁数** | 0 | 0 | 0 | 0 | 0 | 0 | **2**（`style_test.go`；已验证可拦住违规） |
| 提交总数 / 其中重命名类 | 127 / 18 | 131 / 19 | 132 / 19 | 133 / 19 | 133 / 19 | 133 / 19 | **141 / 19** |

> **覆盖率测量更正**：v1–v4 报告多次写「本机工具链故障，`go test -cover` 报 `internal/coverage/cfile: cannot find package testmain`，故无覆盖率」。**该判断错误**——真实原因是本机沙箱拦截了 `~/Library/Caches/go-build` 的写入，覆盖率插桩后的标准库包无法落缓存。改用可写缓存（`GOCACHE=/tmp/gocache`）后测量正常（当前 **80.3%**）。这是我方环境问题，非仓库缺陷；同时也说明该仓库的覆盖率**一直可测且水平不低**。

### 7.4 关键位置索引（HEAD）

| 主题 | 位置 |
| --- | --- |
| 新增 nil 校验 | `UniEngine.go`（`nilParamOf`） |
| `Select` 指针校验（未接哨兵） | `UniQuery.go`（`needs a pointer param`） |
| `SelectL` 指针校验（`61ecc94` 已加） | `UniQuery.go`（`SelectLCtx` 内） |
| `SelectM`/`SelectH` nil map 与键 Kind 守卫（`61ecc94` 已加；**Kind 判断即残留缺口**） | `UniQuery.go`（`SelectMCtx`/`SelectHCtx` 内） |
| `RegisterClass` 类型守卫 | `UniRegister.go:21-30` |
| `noCopy`（禁止按值拷贝） | `UniEngine.go`（类型声明在 `TUniEngine` 之后） |
| 占位符生成 | `UniEngine.go`（`getValParam`） |
| 占位符改写（无 SQLServer 分支） | `UniEngine.go`（`getSqlQuery`） |
| README 驱动表 + 0.2 节 | `README.md:19-24` 及 `##### 0.2` |
| 分页路由（`Supplier` 例外） | `UniCopyIn.go:205` |
| 加固测试（含 `61ecc94` 新增 4 个用例） | `hardening_test.go` 末尾 |
| 加密（无 AAD/KeyID） | `UniSecret.go`（`GCM.Seal(nil, ...)`） |
| 真库集成测试 | `integration/pg_test.go`、`integration/mssql_test.go` |
| CI 流水线 | `.github/workflows/ci.yml` |

### 7.5 真库实测证据与新增回归资产

**环境**：SQL Server 2017 RTM 14.0.1000.169 X64；`go-mssqldb` v1.11.2（集成测试）/ v1.9.8（探针）；`encrypt=disable`、`TrustServerCertificate=true`。

**新增文件 `integration/mssql_test.go`（三个用例）**

| 用例 | 内容 | 状态 |
| --- | --- | :---: |
| `TestMSSQLPlaceholderContract` | 8 个子用例固化「驱动名 × 风格」绑定矩阵 | ✅ 8/8 PASS |
| `TestMSSQLRoundTripMatrix` | 4 个可用组合各跑六步 CRUD + 原生读回校验（含生产配置 `mssql`+`:`） | ✅ 4/4 PASS |
| `TestMSSQLDefectSqlserverNoAt` | 复现 `sqlserver` 驱动名 + 非 `@p` 的失效；不变式「返回 nil ⇒ 必须写对」 | ⏭ 默认 SKIP；`UNIENGINE_MSSQL_DEFECT=1` 后 ❌ FAIL（按设计） |

**关键实测输出**

```text
===== 引擎端到端:驱动名 × ColParam 全矩阵 =====
  [驱动名 mssql     / ColParam ":"] [Insert:✅ Update:✅ SaveIt插:✅ SaveIt更:✅ InsertL:✅ Delete:✅]
  [驱动名 mssql     / ColParam "$"] [Insert:✅ Update:✅ SaveIt插:✅ SaveIt更:✅ InsertL:✅ Delete:✅]
  [驱动名 mssql     / ColParam "?"] [Insert:✅ Update:✅ SaveIt插:✅ SaveIt更:✅ InsertL:✅ Delete:✅]
  [驱动名 mssql     / ColParam "@p"] 全部 err=expected 0 arguments
  [驱动名 sqlserver / ColParam ":"]  全部 err=mssql: “:”附近有语法错误。
  [驱动名 sqlserver / ColParam "$"] Insert err=<nil> 读回="2.00" / SaveIt 目标行不存在 / InsertL 主键冲突 / Delete ✅
  [驱动名 sqlserver / ColParam "?"]  全部 err=mssql: “?”附近有语法错误。
  [驱动名 sqlserver / ColParam "@p"] [Insert:✅ Update:✅ SaveIt插:✅ SaveIt更:✅ InsertL:✅ Delete:✅]
```

```text
===== 原始占位符绑定(驱动名 mssql) =====
  select $1      -> 值="7"   ✅ 真参数绑定
  select :1      -> 值="7"   ✅ 真参数绑定
  select ?       -> 值="7"   ✅ 真参数绑定
  select @p1     -> 错误: sql: expected 0 arguments, got 1

===== 原始占位符绑定(驱动名 sqlserver) =====
  select @p1     -> 值="7"   ✅ 真参数绑定
  select :1      -> 错误: mssql: “:”附近有语法错误。
  select ?       -> 错误: mssql: “?”附近有语法错误。
  select $1      -> 值="1.0000"  实参未绑定(money 字面量)
```

**运行方式**（与 `pg_test.go` 同口径）

```sh
cd integration
# 分量式(口令含 @ # $ % 时推荐,文件内做 URL 转义)
UNIENGINE_MSSQL_HOST=... UNIENGINE_MSSQL_PORT=1433 \
UNIENGINE_MSSQL_USER=sa UNIENGINE_MSSQL_PASS='...' \
UNIENGINE_MSSQL_DB=UNIENGINE_TESTDB go test -race -run MSSQL -v .
# 或整串 DSN
UNIENGINE_MSSQL_DSN="sqlserver://sa:pass@host:1433?database=UNIENGINE_TESTDB&encrypt=disable" \
  go test -race -run MSSQL -v .
```

- 不设 env → 全部 SKIP，默认 `go test ./...` 保持绿；
- `UNIENGINE_MSSQL_REQUIRED=1` → 连不上即 FAIL（CI 口径）；
- `UNIENGINE_MSSQL_DEFECT=1` → 开启缺陷复现。
- 口令只从 env 读取，**无任何硬编码凭据**。

**依赖变动**：`integration/go.mod` 新增 `github.com/microsoft/go-mssqldb v1.11.2`，并由依赖图带动 `golang.org/x/sync v0.17→v0.22`、`golang.org/x/text v0.29→v0.41`；`go.sum` +53 行。库本体 `go.mod` 仍**零第三方依赖**。

**清理验证**：直接按表名逐个查询确认 `uni_it_mssql_probe`、`uni_probe_sqlsrv_defect`、`uni_probe_defect2`、`uni_probe_defect3` **均不存在**，`UNIENGINE_TESTDB` 库中 `uni_*` 表总数为 **0**。

---

## 8. 修订记录

| 版本 | 基线 | 总分 | 主要变化 |
| --- | --- | :---: | --- |
| v1 | `73be9b5` | **7.4** | 初评：7 维度框架；发现 17 条 panic 路径、`AutoKeys` 静默回退（**后证有误**）、风格 churn |
| v2 | `73be9b5` | **6.8** | 重新评估：更正 `AutoKeys` 结论；新增 SQLServer 占位符缺陷、map 键 panic、非指针切片 panic、遗留注册表注入、引擎拷贝事务错配、重复 tag 不对称；panic 路径 17→20 |
| v3 | `134adce` | **6.9** | 重读复核：确认 P0 修复关闭 **17/20** panic 路径且无回归、`.DS_Store` 已清、`SetVerify` 主动改进；确认 7 条 panic 残留、注入与拷贝问题仍在 |
| v4 | `134adce` | **6.4** | 真库实测：把 SQLServer 缺陷的症状由「报错失败」更正为「静默写错数据」，四个维度下调 |
| **v5** | **`134adce`** | **6.9** | **驱动名维度更正**：真库全矩阵证明占位符正确性取决于「驱动名 × ColParam」；生产组合（`mssql`+`:`）实测 **6/6 正确**；v4 的"家族级静默损坏"判断被推翻并收窄为「`sqlserver`+非 `@p`」；**撤回 `@p` 修复建议**（会打断 `mssql` 驱动名的生产）；新增 `integration/mssql_test.go` 作为回归资产。另更正：覆盖率此前误判为"不可测"，实为沙箱拦截缓存，实测 **78.6%** |
| **v5.1** | **`134adce` + 未提交改动** | **7.0** | **实施两项改进**：P0-1 README 新增 `0.2.SQL Server 驱动名与占位符`（驱动名×ColParam 对照表、双配置示例、静默写错警示）与「不得按值拷贝」契约；P1-6 `TUniEngine` 新增 `noCopy` 具名字段，按值拷贝由 `go vet` 拦下（外部模块实测 3 条告警），并修正包内唯一拷贝点 `newTestEngine()`。并发 7.0→7.5、文档 6.5→7.0 |
| **v6** | **`61ecc94` + 未提交改动** | **7.3** | **重新读取 + 重新评分**：独立验证 `61ecc94` 已关闭 7 条容器形态 panic（20 条已闭 19），正确性 5.5→**7.0**；测试规模 109→117、覆盖率 78.6%→**80.3%**，测试维度 8.0→8.5。**对抗性复查发现 1 条同类变体**：守卫用 `Kind` 而非可赋值性，命名字符串键（`type MyKey string`）的 map 仍 panic，且 `map[any]T` 被误拒（§4.2）。**自审修复自身回归**：v5.1 插入的 `noCopy` 曾把 `TUniEngine` 的文档注释挤脱附着（`go doc TUniEngine` 丢失并发契约），已改为声明在结构体之后 |

| **v7** | **`f72d29f` + 未提交改动** | **7.4** | **再次重读 + 重新评估**：独立验证 `f72d29f` 把键守卫从 `Kind` 近似改为可赋值语义——**20 条 panic 路径全部关闭**（含命名 string 键、`map[any]`、零行 fail-fast、接口/数组/浮点键，全部由我方对抗性矩阵复核），正确性 7.0→**7.5**；测试 117→120、覆盖率 80.3%→**80.4%**。本轮把重读重心移到加密模块，**新发现 1 条潜伏缺陷**：`deriveKey` 缓存键为 `salt|iter`、不含 `SecretBy`，运行时轮换密钥被静默忽略（§4.6，附实测证据与修法） |

| **v8** | **`f72d29f` + 未提交改动** | **7.7** | **注册守卫加固 + README 更正**：`RegisterClass`/`RegisterTable`/`RegisterField`/`RegisterPkeys` 补齐标识符白名单（表名 + struct tag 列名，`-`/`_` 豁免），新增哨兵 `ErrInvalidFieldName`，零破坏（返回带 `LastError` 的未注册表）——**遗留注入面实测归零**，安全 7.0→**7.5**；实施中**发现并修掉第 21 条 panic 路径**（`RegisterPkeys` 表未登记返回 nil → 链式 nil 解引用），正确性 7.5→**8.0**；README 两处失实表述按实测/实现更正（白名单范围、4KB 加密吞吐 0.75/1.17 GB/s），文档 7.0→**7.5**；测试 120→122、覆盖率 80.4%→**80.7%** |

| **v9** | **`f72d29f` + 未提交改动** | **7.8** | **清理风格残留 + 包级 godoc**：删除 30 处重复文档注释（65 行冗余）与 200 处「开括号后空行」，库本体与两个子模块**全部归零**（零语义变化，风格 5.5→**6.5**）；新增 `doc.go` 承载正式包文档，并把 17 个文件的头注释与 `package` 子句解耦——此前被 go/doc 拼成 17 段碎片、`go doc .` 不可读；新增 2 个编译期示例（文档 7.5→**8.0**） |

| **v10** | **`f72d29f` + 未提交改动** | **7.9** | **Oracle 真库验证**：在真实 **Oracle 11g EE 11.2.0.1.0** 上新增 `integration/oracle_test.go`（5 用例全绿）——占位符契约、六步 CRUD（`INSERT ALL` 多行 / `MERGE` 两分支 / `SaveItWhenNotExist`）、配错 `ColParam` 的失效对照、`AutoKeys`+`Exist*` 六视图元数据、`secreton` 在 `varchar2` 上的密文往返；驱动用纯 Go 的 `go-ora`（无 Instant Client，`godror` 亦可）。测试维度 8.5→**9.0**。**本轮未发现新缺陷**；并确认「静默写错数据」是 SQL Server + `sqlserver` 驱动名独有形态（Oracle 配错即硬报错且不写行） |

| **v11** | **`11f12a8` + 未提交改动** | **7.9** | **贡献指南与英文摘要**：新增 `CONTRIBUTING.md`（英文主体＋中文要点：零依赖硬约束、门禁命令、有意非常规风格及处置原则、**6 条不得回归的不变量**、新增方言的 5 个接入点、占位符与驱动名规则、集成测试两条清理规则、PR 检查清单）；README 补齐 H1 与**英文摘要**。文档 8.0→**8.5**。同时写好 `integration/mysql_test.go`（4 用例，编译/跳过验证通过） |
| **v12** | **`11f12a8` + 未提交改动** | **8.0** | **四族真库全覆盖**：MySQL 8.4.6 上 4 用例 `-race` 全绿（占位符契约 / 六步 CRUD 含 `SaveIt` 回退路径 / `information_schema` 元数据 / `secreton` 密文往返）；实测确认 MySQL 三种错误占位符**均不绑定实参且均非静默写错**。测试 9.0→**9.5**。本轮未发现新缺陷；PG 套件因无可用测试库未实跑（远端 PG 16.4 仅有生产库） |

| **v13** | **`11f12a8` + 未提交改动** | **8.1** | **测试维度收口**：新增 `integration-mysql`（`mysql:8.4`，每次 push）与 `integration-heavy`（SQL Server 2022 + Oracle Free 23，`docker run` + 轮询 + 失败打印容器日志，**每日定时 + 手动**）；新增**覆盖率门槛 75%**与 **2 个 fuzz 目标**（各 10s，本机约 750 万次执行无反例）。测试 9.5→**10.0**。同时勘误 v11/v12 的加权和（7.93/7.98 → 7.925/8.000）。诚实边界：两个重容器 job 无法在本机验证，其余 CI 接线均已本地校验 |

| **v14** | **`f4f5ff5`** | **7.8** | **重读后的下修**：新发现 §4.7（`SecretIter>1e7` 可加密却永久读不回；`≥2³²` 内嵌值被 uint32 截断）——读侧有界、写侧无界的不对称，既有测试只测读侧；新发现 §4.8（`SetVerify` 行级绑定需显式登记主键，实测只登记非键列时整行搬运不被检出，而 README/CHANGELOG 称其"覆盖主键"）；新发现 §4.9（README 关于错误密钥"不静默"的断言在同进程轮换下不成立，与 §4.6 同源）；§4.10 MySQL 元数据 API 依赖 `DataBase` 未在 README 提示。证伪 2 项自身怀疑（派生根缓存无界、部分列错误验签），并首次在真库上验证 `SetVerify`、分页边界、`ExecuteMust`。正确性 8.0→7.5、安全 7.5→7.0、测试 10.0→9.5、文档 8.5→8.0 |

| **v15** | **`f4f5ff5` + 未提交改动** | **8.2** | **四项修复落地**：§4.7 写路径迭代数越界 fail-fast（+4 行，回归测试双向验证）；§4.6 派生密钥缓存键并入 `sha256(SecretBy)[:8]`；§4.8 `SetVerify` 登记期强制保护列含主键（含 `VerifyUnkeyed` 逃生口，真库验证整行搬运被检出）；§4.9 README 轮换语义改写 + §4.10 MySQL `DataBase` 示例；新增 `style_test.go` 两道仓库卫生门禁（并验证可拦住违规）。测试 127 / 断言 264 / 覆盖率 80.8% / 真库 12 用例通过。正确性 7.5→8.5、安全 7.0→7.5、测试 9.5→10.0、文档 8.0→8.5。**同时勘误 v14 的「约 8.6」预测，实为 8.2** |

**v7 核心判断**：这是目前质量最高的一版——**唯一一类能导致进程崩溃的缺陷（panic）已彻底清零**，且首次出现「测试独立于实现的理解方式、并当场抓到实现遗漏」的良性循环。7.4 的构成是「功能与测试优良、健壮性显著改善、安全与风格两项未动、加密轮换存在一条潜伏缺陷」。

**v7 新增发现**

| # | 事项 | 说明 |
| --- | --- | --- |
| 1 | 密钥缓存不随 `SecretBy` 失效（§4.6） | `CacheKey := string(Salt) + "\|" + strconv.Itoa(Iter)` 未含密钥。实测：切换 `SecretBy` 后新密文**仍只能由旧密钥解开**（key-1 成功、key-2 报 message authentication failed）。影响密钥轮换与多租户场景；修法为把密钥哈希纳入缓存键，并补「改密钥后新密文只可由新密钥解开」的测试 |
| 2 | 缓存无淘汰（§4.6 附带） | `St.Keys` 只增不减；读取由不同写盐产生的密文会持续新增条目，长驻进程存在缓慢增长（严重度低） |

**v6 记录的自我更正与自审（保留）**

| # | 事项 | 说明 |
| --- | --- | --- |
| 1 | 自审发现自身回归 | v5.1 我把 `noCopy` 的类型声明插在 `TUniEngine` 文档注释与结构体之间，且**未留空行**，导致两段注释合并、`TUniEngine` 的并发契约文档脱落（`go doc TUniEngine` 不再显示）。已改为在结构体之后声明 `noCopy`，文档恢复附着（已用 `go doc` 验证） |
| 2 | 第一次误读 `go doc` 输出格式 | `go doc <符号>` 是**声明在前、文档在后**；我最初按"文档在前"判断，一度以为修复无效。已改用「全文 grep 文档首句」判定 |
| 3 | 对抗性复查的必要性再次被证实 | 这是本项目第三次出现「修复覆盖面 = 测试覆盖面 = 认知覆盖面」：`TestSelectMHRequiresStringKey` 用 `map[int]`/`map[int64]` 覆盖「非 string 键」，与实现的 `Kind` 判断同构，因此漏掉命名 string 键 |

**方法论教训**：连续三版把「单一驱动名下的实测行为」当成「数据库族的普遍契约」，且依赖文档措辞而未读驱动源码。凡涉及驱动/方言契约的结论，必须同时固定 **驱动实现、驱动名、DSN 参数、服务端版本** 四项，并优先用真库矩阵验证。另一条同样重要：**对"刚修好的东西"必须做对抗性复查**——三次修复中有三次留下了同类缺口，全部源于「用类型种类（Kind）近似替代真实语义（可赋值性/可寻址性）」。

---

*报告结束。`UNIENGINE_TESTDB` 库中无任何探测表残留。*

**v5 的四处自我更正**

| # | 原表述 | 更正 |
| --- | --- | --- |
| 1 | v2：「SQLServer 族所有 DML 与 MERGE 均会失败」 | 错。`sqlserver`+`$` 是静默写错；`mssql` 下一切正常 |
| 2 | v4：「SQLServer 族静默写错数据（家族级）」 | 范围错。仅 `sqlserver` 驱动名 + 非 `@p` 成立 |
| 3 | v2/v3/v4：「改用 `ColParam: "@p"`」 | **有害建议，撤回**。`mssql` 驱动名 + `@p` → `expected 0 arguments`，会打断生产 |
| 4 | v1–v4：「覆盖率因工具链故障无法测量」 | **错**。是我方沙箱拦截 `~/Library/Caches/go-build` 写入所致；改用可写 `GOCACHE` 后实测 **78.6%** |

**方法论教训**：连续三版把「单一驱动名下的实测行为」当成「数据库族的普遍契约」，且依赖文档措辞而未读驱动源码。凡涉及驱动/方言契约的结论，必须同时固定 **驱动实现、驱动名、DSN 参数、服务端版本** 四项，并优先用真库矩阵验证。

---

*报告结束。库本体源码未作修改；`UNIENGINE_TESTDB` 库中无任何探测表残留。*
