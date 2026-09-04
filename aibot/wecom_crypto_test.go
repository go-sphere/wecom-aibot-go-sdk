package aibot

import (
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
