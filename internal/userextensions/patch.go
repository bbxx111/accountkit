package userextensions

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/bbxx111/accountkit/internal/extensionjson"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

type Patch struct{ value map[string]any }

var documentLimits = extensionjson.Limits{MaxBytes: 16384, MaxDepth: 8, MaxNodes: 512}

// DecodePatch performs syntax and resource checks only. Declared path checks
// intentionally wait until Apply, after the domain's state/revision checks.
func DecodePatch(raw json.RawMessage) (*Patch, error) {
	v, err := extensionjson.Decode(raw, documentLimits)
	if err != nil {
		return nil, errDocument
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, errDocument
	}
	return &Patch{value: m}, nil
}

// NormalizeStored validates generic storage invariants independently of a
// currently configured schema, so historical documents remain readable.
func NormalizeStored(raw json.RawMessage) (json.RawMessage, error) {
	value, err := stored(raw)
	if err != nil {
		return nil, err
	}
	canonical, err := extensionjson.Canonical(value)
	if err != nil {
		return nil, errDocument
	}
	return canonical, nil
}
func stored(raw []byte) (map[string]any, error) {
	// PostgreSQL's JSONB wire representation adds insignificant whitespace.
	// Bound decoding separately, then enforce the normalized document budget.
	storageLimits := documentLimits
	storageLimits.MaxBytes = 65536
	v, err := extensionjson.Decode(raw, storageLimits)
	if err != nil {
		return nil, errDocument
	}
	m, ok := v.(map[string]any)
	if !ok || hasNull(v) {
		return nil, errDocument
	}
	canonical, err := extensionjson.Canonical(v)
	if err != nil || len(canonical) > documentLimits.MaxBytes {
		return nil, errDocument
	}
	return m, nil
}
func hasNull(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case map[string]any:
		for _, item := range x {
			if hasNull(item) {
				return true
			}
		}
	case []any:
		for _, item := range x {
			if hasNull(item) {
				return true
			}
		}
	}
	return false
}

func (p *Policy) Apply(current json.RawMessage, patch *Patch) (json.RawMessage, bool, error) {
	if p == nil || patch == nil {
		return nil, false, errDocument
	}
	value, err := stored(current)
	if err != nil {
		return nil, false, err
	}
	if err := checkPaths(patch.value, p.root, "", true); err != nil {
		return nil, false, err
	}
	merged := merge(extensionjson.Clone(value), patch.value)
	canonical, err := extensionjson.Canonical(merged)
	if err != nil {
		return nil, false, invalid("", "limit")
	}
	if _, err := stored(canonical); err != nil {
		return nil, false, invalid("", "limit")
	}
	if err := p.schema.Validate(merged); err != nil {
		return nil, false, p.safeError(err)
	}
	previous, err := extensionjson.Canonical(value)
	if err != nil {
		return nil, false, errDocument
	}
	return canonical, !bytes.Equal(previous, canonical), nil
}
func merge(current, patch any) any {
	object, ok := patch.(map[string]any)
	if !ok {
		return extensionjson.Clone(patch)
	}
	target, ok := current.(map[string]any)
	if !ok {
		target = map[string]any{}
	}
	for key, v := range object {
		if v == nil {
			delete(target, key)
		} else {
			target[key] = merge(target[key], v)
		}
	}
	return target
}
func pointer(parent, key string) string {
	return parent + "/" + strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
}
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
func checkPaths(value any, d *declaration, path string, patch bool) error {
	if value == nil {
		if patch {
			return nil
		}
		return invalid(path, "type")
	}
	switch x := value.(type) {
	case map[string]any:
		if d.kind != "object" {
			return invalid(path, "type")
		}
		for _, key := range sortedKeys(x) {
			child, ok := d.properties[key]
			if !ok {
				return invalid(path, "additionalProperties")
			}
			if err := checkPaths(x[key], child, pointer(path, key), patch); err != nil {
				return err
			}
		}
	case []any:
		if d.items == nil {
			return invalid(path, "type")
		}
		for i, item := range x {
			if err := checkPaths(item, d.items, pointer(path, strconv.Itoa(i)), false); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *Policy) safeError(err error) error {
	result := &ValidationError{}
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return invalid("", "schema")
	}
	seen := map[Violation]bool{}
	var visit func(*jsonschema.ValidationError)
	visit = func(e *jsonschema.ValidationError) {
		if len(result.Violations) >= 16 {
			return
		}
		if len(e.Causes) > 0 {
			for _, child := range e.Causes {
				visit(child)
			}
			return
		}
		rule := "schema"
		if keys := e.ErrorKind.KeywordPath(); len(keys) > 0 && keywords[keys[0]] {
			rule = keys[0]
		}
		path := ""
		d := p.root
		for _, part := range e.InstanceLocation {
			if d.kind == "object" {
				child, ok := d.properties[part]
				if !ok {
					break
				}
				d = child
			} else if d.kind == "array" {
				index, err := strconv.Atoi(part)
				if err != nil || index < 0 {
					break
				}
				d = d.items
			} else {
				break
			}
			path = pointer(path, part)
		}
		violation := Violation{Path: path, Rule: rule}
		if !seen[violation] {
			seen[violation] = true
			result.Violations = append(result.Violations, violation)
		}
	}
	visit(ve)
	if len(result.Violations) == 0 {
		return invalid("", "schema")
	}
	return result
}
