package UniEngine

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
)

// TUniEngine is a multi-database ORM-like engine built on database/sql.
//
// Concurrency: TUniEngine is safe for concurrent use. Query/write paths hold
// the registry RWMutex only for the duration of a lookup (class registry /
// current transaction), and Register* may run alongside queries. Two
// exceptions:
//   - while a transaction is active (between Begin and Commit/Cancel), calls
//     must be serialized — database/sql's *sql.Tx is not safe for concurrent
//     use, and every statement is routed to that transaction;
//   - config Fields (ColLabel/ColParam/Provider/SecretOn/SecretBy/...) and
//     table-structure edits (SetKeys/SetSecret/AutoKeys/PrepareTables/
//     PrepareRunSQL) should complete before concurrent queries begin.
//
// TUniEngine 不得按值拷贝(禁止原因见 noCopy 声明处);请一律使用 *TUniEngine。
type TUniEngine struct {
	noCopy noCopy //#禁止按值拷贝(go vet copylocks);请一律使用 *TUniEngine

	Db *sql.DB

	mu        *sync.RWMutex //#保护 HashTabl 与事务状态;指针使历史副本共享同一把锁
	txGate    *sync.Mutex   //#串行化 Begin/Commit/Cancel 三个事务生命周期操作(不护查询);同样共享
	locksOnce sync.Once     //#并发零值引擎的懒初始化;Initialize 后复制副本时 done 已置位

	//#说明:这两个锁必须是共享指针。历史调用方可能大量按值复制
	//#TUniEngine(每请求 c.Set("DB", ...)),若改为内嵌值,副本会各自持锁,
	//#却仍写同一张 HashTabl,运行时 RegisterClass/PrepareTables 会与读并发。
	//#新代码仍应使用 *TUniEngine;noCopy 只是静态提示,不能强制既有调用方。
	ColLabel string //#字段字号
	ColParam string //#参数符号
	HashTabl map[string]*TUniTable

	SecretOn   int64       //#开启敏感信息加密
	SecretBy   string      //#敏感信息加密密钥,推荐BASE64
	SecretIter int         //#PBKDF2迭代次数#0用缺省(UniSecretIter);仅影响新写入,读取以密文内嵌值为准
	SecretHook TSecretHook //#应用加密钩子#为空时使用内置AES-256-GCM

	VerifyOn   int64       //#开启存储完整性校验#写路径回填MAC伴随列,读路径逐行验签(与TUniTable.VerifyOn同时开启才生效)
	VerifyBy   string      //#内置HMAC-SHA256密钥,推荐BASE64#VerifyHook非空时不使用
	VerifyMode int64       //#验签模式#1=宽容(无MAC标签的存量行跳过验签,回填期用);2=严格(无标签行报错,回填完成后切换);0及其它按1处理
	VerifyHook TVerifyHook //#应用完整性钩子#为空时使用内置HMAC-SHA256#应用可实现国密HMAC-SM3等

	CopyInHook TCopyInHook //#COPY协议执行钩子#为空时使用内置pq风格协议(lib/pq兼容)

	// Instance 数据库实例名。引擎内部未使用(全仓库零引用),保留仅为兼容既有调用方。
	// Deprecated: 设置它不会产生任何效果;需要实例名的场景请自行维护或经 SQL 传递。
	Instance string

	// DataBase 数据库名称。MySQL 族的元数据探测(Exist*/ExistConst/AutoKeys/IfDropView)
	// 走 information_schema,必须提供库名,否则报 "database is not specified";其余族无需该字段。
	DataBase string

	// DataUser 数据库用户。引擎内部未使用(全仓库零引用),保留仅为兼容既有调用方。
	// Deprecated: 设置它不会产生任何效果。
	DataUser string

	// Provider 数据库驱动标识:决定协议族(占位符风格/标识符引用/元数据目录/upsert 语法/
	// INSERT ALL/COPY 白名单)。方言相关的一切分支都只看本字段。
	Provider TDriveType

	// Supplier **仅**用于 InsertP 的批量路由:PolarDB 时改走 COPY 协议。
	// 引擎内部从不赋值,须由调用方显式设置;它与 Provider 是两套开关——
	// 只设 Provider=DtPOLODB 不会让 InsertP 走 COPY,
	// 而 Supplier=DtPOLODB 配非 PG 族 Provider 会让 InsertP 直接报错。
	Supplier TDriveType

	// ProvideName 由开发者指定的 database/sql 驱动名。SQL Server 必填:
	// "mssql" 使用 :/$/? 占位符,"sqlserver" 使用 @p。
	// 此字段必须与 sql.Open 或连接器实际使用的驱动模式一致。
	ProvideName string

	tx       *sql.Tx //#当前事务(Begin 之后非 nil)
	inTx     bool    //#事务进行中(期间调用须串行)
	runDebug int32   //#SQL调试输出开关#原子读写,可运行中切换

	secret *secretState //#派生密钥缓存与本进程写盐(懒初始化,详见UniSecret.go)

}

// noCopy 本身不实现任何同步语义,唯一用途是让 go vet 的 copylocks 分析器
// 在静态检查阶段拦下 TUniEngine 的按值拷贝。
//
// 为什么必须禁止按值拷贝:拷贝会复制 Db/ColLabel/ColParam/Provider/tx/inTx 等值字段,
// 以及事务状态(tx/inTx)等值字段,于是副本与原件出现"事务状态各自一份"的错配——
// 副本 Begin 后原件仍认为无事务,原件上的写入会绕过该事务直接落到连接池。
// 锁指针(mu/txGate)特意共享,以兼容历史副本;新代码仍应使用 *TUniEngine。
//
// 作为具名字段(非匿名嵌入)使用,故不会把 Lock/Unlock 提升到 TUniEngine 上;
// 按值拷贝会被报告为:
//
//	passes lock by value: UniEngine.TUniEngine contains UniEngine.noCopy
//
// 注意:这是 vet 期检查而非编译错误;`go vet ./...` 已纳入本仓库 CI 门禁。
// 分析按包进行,故 import 本包的外部调用方在各自 `go vet` 时同样会被提示。
type noCopy struct{}

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}

// ensureLocks 为历史零值引擎/复制引擎补齐共享锁指针。
// #用 sync.Once 而非"读指针再补写":并发首次调用零值引擎时,普通双重检查
// #仍会对 mu/txGate 字段产生数据竞争。Initialize 会先调用它;生产代码在复制
// #引擎前已完成初始化,副本的 locksOnce 已置位,Do 为无操作。
func (this *TUniEngine) ensureLocks() {
	this.locksOnce.Do(func() {
		if this.mu == nil {
			this.mu = &sync.RWMutex{}
		}
		if this.txGate == nil {
			this.txGate = &sync.Mutex{}
		}
	})
}

// lockTables 加写锁（注册/表结构变更/事务状态变更）
func (this *TUniEngine) lockTables() {
	this.ensureLocks()
	this.mu.Lock()
}

// rlockTables 加读锁（注册表/事务状态查询）
func (this *TUniEngine) rlockTables() {
	this.ensureLocks()
	this.mu.RLock()
}

// debugging 返回调试输出开关状态（原子读，可运行中切换）
func (this *TUniEngine) debugging() bool {
	return atomic.LoadInt32(&this.runDebug) != 0
}

// tableByType 按类全名取注册表条目（读锁；未注册返回 nil）
func (this *TUniEngine) tableByType(TypeName string) *TUniTable {
	this.rlockTables()
	defer this.mu.RUnlock()

	return this.HashTabl[TypeName]
}

// tableByName 按表名取注册表条目（读锁；未注册返回 nil）
func (this *TUniEngine) tableByName(TableName string) *TUniTable {
	this.rlockTables()
	defer this.mu.RUnlock()

	return this.HashTabl[strings.ToLower(TableName)]
}

// currentTx 返回当前事务（无事务时 nil）；读锁只护状态读取,生命周期操作由 txGate 串行
func (this *TUniEngine) currentTx() *sql.Tx {
	this.rlockTables()
	defer this.mu.RUnlock()

	if this.inTx {
		return this.tx
	}

	return nil
}

// validIdent 校验数据库标识符仅含安全字符（字母/数字/下划线/点），用于表名/约束名拼接前防注入。
// 点号只对表名有语义(schema.table);列名请使用 validColumnName,否则 "a.b" 会被引成 schema 限定引用。
func validIdent(name string) bool {
	if name == "" {
		return false
	}

	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '.') {
			return false
		}
	}

	return true
}

// validColumnName 校验列名:比 validIdent 更严格,不允许点号。
// 列名不是 SQL 标识符路径,"a.b" 若放行会被 quoteIdent 拆成 "a"."b",
// 在 PG/Oracle 下静默指向另一个 schema 的列。
func validColumnName(name string) bool {
	return validIdent(name) && !strings.Contains(name, ".")
}

// ensureDb 执行入口的统一装配校验:未设置 Db 时返回 ErrNoDatabase,而非在 database/sql 内空指针 panic。
func (this *TUniEngine) ensureDb() error {
	if this.Db == nil {
		return ErrNoDatabase
	}

	return nil
}

// nilParamOf 公共入口的 nil 参数统一校验:裸 nil 与 typed nil 均返回 ErrNilParam 包装错误。
// 判断只依赖 reflect.ValueOf(对裸 nil 安全),调用方必须在本函数之后才允许 reflect.TypeOf/Kind
// ——裸 nil 时 TypeOf 返回 nil Type,直接取 Kind 会空指针解引用(评审 P0 缺陷根源)。
func nilParamOf(Method string, i interface{}) error {
	if i == nil {
		return fmt.Errorf("%w: method [%s] got a nil param; check your code;", ErrNilParam, Method)
	}

	if rv := reflect.ValueOf(i); rv.Kind() == reflect.Ptr && rv.IsNil() {
		return fmt.Errorf("%w: method [%s] got a nil pointer param; check your code;", ErrNilParam, Method)
	}

	return nil
}

func (this *TUniEngine) getValParam(aIndex int) string {
	if this.dbFamily() == FmMYSQLN {
		return fmt.Sprintf("%s", this.ColParam)
	}

	return fmt.Sprintf("%s%d", this.ColParam, aIndex)
}

// validateSQLServerPlaceholderConfig 按开发者声明的实际驱动名校验占位符。
// database/sql 不暴露 sql.Open 的驱动名;调用方必须保证 ProvideName 与连接一致。
func (this *TUniEngine) validateSQLServerPlaceholderConfig() error {
	if this.dbFamily() != FmSQLSRV {
		return nil
	}
	if this.Db == nil {
		return fmt.Errorf("%w: SQL Server Db is nil", ErrInvalidPlaceholderBinding)
	}
	if this.ProvideName == "" {
		return fmt.Errorf("%w: SQL Server ProvideName is required (mssql or sqlserver)", ErrInvalidPlaceholderBinding)
	}
	switch this.ProviderName() {
	case "mssql":
		if this.ColParam == ":" || this.ColParam == "$" || this.ColParam == "?" {
			return nil
		}
	case "sqlserver":
		if this.ColParam == "@p" {
			return nil
		}
	default:
		return fmt.Errorf("%w: unsupported SQL Server ProvideName %q", ErrInvalidPlaceholderBinding, this.ProvideName)
	}
	return fmt.Errorf("%w: SQL Server ProvideName %q does not accept ColParam %q", ErrInvalidPlaceholderBinding, this.ProvideName, this.ColParam)
}

func (this *TUniEngine) getColParam(FieldName string) string {
	if this.dbFamily() == FmMYSQLN {
		return fmt.Sprintf("%s", FieldName)
	}

	if this.dbFamily() == FmORACLE {
		return fmt.Sprintf("%s", FieldName)
	}

	return quoteIdent(FieldName)
}

// getSqlQuery 方言占位符转换(评审 Q5:正则替换会误改字符串常量/注释里的 $N,
// 且 MySQL 全部替成 ? 却不重排实参,乱序/重复编号时错绑)。
// 现经 rewriteParamPlaceholder 词法扫描,仅代码区的 $N 参与转换;MySQL 分支按
// 占位符**出现序**重排/复制实参($1 $2 $1 -> ?,?,? + [a1,a2,a1]),编号越界
// 或实参多余时显式报错。SQL 无代码区 $N(如原生 ?/无占位符)时原样透传。
// 返回转换后 SQL 与重排后实参(Oracle/透传路径实参原样)。
func (this *TUniEngine) getSqlQuery(SqlQuery string, Args ...interface{}) (string, []interface{}, error) {
	if len(Args) == 0 {
		return SqlQuery, Args, nil
	}

	if this.dbFamily() == FmORACLE {
		if this.debugging() {
			fmt.Println(`UniEngine: Oracle驱动时,替换"$"到":"`)
		}

		Result, _, eror := rewriteParamPlaceholder(SqlQuery, phOracle)
		if eror != nil {
			return "", nil, eror
		}
		return Result, Args, nil
	}

	if this.dbFamily() == FmMYSQLN {
		Result, Seq, eror := rewriteParamPlaceholder(SqlQuery, phMySQL)
		if eror != nil {
			return "", nil, eror
		}
		if len(Seq) == 0 {
			//#原生 ? 占位符或无占位符:不做任何改写与校验,保持既有行为
			return SqlQuery, Args, nil
		}

		MaxUsed := 0
		for _, Number := range Seq {
			if Number < 1 {
				return "", nil, fmt.Errorf("UniEngine: invalid placeholder $%d in sql (numbering starts at $1); check your code;", Number)
			}
			if Number > len(Args) {
				return "", nil, fmt.Errorf("UniEngine: placeholder $%d exceeds %d arguments; check your code;", Number, len(Args))
			}
			if Number > MaxUsed {
				MaxUsed = Number
			}
		}
		if len(Args) > MaxUsed {
			return "", nil, fmt.Errorf("UniEngine: %d arguments but max placeholder is $%d; mixed placeholders or wrong arg count; check your code;", len(Args), MaxUsed)
		}

		ListArgs := make([]interface{}, 0, len(Seq))
		for _, Number := range Seq {
			ListArgs = append(ListArgs, Args[Number-1])
		}

		if this.debugging() {
			fmt.Println(`UniEngine: MySQL驱动时,替换"$"到"?"并按出现序重排实参`)
		}
		return Result, ListArgs, nil
	}

	return SqlQuery, Args, nil
}

// sqlDebug 统一的 SQL 调试输出（runDebug 开启时）。
// 只打印语句与实参个数,不打印实参值:加密前的明文密码/证件号等会随实参进入日志,
// 而调试开关常在生产排障时临时打开。需要看具体值请在应用侧脱敏后自行记录。
func (this *TUniEngine) sqlDebug(Kind string, SqlQuery interface{}, Args interface{}) {
	if !this.debugging() {
		return
	}

	fmt.Printf("UniEngine: %s.sql: %v\n", Kind, SqlQuery)
	fmt.Printf("UniEngine: %s.args: %d (values redacted)\n", Kind, argCountOf(Args))
}

// argCountOf 返回一次调用的实参个数(兼容单行 []interface{} 与 COPY 的 [][]interface{})
func argCountOf(Args interface{}) int {
	switch Value := Args.(type) {
	case nil:
		return 0
	case []interface{}:
		return len(Value)
	case [][]interface{}:
		return len(Value)
	}

	return 0
}

// ProviderName 返回驱动名称。SQL Server 在配置 ProvideName 后返回该声明;
// 未填写时沿用旧的协议族名称,但 Initialize 会拒绝缺失的 SQL Server 配置。
func (this *TUniEngine) ProviderName() string {
	var Result string
	if this.Provider == DtSQLSRV && this.ProvideName != "" {
		return this.ProvideName
	}

	switch this.Provider {
	case DtORACLE:
		{
			Result = "oracle"
		}
	case DtDAMENG:
		{
			Result = "dameng"
		}
	case DtSQLSRV:
		{
			Result = "sqlserver"
		}
	case DtPOSTGR:
		{
			Result = "postgresql"
		}
	case DtKINGES:
		{
			Result = "kingbase"
		}
	case DtOPENGS:
		{
			Result = "opengauss"
		}
	case DtPOLODB:
		{
			Result = "polardb"
		}
	case DtMYSQLN:
		{
			Result = "mysql"
		}
	case DtTAURUS:
		{
			Result = "taurus"
		}
	}

	return Result
}

// 批量写入的分页常量。与 UI 分页(DefaultPageSize/SpecialPageSize)解耦:
// 批量页大小受单条 SQL 绑定参数上限约束,0/负值回退 999 行,且最高 999 行。
const (
	defaultBatchRows int64 = 999
	maxBatchRows     int64 = 999
	//#Oracle INSERT ALL 的性能拐点:999 个 INTO 约 144s,500 约 14s,200 约 0.3s,
	//#100 约 0.1s,50 约 0.05s(10 列/1 列真机均复现);按 100 行/页收敛。
	oracleMaxBatchRows int64 = 100

	//#单语句绑定参数预算(预留余量):Oracle/达梦 65535 工程上限取 60000;
	//#SQLServer 2100 上限取 2000;PG/MySQL 65535 取 60000。
	oracleMaxBindParams    int64 = 60000
	sqlserverMaxBindParams int64 = 2000
	defaultMaxBindParams   int64 = 60000
)

// maxBatchBindParams 返回当前方言单条 INSERT 的绑定参数预算。
func (this *TUniEngine) maxBatchBindParams() int64 {
	switch this.Provider {
	case DtORACLE, DtDAMENG:
		return oracleMaxBindParams
	case DtSQLSRV:
		return sqlserverMaxBindParams
	}

	return defaultMaxBindParams
}

// normalizeBatchPageSize 归一化批量写入页大小:
//   - 0/负值 → 999 行;
//   - 超过 999 → 999;
//   - Oracle/达梦统一压到 100 行/页(INSERT ALL 的 INTO 数量是性能拐点);
//   - 按列数把"行数 × 列数"压到方言绑定参数预算内,至少 1 行。
//
// Columns 为 0(未解析/未知)时只做行数上限,不做参数预算;单行超过预算时仍返回 1,
// 由数据库报错,避免静默丢行。
func (this *TUniEngine) normalizeBatchPageSize(Requested int64, Columns int) int64 {
	Rows := Requested
	if Rows <= 0 {
		Rows = defaultBatchRows
	}
	if Rows > maxBatchRows {
		Rows = maxBatchRows
	}
	if (this.Provider == DtORACLE || this.Provider == DtDAMENG) && Rows > oracleMaxBatchRows {
		Rows = oracleMaxBatchRows
	}

	if Columns > 0 {
		MaxRows := this.maxBatchBindParams() / int64(Columns)
		if MaxRows < 1 {
			MaxRows = 1
		}
		if Rows > MaxRows {
			Rows = MaxRows
		}
	}

	if Rows < 1 {
		Rows = 1
	}

	return Rows
}

// SpecialPageSize 返回调用方请求的分页大小；非正值（0/负数）回退 DefaultPageSize。
func (this *TUniEngine) SpecialPageSize(PageSize int64) int64 {
	if PageSize <= 0 {
		return this.DefaultPageSize()
	}

	return PageSize
}

func (this *TUniEngine) DefaultPageSize() int64 {
	switch this.dbFamily() {
	case FmORACLE, FmPOSTGR:
		{
			return 99
		}
	case FmSQLSRV, FmMYSQLN:
		{
			return 10
		}
	}

	return 0
}

// ---------------------------------------------------------------------------
// 语句/事务管理
// ---------------------------------------------------------------------------

// prepareCtx 在当前事务(若有)或连接池上预备语句。
// 语句是局部变量,不再保存到引擎——这是并发安全的关键:查询路径不共享可变状态。
// 返回的语句由调用方负责 Close。
func (this *TUniEngine) prepareCtx(Ctx context.Context, SqlQuery string) (*sql.Stmt, error) {
	if eror := this.validateSQLServerPlaceholderConfig(); eror != nil {
		return nil, eror
	}
	if eror := this.ensureDb(); eror != nil {
		return nil, eror
	}
	if tx := this.currentTx(); tx != nil {
		return tx.PrepareContext(Ctx, SqlQuery)
	}

	return this.Db.PrepareContext(Ctx, SqlQuery)
}

func (this *TUniEngine) Begin() error {
	return this.BeginCtx(context.Background())
}

func (this *TUniEngine) BeginCtx(Ctx context.Context) error {
	if eror := this.validateSQLServerPlaceholderConfig(); eror != nil {
		return eror
	}

	//#txGate 串行化事务生命周期操作;驱动调用的网络等待不持有 mu,
	//#查询路径只会在 mu 的短临界区内取事务指针,不会排在 BeginTx 后面。
	this.ensureLocks()
	this.txGate.Lock()
	defer this.txGate.Unlock()

	this.lockTables()

	// 重复 Begin 会让上一个事务失去引用(悬挂/泄漏),此处显式拒绝。
	// 先于 Db 装配检查:重复 Begin 的语义错误不应被"Db 未设置"掩盖。
	if this.inTx || this.tx != nil {
		this.mu.Unlock()
		return ErrAlreadyInTransaction
	}

	if eror := this.ensureDb(); eror != nil {
		this.mu.Unlock()
		return eror
	}

	this.mu.Unlock()

	tx, eror := this.Db.BeginTx(Ctx, nil)
	if eror != nil {
		return eror
	}

	this.lockTables()
	this.tx = tx
	this.inTx = true
	this.mu.Unlock()

	return nil
}

func (this *TUniEngine) Cancel() error {
	this.ensureLocks()
	this.txGate.Lock()
	defer this.txGate.Unlock()

	this.lockTables()

	if !this.inTx || this.tx == nil {
		this.mu.Unlock()
		return ErrNoTransaction
	}

	//#先回收状态再发起网络回滚:Rollback 可能等待驱动/网络,
	//#不能让整个注册表读写(所有查询)陪着持锁等待。
	//#sql.Tx 一旦调用 Commit/Rollback 即进入终态:即使驱动返回错误,该事务上的
	//#后续操作也只会得到 ErrTxDone。引擎引用必须无条件回收,而非仅在成功分支——
	//#否则 BeginCtx 的 context 取消触发 database/sql 自动回滚后,Cancel 返回
	//#ErrTxDone 而引擎仍滞留"事务中",实例永久不可用(评审 Q3)。
	//#底层错误原样返回,由调用方 errors.Is 判别
	tx := this.tx
	this.tx = nil
	this.inTx = false
	this.mu.Unlock()

	return tx.Rollback()
}

func (this *TUniEngine) Commit() error {
	this.ensureLocks()
	this.txGate.Lock()
	defer this.txGate.Unlock()

	this.lockTables()

	if !this.inTx || this.tx == nil {
		this.mu.Unlock()
		return ErrNoTransaction
	}

	//#同 Cancel:提交失败(驱动错误/已被超时回滚)后事务同样进入终态,
	//#引擎引用无条件回收,实例可继续执行并重新 Begin(评审 Q3)
	tx := this.tx
	this.tx = nil
	this.inTx = false
	this.mu.Unlock()

	return tx.Commit()
}

// WithTx 在引擎当前事务状态上执行闭包:fn 返回 nil 才 Commit,返回错误或 panic 则 Cancel。
// 它是现有 Begin/Commit/Cancel 的语法糖,不改变事务状态机:
//   - 闭包内继续使用同一个 *TUniEngine(通常是当前请求副本);
//   - 嵌套 WithTx/Begin 会被 Begin 的 ErrAlreadyInTransaction 拒绝;
//   - 闭包内自行 Commit/Cancel 属于混用,WithTx 返回 ErrNoTransaction;
//   - panic 时先回滚再重新 panic,避免吞掉业务异常;
//   - 回滚错误不覆盖 fn 的原始错误/panic,避免改变调用方语义。
func (this *TUniEngine) WithTx(fn func() error) (err error) {
	if fn == nil {
		return fmt.Errorf("UniEngine: WithTx requires a non-nil callback")
	}
	if eror := this.Begin(); eror != nil {
		return eror
	}

	finalized := false
	defer func() {
		if rec := recover(); rec != nil {
			_ = this.Cancel()
			panic(rec)
		}
		if !finalized {
			_ = this.Cancel()
		}
	}()

	if eror := fn(); eror != nil {
		return eror
	}

	//#闭包内自行 Commit/Cancel 会让这里的 Commit 变成 ErrNoTransaction;
	//#显式识别,避免把"混用"误报成普通提交失败。
	this.rlockTables()
	active := this.inTx && this.tx != nil
	this.mu.RUnlock()
	if !active {
		return fmt.Errorf("%w: WithTx callback committed or cancelled the transaction; do not mix WithTx with Begin/Commit/Cancel", ErrNoTransaction)
	}

	if eror := this.Commit(); eror != nil {
		return eror
	}
	finalized = true
	return nil
}

func (this *TUniEngine) RunDebug(Value bool) error {
	if Value {
		atomic.StoreInt32(&this.runDebug, 1)
	} else {
		atomic.StoreInt32(&this.runDebug, 0)
	}

	return nil
}

// validateBasicConfig 校验实际执行 SQL 所需的基础配置。
// Db 为空时跳过:零值引擎/仅注册元数据的用法仍然合法。
func (this *TUniEngine) validateBasicConfig() error {
	if this.Db == nil {
		return nil
	}
	if strings.TrimSpace(this.ColLabel) == "" {
		return fmt.Errorf("%w: ColLabel is empty", ErrInvalidConfig)
	}
	switch this.ColParam {
	case "$", ":", "?", "@p":
	default:
		return fmt.Errorf("%w: unsupported ColParam %q (want $, :, ? or @p)", ErrInvalidConfig, this.ColParam)
	}
	if dbFamilyOf(this.Provider) == FmUNKNOWN {
		return fmt.Errorf("%w: unsupported Provider %v", ErrInvalidConfig, this.Provider)
	}
	return nil
}

func (this *TUniEngine) Initialize() error {
	//#先装配共享锁指针:调用方可能在 Initialize 之后按值复制引擎。
	this.ensureLocks()

	//#基础配置校验:只在已装配 Db 时执行。零值引擎仍可用于注册/单元测试,
	//#避免把"仅登记元数据"的合法用法误判为配置错误。
	if eror := this.validateBasicConfig(); eror != nil {
		return eror
	}

	//#SQL Server 初始化时检查开发者声明的驱动名与占位符组合。
	//#失败时不登记内置表,由调用方直接处理初始化错误。
	if eror := this.validateSQLServerPlaceholderConfig(); eror != nil {
		return eror
	}

	//#并发契约:表结构变更(SetKeys/SetSecret/AutoKeys/PrepareTables/PrepareRunSQL)
	//#与配置字段(ColLabel/ColParam/Provider/SecretOn 等)须在并发查询开始前完成;
	//#注册(RegisterClass/RegisterTable/...)与查询可并发。
	//#Initialize 不触碰事务状态:进行中的事务由调用方 Commit/Cancel 终结,
	//#否则会出现 inTx=false 而 tx!=nil 的悬挂状态(Begin 被拒、Commit 报无事务)。
	//#内置元数据类的登记名必须是合法标识符:注册 API 现对表名做白名单校验,
	//#旧值 "github.com/kazarus/uniengine/unitable" 含 '/' 会被拒。
	//#该键仅作小写别名(另以类全名 t.String() 注册),改成短名不影响任何查找路径。
	if tbl := this.RegisterClass(TUniTable{}, "unitable"); tbl.LastError != nil {
		return tbl.LastError
	}
	if tbl := this.RegisterClass(TUniField{}, "unifield"); tbl.LastError != nil {
		return tbl.LastError
	}

	return nil
}
