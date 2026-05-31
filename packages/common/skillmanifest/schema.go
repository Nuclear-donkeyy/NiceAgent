package skillmanifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	gojsonschema "github.com/google/jsonschema-go/jsonschema"
)

func ValidateJSONSchema(raw string) error {
	_, err := ResolveJSONSchema(raw)
	return err
}

func ResolveJSONSchema(raw string) (*gojsonschema.Resolved, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var rawSchema any
	if err := json.Unmarshal([]byte(raw), &rawSchema); err != nil {
		return nil, fmt.Errorf("invalid json schema: %w", err)
	}
	if err := validateSchemaKeywords("root", rawSchema); err != nil {
		return nil, fmt.Errorf("invalid json schema: %w", err)
	}
	var schema gojsonschema.Schema
	if err := json.Unmarshal([]byte(raw), &schema); err != nil {
		return nil, fmt.Errorf("invalid json schema: %w", err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return nil, fmt.Errorf("invalid json schema: %w", err)
	}
	return resolved, nil
}

func ValidateJSONDocument(schemaRaw, documentRaw string) error {
	document, err := DecodeJSONDocument(documentRaw)
	if err != nil {
		return err
	}
	resolved, err := ResolveJSONSchema(schemaRaw)
	if err != nil {
		return err
	}
	if resolved == nil {
		return nil
	}
	if err := resolved.Validate(document); err != nil {
		return fmt.Errorf("json document does not match schema: %w", err)
	}
	return nil
}

func DecodeJSONDocument(raw string) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	var document any
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("invalid json document: %w", err)
	}
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("invalid json document: %w", err)
		}
		if token != nil {
			return nil, fmt.Errorf("invalid json document: trailing data")
		}
	}
	return document, nil
}

func NormalizeJSON(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	var buffer bytes.Buffer
	if err := json.Compact(&buffer, []byte(raw)); err != nil {
		return "", err
	}
	return buffer.String(), nil
}

var validJSONSchemaTypes = map[string]struct{}{
	"array":   {},
	"boolean": {},
	"integer": {},
	"null":    {},
	"number":  {},
	"object":  {},
	"string":  {},
}

func validateSchemaKeywords(path string, node any) error {
	switch schema := node.(type) {
	case bool:
		return nil
	case map[string]any:
		if rawType, ok := schema["type"]; ok {
			if err := validateSchemaType(path+".type", rawType); err != nil {
				return err
			}
		}
		if rawRequired, ok := schema["required"]; ok {
			values, ok := rawRequired.([]any)
			if !ok {
				return fmt.Errorf("%s.required must be an array of strings", path)
			}
			for i, value := range values {
				if _, ok := value.(string); !ok {
					return fmt.Errorf("%s.required[%d] must be a string", path, i)
				}
			}
		}
		for _, keyword := range []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas"} {
			if raw, ok := schema[keyword]; ok {
				children, ok := raw.(map[string]any)
				if !ok {
					return fmt.Errorf("%s.%s must be an object", path, keyword)
				}
				for name, child := range children {
					if err := validateSchemaKeywords(path+"."+keyword+"."+name, child); err != nil {
						return err
					}
				}
			}
		}
		for _, keyword := range []string{"additionalProperties", "unevaluatedProperties", "propertyNames", "items", "contains", "not", "if", "then", "else"} {
			if raw, ok := schema[keyword]; ok {
				if err := validateSchemaOrSchemaArray(path+"."+keyword, raw); err != nil {
					return err
				}
			}
		}
		for _, keyword := range []string{"prefixItems", "allOf", "anyOf", "oneOf"} {
			if raw, ok := schema[keyword]; ok {
				values, ok := raw.([]any)
				if !ok {
					return fmt.Errorf("%s.%s must be an array of schemas", path, keyword)
				}
				for i, child := range values {
					if err := validateSchemaKeywords(fmt.Sprintf("%s.%s[%d]", path, keyword, i), child); err != nil {
						return err
					}
				}
			}
		}
		return nil
	default:
		return fmt.Errorf("%s must be a JSON object or boolean schema", path)
	}
}

func validateSchemaOrSchemaArray(path string, raw any) error {
	if values, ok := raw.([]any); ok {
		for i, child := range values {
			if err := validateSchemaKeywords(fmt.Sprintf("%s[%d]", path, i), child); err != nil {
				return err
			}
		}
		return nil
	}
	return validateSchemaKeywords(path, raw)
}

func validateSchemaType(path string, raw any) error {
	switch typed := raw.(type) {
	case string:
		if _, ok := validJSONSchemaTypes[typed]; !ok {
			return fmt.Errorf("%s has unsupported type %q", path, typed)
		}
	case []any:
		if len(typed) == 0 {
			return fmt.Errorf("%s must not be empty", path)
		}
		for i, value := range typed {
			name, ok := value.(string)
			if !ok {
				return fmt.Errorf("%s[%d] must be a string", path, i)
			}
			if _, ok := validJSONSchemaTypes[name]; !ok {
				return fmt.Errorf("%s[%d] has unsupported type %q", path, i, name)
			}
		}
	default:
		return fmt.Errorf("%s must be a string or array of strings", path)
	}
	return nil
}
