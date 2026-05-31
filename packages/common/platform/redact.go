package platform

import "strings"

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
		out[key] = value
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
