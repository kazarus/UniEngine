package UniEngine

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
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
type TUniEngine struct {
	Db *sql.DB

	mu       *sync.RWMutex //#保护 HashTabl 与事务状态（指针：按值传递引擎时共享同一把锁）
	ColLabel string        //#字段字号
	ColParam string        //#参数符号
	HashTabl map[string]*TUniTable

	SecretOn   int64       //#开启敏感信息加密
	SecretBy   string      //#敏感信息加密密钥
	SecretIter int         //#PBKDF2迭代次数#0用缺省(UniSecretIter);仅影响新写入,读取以密文内嵌值为准
	SecretHook TSecretHook //#应用加密钩子#为空时使用内置AES-256-GCM

	VerifyOn   int64       //#开启存储完整性校验#写路径回填MAC伴随列,读路径逐行验签(与TUniTable.VerifyOn同时开启才生效)
	VerifyBy   string      //#内置HMAC-SHA256密钥#VerifyHook非空时不使用
	VerifyMode int64       //#验签模式#1=宽容(无MAC标签的存量行跳过验签,回填期用);2=严格(无标签行报错,回填完成后切换);0及其它按1处理
	VerifyHook TVerifyHook //#应用完整性钩子#为空时使用内置HMAC-SHA256#应用可实现国密HMAC-SM3等

	CopyInHook TCopyInHook //#COPY协议执行钩子#为空时使用内置pq风格协议(lib/pq兼容)

	Instance string     //#数据库实例
	DataBase string     //#数据库名称
	DataUser string     //#数据库用户
	Provider TDriveType //#数据库驱动
	Supplier TDriveType //#数据库驱动(区分 DtORACLE VS DtDAMENG)

	tx       *sql.Tx //#当前事务(Begin 之后非 nil)
	inTx     bool    //#事务进行中(期间调用须串行)
	runDebug int32   //#SQL调试输出开关#原子读写,可运行中切换

	secret *secretState //#派生密钥缓存与本进程写盐(懒初始化,详见UniSecret.go)
}

// muLazy 保护各引擎 mu 的懒初始化（仅覆盖建锁窗口，不护业务临界区）

// muLazy 保护各引擎 mu 的懒初始化（仅覆盖建锁窗口，不护业务临界区）
var muLazy sync.Mutex

// initLock 保证 this.mu 已创建（并发安全；供 Initialize 与 lockTables 调用）

// initLock 保证 this.mu 已创建（并发安全；供 Initialize 与 lockTables 调用）
func (this *TUniEngine) initLock() {

	muLazy.Lock()
	defer muLazy.Unlock()

	if this.mu == nil {
		this.mu = &sync.RWMutex{}
	}
}

// lockTables 保证锁可用并加写锁（注册/表结构变更/事务状态变更）

// lockTables 保证锁可用并加写锁（注册/表结构变更/事务状态变更）
func (this *TUniEngine) lockTables() {

	this.initLock()
	this.mu.Lock()
}

// rlockTables 保证锁可用并加读锁（注册表/事务状态查询）

// rlockTables 保证锁可用并加读锁（注册表/事务状态查询）
func (this *TUniEngine) rlockTables() {

	this.initLock()
	this.mu.RLock()
}

// debugging 返回调试输出开关状态（原子读，可运行中切换）

// debugging 返回调试输出开关状态（原子读，可运行中切换）
func (this *TUniEngine) debugging() bool {
	return atomic.LoadInt32(&this.runDebug) != 0
}

// tableByType 按类全名取注册表条目（读锁；未注册返回 nil）

// tableByType 按类全名取注册表条目（读锁；未注册返回 nil）
func (this *TUniEngine) tableByType(TypeName string) *TUniTable {

	this.rlockTables()
	defer this.mu.RUnlock()

	return this.HashTabl[TypeName]
}

// tableByName 按表名取注册表条目（读锁；未注册返回 nil）

// tableByName 按表名取注册表条目（读锁；未注册返回 nil）
func (this *TUniEngine) tableByName(TableName string) *TUniTable {

	this.rlockTables()
	defer this.mu.RUnlock()

	return this.HashTabl[strings.ToLower(TableName)]
}

// currentTx 返回当前事务（无事务时 nil）；读锁保护，与 Begin/Commit/Cancel 互斥

// currentTx 返回当前事务（无事务时 nil）；读锁保护，与 Begin/Commit/Cancel 互斥
func (this *TUniEngine) currentTx() *sql.Tx {

	this.rlockTables()
	defer this.mu.RUnlock()

	if this.inTx {
		return this.tx
	}

	return nil
}

// validIdent 校验数据库标识符仅含安全字符（字母/数字/下划线/点），用于表名/字段名拼接前防注入

// validIdent 校验数据库标识符仅含安全字符（字母/数字/下划线/点），用于表名/字段名拼接前防注入
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

func (this *TUniEngine) getValParam(aIndex int) string {

	if this.dbFamily() == FmMYSQLN {
		return fmt.Sprintf("%s", this.ColParam)
	}

	return fmt.Sprintf("%s%d", this.ColParam, aIndex)
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

// 参数占位符模式：$1 / $2 ...（Oracle 转 :1，MySQL 转 ?）

// 参数占位符模式：$1 / $2 ...（Oracle 转 :1，MySQL 转 ?）
var reParamPlaceholder = regexp.MustCompile(`\$\d+`)

func (this *TUniEngine) getSqlQuery(SqlQuery string, Args ...interface{}) string {

	if this.dbFamily() == FmORACLE && len(Args) > 0 {

		if this.debugging() {
			fmt.Println(`UniEngine: Oracle驱动时,替换"$"到":"`)
		}

		// 仅替换参数占位符（$1 -> :1），避免误改 SQL 文本中其它 $ 字符
		SqlQuery = reParamPlaceholder.ReplaceAllStringFunc(SqlQuery, func(m string) string {
			return ":" + m[1:]
		})
	}

	if this.dbFamily() == FmMYSQLN && len(Args) > 0 {

		if this.debugging() {
			fmt.Println(`UniEngine: MySQL驱动时,替换"$"到"?"`)
		}

		// 包级预编译正则，消除每次调用 Compile 的开销与静默失败返回空串的路径
		SqlQuery = reParamPlaceholder.ReplaceAllString(SqlQuery, "?")
	}

	return SqlQuery
}

// sqlDebug 统一的 SQL 调试输出（runDebug 开启时）

// sqlDebug 统一的 SQL 调试输出（runDebug 开启时）
func (this *TUniEngine) sqlDebug(Kind string, SqlQuery interface{}, Args interface{}) {

	if !this.debugging() {
		return
	}

	fmt.Printf("UniEngine: %s.sql: %v\n", Kind, SqlQuery)
	fmt.Printf("UniEngine: %s.val: %v\n", Kind, Args)
}

func (this *TUniEngine) ProviderName() string {

	var Result string

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

// SpecialPageSize 返回调用方请求的分页大小；非正值（0/负数）回退 DefaultPageSize。

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

	if tx := this.currentTx(); tx != nil {
		return tx.PrepareContext(Ctx, SqlQuery)
	}

	return this.Db.PrepareContext(Ctx, SqlQuery)
}

func (this *TUniEngine) Begin() error {
	return this.BeginCtx(context.Background())
}

func (this *TUniEngine) BeginCtx(Ctx context.Context) error {

	this.lockTables()
	defer this.mu.Unlock()

	// 重复 Begin 会让上一个事务失去引用(悬挂/泄漏),此处显式拒绝
	if this.inTx || this.tx != nil {
		return ErrAlreadyInTransaction
	}

	tx, eror := this.Db.BeginTx(Ctx, nil)
	if eror != nil {
		return eror
	}

	this.tx = tx
	this.inTx = true

	return nil
}

func (this *TUniEngine) Cancel() error {

	this.lockTables()
	defer this.mu.Unlock()

	if !this.inTx || this.tx == nil {
		return ErrNoTransaction
	}

	if eror := this.tx.Rollback(); eror != nil {
		return eror
	}

	this.tx = nil
	this.inTx = false

	return nil
}

func (this *TUniEngine) Commit() error {

	this.lockTables()
	defer this.mu.Unlock()

	if !this.inTx || this.tx == nil {
		return ErrNoTransaction
	}

	if eror := this.tx.Commit(); eror != nil {
		return eror
	}

	this.tx = nil
	this.inTx = false

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

func (this *TUniEngine) Initialize() error {

	//#并发契约:表结构变更(SetKeys/SetSecret/AutoKeys/PrepareTables/PrepareRunSQL)
	//#与配置字段(ColLabel/ColParam/Provider/SecretOn 等)须在并发查询开始前完成;
	//#注册(RegisterClass/RegisterTable/...)与查询可并发
	this.initLock()

	this.RegisterClass(TUniTable{}, "github.com/kazarus/uniengine/unitable")
	this.RegisterClass(TUniField{}, "github.com/kazarus/uniengine/unifield")

	this.inTx = false

	return nil
}
