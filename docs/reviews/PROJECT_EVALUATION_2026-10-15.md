# UniEngine 项目评价与可提分项

> **本文回答两个问题:项目现在处于什么水平;在两套既有评分模型下,还有哪些改动能加分。**
>
> | 项 | 内容 |
> | --- | --- |
> | 评价版本 | `85c4ad1`(HEAD,`master`)+ 工作区未提交的 SQL Server 配置校验变更(见 §5) |
> | 评价日期 | 2026-10-15 |
> | 验证环境 | `go1.27.1 darwin/arm64`;本轮实测:`go test ./...`(全绿,覆盖率 **88.0%**)、`go vet`(零告警)、`gofmt -l`(零输出);并发新发现以临时探针在 `-race` 下实证后删除,全量套件复跑通过 |
> | 既有基线 | 内部七维模型 **8.4 / 10**(`IMPROVEMENT_BACKLOG_2026-10-14.md`,基线 `2cbb3b3`+T1);外部六维模型 **87 / 100**(`CODE_QUALITY_REVIEW_2026-10-14.md`,基线 `85c4ad1`)。工作区变更尚未纳入任一评分 |
> | 与旧清单关系 | 本文**不取代**两份旧清单,做三件事:①复核旧条目在当前代码中是否仍成立(§4);②收录本轮独立评审的**新发现**(§3,旧清单均无);③给出合并后的提分路线(§6) |

---

## 1. 总体评价

**底盘扎实,短板从"单点缺陷"转移到了"模型一致性"。** 本轮全量复核确认:密码学核心(AES-256-GCM 随机 nonce、PBKDF2 600k、HMAC-SHA256 全量、`hmac.Equal` 常量时间比对、缓存键覆盖全部派生输入)、Q1–Q8 修复、COW 快照(三登记 API)、事务终态回收、标识符白名单、四族真库测试、88.0% 实测覆盖率,全部维持在已修复/已具备状态。行循环统一上抛 `Rows.Err`、nil 参数前置防御、方言原生 UPSERT 等细节好于同类自制 ORM。

但本轮 -race 实证与代码追踪发现:**上一轮"同表注册 COW"修复只覆盖了 `RegisterTable/RegisterField/RegisterPkeys` 三个入口,同级的 `PrepareTables/PrepareRunSQL` 仍在原地改已发布快照,`RegisterClass` 返回的已发布对象仍被 `SetKeys/SetSecret/SetVerify/AutoKeys` 链式无锁变异**——两处都是真实数据竞争,后者并发 map 写是进程级 fatal。加上钩子接口的指针接收者静默失效、标量查询多行取尾行、AutoKeys 违反自身 LastError 契约构成的注入通道,构成本轮的 P0 批次。这些条目与 `doc.go` 宣称的快照模型/并发契约直接矛盾,是外部评审最可能命中的下一批扣分点。

---

## 2. 现状基线(本轮实测)

| 指标 | 数值 |
| --- | --- |
| 测试 | 全绿(`go test ./...`) |
| 语句覆盖率 | 88.0%(CI 门槛 80%) |
| `go vet` / `gofmt -l` | 零告警 / 零输出 |
| 代码规模 | 库约 4400 行,测试约 5300 行(前次口径) |
| 真库 | PG / MySQL / MSSQL / Oracle 四族集成测试模块;MSSQL 16 子例、MySQL 8.4.6 于最近复评通过 |
| 已销账 | 外部评审 Q1–Q8:7 项已销账、1 项有条件销账(Q1 存量 v1 迁移) |

---

## 3. 新发现(本轮独立评审,两份旧清单均未收录)

> 按严重度分三档。标注【-race 实证】的条目由临时探针在 `-race` 下复现后删除探针;标注【确认】的条目经代码逐行追踪;标注【疑似】需运行期复现。行号为当前工作区。

### P0 · 必修(数据竞争 / 静默错误数据 / 注入通道)

#### 3.1 `PrepareTables/PrepareRunSQL` 原地改已发布快照【-race 实证】

| 项 | 内容 |
| --- | --- |
| 位置 | `UniRegister.go:314`(`UniTable.ListField = ListField`)、`:323`(ListPkeys)、`:359/380/415/432`(原地写 SqlSelect/SqlInsert/SqlUpdate/SqlDelete) |
| 问题 | 写锁只护注册表 map,不护"读者已拿走的指针"。读者 `tableByType`(`UniEngine.go:136-140`)取指针即放读锁,之后在 `orderedField`(`UniTable.go:67-77`)无锁迭代 ListField。探针复现:`PrepareTables` 写 vs `UpdateCtx→writableFields→orderedField` 读,WARNING: DATA RACE |
| 矛盾点 | 这两个 API 持有与 Register* 同一把写锁,用户会合理推断同级安全;`doc.go:34` 宣称"同表登记以新元数据快照发布"被这两处破坏——即 Q6 修复的残留缺口 |
| 修复 | 改为 `cloneTableMetadata` → 改副本 → `publishTableMetadata`,与其余 Register* 对齐 |
| 验收 | 新增 `TestPrepareTablesConcurrentWithUpdate`(写循环 Update + 主循环 PrepareTables)在 `-race` 下通过 |
| 预估提分 | 并发维度 +0.5~1.0;消除"带锁却仍竞态"的自相矛盾 |

#### 3.2 `RegisterClass` 链式变异已发布对象,可致进程 fatal【-race 实证】

| 项 | 内容 |
| --- | --- |
| 位置 | `UniRegister.go:141-142` 先发布进 `HashTabl`;返回值被 `SetKeys`(`UniTable.go:222-225` 写 HashPkeys/ListPkeys)、`SetSecret`(:258-281 写 HashField/ListField)、`SetVerify`(:383-385)、`AutoKeys`(:448-451,还先经 `SelectL` 走一次 DB 查询再无锁写)原地变异 |
| 问题 | 与并发查询/写入同时发生时:轻则脏读元数据,重则 **concurrent map read/write → 运行时 fatal,不可 recover**。探针复现:`SetKeys` 写 HashPkeys vs `resolveTarget`(`UniWrite.go:50`)读 len(HashPkeys),WARNING: DATA RACE |
| 矛盾点 | `doc.go:11` 快速开始即示范 `RegisterClass(...).SetKeys(...).SetSecret(...)` 链式写法;`UniEngine.go:17` 又称 "Register* may run alongside queries"。注册本身并发安全、链式变异不安全,两句契约之间有缝 |
| 修复 | 短期:并发契约明确"链式登记完成前不并发使用该表";根治:`SetKeys/SetSecret/SetVerify/AutoKeys` 改引擎侧 API(clone+publish,参照 `RegisterPkeys` 模式)。**不建议**给链式 API 加锁(引锁序问题) |
| 验收 | `TestSetKeysConcurrentWithUpdate`(-race);AutoKeys 版本用 mock 元数据行并发跑 |
| 预估提分 | 并发 + 正确性合计 +0.5~1.5;消除潜在进程崩溃类缺陷 |

#### 3.3 指针接收者钩子被静默忽略,各入口检测口径不一致【确认】

| 项 | 内容 |
| --- | --- |
| 位置 | `UniQuery.go:293/342/387`(SelectL/M/H 用结构体类型 `t.Implements(...)` 判定)对照 `UniQuery.go:242`(Select 对传入值断言,正确——但 :243 注释表明是"保持旧语义"而非统一决策);`UniWrite.go:89/92/172/175/250/254`、`UniCopyIn.go:51-63`(用 `v.Interface()` 即结构体值断言) |
| 问题 | 类按 Go 惯用指针接收者 `func (u *TUser) SetSqlValues(...)` 实现钩子:Select 能识别,SelectL/M/H、Update/Insert/SaveIt 识别不了——钩子**静默跳过**走默认映射/默认语句,零报错;`canNativeSave` 还会把带钩子的类误判为可原生 UPSERT。同一类在不同入口行为分裂 |
| 加重因素 | 仓库内唯一钩子测试恰用值接收者,掩盖盲区 |
| 修复 | 统一 `reflect.PtrTo(t).Implements(...)`(或值/指针双探测);更优:RegisterClass 期按 ClassType 一次判定并缓存钩子能力。**必须全入口一次改齐**,只改一处会加重分裂 |
| 验收 | 指针接收者钩子类在 Select/L/M/H/Insert/Update/SaveIt 全部走钩子;值接收者行为不回归 |
| 预估提分 | 正确性 +0.5~1.0;一处修复全部入口受益 |

#### 3.4 `SelectD/SelectF/SelectS` 多行时静默取最后一行【确认】

| 项 | 内容 |
| --- | --- |
| 位置 | `UniQuery.go:40-47`(`selectColsCtx` 逐行 `Rows.Scan(Dst)` 覆写目标,无行数守卫) |
| 问题 | 条件写漏返回多行时不报错,悄悄返回**最后一行**的金额/状态——偶发取错行,极难排查。Select 已修同款坑(单 struct 目标多行改为报错),标量三兄弟漏改 |
| 修复 | `selectColsCtx` 计行,>1 行报错,与 Select 对齐 |
| 验收 | 多行结果下 SelectD/F/S 返回明确错误 |
| 预估提分 | 正确性 +0.5;属"静默错误数据"类,评审权重高 |

#### 3.5 `AutoKeys` 不查 `LastError`:未校验表名可进元数据 SQL(注入通道)【确认】

| 项 | 内容 |
| --- | --- |
| 位置 | `UniTable.go:391-396`(AutoKeys 入口只查 `VerifyOn`,不查 `LastError`;对照 SetKeys/SetSecret/SetVerify 均先查并短路);`UniRegister.go:16-18/81-83/155-157/226-231`(`invalidTable` 载体保留未校验原始 TableName);`DbaEngine.go:404-443`+`:225-243`(GetSqlAutoKeys/GetSqlExist* 全族 `fmt.Sprintf` 把表名拼进单引号字面量,如 `relname='%s'`);拼接结果经 `SelectL` 直接执行(`UniTable.go:427`) |
| 问题 | `RegisterClass(X{}, "x' union select ...--")` 返回带 LastError 的载体;调用方先调 `.AutoKeys(engine)`,坏表名进入单引号拼接并执行。违反库自身"见 LastError 即短路"契约。主流驱动 Prepare 不支持堆叠语句,主要是 UNION 探测/时间盲注面。同族:`TAutoKeys4MYSQLN` 路径拼入的 `DataBase` 从未校验 |
| 修复 | ① AutoKeys 入口加 `if this.LastError != nil { return this.LastError }`(一行);② 根因:全部元数据 SQL 生成器改占位符传参,或输出前过 `validIdent`;③ `DataBase` 纳入校验 |
| 验收 | 带坏表名的载体调 AutoKeys 返回原 LastError,不触网;注入探针(含单引号表名)被拒 |
| 预估提分 | 安全 +0.3~0.5;"Sprintf 拼 SQL + 错误未检查"双模式极易被评审命中,修复成本极低 |

#### 3.6 `Initialize` 无锁写 `inTx`【-race 实证】

| 项 | 内容 |
| --- | --- |
| 位置 | `UniEngine.go:496`(`this.inTx = false`,未持 `this.mu`) |
| 问题 | 与 `currentTx`(`UniEngine.go:155`)并发读构成数据竞争。后果链:若误在事务进行中调 Initialize,inTx 被清而 tx 非 nil → Commit/Cancel 走 `!inTx` 分支返回 ErrNoTransaction,Begin 又被 `tx != nil` 拒绝——实例卡死在"既不能提交也不能重开",事务泄漏到 ctx 结束 |
| 修复 | 删掉该行(RegisterClass 已持写锁,inTx 零值即 false),或移进 lockTables 临界区 |
| 验收 | `TestInitializeConcurrentWithCurrentTx`(-race);误用场景断言 Initialize 不破坏活动事务 |
| 预估提分 | 并发 +0.3;一行修复 |

### P1 · 应修(误判 / 静默覆盖 / 边界)

| # | 标题 | 位置 | 问题与修复 | 提分 |
| --- | --- | --- | --- | --- |
| 3.7 | ExistTable 把视图/索引/序列当表;Oracle 范围混用【确认】 | `DbaEngine.go:225-229`(pg_class 无 `relkind in ('r','p')`;视图分支 :233 反而有 `relkind='v'`)、`:239-243`(sysobjects 表分支缺 `xtype='U'`)、MySQL information_schema 含 VIEW;Oracle ExistTable 用 all_ 而 AutoKeys 用 user_ | PG 存在同名索引/视图时 ExistTable 误 true → 建表迁移走错分支;Oracle 跨 schema 同名表 ExistTable true 而 AutoKeys 找不到,自相矛盾。修:各加对象类型过滤;Oracle 统一 user_/all_ 口径 | +0.3~0.5 |
| 3.8 | 注册表双键静默覆盖,丢主键/丢加密登记【确认】 | `UniRegister.go:139-142`(表名键 + 类全名键无条件覆盖) | 先 RegisterTable+RegisterPkeys("t") 再 RegisterClass(T{},"t") → 主键静默丢失,后续 Delete 报 ErrNoPkeys 无警告;同类注册两张分表时类名键被后者占用,按类型路由全指向最后注册的表。修:键已被不同对象占用时报错(参照 Q7 登记期拒绝) | +0.3~0.5 |
| 3.9 | 分页负值/int64 截断使分页静默失效【确认】 | `UniCopyIn.go:161-163/219-221`(仅 `==0||==-1` 回退);`UniEngine.go:363-384` 的 `SpecialPageSize`(<=0 统一回退)引擎内零调用 | PageSize=-2 时永不分页 → 撞 SQLServer 2100 参数上限,错误提示还引导"try InsertP"形成死循环;32 位平台大值截断。修:统一走 SpecialPageSize 并对 >MaxInt32 clamp | +0.2~0.4 |
| 3.10 | 表名不引号而 COPY 引,同表两写路径行为分裂【确认】 | `UniWrite.go:135/204/347`、`UniSave.go:56/94/124/234` 裸拼;对照 `UniCopyIn.go:126`+`DbaEngine.go:201-207` COPY 引表名 | PG 引号保留大小写的表 `CopyInL` 可写、`InsertL` 报 relation 不存在。修:写路径统一 quoteIdent 表名,或统一不引并文档声明"标识符一律小写" | +0.2~0.3 |
| 3.11 | `Db` 为 nil 时全部执行入口 panic【确认】 | `UniEngine.go:393-402/478-499`;装配约定是裸结构体字面量 | 忘赋 Db → Initialize 返回 nil,首条 Select 空指针 panic 且栈在 database/sql 内,与已修的 nil-param 防御哲学相悖。修:Initialize 校验 Db 非 nil;prepareCtx 兜底返回错误 | +0.2~0.3 |
| 3.12 | `SetVerify` 登记期不校验保护列类型【确认】 | `UniVerify.go:77-102`(verifyFormat 不支持 time.Time/[]byte);`UniTable.go:294-388` 仅校验 MAC 列为 string | 保护列含 time.Time(常见的更新时间列)→ 登记成功,首次读写报 "verify does not support field kind [struct]",迟到且不指向真实原因,等于整表不可读写。修:SetVerify 用 ClassType 在登记期校验 kind ∈ 支持集 | +0.2~0.3 |
| 3.13 | 字段 tag 白名单允许 ".":列名 "a.b" 被引成 schema 限定引用【确认】 | `UniRegister.go:116-119`(字段复用含点的 validIdent) | `db:"usr.col"` 过注册,生成 SQL 引成 `"usr"."col"`(PG 语义=usr 表的 col 列)→ 静默指向错误列。表名允许点是 schema.table 合法需求,字段名永不合法,两者共用同一 validIdent。修:加不含点的字段名校验变体 | +0.2 |

### P2 · 低危加固 / 一致性(择机)

| # | 标题 | 位置 | 摘要 | 提分 |
| --- | --- | --- | --- | --- |
| 3.14 | `SelectH` 的 f 为 nil 时整批折叠到键 ""【确认】 | `UniQuery.go:388-391`(nil 判定在循环内) | 每行键 ""、map 只剩一条,查询"成功"数据静默丢失。入口处 nil 即报错 | +0.2 |
| 3.15 | RegisterClass 接受内嵌结构体但从不映射提升字段【确认】 | `UniRegister.go:97-137` 仅遍历顶层 | 内嵌带 db tag 的公共基类:注册成功,SELECT * 报"库有字段类没有",文案指错方向。登记期遇 Anonymous 字段直接报错 | +0.2 |
| 3.16 | 写路径钩子 SQL 不经占位符转换【疑似】 | `UniWrite.go:89-94/172-177` 直 prepareCtx;对照 UniMeta/UniQuery 入口均过 getSqlQuery | 钩子按可移植风格写 $1:查询/Execute 会被改写,Update/Insert 原样下发 → MySQL 语法错。统一过 getSqlQuery 或文档明示 | +0.1~0.2 |
| 3.17 | 钩子产出空 SQL 仍送 Prepare【疑似】 | `UniWrite.go:96/179` | 只实现 SetSqlValues 不给 SQL → 默认构建被跳过,prepareCtx("") 报驱动级晦涩错误。空 SQL 无条件报 "hook produced empty sql" | +0.1 |
| 3.18 | `CopyInLCtx` 三处失败两套错误口径【确认】 | `UniCopyIn.go:135-140` 裸 return vs :128-146 包装 | 逐行 Exec 失败无上下文无行号。统一包装 | +0.1 |
| 3.19 | DbaEngine 死常量拼错且缺项【确认】 | `DbaEngine.go:11-30`(`CONST_PROVIDER_NAME_TO_POLODB = "PolorDB"`;CODE 系列缺 POLODB;两系列缺 TAURUS;全仓库零引用) | 外部拿常量拼驱动名会 sql.Open 失败。删除或由 ProviderName() 单一真源生成 | +0.1 |
| 3.20 | `IfDropView` 返回值语义含混;`PkeyOnly` 赋值落副本无效【确认】 | `UniMeta.go:76-98`(删了/本不存在都返回 true,nil);`UniRegister.go:393-395`(ItemPara 值拷贝,`PkeyOnly=true` 即丢) | 调用方无法审计是否真删除;PkeyOnly 注释称"数据同步用到"但从未生效。改返回 (dropped, err);删除无效赋值 | +0.1 |
| 3.21 | 加密读路径无"严格模式":摘除 ENC 标签的明文替换零信号【确认】 | `UniSecret.go:258-261`(无标签即明文直通) | 库内写权限攻击者把密文换成自选明文,引擎零信号直通。完整性侧有 VerifyMode=2,加密侧无对应物,防护不对称。加引擎级 `SecretStrict` 开关(镜像 VerifyMode 语义)或可观测计数。**不要改成缺省报错**(破坏存量迁移路径) | +0.2~0.3 |
| 3.22 | 密钥卫生:无最小长度校验;缓存淘汰不清零【确认】 | `UniVerify.go:65-73`、`UniSecret.go:103-109/208-210` | 1 字节密钥照跑;LRU 淘汰仅 delete 交 GC。可选 16 字节下限(空钩子场景);派生密钥淘汰时尽力清零 | +0.1 |
| 3.23 | 进程级固定写盐:README 措辞与实现偏差【确认】 | `UniSecret.go:74-90`;README:206-207 | 单进程全部密文共享同一盐,README"按盐逐份暴力破解"的放大效应该场景不成立。改措辞"盐进程内固定、跨进程随机";可选按次数/时间轮换写盐 | +0.1 |
| 3.24 | `muSecretState` 包级锁落在每次加解密热路径【确认】 | `UniSecret.go:52-63/76/117/140/151` | 每次行级解密抢全局 mutex 只为 nil 判读。`this.secret` 改 atomic.Pointer 或构造期初始化 | +0.1~0.2 |
| 3.25 | `BeginCtx` 的 ctx 须覆盖事务全程,无警示;缺 CommitCtx/CancelCtx【顾问】 | `UniEngine.go:420/431/451` | 请求级 ctx 事务中途静默回滚后续 ErrTxDone(状态回收已修);Commit/Rollback 无法设超时,与持锁网络调用叠加可无限期阻塞。文档明示 + 补 Ctx 变体 | +0.1~0.2 |
| 3.26 | 同类多别名并发注册,类名键最后写者胜【理论】 | `UniRegister.go:141-142` | 无数据竞争(写锁内)但解析结果随调度漂移;与 3.8 同根,一并修 | +0.1 |
| 3.27 | 宽容验签是缺省姿态:部分投影/标量路径/空 MAC 全部静默跳过【确认,属姿态而非缺陷】 | `UniVerify.go:282-287/300-305/320-325`;`UniQuery.go:171-212`;`UniEngine.go:44` | 默认配置下 SELECT 少投影一列的数据完全未经校验无提示。文档把 VerifyMode=2 定位为回填完成后的推荐稳态;宽容跳过加可观测计数。**不要改缺省值** | +0.1 |

---

## 4. 旧清单条目复核(当前工作区)

### 仍开放(行号按当前代码更新)

| 旧条目 | 现状 |
| --- | --- |
| `sqlDebug` 直印实参(`IMPROVEMENT_BACKLOG` §3.1-1 / `CODE_QUALITY_IMPROVEMENTS` 高优先) | 仍在,`UniEngine.go` `sqlDebug()` 于 `RunDebug(true)` 后打印全部实参 |
| 事务终结持写锁做网络往返 + 包级 muLazy(§3.1-2) | 仍在:`UniEngine.go:412→420`(BeginTx)、`432→444`(Rollback)、`452→461`(Commit)写锁横跨驱动调用;`UniEngine.go:105-115/119/125` 每次查询过包级锁。注:工作区变更删除了 Initialize 的 15s 探针与 sqlServerValidationMu,锁窗口略有缩小,但三处事务函数未变 |
| `Supplier`/`Provider` 分裂(§3.1-3) | 仍在:`insertPage` 只认 `Supplier==DtPOLODB` |
| `WithTx`/局部事务对象(§3.2-1) | 仍开放:tx/inTx 仍是引擎字段(`UniEngine.go:77-78`) |
| 风格统一或门禁(§3.2-2) | 仍开放 |
| README 双语(§3.2-3) | 仍开放 |
| Q1 存量 MAC v1 迁移 | 仍开放(部署侧工作) |
| MySQL `SaveIt` count-then-write 并发窗口 | 仍开放 |

### 维持已修复(本轮复核确认)

Q2 主键冻结、Q3 事务终态回收(`UniEngine.go:444-448/461-465` + 三个 Q3 用例)、Q4 CanSet、Q5 词法扫描/重排、Q6 三登记 API 的 COW 快照(`UniRegister.go:22-49/167/199/246`)、Q7 重复列拒绝、Q8 缓存上限 128——全部在位;`InsertP/CopyInP` 分页状态全为局部变量,无共享态;CopyInHook 事务拒绝守卫正确且有测试。

---

## 5. 未提交变更评价(SQL Server 静态校验)

工作区正把 `Initialize()` 的 SQL Server 校验从"真连接只读回显探针"改为开发者声明的 `ProvideName × ColParam` 静态检查(`validateSQLServerPlaceholderConfig`,无锁、无 I/O),`ProvideName` 从 Deprecated 重新启用。

- **并发侧:净改善。** 删除了原"每条语句前可能触发全局互斥探针"的隐藏竞争点与锁窗口。
- **自检能力:回退。** 静态检查无法发现声明与 `sql.Open`/连接器真实模式不符——这正是旧文档"引擎无法自检"结论曾靠探针推翻、如今部分退回的点。`CODE_QUALITY_IMPROVEMENTS` 已把"SQL Server 装配防错"列为高优先(提供把 Db/ProvideName/ColParam 一起配置的构造入口或装配示例)。
- **建议**:按旧探针思路保留一个**可选**的显式自检入口(如 `ValidateSQLServerBinding(ctx)` 一次性探针,应用启动时调用),静态检查做快路径;两套真库测试矩阵继续跑。该行为变更完成测试后应重新评分,当前两处评分均未纳入。

---

## 6. 提分路线图(合并新旧;分数为工程估计,项目间有重叠、不可直接相加)

| 批次 | 内容 | 七维模型(8.4 起) | 六维模型(87 起) | 成本 |
| --- | --- | --- | --- | --- |
| **P0** 竞态与静默错误 | §3.1–3.6:两处 COW 收口 + Initialize 一行 + 钩子统一 + 标量多行 + AutoKeys LastError | → **~8.8–8.9**(并发 8.0→9.0,正确性 8.5→9.0) | → **~90**(正确性 9.0→9.3) | 中;全是局部修复,不需设计文档 |
| **P1** 观测与配置安全 | 旧 T2(sqlDebug 脱敏、Supplier 统一、锁粒度)+ §3.7–3.13 | → **~9.1** | → **~91–92** | 中;锁粒度需基准护航 |
| **P2** 架构与文档 | 旧 T3(WithTx、风格门禁、双语)+ §3.14–3.27 择机 + Q1 存量迁移(部署侧并行推进) | → **~9.3+** | → **~93** | 大;需业主决策 |

**建议顺序**:3.5(AutoKeys 一行修,即刻)→ 3.6(一行)→ 3.4(数行)→ 3.1/3.2(同根,按 RegisterPkeys 的 clone+publish 模式一并收口,配 -race 用例)→ 3.3(钩子全入口统一)→ 旧 T2 三项 → 其余。P0 全部完成后建议先跑一次全量 `-race` + 四族真库再继续。

---

## 7. 不建议做的(在旧清单 §5 七条基础上新增)

8. **为修 §3.2 给链式 API(SetKeys 等)加锁**。会引入锁序/死锁问题且与热路径争用;正确做法是 clone+publish。
9. **为修 §3.3 只改单个入口的钩子探测**。必须全入口一次改齐(RegisterClass 期缓存判定最优),否则分裂加重。
10. **把加密侧"无标签直通"(§3.21)或宽容验签(§3.27)改成缺省报错**。两者是存量数据迁移/回填的依赖路径;只加 opt-in 严格开关与可观测信号。

---

## 8. 附:本轮验证记录

- `go test ./...` 全绿,根模块覆盖率 88.0%;`go vet ./...` 零告警;`gofmt -l .` 零输出。
- §3.1/3.2/3.6 的数据竞争由临时探针文件在 `-race` 下实证(WARNING: DATA RACE 各一条),探针随后删除,全量套件复跑通过;§3.3/3.4/3.5/3.7 等由双通道逐行核对(发现点 + 主评审独立复核行号与证据)。
- 未连接真库;真库结论沿用 `CODE_QUALITY_REVIEW_2026-10-14.md` 的最近复评记录。
- 本报告未纳入任何评分,仅列提分路径;两处既有评分的调整应在对应修复落地并过验收后进行。
