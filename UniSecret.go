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
	secretHeadLen = secretSaltLen + 4 + 12 //#密文头:盐 + 迭代数(4) + Nonce(12)
	//#密文内嵌迭代数的合法上限,防御损坏/恶意密文触发超高开销派生
	secretIterMax  = 10000000
	secretCacheMax = 128 //#跨进程密文可能携带任意盐;缓存必须有界
)

// #secretState 派生密钥缓存与本进程写盐
// #作为指针字段挂在引擎上:引擎按值拷贝时共享同一份缓存,且不触发 vet copylocks
type secretState struct {
	mu    sync.Mutex
	Salt  []byte            //#本进程写盐,首次加密时懒生成
	Keys  map[string][]byte //#"盐|迭代数|密钥指纹" -> AES-256 密钥
	Order []string          //#先进先出淘汰顺序,长度不超过 secretCacheMax
}

// #懒初始化守卫(仅覆盖建态窗口,不护业务临界区;与 muLazy 同一模式)
var muSecretState sync.Mutex

func (this *TUniEngine) secretStateOf() *secretState {
	muSecretState.Lock()
	defer muSecretState.Unlock()

	if this.secret == nil {
		this.secret = &secretState{Keys: make(map[string][]byte)}
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
	St := this.secretStateOf()

	St.mu.Lock()
	defer St.mu.Unlock()

	if St.Salt == nil {
		Salt := make([]byte, secretSaltLen)
		if _, eror := io.ReadFull(rand.Reader, Salt); eror != nil {
			return nil, eror
		}
		St.Salt = Salt
	}

	return St.Salt, nil
}

func (this *TUniEngine) secretCacheKey(Salt []byte, Iter int) string {
	//#以密钥指纹而非明文入键,避免在缓存映射里额外留存一份密钥明文副本
	SecretTag := sha256.Sum256([]byte(this.SecretBy))
	return string(Salt) + "|" + strconv.Itoa(Iter) + "|" + string(SecretTag[:8])
}

// cacheKeyLocked 在持有 secretState.mu 时插入并淘汰最旧条目。
func (St *secretState) cacheKeyLocked(CacheKey string, Key []byte) {
	if _, Valid := St.Keys[CacheKey]; Valid {
		return
	}
	if len(St.Order) >= secretCacheMax {
		delete(St.Keys, St.Order[0])
		St.Order[0] = ""
		St.Order = St.Order[1:]
	}
	St.Keys[CacheKey] = Key
	St.Order = append(St.Order, CacheKey)
}

// #deriveKey 用于写入:按"盐+迭代数+密钥指纹"缓存,容量有界。
// #同一冷密钥的并发派生由锁串行化,后续调用可命中缓存。
// #键必须覆盖**全部派生输入**:曾漏掉 SecretBy,导致运行时改密钥仍命中旧派生密钥,
// #轮换既不生效也不报错(见 docs/reviews/CODE_QUALITY_REVIEW_2026-10-14.md §4.6,回归测试 TestSecretRotateKeyTakesEffect)
func (this *TUniEngine) deriveKey(Salt []byte, Iter int) ([]byte, error) {
	St := this.secretStateOf()
	CacheKey := this.secretCacheKey(Salt, Iter)

	St.mu.Lock()
	defer St.mu.Unlock()

	if Key, Valid := St.Keys[CacheKey]; Valid {
		return Key, nil
	}

	Key, eror := pbkdf2.Key(sha256.New, this.SecretBy, Salt, Iter, 32)
	if eror != nil {
		return nil, eror
	}

	St.cacheKeyLocked(CacheKey, Key)

	return Key, nil
}

// deriveKeyForDecrypt 仅查已有缓存;冷派生在锁内串行执行,认证成功前不缓存。
// 这样无效密文即使使用不同盐,也不会持久占用缓存。
func (this *TUniEngine) deriveKeyForDecrypt(Salt []byte, Iter int) ([]byte, error) {
	St := this.secretStateOf()
	CacheKey := this.secretCacheKey(Salt, Iter)
	St.mu.Lock()
	defer St.mu.Unlock()
	if Key, Valid := St.Keys[CacheKey]; Valid {
		return Key, nil
	}
	return pbkdf2.Key(sha256.New, this.SecretBy, Salt, Iter, 32)
}

func (this *TUniEngine) cacheVerifiedKey(Salt []byte, Iter int, Key []byte) {
	St := this.secretStateOf()
	CacheKey := this.secretCacheKey(Salt, Iter)
	St.mu.Lock()
	defer St.mu.Unlock()
	St.cacheKeyLocked(CacheKey, Key)
}

// #GCMOpen 以给定密钥做 AES-256-GCM 解密(Data 为 Nonce+密文)
func GCMOpen(Key, Data []byte) ([]byte, error) {
	Block, eror := aes.NewCipher(Key)
	if eror != nil {
		return nil, eror
	}

	GCM, eror := cipher.NewGCM(Block)
	if eror != nil {
		return nil, eror
	}

	NonceSize := GCM.NonceSize()
	if len(Data) < NonceSize {
		return nil, errors.New("UniEngine: decrypt fail,cipher Text is too short")
	}

	PlainText, eror := GCM.Open(nil, Data[:NonceSize], Data[NonceSize:], nil)
	if eror != nil {
		return nil, fmt.Errorf("UniEngine: decrypt fail: %w", eror)
	}

	return PlainText, nil
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
		Salt, eror := this.writeSalt()
		if eror != nil {
			return "", eror
		}

		Iter := this.secretIterOf()
		//#写路径必须 fail-fast:越界迭代数会被读路径拒绝(见下方 secretIterMax 校验),
		//#容忍它只会产出"引擎自己永远读不回"的密文,且 PBKDF2 开销随迭代数线性增长
		if Iter > secretIterMax {
			return "", fmt.Errorf("UniEngine: SecretIter %d exceeds max %d; ciphertext would be unreadable", Iter, secretIterMax)
		}

		Key, eror := this.deriveKey(Salt, Iter)
		if eror != nil {
			return "", eror
		}

		Block, eror := aes.NewCipher(Key)
		if eror != nil {
			return "", eror
		}

		GCM, eror := cipher.NewGCM(Block)
		if eror != nil {
			return "", eror
		}

		Nonce := make([]byte, GCM.NonceSize())
		if _, eror = io.ReadFull(rand.Reader, Nonce); eror != nil {
			return "", eror
		}

		CipherText := GCM.Seal(nil, Nonce, []byte(Value), nil)

		Payload := make([]byte, 0, secretHeadLen+len(CipherText))
		Payload = append(Payload, Salt...)
		var IterBuf [4]byte
		binary.BigEndian.PutUint32(IterBuf[:], uint32(Iter))
		Payload = append(Payload, IterBuf[:]...)
		Payload = append(Payload, Nonce...)
		Payload = append(Payload, CipherText...)

		return UniSecretTag + base64.StdEncoding.EncodeToString(Payload), nil
	}

	//#存量数据鉴别:无标签的值视为存量明文,直通返回(不尝试解密)
	if !this.IsEncrypted(Value) {
		return Value, nil
	}

	//#密文:盐与迭代数内嵌,读取自包含(跨进程/跨密文均可解)
	Data, eror := base64.StdEncoding.DecodeString(strings.TrimPrefix(Value, UniSecretTag))
	if eror != nil {
		return "", fmt.Errorf("UniEngine: decrypt fail: %w", eror)
	}
	if len(Data) < secretHeadLen {
		return "", errors.New("UniEngine: decrypt fail,cipher Text is too short")
	}

	Salt := Data[:secretSaltLen]
	Iter := int(binary.BigEndian.Uint32(Data[secretSaltLen : secretSaltLen+4]))
	if Iter <= 0 || Iter > secretIterMax {
		return "", errors.New("UniEngine: decrypt fail,invalid iterations in cipher Text")
	}

	Key, eror := this.deriveKeyForDecrypt(Salt, Iter)
	if eror != nil {
		return "", eror
	}

	PlainText, eror := GCMOpen(Key, Data[secretSaltLen+4:])
	if eror != nil {
		return "", eror
	}
	this.cacheVerifiedKey(Salt, Iter, Key)

	return string(PlainText), nil
}

// #查询结果解密:把标记了 secreton 的字段,从密文还原为明文
func (this *TUniEngine) DecryptResult(Result *reflect.Value, UniTable *TUniTable, Column []string) error {
	if this.SecretOn == 0 {
		return nil
	}

	for _, ItemPara := range Column {
		UniField, Valid := UniTable.HashField[strings.ToLower(ItemPara)]
		if !Valid || !UniField.SecretOn {
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
// #EncryIdx 由 selectRowsCtx 在行循环前一次性算好)
func (this *TUniEngine) decryptRow(Result reflect.Value, EncryIdx []int) error {
	if this.SecretOn == 0 {
		return nil
	}

	for _, Idx := range EncryIdx {
		FieldValue := Result.Field(Idx)
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

// #加密写入值:secreton 仅支持 string 字段,非 string 字段直接报错
// #(读取侧 DecryptResult 只还原 string 字段;写入侧若放行其它类型,会出现写入密文/读取不解密的不对称,故 fast-fail)
func (this *TUniEngine) secretEncrypt(FieldValue reflect.Value) (interface{}, error) {
	if !FieldValue.IsValid() || FieldValue.Kind() != reflect.String {
		return nil, fmt.Errorf("UniEngine: secreton only supports string field, but got [%s]", FieldValue.Kind().String())
	}

	return this.Secret(FieldValue.String(), true)
}
