package auth

import (
	"encoding/base64"
)

// EncodeBase64 对字符串进行 Base64 编码
func EncodeBase64(data string) string {
	return base64.StdEncoding.EncodeToString([]byte(data))
}

// DecodeBase64 对 Base64 字符串进行解码
func DecodeBase64(data string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

// GenerateEncoded 生成强智教务系统登录所需的 encoded 字符串
// 格式: Base64(用户名) + "%%%" + Base64(密码)
func GenerateEncoded(username, password string) string {
	usernameBase64 := EncodeBase64(username)
	passwordBase64 := EncodeBase64(password)
	return usernameBase64 + "%%%" + passwordBase64
}
