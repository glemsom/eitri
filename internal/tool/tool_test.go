package tool

import (
	"encoding/json"
	"reflect"
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
	ID       string         `json:"id" jsonschema:"Unique identifier"`
	Label    string         `json:"label,omitempty"`
	Score    float64        `json:"score"`
	Active   bool           `json:"active,omitempty" jsonschema:"Whether active"`
	Tags     []string       `json:"tags,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
	Count    *int           `json:"count,omitempty"`
}

func TestSchemaOf_SimpleStruct(t *testing.T) {
	schema := SchemaOf[testSimpleArgs]()
	if schema == nil {
		t.Fatal("schema is nil")
	}

	var parsed map[string]any
	if err := json.Unmarshal(schema, &parsed); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}

	// Verify type
	if parsed["type"] != "object" {
		t.Errorf("type = %v, want 'object'", parsed["type"])
	}

	props, ok := parsed["properties"].(map[string]any)
	if !ok {
		t.Fatal("properties not found")
	}

	// Check name field
	nameProp, ok := props["name"].(map[string]any)
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
	countProp, ok := props["count"].(map[string]any)
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
	required, ok := parsed["required"].([]any)
	if !ok {
		t.Fatal("required not found")
	}
	if len(required) != 2 {
		t.Errorf("len(required) = %d, want 2", len(required))
	}
}

func TestSchemaOf_WithOmitempty(t *testing.T) {
	schema := SchemaOf[testWithOptional]()
	var parsed map[string]any
	if err := json.Unmarshal(schema, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	required, _ := parsed["required"].([]any)
	if len(required) != 1 || required[0] != "required" {
		t.Errorf("required = %v, want ['required']", required)
	}
}

func TestSchemaOf_MixedTypes(t *testing.T) {
	schema := SchemaOf[testMixed]()
	var parsed map[string]any
	if err := json.Unmarshal(schema, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	props, _ := parsed["properties"].(map[string]any)

	// ID is required string
	idProp := props["id"].(map[string]any)
	if idProp["type"] != "string" {
		t.Errorf("id.type = %v, want 'string'", idProp["type"])
	}

	// Score is required number
	scoreProp := props["score"].(map[string]any)
	if scoreProp["type"] != "number" {
		t.Errorf("score.type = %v, want 'number'", scoreProp["type"])
	}

	// Active is optional boolean
	activeProp := props["active"].(map[string]any)
	if activeProp["type"] != "boolean" {
		t.Errorf("active.type = %v, want 'boolean'", activeProp["type"])
	}
	if activeProp["description"] != "Whether active" {
		t.Errorf("active.description = %v, want 'Whether active'", activeProp["description"])
	}

	// Tags is optional array of strings
	tagsProp := props["tags"].(map[string]any)
	if tagsProp["type"] != "array" {
		t.Errorf("tags.type = %v, want 'array'", tagsProp["type"])
	}

	// Metadata is optional object
	metaProp := props["metadata"].(map[string]any)
	if metaProp["type"] != "object" {
		t.Errorf("metadata.type = %v, want 'object'", metaProp["type"])
	}

	// Count is optional ptr-Int
	countProp := props["count"].(map[string]any)
	if countProp["type"] != "integer" {
		t.Errorf("count.type = %v, want 'integer'", countProp["type"])
	}

	// Only ID and Score are required
	required, _ := parsed["required"].([]any)
	if len(required) != 2 {
		t.Errorf("len(required) = %d, want 2", len(required))
	}
}

func TestSchemaOf_IgnoresUnexported(t *testing.T) {
	type test struct {
		Name     string `json:"name"`
		hidden   string `json:"-"` // unexported, should be ignored
		Exported string `json:"exported"`
	}
	schema := SchemaOf[test]()
	var parsed map[string]any
	if err := json.Unmarshal(schema, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	props, _ := parsed["properties"].(map[string]any)
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
		Name   string `json:"name"`
		NoTag  string // no json tag, should be ignored
		SkipMe string `json:"-"`
	}
	schema := SchemaOf[test]()
	var parsed map[string]any
	if err := json.Unmarshal(schema, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	props, _ := parsed["properties"].(map[string]any)
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
	var parsed map[string]any
	if err := json.Unmarshal(schema, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed["type"] != "object" {
		t.Errorf("type = %v, want 'object'", parsed["type"])
	}
	props, _ := parsed["properties"].(map[string]any)
	if len(props) != 0 {
		t.Errorf("len(props) = %d, want 0", len(props))
	}
	if _, ok := parsed["required"]; ok {
		t.Error("empty struct should not have required")
	}
}

func TestSchemaOf_WithNestedStruct(t *testing.T) {
	type inner struct {
		Value string `json:"value" jsonschema:"Inner value"`
	}
	type outer struct {
		Name   string `json:"name"`
		Config inner  `json:"config"`
	}
	schema := SchemaOf[outer]()
	var parsed map[string]any
	if err := json.Unmarshal(schema, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	props, _ := parsed["properties"].(map[string]any)
	configProp, ok := props["config"].(map[string]any)
	if !ok {
		t.Fatal("config property not found")
	}
	if configProp["type"] != "object" {
		t.Errorf("config.type = %v, want 'object'", configProp["type"])
	}
}

// ── goTypeToJSONType tests ─────────────────────────────────────────────────

func TestGoTypeToJSONType(t *testing.T) {
	tests := []struct {
		kind string
		want string
	}{
		{"string", "string"},
		{"int", "integer"},
		{"int8", "integer"},
		{"int16", "integer"},
		{"int32", "integer"},
		{"int64", "integer"},
		{"uint", "integer"},
		{"uint8", "integer"},
		{"uint16", "integer"},
		{"uint32", "integer"},
		{"uint64", "integer"},
		{"float32", "number"},
		{"float64", "number"},
		{"bool", "boolean"},
		{"slice", "array"},
		{"map", "object"},
		{"struct", "object"},
		{"chan", "string"},   // fallback
		{"func", "string"},   // fallback
	}

	// Use reflect to map Kind names to reflect.Kind values
	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			// Find the reflect.Kind by name
			var kind reflect.Kind
			switch tt.kind {
			case "string":
				kind = reflect.String
			case "int":
				kind = reflect.Int
			case "int8":
				kind = reflect.Int8
			case "int16":
				kind = reflect.Int16
			case "int32":
				kind = reflect.Int32
			case "int64":
				kind = reflect.Int64
			case "uint":
				kind = reflect.Uint
			case "uint8":
				kind = reflect.Uint8
			case "uint16":
				kind = reflect.Uint16
			case "uint32":
				kind = reflect.Uint32
			case "uint64":
				kind = reflect.Uint64
			case "float32":
				kind = reflect.Float32
			case "float64":
				kind = reflect.Float64
			case "bool":
				kind = reflect.Bool
			case "slice":
				kind = reflect.Slice
			case "map":
				kind = reflect.Map
			case "struct":
				kind = reflect.Struct
			case "chan":
				kind = reflect.Chan
			case "func":
				kind = reflect.Func
			default:
				t.Fatalf("unknown kind: %s", tt.kind)
			}
			if got := goTypeToJSONType(kind); got != tt.want {
				t.Errorf("goTypeToJSONType(%s) = %q, want %q", tt.kind, got, tt.want)
			}
		})
	}
}

// ── fieldSchema tests ──────────────────────────────────────────────────────

func TestFieldSchema_WithDescription(t *testing.T) {
	sp := fieldSchema(reflect.TypeOf(""), "a description")
	if sp.Type != "string" {
		t.Errorf("Type = %q, want 'string'", sp.Type)
	}
	if sp.Description != "a description" {
		t.Errorf("Description = %q, want 'a description'", sp.Description)
	}
}

func TestFieldSchema_WithoutDescription(t *testing.T) {
	sp := fieldSchema(reflect.TypeOf(42), "")
	if sp.Type != "integer" {
		t.Errorf("Type = %q, want 'integer'", sp.Type)
	}
	if sp.Description != "" {
		t.Errorf("Description = %q, want empty", sp.Description)
	}
}

func TestFieldSchema_PointerType(t *testing.T) {
	var x *int
	sp := fieldSchema(reflect.TypeOf(x), "")
	if sp.Type != "integer" {
		t.Errorf("Type = %q, want 'integer'", sp.Type)
	}
}

func TestFieldSchema_SliceOfStrings(t *testing.T) {
	sp := fieldSchema(reflect.TypeOf([]string{}), "")
	if sp.Type != "array" {
		t.Errorf("Type = %q, want 'array'", sp.Type)
	}
	if sp.Items == nil {
		t.Fatal("Items is nil")
	}
	if sp.Items.Type != "string" {
		t.Errorf("Items.Type = %q, want 'string'", sp.Items.Type)
	}
}

func TestFieldSchema_MapType(t *testing.T) {
	sp := fieldSchema(reflect.TypeOf(map[string]any{}), "")
	if sp.Type != "object" {
		t.Errorf("Type = %q, want 'object'", sp.Type)
	}
	if sp.AdditionalProperties == nil || !*sp.AdditionalProperties {
		t.Error("AdditionalProperties should be true")
	}
}

func TestFieldSchema_StructType(t *testing.T) {
	type nested struct{}
	sp := fieldSchema(reflect.TypeOf(nested{}), "")
	if sp.Type != "object" {
		t.Errorf("Type = %q, want 'object'", sp.Type)
	}
}

func TestFieldSchema_BoolType(t *testing.T) {
	sp := fieldSchema(reflect.TypeOf(true), "")
	if sp.Type != "boolean" {
		t.Errorf("Type = %q, want 'boolean'", sp.Type)
	}
}

func TestFieldSchema_FloatType(t *testing.T) {
	sp := fieldSchema(reflect.TypeOf(3.14), "")
	if sp.Type != "number" {
		t.Errorf("Type = %q, want 'number'", sp.Type)
	}
}
