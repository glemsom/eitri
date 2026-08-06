package tool

import (
	"encoding/json"
	"testing"

	"github.com/voocel/litellm"
)

// schemaProps returns the "properties" map of a tool's JSON schema.
func schemaProps(t *testing.T, schema litellm.Schema) map[string]map[string]any {
	t.Helper()
	var schemaObj map[string]any
	if err := json.Unmarshal(schema, &schemaObj); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	raw, ok := schemaObj["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema missing properties")
	}
	props := make(map[string]map[string]any, len(raw))
	for k, v := range raw {
		m, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("property %q is not an object", k)
		}
		props[k] = m
	}
	return props
}

// schemaProp returns the property map for a named parameter, failing if absent.
func schemaProp(t *testing.T, props map[string]map[string]any, name string) map[string]any {
	t.Helper()
	p, ok := props[name]
	if !ok {
		t.Fatalf("schema missing %q property", name)
	}
	return p
}

// assertBounds verifies a numeric parameter carries the given minimum/maximum
// in its schema. A zero maxValue means no maximum is expected.
func assertBounds(t *testing.T, prop map[string]any, param string, wantMin int, wantMax int) {
	t.Helper()
	if got, present := prop["minimum"]; !present {
		t.Errorf("%s should declare jsonschema minimum=%d, but has none", param, wantMin)
	} else if got != float64(wantMin) {
		t.Errorf("%s minimum = %v, want %d", param, got, wantMin)
	}
	if wantMax > 0 {
		if got, present := prop["maximum"]; !present {
			t.Errorf("%s should declare jsonschema maximum=%d, but has none", param, wantMax)
		} else if got != float64(wantMax) {
			t.Errorf("%s maximum = %v, want %d", param, got, wantMax)
		}
	}
}

func TestRead_SchemaStartEndLineBounds(t *testing.T) {
	t.Parallel()
	props := schemaProps(t, NewReadTool("/tmp", nil).JSONSchema())
	assertBounds(t, schemaProp(t, props, "start_line"), "start_line", 1, 0)
	assertBounds(t, schemaProp(t, props, "end_line"), "end_line", 1, 0)
}

func TestWebFetch_SchemaTimeoutBounds(t *testing.T) {
	t.Parallel()
	props := schemaProps(t, NewWebFetchTool().JSONSchema())
	assertBounds(t, schemaProp(t, props, "timeout"), "timeout", 1, 120)
}

func TestBrowser_SchemaNavigateTimeoutBounds(t *testing.T) {
	t.Parallel()
	schema := NewBrowserTool("ws://test", "/tmp").JSONSchema()
	var schemaObj map[string]any
	if err := json.Unmarshal(schema, &schemaObj); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	argsProp, ok := schemaObj["properties"].(map[string]any)["args"].(map[string]any)
	if !ok {
		t.Fatal("schema missing 'args' property")
	}
	oneOf, ok := argsProp["oneOf"].([]any)
	if !ok {
		t.Fatal("args missing oneOf union")
	}
	// navigate is the second branch (index 1) per browserActions order.
	navigate, ok := oneOf[1].(map[string]any)
	if !ok {
		t.Fatal("navigate branch is not an object")
	}
	navigateProps, ok := navigate["properties"].(map[string]any)
	if !ok {
		t.Fatal("navigate branch missing properties")
	}
	timeoutProp, ok := navigateProps["timeout"].(map[string]any)
	if !ok {
		t.Fatal("navigate timeout is not an object")
	}
	assertBounds(t, timeoutProp, "browser navigate.timeout", 1, 0)
}

func TestDelegate_SchemaMaxTurnsBounds(t *testing.T) {
	t.Parallel()
	props := schemaProps(t, NewDelegate(&fakeSubAgentManager{}).JSONSchema())
	assertBounds(t, schemaProp(t, props, "max_turns"), "max_turns", 1, 0)
}
