// Package tool provides the ToolHandler interface, SchemaOf[T] helper, and
// dispatch map that replace the ADK functiontool system.
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/voocel/litellm"
)

// ToolHandler is the interface each built-in tool implements.
//
// Call returns:
//   - []litellm.Block — content blocks for the tool result (e.g. TextBlock).
//   - error           — a Go-level error that terminates the agent loop
//     (unknown tool, context cancelled, etc.).
//   - bool            — isError: when true the result is wrapped as a
//     ToolResultBlock with IsError=true so the LLM sees a tool error and
//     can decide how to respond.
type ToolHandler interface {
	Name() string
	Description() string
	JSONSchema() litellm.Schema
	Call(ctx context.Context, args json.RawMessage) ([]litellm.Block, error, bool)
}

// SchemaOf generates a litellm.Schema (JSON Schema object) from a Go struct
// type T by reflecting its fields and reading json: and jsonschema: struct tags.
//
// Fields without a json tag are ignored. The "omitempty" json option makes the
// corresponding property not required. The jsonschema tag value becomes the
// property description.
func SchemaOf[T any]() litellm.Schema {
	s, err := schemaOf(reflect.TypeFor[T]())
	if err != nil {
		// Type-checked at compile time; only panics on reflection bugs.
		panic(fmt.Sprintf("tool.SchemaOf: %v", err))
	}
	return s
}

// schemaOf generates a JSON Schema for a struct type.
func schemaOf(t reflect.Type) (litellm.Schema, error) {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("expected struct, got %s", t.Kind())
	}

	schema := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}

	props := schema["properties"].(map[string]interface{})
	var required []string

	numField := t.NumField()
	for i := range numField {
		f := t.Field(i)

		// Skip unexported
		if !f.IsExported() {
			continue
		}

		// Read json tag
		jsonTag := f.Tag.Get("json")
		if jsonTag == "" || jsonTag == "-" {
			continue
		}

		name, opts, _ := strings.Cut(jsonTag, ",")
		hasOmitempty := false
		if opts != "" {
			for _, opt := range strings.Split(opts, ",") {
				if opt == "omitempty" {
					hasOmitempty = true
					break
				}
			}
		}

		// Read jsonschema tag for description
		description := f.Tag.Get("jsonschema")

		// Build property schema
		propSchema := fieldSchema(f.Type, description)
		props[name] = propSchema

		if !hasOmitempty {
			required = append(required, name)
		}
	}

	if len(required) > 0 {
		schema["required"] = required
	}

	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshal schema: %w", err)
	}
	return litellm.Schema(raw), nil
}

// fieldSchema returns the JSON Schema representation of a field type.
func fieldSchema(t reflect.Type, description string) map[string]interface{} {
	schema := map[string]interface{}{}

	// Dereference pointer
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	switch t.Kind() {
	case reflect.String:
		schema["type"] = "string"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		schema["type"] = "integer"
	case reflect.Float32, reflect.Float64:
		schema["type"] = "number"
	case reflect.Bool:
		schema["type"] = "boolean"
	case reflect.Slice:
		schema["type"] = "array"
		elem := t.Elem()
		if elem.Kind() == reflect.Ptr {
			elem = elem.Elem()
		}
		schema["items"] = map[string]interface{}{
			"type": goTypeToJSONType(elem.Kind()),
		}
	case reflect.Map:
		schema["type"] = "object"
		schema["additionalProperties"] = true
	case reflect.Struct:
		schema["type"] = "object"
	default:
		schema["type"] = "string"
	}

	if description != "" {
		schema["description"] = description
	}

	return schema
}

func goTypeToJSONType(t reflect.Kind) string {
	switch t {
	case reflect.String:
		return "string"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Bool:
		return "boolean"
	case reflect.Slice:
		return "array"
	case reflect.Map, reflect.Struct:
		return "object"
	default:
		return "string"
	}
}
