// example_test
// #包级用法示例(编译期校验,不连接真实数据库——因此不带 Output 注释,go test 只编译不执行)
package UniEngine_test

import (
	"database/sql"

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
	db, _ := sql.Open("mysql", "user:pass@tcp(127.0.0.1:3306)/demo")

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
