// Package tool provides the ToolHandler interface, SchemaOf[T] helper, and
// dispatch map that replace the ADK functiontool system.
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
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
	Enum                 []string    `json:"enum,omitempty"`
	Minimum              *float64    `json:"minimum,omitempty"`
	Maximum              *float64    `json:"maximum,omitempty"`
	MinItems             *int        `json:"minItems,omitempty"`
	MaxItems             *int        `json:"maxItems,omitempty"`
}

// fieldOptions carries the constraints derived from a field's jsonschema tag
// family for schema generation.
type fieldOptions struct {
	Description string
	Enum        []string
	Minimum     *float64
	Maximum     *float64
	MinItems    *int
	MaxItems    *int
	ItemDesc    string
}

// SchemaOf generates a litellm.Schema (JSON Schema object) from a Go struct
// type T by reflecting its fields and reading json: and jsonschema: struct tags.
//
// Fields without a json tag are ignored. The "omitempty" json option makes the
// corresponding property not required. The jsonschema tag value becomes the
// property description.
//
// Optional constraint tags extend the schema so the LLM receives precise
// validation rules without hand-coded checks:
//
//	jsonschema_enum:"value1|value2"      allowed values (pipe-separated)
//	jsonschema_minimum:"0"               numeric lower bound
//	jsonschema_maximum:"100"             numeric upper bound
//	jsonschema_min_items:"1"             minimum array length (slice fields)
//	jsonschema_max_items:"5"             maximum array length (slice fields)
//	jsonschema_item_description:"..."    description for each slice element
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

		// Parse the jsonschema tag family for constraints (enum, min/max, items).
		fopts, err := parseFieldTags(f.Name, f.Tag)
		if err != nil {
			return nil, err
		}
		fopts.Description = description

		// Build property schema
		propSchema := fieldSchema(f.Type, fopts)
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

// fieldSchema returns the SchemaProp for a field type, applying any
// constraints declared in the field's jsonschema tag family.
func fieldSchema(t reflect.Type, o fieldOptions) SchemaProp {
	// Dereference pointer
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	var sp SchemaProp

	// json.RawMessage is []byte but should be represented as an object
	// in JSON Schema since it holds arbitrary JSON data.
	if t == reflect.TypeFor[json.RawMessage]() {
		sp.Type = "object"
		sp.AdditionalProperties = boolPtr(true)
	} else {
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
			item := SchemaProp{Type: goTypeToJSONType(elem.Kind())}
			if o.ItemDesc != "" {
				item.Description = o.ItemDesc
			}
			sp.Items = &item
			sp.MinItems = o.MinItems
			sp.MaxItems = o.MaxItems
		case reflect.Map:
			sp.Type = "object"
			sp.AdditionalProperties = boolPtr(true)
		case reflect.Struct:
			sp.Type = "object"
		default:
			sp.Type = "string"
		}
	}

	// enum applies to any type; min/max only make sense for numbers.
	if len(o.Enum) > 0 {
		sp.Enum = o.Enum
	}
	if o.Minimum != nil && (sp.Type == "integer" || sp.Type == "number") {
		sp.Minimum = o.Minimum
	}
	if o.Maximum != nil && (sp.Type == "integer" || sp.Type == "number") {
		sp.Maximum = o.Maximum
	}

	if o.Description != "" {
		sp.Description = o.Description
	}

	return sp
}

// parseFieldTags reads the jsonschema tag family from a struct field:
//
//	jsonschema:"description"
//	jsonschema_enum:"value1|value2|value3"
//	jsonschema_minimum:"0"
//	jsonschema_maximum:"100"
//	jsonschema_min_items:"1"
//	jsonschema_max_items:"5"
//	jsonschema_item_description:"per-item description"
//
// The description lives in the plain jsonschema tag (unchanged from before);
// every other tag is optional and only present when a constraint is desired.
// Enum values are pipe-separated so descriptions may keep using commas.
func parseFieldTags(fieldName string, tags reflect.StructTag) (fieldOptions, error) {
	var opts fieldOptions

	if v := tags.Get("jsonschema_enum"); v != "" {
		opts.Enum = strings.Split(v, "|")
		for i := range opts.Enum {
			opts.Enum[i] = strings.TrimSpace(opts.Enum[i])
		}
	}

	var err error
	if opts.Minimum, err = parseFloatTag(fieldName, "jsonschema_minimum", tags.Get("jsonschema_minimum")); err != nil {
		return opts, err
	}
	if opts.Maximum, err = parseFloatTag(fieldName, "jsonschema_maximum", tags.Get("jsonschema_maximum")); err != nil {
		return opts, err
	}
	if opts.MinItems, err = parseIntTag(fieldName, "jsonschema_min_items", tags.Get("jsonschema_min_items")); err != nil {
		return opts, err
	}
	if opts.MaxItems, err = parseIntTag(fieldName, "jsonschema_max_items", tags.Get("jsonschema_max_items")); err != nil {
		return opts, err
	}

	opts.ItemDesc = tags.Get("jsonschema_item_description")
	return opts, nil
}

// parseFloatTag parses a decimal value from a jsonschema tag, returning nil
// when the tag is absent.
func parseFloatTag(fieldName, tagName, value string) (*float64, error) {
	if value == "" {
		return nil, nil
	}
	f, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return nil, fmt.Errorf("tool schema for %s: invalid %s value %q: %w", fieldName, tagName, value, err)
	}
	return &f, nil
}

// parseIntTag parses an integer value from a jsonschema tag, returning nil
// when the tag is absent.
func parseIntTag(fieldName, tagName, value string) (*int, error) {
	if value == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return nil, fmt.Errorf("tool schema for %s: invalid %s value %q: %w", fieldName, tagName, value, err)
	}
	return &n, nil
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
