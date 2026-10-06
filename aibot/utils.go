package aibot

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"
)

// generateReqId 生成请求 ID
// 格式: {cmd}_{timestamp}_{random}
func generateReqId(cmd string) string {
	now := time.Now().UnixNano()
	randBytes := make([]byte, 4)
	rand.Read(randBytes)
	randHex := hex.EncodeToString(randBytes)
	return fmt.Sprintf("%s_%d_%s", cmd, now, randHex)
}

// GenerateReqId 生成请求 ID（公开方法）
// 格式为 {cmd}_{纳秒时间戳}_{8 位随机十六进制}，可并发调用。
func GenerateReqId(cmd string) string {
	return generateReqId(cmd)
}

// generateRandomString 生成随机字符串
func generateRandomString(length int) string {
	bytes := make([]byte, length)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)[:length]
}

// GenerateRandomString 生成随机字符串（公开方法）
// 返回 length 个随机十六进制字符；length 为负数时 panic。
func GenerateRandomString(length int) string {
	return generateRandomString(length)
}

// md5Sum 计算字节切片的 MD5 哈希值
func md5Sum(data []byte) string {
	h := md5.Sum(data)
	return hex.EncodeToString(h[:])
}

// base64Encode 将字节切片编码为 Base64 字符串
func base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}
