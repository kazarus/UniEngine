// pgxcopy
// #pgx 原生 COPY 集成:把 UniEngine 的 CopyInL/CopyInP 批量写入接到 pgx CopyFrom。
// #
// #背景:pgx 的 database/sql 适配层(stdlib)不模拟 lib/pq 的逐行 COPY 协议——
// #COPY 语句 NumInput()=0,逐行传参被 database/sql 拒绝(expected 0 arguments)。
// #本包经 *sql.Conn.Raw 取原生连接执行 CopyFrom,绕开该限制。
// #
// #用法:
// #  import "github.com/kazarus/UniEngine/contrib/pgxcopy"
// #  UniEngineEx.CopyInHook = pgxcopy.Hook
// #
// #本包为独立模块(自带 go.mod),不引入则库本体保持零第三方依赖。
package pgxcopy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// #ErrNotPgx 底层连接不是 pgx stdlib(驱动不匹配,调用方可回退其它路径)
var ErrNotPgx = errors.New("pgxcopy: underlying driver is not pgx (*stdlib.Conn)")

// #rowSource 把 [][]interface{} 适配为 pgx.CopyFromSource
type rowSource struct {
	rows [][]interface{}
	idx  int
}

func (s *rowSource) Next() bool {
	return s.idx < len(s.rows)
}

func (s *rowSource) Values() ([]interface{}, error) {
	row := s.rows[s.idx]
	s.idx = s.idx + 1
	return row, nil
}

func (s *rowSource) Err() error {
	return nil
}

// CopyIn 以 pgx 原生 CopyFrom 执行批量写入,返回写入行数。
// Rows 为最终写入行(调用方已完成应用加密);TableName 支持 schema.table 限定名。
func CopyIn(ctx context.Context, db *sql.DB, TableName string, Columns []string, Rows [][]interface{}) (int64, error) {

	if len(Columns) == 0 || len(Rows) == 0 {
		return 0, nil
	}

	conn, eror := db.Conn(ctx)
	if eror != nil {
		return 0, eror
	}
	defer conn.Close()

	var affected int64

	eror = conn.Raw(func(driverConn any) error {

		pc, ok := driverConn.(*stdlib.Conn)
		if !ok {
			return fmt.Errorf("%w, got %T", ErrNotPgx, driverConn)
		}

		//#schema.table 按 "." 分段传入 Identifier
		ident := pgx.Identifier(strings.Split(TableName, "."))

		var err error
		affected, err = pc.Conn().CopyFrom(ctx, ident, Columns, &rowSource{rows: Rows})

		return err
	})

	return affected, eror
}

// Hook 可直接赋值给 TUniEngine.CopyInHook(函数签名与 TCopyInHook 一致)。
// 非 pgx 驱动返回 (false,nil) 回退引擎内置协议;pgx 下的执行错误原样上抛。
func Hook(ctx context.Context, db *sql.DB, TableName string, Columns []string, Rows [][]interface{}) (bool, error) {

	_, eror := CopyIn(ctx, db, TableName, Columns, Rows)
	if eror != nil {
		if errors.Is(eror, ErrNotPgx) {
			return false, nil
		}
		return false, eror
	}

	return true, nil
}
