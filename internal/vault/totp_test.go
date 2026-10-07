package vault

import (
	"encoding/base32"
	"testing"
)

// Published vectors from RFC 6238 Appendix B exercise all three hash
// algorithms, leading zeroes, and timestamps beyond the 32-bit range.
func TestRFC6238Vectors(t *testing.T) {
	keys := map[string]string{
		"SHA1":   "12345678901234567890",
		"SHA256": "12345678901234567890123456789012",
		"SHA512": "1234567890123456789012345678901234567890123456789012345678901234",
	}
	vectors := []struct {
		unixTime             int64
		sha1, sha256, sha512 string
	}{
		{59, "94287082", "46119246", "90693936"},
		{1111111109, "07081804", "68084774", "25091201"},
		{1111111111, "14050471", "67062674", "99943326"},
		{1234567890, "89005924", "91819424", "93441116"},
		{2000000000, "69279037", "90698825", "38618901"},
		{20000000000, "65353130", "77737706", "47863826"},
	}
	for _, vector := range vectors {
		for algorithm, want := range map[string]string{"SHA1": vector.sha1, "SHA256": vector.sha256, "SHA512": vector.sha512} {
			input := TokenInput{Secret: base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte(keys[algorithm])), Algorithm: algorithm, Digits: 8, Period: 30}
			code, remaining, err := codeAt(input, vector.unixTime)
			if err != nil || code != want {
				t.Errorf("%s at %d: code=%q error=%v, want=%s", algorithm, vector.unixTime, code, err, want)
			}
			if remaining != 30-int(vector.unixTime%30) {
				t.Errorf("incorrect remaining time: %d", remaining)
			}
		}
	}
}

func TestSixDigitsAndPeriodBoundary(t *testing.T) {
	input := TokenInput{Secret: "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", Algorithm: "SHA1", Digits: 6, Period: 60}
	before, remaining, err := codeAt(input, 59)
	if err != nil || before != "755224" || remaining != 1 {
		t.Fatalf("before boundary: %s %d %v", before, remaining, err)
	}
	after, remaining, err := codeAt(input, 60)
	if err != nil || after != "287082" || remaining != 60 {
		t.Fatalf("after boundary: %s %d %v", after, remaining, err)
	}
	if _, _, err := codeAt(input, -1); err == nil {
		t.Fatal("negative system time was accepted")
	}
}

func TestNormalizeSecretAndInput(t *testing.T) {
	input, err := normalize(TokenInput{Account: " Alice ", Secret: "mzxw 6===", Color: "#AA22bb"})
	if err != nil || input.Secret != "MZXW6" || input.Account != "Alice" || input.Color != "#aa22bb" || input.Algorithm != "SHA1" || input.Digits != 6 || input.Period != 30 {
		t.Fatalf("normalization: %+v, %v", input, err)
	}
	for _, secret := range []string{"", "ABC0EF", "MZXW6=", "MZ=XW6", "not a valid secret!"} {
		if _, err := normalizeSecret(secret); err == nil {
			t.Errorf("invalid secret accepted: %q", secret)
		}
	}
}
