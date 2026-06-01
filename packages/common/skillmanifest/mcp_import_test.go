package skillmanifest

import (
	"strings"
	"testing"
)

func TestPreviewMCPTools(t *testing.T) {
	document := `{
		"tools": [{
			"name": "weather.lookup",
			"description": "Look up weather.",
			"inputSchema": {
				"type": "object",
				"properties": {
					"city": {"type": "string"}
				},
				"required": ["city"]
			},
			"outputSchema": {
				"type": "object",
				"properties": {
					"summary": {"type": "string"}
				}
			},
			"annotations": {
				"readOnlyHint": true,
				"openWorldHint": true
			}
		}]
	}`

	candidates, err := PreviewMCPTools(document)
	if err != nil {
		t.Fatalf("preview mcp tools: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates len = %d, want 1", len(candidates))
	}
	candidate := candidates[0]
	if candidate.Name != "weather.lookup" || candidate.Description != "Look up weather." {
		t.Fatalf("candidate = %#v", candidate)
	}
	if !candidate.ReadOnlyHint || !candidate.OpenWorld || candidate.Destructive {
		t.Fatalf("annotations were not mapped: %#v", candidate)
	}
	if !strings.Contains(candidate.InputSchema, `"city"`) || !strings.Contains(candidate.OutputSchema, `"summary"`) {
		t.Fatalf("schemas were not preserved: %#v", candidate)
	}
	if !strings.Contains(candidate.Annotations, `"readOnlyHint":true`) {
		t.Fatalf("annotations json = %q", candidate.Annotations)
	}
}

func TestPreviewMCPToolsAcceptsJSONRPCResultShape(t *testing.T) {
	candidates, err := PreviewMCPTools(`{
		"jsonrpc": "2.0",
		"id": 1,
		"result": {
			"tools": [{
				"name": "workspace.search",
				"description": "Search workspace files."
			}]
		}
	}`)
	if err != nil {
		t.Fatalf("preview jsonrpc mcp tools: %v", err)
	}
	if len(candidates) != 1 || candidates[0].Name != "workspace.search" {
		t.Fatalf("candidates = %#v", candidates)
	}
	if candidates[0].InputSchema != defaultMCPImportSchema {
		t.Fatalf("default schema = %q", candidates[0].InputSchema)
	}
}

func TestPreviewMCPToolsRejectsInvalidInput(t *testing.T) {
	for name, document := range map[string]string{
		"empty":     "",
		"bad_json":  `{"tools":`,
		"no_tools":  `{"tools":[]}`,
		"no_name":   `{"tools":[{"description":"missing name"}]}`,
		"duplicate": `{"tools":[{"name":"x"},{"name":"x"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := PreviewMCPTools(document); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
