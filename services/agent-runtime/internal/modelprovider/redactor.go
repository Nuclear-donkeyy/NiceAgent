package modelprovider

import (
	"regexp"
	"strings"
)

type Redactor struct {
	Secrets []string
}

func NewRedactor(secrets ...string) Redactor {
	clean := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if secret != "" {
			clean = append(clean, secret)
		}
	}
	return Redactor{Secrets: clean}
}

func (r Redactor) RedactString(value string) string {
	value = redactBearerPattern(value)
	value = redactKeyValuePattern(value)
	value = redactOpenAIStyleKeys(value)
	for _, secret := range r.Secrets {
		if len(secret) >= 4 {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	return value
}

func (r Redactor) RedactMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	redacted := make(map[string]any, len(values))
	for key, value := range values {
		if isSensitiveKey(key) {
			redacted[key] = "[REDACTED]"
			continue
		}
		switch typed := value.(type) {
		case string:
			redacted[key] = r.RedactString(typed)
		case map[string]any:
			redacted[key] = r.RedactMap(typed)
		default:
			redacted[key] = value
		}
	}
	return redacted
}

func isSensitiveKey(key string) bool {
	key = strings.ToLower(key)
	for _, marker := range []string{"api_key", "apikey", "authorization", "bearer", "token", "secret", "password", "cookie"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

var (
	bearerPattern      = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]+`)
	openAIStylePattern = regexp.MustCompile(`\bsk-[A-Za-z0-9._-]+`)
	keyValuePattern    = regexp.MustCompile(`(?i)(api[_-]?key|authorization|bearer|token|secret|password|cookie)(["'\s:=]+)([^"',\s}]+)`)
)

func redactBearerPattern(value string) string {
	return bearerPattern.ReplaceAllString(value, "Bearer [REDACTED]")
}

func redactOpenAIStyleKeys(value string) string {
	return openAIStylePattern.ReplaceAllString(value, "sk-[REDACTED]")
}

func redactKeyValuePattern(value string) string {
	return keyValuePattern.ReplaceAllString(value, "${1}${2}[REDACTED]")
}
