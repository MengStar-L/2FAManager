package vault

import (
	"strings"
	"testing"
)

const testURI = "otpauth://totp/Example:alice%40example.com?secret=JBSWY3DPEHPK3PXP&issuer=Example"

func TestPreviewURI(t *testing.T) {
	input, err := PreviewURI(testURI)
	if err != nil {
		t.Fatal(err)
	}
	if input.Account != "alice@example.com" || input.Issuer != "Example" || input.Algorithm != "SHA1" || input.Period != 30 || input.Digits != 6 {
		t.Fatalf("incorrect parsed token: %+v", input)
	}
	input, err = PreviewURI("otpauth://totp/%E4%BA%91%20%E7%AB%AF%3Aalice%2Bhome%40example.com?secret=JBSWY3DPEHPK3PXP&issuer=%E4%BA%91%20%E7%AB%AF&algorithm=SHA512&digits=8&period=60")
	if err != nil || input.Issuer != "云 端" || input.Account != "alice+home@example.com" || input.Algorithm != "SHA512" || input.Digits != 8 || input.Period != 60 {
		t.Fatalf("incorrect custom token: %+v, %v", input, err)
	}
	input, err = PreviewURI("otpauth://totp/alice?secret=JBSWY3DPEHPK3PXP&issuer=Example&image=https%3A%2F%2Fexample.com%2Ficon.png")
	if err != nil || input.Issuer != "Example" {
		t.Fatalf("issuer query and harmless image metadata: %+v, %v", input, err)
	}
}

func TestPreviewURIRejectsUnsupportedOrAmbiguousInput(t *testing.T) {
	cases := []string{
		"https://example.com/?secret=JBSWY3DPEHPK3PXP",
		"otpauth-migration://offline?data=abc",
		"otpauth://hotp/Example?secret=JBSWY3DPEHPK3PXP&counter=0",
		"otpauth://totp/Example:alice?secret=JBSWY3DPEHPK3PXP&issuer=Other",
		"otpauth://totp/alice?secret=JBSWY3DPEHPK3PXP&secret=JBSWY3DPEHPK3PXP",
		"otpauth://totp/alice?secret=JBSWY3DPEHPK3PXP&encoder=steam",
		"otpauth://totp/alice?secret=JBSWY3DPEHPK3PXP&counter=1",
		"otpauth://totp/alice?secret=JBSWY3DPEHPK3PXP&period=0",
		"otpauth://totp/alice?secret=JBSWY3DPEHPK3PXP&period=121",
		"otpauth://totp/alice?secret=JBSWY3DPEHPK3PXP&period=+30",
		"otpauth://totp/alice?secret=JBSWY3DPEHPK3PXP&period=",
		"otpauth://totp/alice?secret=JBSWY3DPEHPK3PXP&digits=7",
		"otpauth://totp/alice?secret=JBSWY3DPEHPK3PXP&digits=0",
		"otpauth://totp/alice?secret=JBSWY3DPEHPK3PXP&algorithm=MD5",
		"otpauth://totp/alice?secret=JBSWY3DPEHPK3PXP&algorithm=",
		"otpauth://totp/alice?secret=not-valid!",
		"otpauth://totp/alice?secret=",
		"otpauth://totp/?secret=JBSWY3DPEHPK3PXP",
		"otpauth://totp/Example:?secret=JBSWY3DPEHPK3PXP",
		"otpauth://totp/:alice?secret=JBSWY3DPEHPK3PXP",
		"otpauth://totp/Example:alice:extra?secret=JBSWY3DPEHPK3PXP",
		"otpauth://totp/alice%0A?secret=JBSWY3DPEHPK3PXP#fragment",
		"otpauth://user@totp/alice?secret=JBSWY3DPEHPK3PXP",
		"otpauth://totp:123/alice?secret=JBSWY3DPEHPK3PXP",
		"otpauth://totp/alice?secret=%ZZ",
	}
	for _, uri := range cases {
		_, err := PreviewURI(uri)
		if err == nil {
			t.Errorf("unsupported URI accepted: %s", uri)
		} else if strings.Contains(err.Error(), "JBSWY3DPEHPK3PXP") || strings.Contains(err.Error(), "not-valid!") {
			t.Error("URI validation error leaked a secret")
		}
	}
}
