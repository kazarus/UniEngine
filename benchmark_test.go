// benchmark_test
// #基准测试:行扫描热路径与内置加解密
// #运行:go test -bench . -benchmem -run '^$'
package UniEngine

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"io"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

const (
	benchColumns = 10   //#列数,模拟典型宽表
	benchRows    = 1000 //#每次查询的行数
)

// #benchWideRow 全 string 的 10 列宽表
type benchWideRow struct {
	F1  string `db:"f1"`
	F2  string `db:"f2"`
	F3  string `db:"f3"`
	F4  string `db:"f4"`
	F5  string `db:"f5"`
	F6  string `db:"f6"`
	F7  string `db:"f7"`
	F8  string `db:"f8"`
	F9  string `db:"f9"`
	F10 string `db:"f10"`
}

// #benchSecretRow 同宽表,末列标记 encrypt(行扫描+解密联合路径用)
type benchSecretRow struct {
	F1  string `db:"f1"`
	F2  string `db:"f2"`
	F3  string `db:"f3"`
	F4  string `db:"f4"`
	F5  string `db:"f5"`
	F6  string `db:"f6"`
	F7  string `db:"f7"`
	F8  string `db:"f8"`
	F9  string `db:"f9"`
	F10 string `db:"f10,encrypt"`
}

// #newBenchEngine 构造带预置查询结果的引擎(secret=true 时末列预置密文)
func newBenchEngine(b *testing.B, secret bool) *TUniEngine {

	b.Helper()

	columns := make([]string, benchColumns)
	for i := range columns {
		columns[i] = "f" + strconv.Itoa(i+1)
	}

	queryRows := make([][]driver.Value, benchRows)
	for i := range queryRows {
		row := make([]driver.Value, benchColumns)
		for j := range row {
			row[j] = "value-" + strconv.Itoa(j)
		}
		queryRows[i] = row
	}

	d := &mockDriver{columns: columns, queryRows: queryRows}
	eng := &TUniEngine{Db: sql.OpenDB(mockConnector{d: d}), ColLabel: "db", ColParam: "$", Provider: DtPOSTGR}
	eng.Initialize()

	if secret {
		eng.SecretOn = 1
		eng.SecretBy = "bench-secret-key"
		eng.RegisterClass(benchSecretRow{}, "bench_secret")

		//#末列预置密文,读取路径才真实走解密
		cipherText, eror := eng.Secret("value-9", true)
		if eror != nil {
			b.Fatal(eror)
		}
		for _, row := range queryRows {
			row[benchColumns-1] = cipherText
		}
	} else {
		eng.RegisterClass(benchWideRow{}, "bench_wide")
	}

	return eng
}

// ---------------------------------------------------------------------------
// 行扫描:现行预解析路径 vs 旧算法复刻
// ---------------------------------------------------------------------------

// #现行路径:列→字段下标查询前一次预解析,行循环内 Field(idx) 直取
func BenchmarkSelectLScan(b *testing.B) {

	eng := newBenchEngine(b, false)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		var out []benchWideRow
		if eror := eng.SelectL(&out, "select * from bench_wide"); eror != nil {
			b.Fatal(eror)
		}
		if len(out) != benchRows {
			b.Fatalf("expect %d rows, got %d", benchRows, len(out))
		}
	}
}

// #旧算法复刻(优化前行为):每行每列做 HashField 查找 + FieldByName 线性扫描。
// #与上一基准走同一 mock 传输(database/sql 语句/行机制完全相同),
// #两者的差值即列下标预解析这一优化的净收益
func BenchmarkSelectLScanLegacy(b *testing.B) {

	eng := newBenchEngine(b, false)
	db := eng.Db
	typ := reflect.TypeOf(benchWideRow{})
	table := eng.GetTable("bench_wide")

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {

		st, eror := db.Prepare("select * from bench_wide")
		if eror != nil {
			b.Fatal(eror)
		}
		rows, eror := st.Query()
		if eror != nil {
			b.Fatal(eror)
		}
		columns, eror := rows.Columns()
		if eror != nil {
			b.Fatal(eror)
		}

		result := reflect.MakeSlice(reflect.SliceOf(typ), 0, 0)

		for rows.Next() {

			u := reflect.New(typ)
			elem := u.Elem()

			values := make([]interface{}, len(columns))
			for ci, col := range columns {
				field, Valid := table.HashField[strings.ToLower(col)]
				if !Valid {
					b.Fatal("unregistered column " + col)
				}
				values[ci] = elem.FieldByName(field.AttriName).Addr().Interface()
			}

			if eror = rows.Scan(values...); eror != nil {
				b.Fatal(eror)
			}

			result = reflect.Append(result, u.Elem())
		}
		if eror = rows.Err(); eror != nil {
			b.Fatal(eror)
		}
		rows.Close()
		st.Close()

		if result.Len() != benchRows {
			b.Fatalf("expect %d rows, got %d", benchRows, result.Len())
		}
	}
}

// #联合路径:1000行×10列扫描,每行解密1个加密列(密钥派生已缓存)
func BenchmarkSelectLScanDecrypt(b *testing.B) {

	eng := newBenchEngine(b, true)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		var out []benchSecretRow
		if eror := eng.SelectL(&out, "select * from bench_secret"); eror != nil {
			b.Fatal(eror)
		}
		if len(out) != benchRows {
			b.Fatalf("expect %d rows, got %d", benchRows, len(out))
		}
	}
}

// ---------------------------------------------------------------------------
// 内置加解密
// ---------------------------------------------------------------------------

func benchmarkSecretEncrypt(b *testing.B, value string) {

	eng := &TUniEngine{SecretOn: 1, SecretBy: "bench-secret-key"}

	//#预热:生成写盐并把派生密钥写入缓存,度量的是稳定态(AES-GCM+base64)开销
	if _, eror := eng.Secret("warmup", true); eror != nil {
		b.Fatal(eror)
	}

	b.SetBytes(int64(len(value)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if _, eror := eng.Secret(value, true); eror != nil {
			b.Fatal(eror)
		}
	}
}

func benchmarkSecretDecrypt(b *testing.B, value string) {

	eng := &TUniEngine{SecretOn: 1, SecretBy: "bench-secret-key"}

	cipherText, eror := eng.Secret(value, true)
	if eror != nil {
		b.Fatal(eror)
	}

	b.SetBytes(int64(len(value)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		plain, eror := eng.Secret(cipherText, false)
		if eror != nil {
			b.Fatal(eror)
		}
		if plain != value {
			b.Fatal("round trip mismatch")
		}
	}
}

// #现行格式 ENC2:稳定态加/解密(派生密钥缓存命中),按值长度分档
func BenchmarkSecretEncrypt(b *testing.B) {
	for _, size := range []struct {
		name string
		n    int
	}{{"16B", 16}, {"256B", 256}, {"4KB", 4096}} {
		b.Run(size.name, func(b *testing.B) {
			benchmarkSecretEncrypt(b, strings.Repeat("v", size.n))
		})
	}
}

func BenchmarkSecretDecrypt(b *testing.B) {
	for _, size := range []struct {
		name string
		n    int
	}{{"16B", 16}, {"256B", 256}, {"4KB", 4096}} {
		b.Run(size.name, func(b *testing.B) {
			benchmarkSecretDecrypt(b, strings.Repeat("v", size.n))
		})
	}
}

// #benchLegacyCipherText 按旧算法(单次SHA-256派生)构造 ENC: 密文
func benchLegacyCipherText(b *testing.B, secretBy, plain string) string {

	b.Helper()

	sum := sha256.Sum256([]byte(secretBy))
	block, eror := aes.NewCipher(sum[:])
	if eror != nil {
		b.Fatal(eror)
	}
	gcm, eror := cipher.NewGCM(block)
	if eror != nil {
		b.Fatal(eror)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, eror = io.ReadFull(rand.Reader, nonce); eror != nil {
		b.Fatal(eror)
	}
	ct := gcm.Seal(nil, nonce, []byte(plain), nil)

	return UniSecretTag + base64.StdEncoding.EncodeToString(append(nonce, ct...))
}

// #旧格式 ENC: 读取路径(每次调用重建 cipher,无缓存)
func BenchmarkSecretDecryptLegacyENC(b *testing.B) {

	eng := &TUniEngine{SecretOn: 1, SecretBy: "bench-secret-key"}
	legacy := benchLegacyCipherText(b, "bench-secret-key", strings.Repeat("v", 16))

	b.SetBytes(16)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if _, eror := eng.Secret(legacy, false); eror != nil {
			b.Fatal(eror)
		}
	}
}

// #存量明文直通(仅前缀鉴别,不解密)
func BenchmarkSecretDecryptPlaintext(b *testing.B) {

	eng := &TUniEngine{SecretOn: 1, SecretBy: "bench-secret-key"}
	value := strings.Repeat("v", 16)

	b.SetBytes(16)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if _, eror := eng.Secret(value, false); eror != nil {
			b.Fatal(eror)
		}
	}
}

// #冷派生:每次迭代换新盐绕开缓存,度量一次性 PBKDF2(缺省600000次)的真实开销;
// #生产中每进程每份盐只付一次
func BenchmarkSecretPBKDF2DeriveCold(b *testing.B) {

	eng := &TUniEngine{SecretOn: 1, SecretBy: "bench-secret-key"}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if _, eror := eng.deriveKey([]byte("salt-"+strconv.Itoa(i)), UniSecretIter); eror != nil {
			b.Fatal(eror)
		}
	}
}
