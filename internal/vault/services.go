package vault

import "strings"

// Service defaults are deliberately separate from normalize: vault validation
// must continue accepting structurally valid records saved by older versions.
// An explicit group always wins over the service default.
func withDefaultGroup(input TokenInput) TokenInput {
	if input.Group != "" {
		return input
	}
	switch strings.ToLower(strings.TrimSpace(input.Issuer)) {
	case "openai", "open ai", "open-ai", "open_ai", "openai.com", "www.openai.com":
		input.Group = "OpenAI"
	}
	return input
}
