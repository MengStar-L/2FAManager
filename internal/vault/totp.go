// Package vault stores authenticator secrets and produces RFC 6238 TOTP codes.
package vault

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Token is safe to send to the UI. It deliberately contains no secret.
type Token struct {
	ID        string `json:"id"`
	Issuer    string `json:"issuer"`
	Account   string `json:"account"`
	Group     string `json:"group"`
	Favorite  bool   `json:"favorite"`
	Color     string `json:"color"`
	Algorithm string `json:"algorithm"`
	Digits    int    `json:"digits"`
	Period    int    `json:"period"`
	Code      string `json:"code"`
	Remaining int    `json:"remaining"`
}

// TokenInput is accepted only when creating or editing a token. During an edit,
// an empty Secret preserves the saved secret.
type TokenInput struct {
	Issuer    string `json:"issuer"`
	Account   string `json:"account"`
	Secret    string `json:"secret"`
	Group     string `json:"group"`
	Color     string `json:"color"`
	Algorithm string `json:"algorithm"`
	Digits    int    `json:"digits"`
	Period    int    `json:"period"`
}

func normalize(input TokenInput) (TokenInput, error) {
	input.Issuer = strings.TrimSpace(input.Issuer)
	input.Account = strings.TrimSpace(input.Account)
	input.Group = strings.TrimSpace(input.Group)
	for _, field := range []struct {
		name, value string
		limit       int
	}{{"服务名称", input.Issuer, 200}, {"账户名称", input.Account, 320}, {"分组名称", input.Group, 80}} {
		if !utf8.ValidString(field.value) || len([]rune(field.value)) > field.limit || strings.ContainsFunc(field.value, unicode.IsControl) {
			return TokenInput{}, fmt.Errorf("%s过长或包含无效字符", field.name)
		}
	}
	if input.Account == "" {
		return TokenInput{}, errors.New("请输入账户名称")
	}
	secret, err := normalizeSecret(input.Secret)
	if err != nil {
		return TokenInput{}, err
	}
	input.Secret = secret
	input.Algorithm = strings.ToUpper(strings.TrimSpace(input.Algorithm))
	if input.Algorithm == "" {
		input.Algorithm = "SHA1"
	}
	if input.Algorithm != "SHA1" && input.Algorithm != "SHA256" && input.Algorithm != "SHA512" {
		return TokenInput{}, errors.New("仅支持 SHA1、SHA256 和 SHA512 算法")
	}
	if input.Digits == 0 {
		input.Digits = 6
	}
	if input.Digits != 6 && input.Digits != 8 {
		return TokenInput{}, errors.New("验证码位数必须为 6 或 8")
	}
	if input.Period == 0 {
		input.Period = 30
	}
	if input.Period < 1 || input.Period > 120 {
		return TokenInput{}, errors.New("刷新周期必须为 1–120 秒")
	}
	input.Color = strings.ToLower(strings.TrimSpace(input.Color))
	if input.Color == "" {
		input.Color = "#8581d8"
	}
	if !validColor(input.Color) {
		return TokenInput{}, errors.New("请选择有效的令牌颜色")
	}
	return input, nil
}

func validColor(value string) bool {
	if strings.HasPrefix(value, "#") {
		if len(value) != 7 {
			return false
		}
		for _, c := range value[1:] {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return false
			}
		}
		return true
	}
	if len(value) < 2 || len(value) > 24 {
		return false
	}
	for _, c := range value {
		if c < 'a' || c > 'z' {
			return false
		}
	}
	return true
}

func normalizeSecret(value string) (string, error) {
	if len(value) > 2048 {
		return "", errors.New("密钥过长")
	}
	value = strings.ToUpper(strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, value))
	var decoded []byte
	var err error
	if strings.Contains(value, "=") {
		decoded, err = base32.StdEncoding.DecodeString(value)
	} else {
		decoded, err = base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(value)
	}
	if err != nil || len(decoded) == 0 || len(decoded) > 512 {
		return "", errors.New("密钥无效，请输入 Base32 格式的验证器密钥")
	}
	// Re-encoding also canonicalizes unused final bits for duplicate detection.
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(decoded), nil
}

func codeAt(input TokenInput, unixTime int64) (string, int, error) {
	if unixTime < 0 {
		return "", 0, errors.New("系统时间无效，请检查系统时钟")
	}
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(input.Secret)
	if err != nil || len(secret) == 0 {
		return "", 0, errors.New("令牌密钥无效")
	}
	var constructor func() hash.Hash
	switch input.Algorithm {
	case "SHA1":
		constructor = sha1.New
	case "SHA256":
		constructor = sha256.New
	case "SHA512":
		constructor = sha512.New
	default:
		return "", 0, errors.New("令牌算法无效")
	}
	if (input.Digits != 6 && input.Digits != 8) || input.Period < 1 || input.Period > 120 {
		return "", 0, errors.New("令牌参数无效")
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(unixTime/int64(input.Period)))
	mac := hmac.New(constructor, secret)
	_, _ = mac.Write(counter[:])
	digest := mac.Sum(nil)
	offset := digest[len(digest)-1] & 0x0f
	value := binary.BigEndian.Uint32(digest[int(offset):int(offset)+4]) & 0x7fffffff
	modulus := uint32(1000000)
	if input.Digits == 8 {
		modulus = 100000000
	}
	return fmt.Sprintf("%0*d", input.Digits, value%modulus), input.Period - int(unixTime%int64(input.Period)), nil
}
