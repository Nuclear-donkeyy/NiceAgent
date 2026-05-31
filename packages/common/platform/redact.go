package platform

import (
	"encoding/json"
	"strings"
)

var sensitiveKeyFragments = []string{
	"api_key",
	"apikey",
	"authorization",
	"bearer",
	"cookie",
	"password",
	"secret",
	"token",
}

func RedactMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		if IsSensitiveKey(key) {
			out[key] = "[redacted]"
			continue
		}
		out[key] = RedactValue(value)
	}
	return out
}

func RedactValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return RedactMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = RedactValue(item)
		}
		return out
	default:
		return value
	}
}

func RedactJSON(raw string) string {
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return raw
	}
	redacted := RedactValue(value)
	data, err := json.Marshal(redacted)
	if err != nil {
		return raw
	}
	return string(data)
}

func RedactTextSecrets(value string, secrets []string) string {
	out := value
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if len(secret) < 4 {
			continue
		}
		out = strings.ReplaceAll(out, secret, "[redacted]")
	}
	return out
}

func IsSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	for _, fragment := range sensitiveKeyFragments {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}
