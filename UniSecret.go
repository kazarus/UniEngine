// UniSecret
// #应用加密
package UniEngine

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"sync"
)

// #应用加密钩子:Encrypt=true 加密,Encrypt=false 解密
// #应用可以实现自己的加密算法(如国密/业务密钥/对接加密机),
// #然后赋值给 TUniEngine.SecretHook,即可接管全部敏感字段的加解密;
type TSecretHook func(Value string, Encrypt bool) (string, error)

const (
	// #密文标签:PBKDF2 派生密钥,盐与迭代数内嵌密文,读取自包含
	UniSecretTag = "ENC:"
	// #PBKDF2-HMAC-SHA256 缺省迭代次数(OWASP 2023 建议值);
	// #派生密钥带缓存,每份"盐+迭代数"每进程只付一次派生开销
	UniSecretIter = 600000

	secretSaltLen = 16                     //#写盐长度(字节)
	secretHeadLen = secretSaltLen + 4 + 12 //#密文头:盐 + 迭代数(4) + nonce(12)
	//#密文内嵌迭代数的合法上限,防御损坏/恶意密文触发超高开销派生
	secretIterMax = 10000000
)

// #secretState 派生密钥缓存与本进程写盐
// #作为指针字段挂在引擎上:引擎按值拷贝时共享同一份缓存,且不触发 vet copylocks
type secretState struct {
	mu   sync.Mutex
	salt []byte            //#本进程写盐,首次加密时懒生成
	keys map[string][]byte //#"盐|迭代数" -> AES-256 密钥
}

// #懒初始化守卫(仅覆盖建态窗口,不护业务临界区;与 muLazy 同一模式)
var muSecretState sync.Mutex

func (this *TUniEngine) secretStateOf() *secretState {

	muSecretState.Lock()
	defer muSecretState.Unlock()

	if this.secret == nil {
		this.secret = &secretState{keys: make(map[string][]byte)}
	}

	return this.secret
}

// #生效的迭代次数(<=0 用缺省)
func (this *TUniEngine) secretIterOf() int {

	if this.SecretIter <= 0 {
		return UniSecretIter
	}

	return this.SecretIter
}

// #writeSalt 本进程写盐(懒生成;本进程所有新密文共用,读取侧从密文内嵌盐恢复)
func (this *TUniEngine) writeSalt() ([]byte, error) {

	st := this.secretStateOf()

	st.mu.Lock()
	defer st.mu.Unlock()

	if st.salt == nil {
		salt := make([]byte, secretSaltLen)
		if _, eror := io.ReadFull(rand.Reader, salt); eror != nil {
			return nil, eror
		}
		st.salt = salt
	}

	return st.salt, nil
}

// #deriveKey PBKDF2-HMAC-SHA256 派生 AES-256 密钥;按"盐+迭代数"缓存。
// #首个到达的调用持锁派生,并发等待者随后直接命中缓存(避免惊群重复派生);
// #密钥常驻内存是该形态加密的标准代价,与任何进程内密钥持有等价
func (this *TUniEngine) deriveKey(salt []byte, iter int) ([]byte, error) {

	st := this.secretStateOf()

	cacheKey := string(salt) + "|" + strconv.Itoa(iter)

	st.mu.Lock()
	defer st.mu.Unlock()

	if key, Valid := st.keys[cacheKey]; Valid {
		return key, nil
	}

	key, eror := pbkdf2.Key(sha256.New, this.SecretBy, salt, iter, 32)
	if eror != nil {
		return nil, eror
	}

	st.keys[cacheKey] = key

	return key, nil
}

// #gcmOpen 以给定密钥做 AES-256-GCM 解密(data 为 nonce+密文)
func gcmOpen(key, data []byte) ([]byte, error) {

	block, eror := aes.NewCipher(key)
	if eror != nil {
		return nil, eror
	}

	gcm, eror := cipher.NewGCM(block)
	if eror != nil {
		return nil, eror
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return nil, errors.New("UniEngine: decrypt fail,cipher text is too short")
	}

	plainText, eror := gcm.Open(nil, data[:nonceSize], data[nonceSize:], nil)
	if eror != nil {
		return nil, fmt.Errorf("UniEngine: decrypt fail: %w", eror)
	}

	return plainText, nil
}

// #加密入口:SecretOn=0 时直接返回原值(不加密/不解密)
// #应用钩子 SecretHook 为空时,使用内置实现(PBKDF2 派生密钥 + AES-256-GCM)
func (this *TUniEngine) Secret(Value string, Encrypt bool) (string, error) {

	if this.SecretOn == 0 {
		return Value, nil
	}

	if this.SecretHook != nil {
		return this.SecretHook(Value, Encrypt)
	}

	return this.SecretDefault(Value, Encrypt)
}

// #判断值是否为加密密文(带加密标签)#用于存量数据迁移脚本
func (this *TUniEngine) IsEncrypted(Value string) bool {
	return strings.HasPrefix(Value, UniSecretTag)
}

// #内置默认实现:
// #密文格式 ENC: + base64( 盐(16) + 迭代数(4,大端) + 随机nonce + GCM密文 ),
// #  密钥 = PBKDF2-HMAC-SHA256(SecretBy, 盐, 迭代数);盐与迭代数内嵌密文,读取自包含,
// #  同一明文两次加密结果不同(随机nonce),离线暴力破解需按盐逐份进行;
// #无标签的值视为存量明文,直通返回(不尝试解密)
func (this *TUniEngine) SecretDefault(Value string, Encrypt bool) (string, error) {

	if this.SecretBy == "" {
		return "", errors.New("UniEngine: SecretOn is on, but SecretBy is empty")
	}

	if Encrypt {

		salt, eror := this.writeSalt()
		if eror != nil {
			return "", eror
		}

		iter := this.secretIterOf()

		key, eror := this.deriveKey(salt, iter)
		if eror != nil {
			return "", eror
		}

		block, eror := aes.NewCipher(key)
		if eror != nil {
			return "", eror
		}

		gcm, eror := cipher.NewGCM(block)
		if eror != nil {
			return "", eror
		}

		nonce := make([]byte, gcm.NonceSize())
		if _, eror = io.ReadFull(rand.Reader, nonce); eror != nil {
			return "", eror
		}

		cipherText := gcm.Seal(nil, nonce, []byte(Value), nil)

		payload := make([]byte, 0, secretHeadLen+len(cipherText))
		payload = append(payload, salt...)
		var iterBuf [4]byte
		binary.BigEndian.PutUint32(iterBuf[:], uint32(iter))
		payload = append(payload, iterBuf[:]...)
		payload = append(payload, nonce...)
		payload = append(payload, cipherText...)

		return UniSecretTag + base64.StdEncoding.EncodeToString(payload), nil
	}

	//#存量数据鉴别:无标签的值视为存量明文,直通返回(不尝试解密)
	if !this.IsEncrypted(Value) {
		return Value, nil
	}

	//#密文:盐与迭代数内嵌,读取自包含(跨进程/跨密文均可解)
	data, eror := base64.StdEncoding.DecodeString(strings.TrimPrefix(Value, UniSecretTag))
	if eror != nil {
		return "", fmt.Errorf("UniEngine: decrypt fail: %w", eror)
	}
	if len(data) < secretHeadLen {
		return "", errors.New("UniEngine: decrypt fail,cipher text is too short")
	}

	salt := data[:secretSaltLen]
	iter := int(binary.BigEndian.Uint32(data[secretSaltLen : secretSaltLen+4]))
	if iter <= 0 || iter > secretIterMax {
		return "", errors.New("UniEngine: decrypt fail,invalid iterations in cipher text")
	}

	key, eror := this.deriveKey(salt, iter)
	if eror != nil {
		return "", eror
	}

	plainText, eror := gcmOpen(key, data[secretSaltLen+4:])
	if eror != nil {
		return "", eror
	}

	return string(plainText), nil
}

// #查询结果解密:把标记了 encrypt 的字段,从密文还原为明文
func (this *TUniEngine) DecryptResult(Result *reflect.Value, UniTable *TUniTable, column []string) error {

	if this.SecretOn == 0 {
		return nil
	}

	for _, ItemPara := range column {

		UniField, Valid := UniTable.HashField[strings.ToLower(ItemPara)]
		if !Valid || !UniField.Encrypt {
			continue
		}

		FieldValue := Result.FieldByName(UniField.AttriName)
		if !FieldValue.IsValid() || FieldValue.Kind() != reflect.String {
			continue
		}

		PlainText, eror := this.Secret(FieldValue.String(), false)
		if eror != nil {
			return eror
		}

		FieldValue.SetString(PlainText)
	}

	return nil
}

// #decryptRow 按预解析的字段下标解密(热路径;DecryptResult 的无查找版本,
// #encryptIdx 由 queryRowsCtx 在行循环前一次性算好)
func (this *TUniEngine) decryptRow(Result reflect.Value, encryptIdx []int) error {

	if this.SecretOn == 0 {
		return nil
	}

	for _, idx := range encryptIdx {

		FieldValue := Result.Field(idx)
		if FieldValue.Kind() != reflect.String {
			continue
		}

		PlainText, eror := this.Secret(FieldValue.String(), false)
		if eror != nil {
			return eror
		}

		FieldValue.SetString(PlainText)
	}

	return nil
}

// #加密写入值:encrypt 仅支持 string 字段,非 string 字段直接报错
// #(读取侧 DecryptResult 只还原 string 字段;写入侧若放行其它类型,会出现写入密文/读取不解密的不对称,故 fast-fail)
func (this *TUniEngine) secretEncrypt(FieldValue reflect.Value) (interface{}, error) {

	if !FieldValue.IsValid() || FieldValue.Kind() != reflect.String {
		return nil, fmt.Errorf("UniEngine: encrypt only supports string field, but got [%s]", FieldValue.Kind().String())
	}

	return this.Secret(FieldValue.String(), true)
}
