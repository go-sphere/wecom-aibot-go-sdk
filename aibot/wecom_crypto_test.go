package aibot

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"strings"
	"testing"
)

func TestWecomCrypto_RoundTrip(t *testing.T) {
	encodingAESKey := "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"
	token := "QDG6eK"
	crypto, err := NewWecomCrypto(token, encodingAESKey, "")
	if err != nil {
		t.Fatalf("NewWecomCrypto failed: %v", err)
	}

	plaintext := `{"hello":"world"}`
	encrypt, signature, err := crypto.Encrypt(plaintext, "123", "456")
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	if !crypto.VerifySignature(signature, "123", "456", encrypt) {
		t.Fatal("VerifySignature failed")
	}

	decrypted, err := crypto.Decrypt(encrypt)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}

	if decrypted != plaintext {
		t.Fatalf("Round-trip mismatch: got %q, want %q", decrypted, plaintext)
	}
}

func TestWecomCrypto_PadMultipleOfBlockSize(t *testing.T) {
	encodingAESKey := "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"
	token := "QDG6eK"
	crypto, err := NewWecomCrypto(token, encodingAESKey, "")
	if err != nil {
		t.Fatalf("NewWecomCrypto failed: %v", err)
	}

	// 12 bytes plaintext，PKCS7 pad 后应增加 20 bytes（凑满 32）
	plaintext := "xxxxxxxxxxxx"
	encrypt, _, err := crypto.Encrypt(plaintext, "123", "456")
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	decrypted, err := crypto.Decrypt(encrypt)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}

	if decrypted != plaintext {
		t.Fatalf("Decrypted mismatch: got %q, want %q", decrypted, plaintext)
	}
}

func TestWecomCrypto_ComputeSignature(t *testing.T) {
	encodingAESKey := "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"
	token := "QDG6eK"
	crypto, err := NewWecomCrypto(token, encodingAESKey, "")
	if err != nil {
		t.Fatalf("NewWecomCrypto failed: %v", err)
	}

	sig := crypto.ComputeSignature("123", "456", "ENCRYPT")
	if len(sig) != 40 {
		t.Fatalf("Expected SHA1 hex length 40, got %d", len(sig))
	}

	for _, c := range sig {
		if (c < 'a' || c > 'f') && (c < '0' || c > '9') {
			t.Fatalf("Invalid hex character in signature: %c", c)
		}
	}
}

func TestDecodeEncodingAESKey(t *testing.T) {
	// 43 chars Base64 string (without padding)
	key, err := DecodeEncodingAESKey("abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG")
	if err != nil {
		t.Fatalf("DecodeEncodingAESKey failed: %v", err)
	}
	if len(key) != 32 {
		t.Fatalf("Expected 32 bytes, got %d", len(key))
	}

	// With padding
	key2, err := DecodeEncodingAESKey("abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG=")
	if err != nil {
		t.Fatalf("DecodeEncodingAESKey with padding failed: %v", err)
	}
	if len(key2) != 32 {
		t.Fatalf("Expected 32 bytes, got %d", len(key2))
	}

	// Empty
	_, err = DecodeEncodingAESKey("")
	if err == nil {
		t.Fatal("Expected error for empty encodingAESKey")
	}

	// Invalid length
	_, err = DecodeEncodingAESKey("short")
	if err == nil {
		t.Fatal("Expected error for invalid encodingAESKey length")
	}
}

func TestPKCS7Pad(t *testing.T) {
	data := []byte("hello")
	padded := PKCS7Pad(data, 32)
	if len(padded) != 32 {
		t.Fatalf("Expected padded length 32, got %d", len(padded))
	}
	pad := padded[len(padded)-1]
	if int(pad) != 27 {
		t.Fatalf("Expected pad value 27, got %d", pad)
	}

	// Exact multiple of block size
	data2 := make([]byte, 32)
	padded2 := PKCS7Pad(data2, 32)
	if len(padded2) != 64 {
		t.Fatalf("Expected padded length 64, got %d", len(padded2))
	}
	pad2 := padded2[len(padded2)-1]
	if int(pad2) != 32 {
		t.Fatalf("Expected pad value 32, got %d", pad2)
	}
}

func TestPKCS7Unpad(t *testing.T) {
	data := []byte("hello world")
	padded := PKCS7Pad(data, 32)
	unpadded, err := PKCS7Unpad(padded, 32)
	if err != nil {
		t.Fatalf("PKCS7Unpad failed: %v", err)
	}
	if string(unpadded) != "hello world" {
		t.Fatalf("Unpadded mismatch: got %q", unpadded)
	}

	// Invalid padding value
	_, err = PKCS7Unpad([]byte{1, 2, 3, 40}, 32)
	if err == nil {
		t.Fatal("Expected error for invalid padding value")
	}

	// Empty
	_, err = PKCS7Unpad([]byte{}, 32)
	if err == nil {
		t.Fatal("Expected error for empty data")
	}

	// Mismatched padding byte
	badPad := make([]byte, 32)
	badPad[31] = 5
	badPad[30] = 4 // mismatch
	_, err = PKCS7Unpad(badPad, 32)
	if err == nil {
		t.Fatal("Expected error for mismatched padding byte")
	}
}

func TestWecomCrypto_DecryptInvalidCiphertextLength(t *testing.T) {
	encodingAESKey := "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"
	crypto, err := NewWecomCrypto("token", encodingAESKey, "corpid")
	if err != nil {
		t.Fatalf("NewWecomCrypto failed: %v", err)
	}

	// 20 字节密文：非 16 的整数倍，CBC 解密会 panic（若缺少长度校验）。
	// 构造任意 20 字节的 base64 密文。
	bad := base64.StdEncoding.EncodeToString(make([]byte, 20))
	if _, err := crypto.Decrypt(bad); err == nil {
		t.Fatal("Decrypt with non-block-aligned ciphertext succeeded, want error")
	}

	// 空密文同样应报错而非 panic
	if _, err := crypto.Decrypt(""); err == nil {
		t.Fatal("Decrypt with empty ciphertext succeeded, want error")
	}

	// 合法长度的密文（16 字节，解密后 padding 校验会失败）只应返回错误，不应 panic
	validLen := base64.StdEncoding.EncodeToString(make([]byte, aes.BlockSize))
	if _, err := crypto.Decrypt(validLen); err == nil {
		t.Fatal("Decrypt with 16-byte garbage succeeded, want error")
	}
}

func TestDecryptFile_AcceptsPaddedAESKey(t *testing.T) {
	encodingAESKey := "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"
	key, err := DecodeEncodingAESKey(encodingAESKey)
	if err != nil {
		t.Fatalf("DecodeEncodingAESKey failed: %v", err)
	}

	// DecryptFile 解密的是企微媒体文件格式：AES-256-CBC(key 前 16 字节为 IV)，PKCS#7 填充到 32
	plaintext := []byte("hello file content, pad me to 32 bytes boundary!")
	encryptedData := aesEncryptFile(t, key, plaintext)

	// 用带 padding 的 44 字符 key 解密：DecryptFile 应能正常处理（回归 RF-007）
	got, err := DecryptFile(encryptedData, encodingAESKey+"=")
	if err != nil {
		t.Fatalf("DecryptFile with padded key failed: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Fatalf("DecryptFile mismatch: got %q, want %q", got, plaintext)
	}

	// 无 padding 的 43 字符 key 也应能解密
	got2, err := DecryptFile(encryptedData, encodingAESKey)
	if err != nil {
		t.Fatalf("DecryptFile with unpadded key failed: %v", err)
	}
	if string(got2) != string(plaintext) {
		t.Fatalf("DecryptFile(unpadded) mismatch: got %q, want %q", got2, plaintext)
	}
}

// aesEncryptFile 按 DecryptFile 对应的媒体文件格式加密：AES-256-CBC + PKCS#7(32)。
func aesEncryptFile(t *testing.T, key, plaintext []byte) []byte {
	t.Helper()
	padded := PKCS7Pad(plaintext, PKCS7BlockSize)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes.NewCipher failed: %v", err)
	}
	iv := key[:aes.BlockSize]
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, padded)
	return out
}

func TestDecryptFile_InvalidAESKey(t *testing.T) {
	encryptedData := make([]byte, 32)
	_, err := DecryptFile(encryptedData, "invalid-key-length")
	if err == nil {
		t.Fatal("DecryptFile with invalid key succeeded, want error")
	}
	if !strings.Contains(err.Error(), "decryptFile") {
		t.Fatalf("error lacks decryptFile prefix: %v", err)
	}
}
