package aibot

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	// PKCS7BlockSize 企业微信加解密使用的 PKCS#7 块大小
	PKCS7BlockSize = 32
	// AESKeyLength AES-256 密钥长度
	AESKeyLength = 32
)

// DecodeEncodingAESKey 解码企业微信提供的 Base64 encodingAESKey
func DecodeEncodingAESKey(encodingAESKey string) ([]byte, error) {
	trimmed := strings.TrimSpace(encodingAESKey)
	if trimmed == "" {
		return nil, errors.New("encodingAESKey missing")
	}
	withPadding := trimmed
	if !strings.HasSuffix(trimmed, "=") {
		withPadding = trimmed + "="
	}
	key, err := base64.StdEncoding.DecodeString(withPadding)
	if err != nil {
		return nil, fmt.Errorf("invalid encodingAESKey: %w", err)
	}
	if len(key) != AESKeyLength {
		return nil, fmt.Errorf("invalid encodingAESKey (expected %d bytes, got %d)", AESKeyLength, len(key))
	}
	return key, nil
}

// PKCS7Pad PKCS#7 填充
func PKCS7Pad(data []byte, blockSize int) []byte {
	mod := len(data) % blockSize
	pad := blockSize - mod
	if mod == 0 {
		pad = blockSize
	}
	padded := make([]byte, len(data)+pad)
	copy(padded, data)
	for i := len(data); i < len(padded); i++ {
		padded[i] = byte(pad)
	}
	return padded
}

// PKCS7Unpad PKCS#7 解除填充
func PKCS7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("invalid pkcs7 payload")
	}
	pad := int(data[len(data)-1])
	if pad < 1 || pad > blockSize {
		return nil, errors.New("invalid pkcs7 padding value")
	}
	if pad > len(data) {
		return nil, errors.New("invalid pkcs7 payload length")
	}
	for i := 0; i < pad; i++ {
		if data[len(data)-1-i] != byte(pad) {
			return nil, errors.New("invalid pkcs7 padding byte")
		}
	}
	return data[:len(data)-pad], nil
}

// WecomCrypto 企业微信加解密工具
//
// 独立于 Webhook、WebSocket、Agent 的具体协议形态，统一提供基于 AES-256-CBC
// 的加解密与 SHA1 签名计算能力。
type WecomCrypto struct {
	token     string
	aesKey    []byte
	iv        []byte
	receiveID string
}

// NewWecomCrypto 创建 WecomCrypto 实例
//
//	token         - 企业微信后台配置的 Token
//	encodingAESKey - 企业微信后台配置的 EncodingAESKey
//	receiveId      - 对应企业微信的 corpId 或 botId（用于校验与追加）
func NewWecomCrypto(token, encodingAESKey, receiveID string) (*WecomCrypto, error) {
	if token == "" {
		return nil, errors.New("token is required")
	}
	aesKey, err := DecodeEncodingAESKey(encodingAESKey)
	if err != nil {
		return nil, err
	}
	return &WecomCrypto{
		token:     token,
		aesKey:    aesKey,
		iv:        aesKey[:aes.BlockSize],
		receiveID: receiveID,
	}, nil
}

// ComputeSignature 计算 WeCom 消息签名
func (c *WecomCrypto) ComputeSignature(timestamp, nonce, encrypt string) string {
	parts := []string{c.token, timestamp, nonce, encrypt}
	sort.Strings(parts)
	s := sha1.Sum([]byte(strings.Join(parts, "")))
	return fmt.Sprintf("%x", s)
}

// VerifySignature 验证 WeCom 消息签名
func (c *WecomCrypto) VerifySignature(signature, timestamp, nonce, encrypt string) bool {
	expected := c.ComputeSignature(timestamp, nonce, encrypt)
	return expected == signature
}

// Decrypt 消息解密
//
// 返回纯文本字符串（XML 或 JSON 根据上层业务而定）
func (c *WecomCrypto) Decrypt(encryptText string) (string, error) {
	block, err := aes.NewCipher(c.aesKey)
	if err != nil {
		return "", fmt.Errorf("failed to create AES cipher: %w", err)
	}

	encryptedData, err := base64.StdEncoding.DecodeString(encryptText)
	if err != nil {
		return "", fmt.Errorf("failed to decode encryptText: %w", err)
	}

	// CBC 要求密文长度为块大小（16 字节）的整数倍，否则 CryptBlocks 直接 panic
	if len(encryptedData) == 0 || len(encryptedData)%aes.BlockSize != 0 {
		return "", fmt.Errorf("invalid ciphertext length (%d bytes); must be a non-zero multiple of %d", len(encryptedData), aes.BlockSize)
	}

	mode := cipher.NewCBCDecrypter(block, c.iv)
	decryptedPadded := make([]byte, len(encryptedData))
	mode.CryptBlocks(decryptedPadded, encryptedData)

	decrypted, err := PKCS7Unpad(decryptedPadded, PKCS7BlockSize)
	if err != nil {
		return "", fmt.Errorf("pkcs7 unpad failed: %w", err)
	}

	if len(decrypted) < 20 {
		return "", fmt.Errorf("invalid payload (expected >=20 bytes, got %d)", len(decrypted))
	}

	// 16 bytes random + 4 bytes length + msg + receiveId
	msgLen := binary.BigEndian.Uint32(decrypted[16:20])
	msgStart := 20
	msgEnd := msgStart + int(msgLen)
	if msgEnd > len(decrypted) {
		return "", fmt.Errorf("invalid msg length (msgEnd=%d, total=%d)", msgEnd, len(decrypted))
	}
	msg := string(decrypted[msgStart:msgEnd])

	if c.receiveID != "" {
		trailing := string(decrypted[msgEnd:])
		if trailing != c.receiveID {
			return "", fmt.Errorf("receiveId mismatch (expected \"%s\", got \"%s\")", c.receiveID, trailing)
		}
	}

	return msg, nil
}

// Encrypt 消息加密
//
// 加密明文并返回 base64 格式密文与对应的新签名
func (c *WecomCrypto) Encrypt(plainText, timestamp, nonce string) (encrypt, signature string, err error) {
	random16 := make([]byte, 16)
	if _, err := rand.Read(random16); err != nil {
		return "", "", fmt.Errorf("failed to generate random bytes: %w", err)
	}

	msgBuf := []byte(plainText)
	msgLen := make([]byte, 4)
	binary.BigEndian.PutUint32(msgLen, uint32(len(msgBuf)))
	receiveIDBuf := []byte(c.receiveID)

	raw := make([]byte, 0, len(random16)+len(msgLen)+len(msgBuf)+len(receiveIDBuf))
	raw = append(raw, random16...)
	raw = append(raw, msgLen...)
	raw = append(raw, msgBuf...)
	raw = append(raw, receiveIDBuf...)

	padded := PKCS7Pad(raw, PKCS7BlockSize)

	block, err := aes.NewCipher(c.aesKey)
	if err != nil {
		return "", "", fmt.Errorf("failed to create AES cipher: %w", err)
	}

	mode := cipher.NewCBCEncrypter(block, c.iv)
	encryptedBuf := make([]byte, len(padded))
	mode.CryptBlocks(encryptedBuf, padded)

	encryptBase64 := base64.StdEncoding.EncodeToString(encryptedBuf)
	signature = c.ComputeSignature(timestamp, nonce, encryptBase64)

	return encryptBase64, signature, nil
}
