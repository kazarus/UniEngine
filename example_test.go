// example_test
// #包级用法示例(编译期校验,不连接真实数据库——因此不带 Output 注释,go test 只编译不执行)
package UniEngine_test

import (
	"database/sql"
	"encoding/json"
	"log"

	UniEngine "github.com/kazarus/UniEngine"
)

// TUser 示例类:tag 名即列名;secreton 标记敏感列(写入加密/读取解密)。
type TUser struct {
	WhoBuild string `db:"who_build"`
	UserIndx int64  `db:"user_indx"`
	UserName string `db:"user_name"`
	Password string `db:"password,secreton"`
	Remark   string `db:"-"` //只读标记:登记但不参与写入
}

// Example 展示典型的装配、登记与调用流程。
//
// 注意三处契约:ColParam 必须与 sql.Open 的驱动名匹配(见 README 0.1/0.2);
// 注册类 API 的错误收敛到 LastError;引擎一律以 *TUniEngine 使用,不得按值拷贝。
func Example() {
	db, _ := sql.Open("mysql", "user:pass@tcp(127.0.0.1:3306)/UNIENGINE_TESTDB")

	Eng := &UniEngine.TUniEngine{
		Db:       db,
		ColLabel: "db", //tag 键名:db:"..."
		ColParam: "?",  //占位符前缀:需与驱动名/方言匹配
		Provider: UniEngine.DtMYSQLN,
	}
	_ = Eng.Initialize()

	//#链式登记:主键 + 加密列;链中首个错误记入 LastError,链尾统一检查
	Tbl := Eng.RegisterClass(TUser{}, "demo_user").
		SetKeys("WHO_BUILD", "USER_INDX").
		SetSecret("PASSWORD")
	if Tbl.LastError != nil {
		panic(Tbl.LastError)
	}

	//#写入与查询:方法名后缀 Ctx 表示接受 context.Context
	_ = Eng.SaveIt(&TUser{WhoBuild: "cli", UserIndx: 1, Password: "s3cret"})

	var Users []TUser
	_ = Eng.SelectL(&Users, "select who_build,user_indx,user_name,password from demo_user where user_indx=? ", int64(1))

	//#事务:期间调用须串行
	_ = Eng.Begin()
	if eror := Eng.SaveIt(&TUser{WhoBuild: "cli", UserIndx: 2}); eror != nil {
		_ = Eng.Cancel()
	} else {
		_ = Eng.Commit()
	}
}

// ExampleTUniEngine_Secret 展示应用加密钩子的接管方式(默认实现为 PBKDF2 + AES-256-GCM)。
func ExampleTUniEngine_Secret() {
	Eng := &UniEngine.TUniEngine{SecretOn: 1, SecretBy: "high-entropy-key", SecretIter: 600000}

	//#默认实现:返回 ENC: 前缀的自包含密文(盐与迭代数内嵌),同一明文两次加密结果不同
	CipherTxt, _ := Eng.Secret("plain-text", true)
	PlainTxt, _ := Eng.Secret(CipherTxt, false)

	_ = PlainTxt

	//#接管为业务密钥/国密/加密机:赋值 SecretHook 即可(Encrypt=true 加密,false 解密)
	Eng.SecretHook = func(Value string, Encrypt bool) (string, error) {
		if Encrypt {
			return "biz:" + Value, nil
		}
		return Value, nil
	}
}

// TVerifiedUser 完整性校验示例类:mac_text 是 MAC 伴随列(SetVerify 的第一个参数),
// 其余为保护列。写路径自动回填 mac_text,读路径逐行验签。
type TVerifiedUser struct {
	WhoBuild string `db:"who_build"`
	UserIndx int64  `db:"user_indx"`
	UserName string `db:"user_name"`
	MacText  string `db:"mac_text"`
}

// ExampleTUniEngine_StatusHook 展示把完整性校验失败接入应用日志:
// 严格模式阻断读取、宽容模式放行,两种失败都会回调,以 Accepted 区分。
func ExampleTUniEngine_StatusHook() {
	db, _ := sql.Open("postgres", "postgres://user:pass@127.0.0.1:5432/UNIENGINE_TESTDB?sslmode=disable")

	Eng := &UniEngine.TUniEngine{
		Db:               db,
		ColLabel:         "db",
		ColParam:         "$",
		Provider:         UniEngine.DtPOSTGR,
		VerifyOn:         1,
		VerifyBy:         "high-entropy-key",
		VerificationMode: 2, //2=严格(阻断读取);0/1=宽容(记录告警并放行)
	}
	_ = Eng.Initialize()

	//#主键须先于 SetVerify 登记,且保护列应覆盖全部主键(实现行级绑定)
	if Tbl := Eng.RegisterClass(TVerifiedUser{}, "demo_verified_user").
		SetKeys("who_build", "user_indx").
		SetVerify("mac_text", "who_build", "user_indx", "user_name"); Tbl.LastError != nil {
		panic(Tbl.LastError)
	}

	//#失败通知:引擎在查询 goroutine 内同步调用,只做轻量记录,勿阻塞或用它替代错误处理
	Eng.StatusHook = func(Status UniEngine.TUniStatus) {
		log.Printf("integrity: table=%s method=%s row=%d mode=%d accepted=%v err=%v",
			Status.Table, Status.Method, Status.Row, Status.Mode, Status.Accepted, Status.Err)

		if Status.Accepted {
			//#宽容模式已放行:适合升级告警/审计;严格模式下不会走到这里
			return
		}
		//#严格模式:查询已返回错误(见下方 SelectL);
		//#需要额外处置(告警/熔断/人工核查)时在此触发
	}

	var Users []TVerifiedUser
	//#严格模式返回 error,钩子已记录明细;敏感字段不进入载荷,可安全写日志
	_ = Eng.SelectL(&Users, "select who_build,user_indx,user_name,mac_text from demo_verified_user")
}

// ExampleTUniEngine_StatusHook_auditTable 展示把验签失败异步写入审计表。
//
// 审计表结构(以 PostgreSQL 为例):
//
//	create table uni_integrity_audit (
//	    id           bigserial primary key,
//	    happened_at  timestamptz not null default now(),
//	    engine_db    text,
//	    tabl_name    text not null,
//	    method       text not null,
//	    row_no       int  not null,
//	    verify_mode  int  not null,
//	    accepted     boolean not null,
//	    pkeys        text not null,
//	    err_text     text not null
//	);
func ExampleTUniEngine_StatusHook_auditTable() {
	db, _ := sql.Open("postgres", "postgres://user:pass@127.0.0.1:5432/UNIENGINE_TESTDB?sslmode=disable")

	//#审计连接池独立于业务引擎:钩子在查询 goroutine 内执行,
	//#走业务池会在事务中/单连接池下与未关闭的 Rows 抢连接
	audit, _ := sql.Open("postgres", "postgres://user:pass@127.0.0.1:5432/UNIENGINE_TESTDB?sslmode=disable")

	Eng := &UniEngine.TUniEngine{
		Db: db, ColLabel: "db", ColParam: "$", Provider: UniEngine.DtPOSTGR,
		VerifyOn: 1, VerifyBy: "high-entropy-key", VerificationMode: 1,
	}
	_ = Eng.Initialize()

	//#钩子只入队:队列满时丢弃,绝不让审计写库拖慢或阻塞业务查询
	events := make(chan UniEngine.TUniStatus, 1024)
	Eng.StatusHook = func(Status UniEngine.TUniStatus) {
		select {
		case events <- Status:
		default:
		}
	}

	//#写协程用独立连接池落库;Engine 指针提供 DataBase 等来源信息,
	//#Pkeys 是登记顺序的主键实参(JSON 序列化后整列存储)
	go func() {
		const SqlInsert = `insert into uni_integrity_audit
			(engine_db, tabl_name, method, row_no, verify_mode, accepted, pkeys, err_text)
			values ($1,$2,$3,$4,$5,$6,$7,$8)`
		for Status := range events {
			Keys, _ := json.Marshal(Status.Pkeys) //#[{"column":"who_build","value":1},...]
			if _, eror := audit.Exec(SqlInsert,
				Status.Engine.DataBase, Status.Table, Status.Method, Status.Row,
				Status.Mode, Status.Accepted, string(Keys), Status.Err.Error()); eror != nil {
				log.Printf("audit insert failed: %v", eror)
			}
		}
	}()
}
