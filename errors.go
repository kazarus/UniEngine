package UniEngine

import "errors"

// 哨兵错误:调用方应以 errors.Is 判别,而非匹配错误文本。
// 各报错点以 "%w: 动态部分" 形式包装,错误文本与旧版保持一致。
var (
	// ErrUnregisteredClass 类未注册(SaveIt/Insert/Select* 等按类全名查找注册表失败)
	ErrUnregisteredClass = errors.New("UniEngine: no such class registered")
	// ErrNoPkeys 类未登记主键(Update/Delete/SaveIt 需要)
	ErrNoPkeys = errors.New("UniEngine: no pkeys column in class registered")
	// ErrNoTransaction 无进行中的事务(Cancel/Commit)
	ErrNoTransaction = errors.New("UniEngine: no transaction")
	// ErrInvalidTableName 表名标识符不合法(防注入白名单)
	ErrInvalidTableName = errors.New("UniEngine: invalid table name")
	// ErrInvalidConstraintName 约束名标识符不合法(防注入白名单)
	ErrInvalidConstraintName = errors.New("UniEngine: invalid constraint name")
)
