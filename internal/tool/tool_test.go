package tool

import (
	"encoding/json"
	"testing"
)

// ── SchemaOf tests ─────────────────────────────────────────────────────────

type testSimpleArgs struct {
	Name  string `json:"name" jsonschema:"The name field"`
	Count int    `json:"count" jsonschema:"The count"`
}

type testWithOptional struct {
	Required string `json:"required"`
	Optional string `json:"optional,omitempty"`
}

type testMixed struct {
	ID        string   `json:"id" jsonschema:"Unique identifier"`
	Label     string   `json:"label,omitempty"`
	Score     float64  `json:"score"`
	Active    bool     `json:"active,omitempty" jsonschema:"Whether active"`
	Tags      []string `json:"tags,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	Count     *int    `json:"count,omitempty"`
}

func TestSchemaOf_SimpleStruct(t *testing.T) {
	schema := SchemaOf[testSimpleArgs]()
	if schema == nil {
		t.Fatal("schema is nil")
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(schema, &parsed); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}

	// Verify type
	if parsed["type"] != "object" {
		t.Errorf("type = %v, want 'object'", parsed["type"])
	}

	props, ok := parsed["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("properties not found")
	}

	// Check name field
	nameProp, ok := props["name"].(map[string]interface{})
	if !ok {
		t.Fatal("name property not found")
	}
	if nameProp["type"] != "string" {
		t.Errorf("name.type = %v, want 'string'", nameProp["type"])
	}
	if nameProp["description"] != "The name field" {
		t.Errorf("name.description = %v, want 'The name field'", nameProp["description"])
	}

	// Check count field
	countProp, ok := props["count"].(map[string]interface{})
	if !ok {
		t.Fatal("count property not found")
	}
	if countProp["type"] != "integer" {
		t.Errorf("count.type = %v, want 'integer'", countProp["type"])
	}
	if countProp["description"] != "The count" {
		t.Errorf("count.description = %v, want 'The count'", countProp["description"])
	}

	// Both fields are required
	required, ok := parsed["required"].([]interface{})
	if !ok {
		t.Fatal("required not found")
	}
	if len(required) != 2 {
		t.Errorf("len(required) = %d, want 2", len(required))
	}
}

func TestSchemaOf_WithOmitempty(t *testing.T) {
	schema := SchemaOf[testWithOptional]()
	var parsed map[string]interface{}
	if err := json.Unmarshal(schema, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	required, _ := parsed["required"].([]interface{})
	if len(required) != 1 || required[0] != "required" {
		t.Errorf("required = %v, want ['required']", required)
	}
}

func TestSchemaOf_MixedTypes(t *testing.T) {
	schema := SchemaOf[testMixed]()
	var parsed map[string]interface{}
	if err := json.Unmarshal(schema, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	props, _ := parsed["properties"].(map[string]interface{})

	// ID is required string
	idProp := props["id"].(map[string]interface{})
	if idProp["type"] != "string" {
		t.Errorf("id.type = %v, want 'string'", idProp["type"])
	}

	// Score is required number
	scoreProp := props["score"].(map[string]interface{})
	if scoreProp["type"] != "number" {
		t.Errorf("score.type = %v, want 'number'", scoreProp["type"])
	}

	// Active is optional boolean
	activeProp := props["active"].(map[string]interface{})
	if activeProp["type"] != "boolean" {
		t.Errorf("active.type = %v, want 'boolean'", activeProp["type"])
	}
	if activeProp["description"] != "Whether active" {
		t.Errorf("active.description = %v, want 'Whether active'", activeProp["description"])
	}

	// Tags is optional array of strings
	tagsProp := props["tags"].(map[string]interface{})
	if tagsProp["type"] != "array" {
		t.Errorf("tags.type = %v, want 'array'", tagsProp["type"])
	}

	// Metadata is optional object
	metaProp := props["metadata"].(map[string]interface{})
	if metaProp["type"] != "object" {
		t.Errorf("metadata.type = %v, want 'object'", metaProp["type"])
	}

	// Count is optional ptr-Int
	countProp := props["count"].(map[string]interface{})
	if countProp["type"] != "integer" {
		t.Errorf("count.type = %v, want 'integer'", countProp["type"])
	}

	// Only ID and Score are required
	required, _ := parsed["required"].([]interface{})
	if len(required) != 2 {
		t.Errorf("len(required) = %d, want 2", len(required))
	}
}

func TestSchemaOf_IgnoresUnexported(t *testing.T) {
	type test struct {
		Name     string `json:"name"`
		hidden   string `json:"hidden"` // unexported, should be ignored
		Exported string `json:"exported"`
	}
	schema := SchemaOf[test]()
	var parsed map[string]interface{}
	if err := json.Unmarshal(schema, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	props, _ := parsed["properties"].(map[string]interface{})
	if _, ok := props["hidden"]; ok {
		t.Error("hidden field should not be in schema")
	}
	if _, ok := props["name"]; !ok {
		t.Error("name field should be in schema")
	}
	if _, ok := props["exported"]; !ok {
		t.Error("exported field should be in schema")
	}
}

func TestSchemaOf_IgnoresNoJSONTag(t *testing.T) {
	type test struct {
		Name    string `json:"name"`
		NoTag   string // no json tag, should be ignored
		SkipMe  string `json:"-"`
	}
	schema := SchemaOf[test]()
	var parsed map[string]interface{}
	if err := json.Unmarshal(schema, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	props, _ := parsed["properties"].(map[string]interface{})
	if _, ok := props["NoTag"]; ok {
		t.Error("NoTag field should not be in schema (no json tag)")
	}
	if _, ok := props["SkipMe"]; ok {
		t.Error("SkipMe field should not be in schema (json:'-')")
	}
	if _, ok := props["name"]; !ok {
		t.Error("name field should be in schema")
	}
}

func TestSchemaOf_ReturnsValidJSONSchema(t *testing.T) {
	type simple struct {
		A string `json:"a"`
	}
	schema := SchemaOf[simple]()
	if !json.Valid(schema) {
		t.Error("schema is not valid JSON")
	}
}

func TestSchemaOf_EmptyStruct(t *testing.T) {
	type empty struct{}
	schema := SchemaOf[empty]()
	var parsed map[string]interface{}
	if err := json.Unmarshal(schema, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed["type"] != "object" {
		t.Errorf("type = %v, want 'object'", parsed["type"])
	}
	props, _ := parsed["properties"].(map[string]interface{})
	if len(props) != 0 {
		t.Errorf("len(props) = %d, want 0", len(props))
	}
	if _, ok := parsed["required"]; ok {
		t.Error("empty struct should not have required")
	}
}
