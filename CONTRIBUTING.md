# Contributing to UniEngine

Thanks for your interest. This guide covers the build/test loop, the project's
(intentionally non-idiomatic) coding style, how to add a dialect, and the
invariants that must not regress.

中文说明见文末 [中文要点](#中文要点)。README 主体为中文。

---

## 1. Layout

| Path | Module | Third-party deps | Purpose |
| ---- | ------ | ---------------- | ------- |
| `/` (root) | `github.com/kazarus/UniEngine` | **none, by design** | the library |
| `integration/` | `.../integration` | pgx, go-mssqldb, go-ora, go-sql-driver/mysql | real-database tests |
| `contrib/pgxcopy/` | `.../contrib/pgxcopy` | pgx | optional native `CopyFrom` hook for pgx |

**The root module must keep zero dependencies.** Everything that needs a driver
lives in a separate module. If a change would add a dependency to the root
`go.mod`, it does not belong there.

## 2. Build & test

```sh
# library (root module: no network needed, no go.sum)
go build ./... && go vet ./... && gofmt -l . && go test -race -count=1 .
go test -cover -count=1 .                     # keep coverage from regressing (CI floor: 80%)

# integration (real databases; unset env => the suite skips)
cd integration && go vet ./... && go test -race -count=1 .

# repo-hygiene gates also run as part of `go test` (see §3): they walk the tree and
# reject a blank line right after an opening brace, and duplicated doc comments
go test -run '^TestStyle' -v .

# fuzz targets: seed corpus runs with every `go test`; random exploration is explicit
go test -run '^$' -fuzz FuzzValidIdent -fuzztime 30s .
go test -run '^$' -fuzz FuzzUniFieldInitialize -fuzztime 30s .

# optional: static checks
go test -bench . -benchmem -run '^$'
```

CI (`.github/workflows/ci.yml`) gates on `build` / `vet` / `gofmt -l` / `test -race`
(with a **80% coverage floor**) / benchmark smoke / **10s fuzz smoke per target**, on
a Go 1.24–1.27 matrix. `gofmt`, `vet` and the coverage floor are **hard gates** — a
change that fails any of them will not be merged.

Real-database integration tests are split by container weight:

| Job | Family | Trigger | Provisioning |
| --- | ------ | ------- | ------------ |
| `integration-pg` | PostgreSQL 16 | every push/PR | `postgres:16-alpine` service |
| `integration-mysql` | MySQL 8.4 | every push/PR | `mysql:8.4` service |
| `integration-heavy` | SQL Server 2022 + Oracle Free 23 | nightly + manual | `docker run` + in-shell readiness polling |

`integration-heavy` is nightly rather than per-push on purpose: those two images need
1–2 GB of RAM and 30–90 s to become ready. It does not block pull requests but surfaces
rot within a day; trigger it manually before a release, or relax its `if:` if your
runner can absorb it on every push. It prints container logs when a wait times out or
a test fails, so a failure is diagnosable from the job output alone.

## 3. Coding style (deliberately non-standard)

Library identifiers use **PascalCase** (locals and parameters too), the receiver
is always `this`, and methods taking a `context.Context` carry a `...Ctx` suffix.
Tests, `integration/` and `contrib/` use ordinary Go camelCase.

This deviates from the Go community style guide **on purpose**: it is consistent
across the codebase and keeps `gofmt` clean. Two consequences:

- **Do not reformat existing code to "fix" the naming.** Mixed style is worse
  than either extreme.
- If you do want to change the convention, do it as **one mechanical commit**
  with a `gofumpt`/`revive` gate — never as scattered drive-by renames. The
  history already contains too many rename-only commits.

Mechanical artefacts to avoid: duplicated doc comments (a comment block, a blank
line, then the same block again) and a blank line immediately after an opening
brace. Both were cleaned up once and then reintroduced by new code, so they are
now enforced by `style_test.go` — which runs in every `go test`, including the CI
matrix. You will get a `file:line` from the test rather than a review comment.

## 4. Invariants that must not regress

These came out of an external review round and are all covered by tests. If your
change touches a public entry point, check them:

1. **No public API panics on bad input.** Every entry point that takes
   `interface{}` validates before touching `reflect.TypeOf(...).Kind()`
   (bare `nil` makes `TypeOf` return a nil `Type`). Use `nilParamOf` and return
   `ErrNilParam`.
2. **Shape contracts are checked, not assumed.** `Select` needs `*struct`,
   `SelectL` needs `*slice`, `SelectM/H` need a non-nil map whose key type
   `string` is assignable to (`reflect.TypeOf("").AssignableTo(keyType)`).
   *`Kind()` is not enough*: a named string type has `Kind() == String` but a
   `string` value is not assignable to it.
3. **Registration APIs never return `nil`.** `RegisterClass` / `RegisterTable` /
   `RegisterField` / `RegisterPkeys` return a `*TUniTable` carrying `LastError`
   on failure, so chained calls short-circuit instead of dereferencing `nil`.
4. **Identifiers are whitelisted.** Runtime-supplied table/field names and
   struct-tag column names go through `validIdent`
   (`[A-Za-z0-9_.]` only). `db:"-"` / `db:"_"` are readonly markers and are
   exempt.
5. **The engine is never copied by value.** `TUniEngine` embeds `noCopy`; use
   `*TUniEngine`. Copying shares `mu` but duplicates `tx`/`inTx`, so a clone's
   transaction is invisible to the original.
6. **Caches must be keyed by every input.** The derived-key cache is keyed by
   salt + iterations + a fingerprint of `SecretBy`. This was once salt-only, which
   made a runtime key rotation a silent no-op; the regression test is
   `TestSecretRotateKeyTakesEffect`. If you add another input that affects
   derivation, add it to the key.
7. **Encrypt-side and decrypt-side bounds must match.** The iteration count is
   capped at `secretIterMax`; the cap is enforced on **both** the write and the
   read path. A bound enforced on only one side produces ciphertext the engine
   refuses to read (`TestSecretWriteRejectsOversizedIter`).
8. **`SetVerify` protected fields must include a registered pkey.**
   `SetVerify` rejects registration otherwise, because the MAC payload only
   covers the columns you list — without a key column, moving a whole row
   (data + MAC) still verifies. Opt out with `TUniTable.VerifyUnkeyed`
   (before calling `SetVerify`) only if you accept that risk.

## 5. Adding a database / dialect

1. Add the provider to `TDriveType` and map it to a family in `dbFamilyOf`
   (`DbaEngine.go`). Most behaviour branches on `TDbFamily`, not on the provider.
2. Placeholder prefix: `getValParam` / `getSqlQuery` (`UniEngine.go`).
   Identifier quoting: `getColParam` / `quoteIdent`.
3. Metadata generators: `TExistTable4*`, `TExistField4*`, `TExistConst4*`,
   `TAutoKeys4*` in `DbaEngine.go`, wired up in `UniMeta.go` / `UniTable.AutoKeys`.
4. Native UPSERT: `upsertStmt` (`UniSave.go`); return `Ok=false` to fall back to
   the portable count-then-dispatch path.
5. **Add a real-database test.** Text-level assertions are not enough — see
   §6. A dialect whose placeholder only exists in a string comparison can pass
   every unit test and still corrupt data.

## 6. Placeholders and driver names (read this before touching them)

The generated placeholder must match the **driver**, and for SQL Server the
driver *name* changes the rules:

| `sql.Open` name | engine `ColParam` | behaviour |
| --------------- | ----------------- | --------- |
| `mssql` (deprecated but registered) | `:` / `$` / `?` | all three work: the driver rewrites them to `@pN` |
| `mssql` | `@p` | fails: `expected 0 arguments` |
| `sqlserver` | `@p` | the only working choice |
| `sqlserver` | `:` / `?` | server-side syntax error |
| `sqlserver` | `$` | **silently writes wrong data** — `$N` is a T-SQL *money literal* |

There is **no value that works for both driver names**. The developer sets
`ProvideName` to the actual driver mode. `ProviderName()` returns that value
for SQL Server, and `Initialize()` rejects an unsupported `ProvideName` or an
incompatible `ColParam` with `ErrInvalidPlaceholderBinding` before publishing
registry entries. The check performs no I/O and cannot detect a false driver
declaration; callers must keep `ProvideName` aligned with `sql.Open` or the
connector mode (`NewConnectorWithProcessQueryText` uses `ProvideName="mssql"`).
Execution entry points check the current configuration again.
Older deployments using `sqlserver` + `$` may need historical data review.

Any change to this area requires running `TestMSSQLPlaceholderContract`,
`TestMSSQLRoundTripMatrix`, and `TestMSSQLInitializeRejectsMismatchedBinding`
against a real SQL Server; a mock driver that records SQL text cannot tell
"the SQL contains `$1`" from "the parameter was bound".

## 7. Integration tests

Each family has one file and one env prefix; unreachable servers make the suite
**skip**, and `*_REQUIRED=1` turns that into a failure (used by CI).

| File | Env prefix | Notes |
| ---- | ---------- | ----- |
| `pg_test.go` | `UNIENGINE_PG_*` | `pgx`; CI provides a `postgres:16-alpine` service |
| `mssql_test.go` | `UNIENGINE_MSSQL_*` | `go-mssqldb`; covers both driver names; the silent-corruption repro is opt-in via `UNIENGINE_MSSQL_DEFECT=1` |
| `oracle_test.go` | `UNIENGINE_ORACLE_*` | `go-ora` (pure Go) by default, `godror` via `UNIENGINE_ORACLE_DRIVER` |
| `mysql_test.go` | `UNIENGINE_MYSQL_*` | `go-sql-driver/mysql`; metadata probes need the database name |

Rules for these files:

- **Only touch your own objects.** Create a clearly-named temporary table
  (`uni_it_*`) and `DROP` it via `t.Cleanup`.
- **Register `t.Cleanup` before adding table-drop cleanups**, so the drop runs
  while the connection is still open. `defer db.Close()` runs *before* every
  `t.Cleanup`, which silently leaks the table.
- Credentials come from the environment only — never commit a DSN.

## 8. Commits & pull requests

- Commit messages are Chinese, with a short prefix when it helps
  (`评审P0:`, `风格清理:`, `文档更正:`), and a body explaining *why*.
- One concern per commit. Mechanical refactors (renames, formatting) go in their
  own commit so `git log`/`blame` stay readable.
- Before opening a PR:
  - [ ] `gofmt -l .` is empty, `go vet ./...` passes in all three modules
  - [ ] `go test -race -count=1 .` passes; coverage did not drop
  - [ ] affected integration suites were run against real databases (say which)
  - [ ] `CHANGELOG.md` has an entry under the current section
  - [ ] `README.md` still matches the code (driver table, env var names, claims)
  - [ ] no credentials, internal host names or IPs added to any file

## 9. Reporting bugs

Please include: Go version, database + version, driver name and `ColParam`,
a minimal reproduction, and what you expected instead. For anything touching
placeholders, nullability or transactions, a real-database reproduction is worth
far more than a description.

---

## 中文要点

- **库本体零依赖是硬约束**：需要驱动的代码一律放 `integration/` 或 `contrib/`。
- **提交前必须全绿**：`gofmt -l .` 为空、三个模块 `go vet ./...` 通过、
  `go test -race` 通过、覆盖率不回退。
- **编码风格是有意选择**：库代码 PascalCase + `this` 接收者 + `...Ctx` 后缀；
  不要顺手"修正"成社区风格，若要改就单独一次机械式提交。
- **不要触碰这些不变量**（§4）：公共入口不 panic；形态校验用可赋值性而非 `Kind`；
  注册 API 不返回 `nil`；运行时传入的表名/字段名过白名单；引擎不得按值拷贝。
- **方言相关改动必须配真库测试**：只比对 SQL 文本无法发现「参数没绑上」这类问题
  （SQL Server 的 `$N` 就栽在这里）。集成测试只操作自建的 `uni_it_*` 临时表并清理干净。
