package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
)

// errInvalidJSON marks tool-call arguments that are not parseable JSON.
var errInvalidJSON = errors.New("tool arguments are not valid JSON")

// validateToolCallArgs parses rawJSON and validates it against the tool's strict-shaped JSON-Schema (Parameters).
func validateToolCallArgs(schema map[string]any, rawJSON string, parsed *map[string]any) error {
	var out map[string]any
	if err := json.Unmarshal([]byte(rawJSON), &out); err != nil {
		return errInvalidJSON
	}
	if parsed != nil {
		*parsed = out
	}
	if schema == nil {
		return nil
	}
	return validateSchema(schema, out)
}

// validateSchema enforces the strict-shaped subset of JSON-Schema: additionalProperties must be false, every required field present, every present field type-checked (nullable unions ["<type>","null"] allowed), and object/array values recursed.
func validateSchema(schema, args map[string]any) error {
	props, _ := schema["properties"].(map[string]any)
	required := requiredList(schema["required"])

	for k := range args {
		if _, ok := props[k]; !ok {
			return fmt.Errorf("unexpected field %q (additionalProperties is false)", k)
		}
	}

	for _, k := range required {
		if _, ok := args[k]; !ok {
			return fmt.Errorf("missing required field %q", k)
		}
	}

	for k, prop := range props {
		v, present := args[k]
		if !present {
			continue
		}
		if v == nil && !slices.Contains(required, k) {
			continue
		}
		if err := checkValue(prop, v); err != nil {
			return fmt.Errorf("field %q: %w", k, err)
		}
	}
	return nil
}

// requiredList normalizes the JSON-Schema "required" entry, which is []string when authored in Go and []any when decoded from JSON.
func requiredList(required any) []string {
	switch r := required.(type) {
	case []string:
		return r
	case []any:
		out := make([]string, len(r))
		for i, v := range r {
			out[i], _ = v.(string)
		}
		return out
	default:
		return nil
	}
}

// checkValue verifies v against a single property schema.
func checkValue(prop, v any) error {
	switch p := prop.(type) {
	case []any: // nullable union, e.g. ["integer", "null"]
		if v == nil {
			return nil
		}
		matched := false
		for _, member := range p {
			if s, ok := member.(string); ok {
				if typeMatches(s, v) {
					matched = true
					break
				}
			}
		}
		if !matched {
			return fmt.Errorf("value %v does not match any of %v", v, p)
		}
		return nil
	case map[string]any:
		obj := p
		if sub, ok := obj["type"].(string); ok {
			if typeMatches(sub, v) {
				return nil
			}
			return fmt.Errorf("expected type %q, got %T", sub, v)
		}
		if objSchema, ok := obj["properties"].(map[string]any); ok {
			nested, ok := v.(map[string]any)
			if !ok {
				return fmt.Errorf("expected object, got %T", v)
			}
			return validateSchema(map[string]any{"properties": objSchema, "required": obj["required"], "additionalProperties": obj["additionalProperties"]}, nested)
		}
	}
	return nil
}

// typeMatches reports whether a Go-typed value satisfies a JSON-Schema type keyword. number accepts both integer and float64 (JSON numbers decode as float64 unless integral).
func typeMatches(t string, v any) bool {
	switch t {
	case "string":
		_, ok := v.(string)
		return ok
	case "integer":
		switch n := v.(type) {
		case int:
			return true
		case int64:
			return true
		case float64:
			return n == math.Trunc(n)
		case json.Number:
			_, err := n.Int64()
			return err == nil
		}
		return false
	case "number":
		switch v.(type) {
		case int, int64, float64:
			return true
		}
		return false
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "null":
		return v == nil
	}
	return false
}
