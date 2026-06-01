package skillmanifest

import (
	"strings"
	"testing"
)

func TestPreviewOpenAPIHTTPSkills(t *testing.T) {
	document := `{
		"openapi":"3.1.0",
		"servers":[{"url":"https://api.example.com"}],
		"components":{
			"securitySchemes":{
				"bearerAuth":{"type":"http","scheme":"bearer"}
			}
		},
		"security":[{"bearerAuth":[]}],
		"paths":{
			"/weather":{
				"post":{
					"operationId":"getWeather",
					"summary":"Get weather",
					"requestBody":{
						"content":{
							"application/json":{
								"schema":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}
							}
						}
					},
					"responses":{
						"200":{"content":{"application/json":{"schema":{"type":"object","properties":{"temperature":{"type":"number"}}}}}}
					}
				}
			},
			"/legacy":{
				"delete":{
					"operationId":"deleteLegacy",
					"responses":{"204":{"description":"Deleted"}}
				}
			}
		}
	}`

	candidates, err := PreviewOpenAPIHTTPSkills(document, "")
	if err != nil {
		t.Fatalf("preview openapi skills: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %#v, want one supported operation", candidates)
	}
	candidate := candidates[0]
	if candidate.Name != "getWeather" || candidate.Method != "POST" || candidate.URL != "https://api.example.com/weather" {
		t.Fatalf("candidate = %#v, want weather POST candidate", candidate)
	}
	if candidate.AuthType != "bearer" || !candidate.RequiresSecret || candidate.SecurityScheme != "bearerAuth" {
		t.Fatalf("candidate auth = %#v, want bearer secret hint", candidate)
	}
	if !strings.Contains(candidate.InputSchema, `"city"`) || !strings.Contains(candidate.OutputSchema, `"temperature"`) {
		t.Fatalf("candidate schemas = %s / %s", candidate.InputSchema, candidate.OutputSchema)
	}
}

func TestPreviewOpenAPIHTTPSkillsBuildsParameterSchemaAndAllowsBaseURLOverride(t *testing.T) {
	document := `{
		"openapi":"3.1.0",
		"servers":[{"url":"https://wrong.example.com"}],
		"paths":{
			"/search":{
				"get":{
					"summary":"Search",
					"parameters":[
						{"name":"q","in":"query","required":true,"schema":{"type":"string"}}
					],
					"responses":{"200":{"content":{"application/json":{"schema":{"type":"object"}}}}}
				}
			}
		}
	}`

	candidates, err := PreviewOpenAPIHTTPSkills(document, "https://api.example.com/v1")
	if err != nil {
		t.Fatalf("preview openapi skills: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %#v, want one supported operation", candidates)
	}
	candidate := candidates[0]
	if candidate.URL != "https://api.example.com/v1/search" {
		t.Fatalf("candidate url = %q, want override base url", candidate.URL)
	}
	if !strings.Contains(candidate.InputSchema, `"q"`) || !strings.Contains(candidate.InputSchema, `"required":["q"]`) {
		t.Fatalf("input schema = %s, want query parameter schema", candidate.InputSchema)
	}
}

func TestPreviewOpenAPIHTTPSkillsAcceptsYAML(t *testing.T) {
	document := `
openapi: 3.1.0
servers:
  - url: https://api.example.com
components:
  securitySchemes:
    apiToken:
      type: apiKey
      in: header
      name: X-API-Key
paths:
  /status:
    get:
      operationId: getStatus
      summary: Get status
      security:
        - apiToken: []
      parameters:
        - name: region
          in: query
          required: false
          schema:
            type: string
      responses:
        "200":
          content:
            application/json:
              schema:
                type: object
                properties:
                  ok:
                    type: boolean
`

	candidates, err := PreviewOpenAPIHTTPSkills(document, "")
	if err != nil {
		t.Fatalf("preview openapi yaml skills: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %#v, want one supported operation", candidates)
	}
	candidate := candidates[0]
	if candidate.Name != "getStatus" || candidate.Method != "GET" || candidate.URL != "https://api.example.com/status" {
		t.Fatalf("candidate = %#v, want yaml status candidate", candidate)
	}
	if !candidate.RequiresSecret || !candidate.UnsupportedAuth || candidate.SecurityScheme != "apiToken" {
		t.Fatalf("candidate auth = %#v, want unsupported api key hint", candidate)
	}
	if !strings.Contains(candidate.InputSchema, `"region"`) || !strings.Contains(candidate.OutputSchema, `"ok"`) {
		t.Fatalf("candidate schemas = %s / %s", candidate.InputSchema, candidate.OutputSchema)
	}
}

func TestPreviewOpenAPIHTTPSkillsRejectsInvalidInput(t *testing.T) {
	if _, err := PreviewOpenAPIHTTPSkills(`{"openapi":"3.1.0","paths":{}}`, ""); err == nil {
		t.Fatal("expected missing paths error")
	}
	if _, err := PreviewOpenAPIHTTPSkills(`{"openapi":"3.1.0","paths":{"/x":{"get":{"responses":{}}}}}`, "http://api.example.com"); err == nil {
		t.Fatal("expected non-https base url error")
	}
}
