package aibot

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"errors"
)

// DecryptFile 使用 AES-256-CBC 解密文件
//
//	encryptedData - 加密的文件数据
//	aesKey       - Base64 编码的 AES-256 密钥（43字符的 Base64 字符串，需要添加 padding）
//
// 返回解密后的文件数据
func DecryptFile(encryptedData []byte, aesKey string) ([]byte, error) {
	// 参数验证
	if len(encryptedData) == 0 {
		return nil, errors.New("decryptFile: encryptedData is empty or not provided")
	}

	if aesKey == "" {
		return nil, errors.New("decryptFile: aesKey must be a non-empty string")
	}

	// 添加 padding（encodingAESKey 是 43 字符的 Base64，解码后 32 字节）
	aesKeyWithPadding := aesKey + "="

	// 将 Base64 编码的 aesKey 解码
	key, err := base64.StdEncoding.DecodeString(aesKeyWithPadding)
	if err != nil {
		return nil, errors.New("decryptFile: failed to decode aesKey from base64: " + err.Error())
	}

	// 密钥必须是 32 字节 (AES-256)
	if len(key) != 32 {
		return nil, errors.New("decryptFile: aesKey must be 32 bytes (256 bits) after base64 decoding")
	}

	// 创建 AES 块
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("decryptFile: failed to create AES cipher: " + err.Error())
	}

	// block size 是 32（因为 key 是 32 字节）
	const blockSize = 32
	if len(encryptedData)%blockSize != 0 {
		return nil, errors.New("decryptFile: encrypted data length is not a multiple of block size")
	}

	// IV 取 key 的前 16 字节
	iv := key[:aes.BlockSize]

	// 创建 CBC 解密器
	mode := cipher.NewCBCDecrypter(block, iv)

	// 解密（就地操作）
	decrypted := make([]byte, len(encryptedData))
	copy(decrypted, encryptedData)
	mode.CryptBlocks(decrypted, decrypted)

	// 移除 PKCS#7 填充（blockSize = 32）
	plaintext, err := pkcs7Unpadding(decrypted, blockSize)
	if err != nil {
		return nil, errors.New("decryptFile: " + err.Error())
	}

	return plaintext, nil
}

// pkcs7Unpadding 移除 PKCS#7 填充
func pkcs7Unpadding(plaintext []byte, blockSize int) ([]byte, error) {
	plaintextLen := len(plaintext)
	if plaintextLen == 0 {
		return nil, errors.New("pkcs7Unpadding error: nil or zero")
	}
	if plaintextLen%blockSize != 0 {
		return nil, errors.New("pkcs7Unpadding text not a multiple of the block size")
	}
	paddingLen := int(plaintext[plaintextLen-1])
	return plaintext[:plaintextLen-paddingLen], nil
}
