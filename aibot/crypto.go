package aibot

import (
	"crypto/aes"
	"crypto/cipher"
	"errors"
)

// DecryptFile 使用 AES-256-CBC 解密文件
//
//	encryptedData - 加密的文件数据
//	aesKey       - Base64 编码的 AES-256 密钥（43字符的 Base64 字符串，可能带或不带 padding）
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

	// 复用 DecodeEncodingAESKey：兼容 43 字符（缺 padding）与 44 字符（带 padding）两种输入
	key, err := DecodeEncodingAESKey(aesKey)
	if err != nil {
		return nil, errors.New("decryptFile: " + err.Error())
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

// pkcs7Unpadding 移除 PKCS#7 填充（内部复用 PKCS7Unpad）
func pkcs7Unpadding(plaintext []byte, blockSize int) ([]byte, error) {
	return PKCS7Unpad(plaintext, blockSize)
}
