// Package UniEngine 是一个基于 database/sql 的多数据库 ORM-like 引擎:
// 用结构体 tag 声明列映射,以注册表驱动 SQL 生成,支持 PostgreSQL / SQL Server /
// Oracle / MySQL 及其兼容库(金仓 / 达梦 / openGauss / PolarDB / Taurus)。
//
// # 快速开始
//
// 装配引擎 → 登记类 → 调用查询/写入方法:
//
//	eng := &UniEngine.TUniEngine{Db: db, ColLabel: "db", ColParam: "?", Provider: UniEngine.DtMYSQLN}
//	if err := eng.Initialize(); err != nil { return err }
//	_ = eng.RegisterClass(TUser{}, "t_user").SetKeys("user_indx").SetSecret("password").LastError
//
//	eror := eng.SaveIt(&user)                       // 方言原生 UPSERT(PG on conflict / Oracle·SQLServer merge)
//	eror = eng.SelectL(&users, "select * from t_user where data_stat=?", 1)
//
// 每个查询/写入方法都有对应的 *Ctx 变体(接受 context.Context),原方法等价于以
// context.Background() 调用该变体。
//
// # 方言与占位符
//
// 引擎行为按「协议族」判断,而非逐个具体驱动:占位符风格、标识符引用、元数据目录、
// UPSERT 语法、COPY 白名单均以 TDbFamily 分支。新增兼容库时按所兼容的协议归族即可。
//
// 注意 ColParam 必须与 sql.Open 所用的**驱动名**匹配,SQL Server 尤其关键:
// go-mssqldb 的 "mssql" 会把 ? / :N / $N 预处理为 @pN,而 "sqlserver" 只认 @pN——
// SQL Server 须由开发者把实际驱动名(mssql/sqlserver)填入 ProvideName;
// Initialize 按 ProviderName() 返回值与 ColParam 校验配置,错误组合返回
// ErrInvalidPlaceholderBinding。该检查不连接数据库,调用方须保证声明与实际驱动一致。
// 详见 README 的 0.1 / 0.2 两节。
//
// # 并发契约
//
// 查询与写入路径并发安全:预备语句是局部变量,查询仅持读锁完成注册表/事务查找,
// Register* 可与查询并发执行;同表登记以新元数据快照发布,调用方不要直接并发修改
// 已返回表指针内的 map/slice。三个例外:
//   - 事务期间(Begin 与 Commit/Cancel 之间)调用须串行——底层 *sql.Tx 非并发安全;
//   - 配置字段(ColLabel/ColParam/Provider/SecretOn 等)与表结构变更
//     (SetKeys/SetSecret/SetVerify/AutoKeys/PrepareTables/PrepareRunSQL)
//     应在并发查询开始前完成;
//   - 引擎**不得按值拷贝**,请一律使用 *TUniEngine:拷贝会复制 tx/inTx 与内嵌的
//     mu/txGate,导致副本开启的事务对原件不可见。该约束由 noCopy + go vet(copylocks
//     分析器)在静态检查阶段强制。
//
// # 错误处理
//
// 报错点以哨兵错误(见 errors.go)加 %w 包装,调用方应使用 errors.Is 判别而非匹配文本:
// ErrUnregisteredClass / ErrNoPkeys / ErrNilParam / ErrInvalidTableName /
// ErrInvalidFieldName / ErrInvalidConstraintName / ErrNoTransaction / ErrAlreadyInTransaction /
// ErrInvalidPlaceholderBinding / ErrIncompleteVerification / ErrNoDatabase /
// ErrInvalidDataBase / ErrMultipleRows / ErrTableConflict / ErrClassConflict /
// ErrRegistryInconsistent。
// 注册类 API(RegisterClass/RegisterTable/RegisterField/RegisterPkeys)遇到非法输入时不 panic,
// 而是返回**带 LastError 的未注册表**,便于链式调用在链尾统一检查。
// RegisterClass 不再静默覆盖双键:类改绑报 ErrClassConflict,表名被手工/他类占用报
// ErrTableConflict;确需替换手工表用 RegisterClassForce。
//
// # 可空列与 NULL
//
// 扫描目标就是结构体字段本身,引擎不做 NULL 转换:字段为不可空类型(string/int64/bool/time.Time 等)
// 而库中该列为 NULL 时,Select/SelectL/SelectM/SelectH 报错,且 SelectL 会整批丢弃(不是跳过该行)。
// 请改用 sql.NullString 等 sql.NullXxx 或指针(*string/*int64);[]byte 天然接受 NULL(置 nil)。
// 标量入口 SelectS/SelectD/SelectF 相反:内部使用 sql.NullXxx,NULL 时返回零值且不报错;
// 但三者只接受单行结果,多行时返回 ErrMultipleRows。
//
// # 应用层加密与存储完整性
//
// 敏感列以 tag 标记(db:"password,secreton")或 SetSecret 登记:写入自动加密、读取自动解密,
// 内置实现为 PBKDF2-HMAC-SHA256 派生密钥 + AES-256-GCM(密文自包含盐与迭代数);
// SecretHook 可接管为业务密钥/国密/加密机。存量明文(无 ENC: 标签)读取时直通,便于渐进迁移。
// 另提供存储完整性校验(SetVerify):对登记的保护列写时签名、读时验签。
// 验签失败(含宽容模式放行、严格模式列投影不足)经 TUniEngine.StatusHook 回调
// TUniStatus{Engine, Table, Method, Row, Pkeys, Mode, Accepted, Err}:Engine 为触发事件的
// 引擎实例,Pkeys 为当前行主键实参(供审计定位/落审计表)。钩子在查询 goroutine 内、
// Rows 关闭前同步执行,落库请用独立连接池异步写入,勿在钩子里复用同一引擎/事务。
// 统一写 MAC: 无歧义载荷;严格模式下
// 缺保护列或 MAC 列的结构体查询返回 ErrIncompleteVerification。内置解密的
// 派生密钥缓存最多 128 项,认证失败的冷密文不会进入缓存。
//
// # 模块布局
//
// 本模块(zero dependency)不引入任何第三方驱动;真实数据库集成测试与 pgx COPY 适配
// 分别位于独立模块 integration/ 与 contrib/pgxcopy,按需引入。
package UniEngine
