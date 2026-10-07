package vault

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
)

// PreviewURI validates an otpauth URI without saving it. The returned input
// contains a secret, so callers must not log it or put it in a token-list DTO.
func PreviewURI(raw string) (TokenInput, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) > 8192 {
		return TokenInput{}, errors.New("二维码内容过长")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return TokenInput{}, errors.New("二维码中的令牌链接格式无效")
	}
	if strings.EqualFold(u.Scheme, "otpauth-migration") {
		return TokenInput{}, errors.New("暂不支持 Google 验证器批量迁移二维码，请使用服务提供的单个 TOTP 二维码")
	}
	if !strings.EqualFold(u.Scheme, "otpauth") {
		return TokenInput{}, errors.New("未找到有效的 otpauth 令牌链接")
	}
	if strings.EqualFold(u.Host, "hotp") {
		return TokenInput{}, errors.New("暂不支持 HOTP 计数器令牌，请使用 TOTP 时间令牌")
	}
	if !strings.EqualFold(u.Host, "totp") || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return TokenInput{}, errors.New("仅支持标准的 otpauth://totp 令牌链接")
	}
	if !strings.HasPrefix(u.Path, "/") || len(u.Path) < 2 {
		return TokenInput{}, errors.New("令牌链接缺少账户名称")
	}
	values, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return TokenInput{}, errors.New("令牌链接参数无效")
	}
	for key, entries := range values {
		if len(entries) != 1 {
			return TokenInput{}, errors.New("令牌链接包含重复参数")
		}
		switch key {
		case "secret", "issuer", "algorithm", "digits", "period", "image":
			// image is harmless display metadata; no remote content is fetched.
		default:
			return TokenInput{}, errors.New("令牌链接包含不支持的参数")
		}
	}
	input := TokenInput{Secret: values.Get("secret"), Issuer: strings.TrimSpace(values.Get("issuer"))}
	label := strings.TrimSpace(strings.TrimPrefix(u.Path, "/"))
	if issuer, account, hasIssuer := strings.Cut(label, ":"); hasIssuer {
		issuer = strings.TrimSpace(issuer)
		if issuer == "" || strings.Contains(account, ":") {
			return TokenInput{}, errors.New("令牌链接中的服务或账户名称无效")
		}
		if input.Issuer != "" && input.Issuer != issuer {
			return TokenInput{}, errors.New("令牌链接中的服务名称不一致")
		}
		input.Issuer = issuer
		input.Account = strings.TrimSpace(account)
	} else {
		input.Account = label
	}
	if values.Has("algorithm") {
		input.Algorithm = values.Get("algorithm")
		if input.Algorithm == "" {
			return TokenInput{}, errors.New("令牌链接中的算法不能为空")
		}
	}
	for _, parameter := range []struct {
		name string
		out  *int
	}{{"digits", &input.Digits}, {"period", &input.Period}} {
		if !values.Has(parameter.name) {
			continue
		}
		value := values.Get(parameter.name)
		if value == "" || strings.ContainsFunc(value, func(r rune) bool { return r < '0' || r > '9' }) {
			return TokenInput{}, errors.New("令牌链接中的位数或周期无效")
		}
		number, err := strconv.Atoi(value)
		if err != nil || number == 0 {
			return TokenInput{}, errors.New("令牌链接中的位数或周期无效")
		}
		*parameter.out = number
	}
	return normalize(input)
}
