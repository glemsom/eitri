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
//   - ToolResult — content blocks plus optional IsError / NeedsConfirm flags
//   - error      — a Go-level error that terminates the agent loop
//     (unknown tool, context cancelled, etc.).
//     Non-nil error is returned directly; the ToolResult is ignored.
type ToolHandler interface {
	Name() string
	Description() string
	JSONSchema() litellm.Schema
	Call(ctx context.Context, args json.RawMessage) (ToolResult, error)
}

// JSONSchema is a strongly-typed JSON Schema object builder.
type JSONSchema struct {
	Type                 string                `json:"type"`
	Properties           map[string]SchemaProp `json:"properties,omitempty"`
	Required             []string              `json:"required,omitempty"`
	AdditionalProperties *bool                 `json:"additionalProperties,omitempty"`
	Items                *SchemaProp           `json:"items,omitempty"`
	Description          string                `json:"description,omitempty"`
}

// SchemaProp represents a single JSON Schema property.
type SchemaProp struct {
	Type                 string      `json:"type"`
	Description          string      `json:"description,omitempty"`
	Items                *SchemaProp `json:"items,omitempty"`
	AdditionalProperties *bool       `json:"additionalProperties,omitempty"`
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

	props := make(map[string]SchemaProp)
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

	js := objectSchema(props, required)

	raw, err := json.Marshal(js)
	if err != nil {
		return nil, fmt.Errorf("marshal schema: %w", err)
	}
	return litellm.Schema(raw), nil
}

// fieldSchema returns the SchemaProp for a field type.
func fieldSchema(t reflect.Type, description string) SchemaProp {
	// Dereference pointer
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	var sp SchemaProp

	switch t.Kind() {
	case reflect.String:
		sp.Type = "string"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		sp.Type = "integer"
	case reflect.Float32, reflect.Float64:
		sp.Type = "number"
	case reflect.Bool:
		sp.Type = "boolean"
	case reflect.Slice:
		sp.Type = "array"
		elem := t.Elem()
		if elem.Kind() == reflect.Ptr {
			elem = elem.Elem()
		}
		sp.Items = &SchemaProp{Type: goTypeToJSONType(elem.Kind())}
	case reflect.Map:
		sp.Type = "object"
		sp.AdditionalProperties = boolPtr(true)
	case reflect.Struct:
		sp.Type = "object"
	default:
		sp.Type = "string"
	}

	if description != "" {
		sp.Description = description
	}

	return sp
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

// objectSchema creates a typed JSON Schema for an object type.
func objectSchema(props map[string]SchemaProp, required []string) JSONSchema {
	return JSONSchema{
		Type:       "object",
		Properties: props,
		Required:   required,
	}
}

// boolPtr returns a pointer to a bool.
func boolPtr(b bool) *bool {
	return &b
}
