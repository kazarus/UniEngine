// UniSecret
// #应用加密
package UniEngine

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
)

// #应用加密钩子:Encrypt=true 加密,Encrypt=false 解密
// #应用可以实现自己的加密算法(如国密/业务密钥/对接加密机),
// #然后赋值给 TUniEngine.SecretHook,即可接管全部敏感字段的加解密;
type TSecretHook func(Value string, Encrypt bool) (string, error)

// #加密入口:SecretOn=0 时直接返回原值(不加密/不解密)
// #应用钩子 SecretHook 为空时,使用内置 AES-256-GCM(密钥取 SecretBy 的 SHA-256)
func (self *TUniEngine) Secret(Value string, Encrypt bool) (string, error) {

	if self.SecretOn == 0 {
		return Value, nil
	}

	if self.SecretHook != nil {
		return self.SecretHook(Value, Encrypt)
	}

	return self.SecretDefault(Value, Encrypt)
}

// #内置默认实现:AES-256-GCM
// #密文格式:base64( 随机nonce + GCM密文 ) ;随机nonce,同一明文两次加密结果不同;
func (self *TUniEngine) SecretDefault(Value string, Encrypt bool) (string, error) {

	if self.SecretBy == "" {
		return "", errors.New("UniEngine: SecretOn is on, but SecretBy is empty")
	}

	sum := sha256.Sum256([]byte(self.SecretBy))
	block, eror := aes.NewCipher(sum[:])
	if eror != nil {
		return "", eror
	}

	gcm, eror := cipher.NewGCM(block)
	if eror != nil {
		return "", eror
	}

	switch Encrypt {
	case true:
		{
			nonce := make([]byte, gcm.NonceSize())
			if _, eror = io.ReadFull(rand.Reader, nonce); eror != nil {
				return "", eror
			}

			cipherText := gcm.Seal(nil, nonce, []byte(Value), nil)
			return base64.StdEncoding.EncodeToString(append(nonce, cipherText...)), nil
		}
	default:
		{
			data, eror := base64.StdEncoding.DecodeString(Value)
			if eror != nil {
				return "", fmt.Errorf("UniEngine: decrypt fail,%s", eror.Error())
			}

			nonceSize := gcm.NonceSize()
			if len(data) < nonceSize {
				return "", errors.New("UniEngine: decrypt fail,cipher text is too short")
			}

			plainText, eror := gcm.Open(nil, data[:nonceSize], data[nonceSize:], nil)
			if eror != nil {
				return "", fmt.Errorf("UniEngine: decrypt fail,%s", eror.Error())
			}

			return string(plainText), nil
		}
	}
}

// #查询结果解密:把标记了 encrypt 的字段,从密文还原为明文
func (self *TUniEngine) DecryptResult(Result *reflect.Value, UniTable *TUniTable, column []string) error {

	if self.SecretOn == 0 {
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

		PlainText, eror := self.Secret(FieldValue.String(), false)
		if eror != nil {
			return eror
		}

		FieldValue.SetString(PlainText)
	}

	return nil
}
