package vault

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxImportTokens     = 100
	maxMigrationURI     = 64 << 10
	maxMigrationPayload = 48 << 10
	maxMigrationPages   = 100
)

// ImportChunk represents one source QR. URI and Inputs contain secrets: neither
// may be logged or included in the regular token-list DTO.
type ImportChunk struct {
	URI       string
	Inputs    []TokenInput
	Migration *MigrationBatch
}

type MigrationBatch struct {
	ID    string `json:"id"`
	Size  int    `json:"size"`
	Index int    `json:"index"`
}

// ParseImportURI accepts a standard TOTP URI or one Google Authenticator export
// page. The latter's protobuf layout is an independently documented format:
// https://github.com/qistoph/otp_export/blob/master/OtpMigration.proto
// Unspecified OTP fields use SHA1/6 digits/TOTP, matching Aegis's importer:
// https://github.com/beemdevelopment/Aegis/blob/master/app/src/main/java/com/beemdevelopment/aegis/otp/GoogleAuthInfo.java
func ParseImportURI(raw string) (ImportChunk, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) > maxMigrationURI {
		return ImportChunk{}, errors.New("二维码内容过长")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ImportChunk{}, errors.New("二维码中的令牌链接格式无效")
	}
	if !strings.EqualFold(u.Scheme, "otpauth-migration") {
		input, err := PreviewURI(raw)
		if err != nil {
			return ImportChunk{}, err
		}
		return ImportChunk{URI: raw, Inputs: []TokenInput{input}}, nil
	}
	if !strings.EqualFold(u.Host, "offline") || u.User != nil || u.Opaque != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return ImportChunk{}, errors.New("Google 验证器迁移链接格式无效")
	}
	// A few scanners preserve literal '+' from a standard Base64 payload. It is
	// a Base64 character here, never form whitespace; preserve it before parsing.
	values, err := url.ParseQuery(strings.ReplaceAll(u.RawQuery, "+", "%2B"))
	if err != nil || len(values) != 1 || len(values["data"]) != 1 || values.Get("data") == "" {
		return ImportChunk{}, errors.New("Google 验证器迁移链接参数无效")
	}
	encoded := values.Get("data")
	if strings.ContainsAny(encoded, " \t\r\n") {
		return ImportChunk{}, errors.New("Google 验证器迁移数据编码无效")
	}
	var payload []byte
	for _, encoding := range []*base64.Encoding{base64.StdEncoding.Strict(), base64.RawStdEncoding.Strict(), base64.URLEncoding.Strict(), base64.RawURLEncoding.Strict()} {
		payload, err = encoding.DecodeString(encoded)
		if err == nil {
			break
		}
		clear(payload)
	}
	defer clear(payload)
	if err != nil || len(payload) == 0 || len(payload) > maxMigrationPayload {
		return ImportChunk{}, errors.New("Google 验证器迁移数据编码无效或内容过长")
	}
	chunk, err := parseMigrationPayload(payload)
	if err != nil {
		return ImportChunk{}, err
	}
	chunk.URI = "otpauth-migration://offline?data=" + url.QueryEscape(base64.StdEncoding.EncodeToString(payload))
	if len(chunk.URI) > maxMigrationURI {
		return ImportChunk{}, errors.New("Google 验证器迁移数据内容过长")
	}
	return chunk, nil
}

// ParseImportURIs validates all pages before a caller can save any of them.
// A repeated migration page is idempotent. Different contents for the same
// batch/index are rejected, as are missing pages when requireComplete is true.
func ParseImportURIs(raws []string, requireComplete bool) ([]ImportChunk, error) {
	if len(raws) == 0 {
		return nil, errors.New("没有可导入的令牌")
	}
	if len(raws) > maxImportTokens {
		return nil, errors.New("一次最多导入 100 个令牌或二维码")
	}
	type batchState struct {
		size  int
		pages map[int]string
	}
	batches := make(map[string]*batchState)
	chunks := make([]ImportChunk, 0, len(raws))
	total := 0
	for i, raw := range raws {
		chunk, err := ParseImportURI(raw)
		if err != nil {
			return nil, fmt.Errorf("第 %d 个二维码或链接: %w", i+1, err)
		}
		if batch := chunk.Migration; batch != nil {
			state, exists := batches[batch.ID]
			if !exists {
				state = &batchState{size: batch.Size, pages: make(map[int]string)}
				batches[batch.ID] = state
			}
			if state.size != batch.Size {
				return nil, errors.New("Google 验证器同一批次的二维码总数不一致，请重新导出")
			}
			if prior, exists := state.pages[batch.Index]; exists {
				if prior != chunk.URI {
					return nil, errors.New("Google 验证器同一页二维码内容冲突，请重新导出")
				}
				continue
			}
			state.pages[batch.Index] = chunk.URI
		}
		total += len(chunk.Inputs)
		if total > maxImportTokens {
			return nil, errors.New("一次最多导入 100 个令牌，请分次导出")
		}
		chunks = append(chunks, chunk)
	}
	if requireComplete {
		for _, chunk := range chunks {
			if batch := chunk.Migration; batch != nil {
				state := batches[batch.ID]
				if len(state.pages) != state.size {
					return nil, fmt.Errorf("Google 验证器迁移二维码尚未收齐（%d/%d），请继续添加其余二维码", len(state.pages), state.size)
				}
			}
		}
	}
	return chunks, nil
}

func parseMigrationPayload(payload []byte) (ImportChunk, error) {
	chunk := ImportChunk{Inputs: make([]TokenInput, 0)}
	var metadata [6]int32
	var seen [6]bool
	reader := migrationReader{data: payload}
	for len(reader.data) > 0 {
		field, wire, number, data, err := reader.next()
		if err != nil {
			return ImportChunk{}, err
		}
		switch field {
		case 1:
			if wire != 2 || len(chunk.Inputs) >= maxImportTokens {
				return ImportChunk{}, errors.New("Google 验证器迁移令牌数据无效或数量超过 100 个")
			}
			input, err := parseMigrationToken(data)
			if err != nil {
				return ImportChunk{}, fmt.Errorf("迁移二维码中的第 %d 个令牌: %w", len(chunk.Inputs)+1, err)
			}
			chunk.Inputs = append(chunk.Inputs, input)
		case 2, 3, 4, 5:
			if wire != 0 || seen[field] {
				return ImportChunk{}, errors.New("Google 验证器迁移批次参数重复或格式无效")
			}
			// int32 allows the standard sign-extended 10-byte encoding, and
			// older encoders that emitted the unsigned 32-bit representation.
			if number > 0xffffffff && number < 0xffffffff80000000 {
				return ImportChunk{}, errors.New("Google 验证器迁移批次参数超出范围")
			}
			metadata[field], seen[field] = int32(number), true
		default:
			// Well-formed unknown scalar/byte fields can be safely skipped.
			// Unsupported versions are rejected below before any token is used.
		}
	}
	if len(chunk.Inputs) == 0 {
		return ImportChunk{}, errors.New("Google 验证器迁移二维码中没有令牌")
	}
	if metadata[2] != 0 && metadata[2] != 1 && metadata[2] != 2 {
		return ImportChunk{}, errors.New("暂不支持此 Google 验证器迁移版本，请更新程序后重试")
	}
	size, index, id := int(metadata[3]), int(metadata[4]), metadata[5]
	if size == 0 {
		size = 1
	}
	if size < 1 || size > maxMigrationPages || index < 0 || index >= size {
		return ImportChunk{}, errors.New("Google 验证器迁移二维码页码或总数无效")
	}
	batchID := strconv.FormatInt(int64(id), 10)
	if id == 0 && size == 1 {
		// Older single-page exports may omit all metadata. Keep separate
		// such exports independent while retaining stable page deduplication.
		digest := sha256.Sum256(payload)
		batchID = "single-" + hex.EncodeToString(digest[:])
	}
	chunk.Migration = &MigrationBatch{ID: batchID, Size: size, Index: index}
	return chunk, nil
}

func parseMigrationToken(payload []byte) (TokenInput, error) {
	var input TokenInput
	var values [8]uint64
	var seen [8]bool
	reader := migrationReader{data: payload}
	for len(reader.data) > 0 {
		field, wire, number, data, err := reader.next()
		if err != nil {
			return TokenInput{}, err
		}
		if field < 1 || field > 7 {
			continue
		}
		if seen[field] {
			return TokenInput{}, errors.New("Google 验证器迁移令牌包含重复参数")
		}
		seen[field] = true
		if field <= 3 {
			if wire != 2 {
				return TokenInput{}, errors.New("Google 验证器迁移令牌参数格式无效")
			}
			switch field {
			case 1:
				if len(data) == 0 || len(data) > 512 {
					return TokenInput{}, errors.New("Google 验证器迁移令牌密钥无效或过长")
				}
				input.Secret = base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(data)
			case 2, 3:
				limit := 320
				if field == 3 {
					limit = 200
				}
				if len(data) > limit*utf8.UTFMax || !utf8.Valid(data) || utf8.RuneCount(data) > limit || strings.ContainsFunc(string(data), unicode.IsControl) {
					return TokenInput{}, errors.New("Google 验证器迁移账户或服务名称过长或无效")
				}
				if field == 2 {
					input.Account = strings.TrimSpace(string(data))
				} else {
					input.Issuer = strings.TrimSpace(string(data))
				}
			}
		} else {
			if wire != 0 {
				return TokenInput{}, errors.New("Google 验证器迁移令牌参数格式无效")
			}
			values[field] = number
		}
	}
	switch values[4] {
	case 0, 1:
		input.Algorithm = "SHA1"
	case 2:
		input.Algorithm = "SHA256"
	case 3:
		input.Algorithm = "SHA512"
	default:
		return TokenInput{}, errors.New("迁移二维码包含不支持的算法，仅支持 SHA1、SHA256 和 SHA512；未导入任何令牌")
	}
	switch values[5] {
	case 0, 1:
		input.Digits = 6
	case 2:
		input.Digits = 8
	default:
		return TokenInput{}, errors.New("迁移二维码包含不支持的验证码位数，仅支持 6 或 8 位；未导入任何令牌")
	}
	switch values[6] {
	case 0, 2:
	case 1:
		return TokenInput{}, errors.New("迁移二维码包含 HOTP 计数器令牌，暂不支持；未导入任何令牌")
	default:
		return TokenInput{}, errors.New("迁移二维码包含不支持的令牌类型；未导入任何令牌")
	}
	if values[7] != 0 {
		return TokenInput{}, errors.New("Google 验证器 TOTP 迁移令牌包含非零计数器，未导入任何令牌")
	}
	if prefix, account, ok := strings.Cut(input.Account, ":"); ok {
		prefix = strings.TrimSpace(prefix)
		if prefix != "" && (input.Issuer == "" || strings.EqualFold(prefix, input.Issuer)) {
			if input.Issuer == "" {
				input.Issuer = prefix
			}
			input.Account = strings.TrimSpace(account)
		}
	}
	input.Period = 30
	return normalize(input)
}

// migrationReader supports protobuf scalar/length-delimited wire fields. It is
// bounded by the already-limited payload and never recursively interprets an
// unknown message. Groups are unsupported by the migration schema.
type migrationReader struct{ data []byte }

func (r *migrationReader) next() (field, wire int, number uint64, data []byte, err error) {
	invalid := errors.New("Google 验证器迁移数据损坏或格式无效")
	tag, n := binary.Uvarint(r.data)
	if n <= 0 || tag>>3 == 0 || tag>>3 > (1<<29)-1 {
		return 0, 0, 0, nil, invalid
	}
	r.data = r.data[n:]
	field, wire = int(tag>>3), int(tag&7)
	switch wire {
	case 0:
		number, n = binary.Uvarint(r.data)
		if n <= 0 {
			return 0, 0, 0, nil, invalid
		}
		r.data = r.data[n:]
	case 1, 5:
		n = 8
		if wire == 5 {
			n = 4
		}
		if len(r.data) < n {
			return 0, 0, 0, nil, invalid
		}
		r.data = r.data[n:]
	case 2:
		length, size := binary.Uvarint(r.data)
		if size <= 0 || length > uint64(len(r.data)-size) {
			return 0, 0, 0, nil, invalid
		}
		r.data = r.data[size:]
		data = r.data[:int(length)]
		r.data = r.data[int(length):]
	default:
		return 0, 0, 0, nil, invalid
	}
	return field, wire, number, data, nil
}
