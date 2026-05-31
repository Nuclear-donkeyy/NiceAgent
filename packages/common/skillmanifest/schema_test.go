package skillmanifest

import (
	"testing"

	"niceagent/common/protocol"
)

func TestValidateJSONDocument(t *testing.T) {
	schema := `{"type":"object","required":["query"],"properties":{"query":{"type":"string"}},"additionalProperties":false}`
	if err := ValidateJSONDocument(schema, `{"query":"weather"}`); err != nil {
		t.Fatalf("valid document rejected: %v", err)
	}
	if err := ValidateJSONDocument(schema, `{"query":42}`); err == nil {
		t.Fatal("invalid document accepted")
	}
}

func TestValidateHTTPSkillInput(t *testing.T) {
	err := ValidateHTTPSkillInput(testHTTPSkillInput("http://127.0.0.1:8080/hook"))
	if err == nil {
		t.Fatal("private http url accepted")
	}

	err = ValidateHTTPSkillInput(testHTTPSkillInput("https://api.example.com/hook"))
	if err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
}

func testHTTPSkillInput(targetURL string) protocol.HTTPSkillInput {
	return protocol.HTTPSkillInput{
		Name:   "Weather",
		Method: "POST",
		URL:    targetURL,
	}
}
