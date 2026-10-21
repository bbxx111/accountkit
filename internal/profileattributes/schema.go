package profileattributes

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var propertyPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)
var reserved = wordSet("id name state display_name create_time update_time delete_time purge_time profile_attributes")
var keywords = wordSet("$schema $id $ref $defs type properties items additionalProperties enum const required minLength maxLength pattern format minimum maximum exclusiveMinimum exclusiveMaximum multipleOf minItems maxItems uniqueItems minProperties maxProperties dependentRequired allOf anyOf oneOf not if then else title description default examples readOnly writeOnly deprecated $comment")
var formats = wordSet("date date-time email uri uuid")
var types = wordSet("object array string boolean integer number")

func wordSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, k := range strings.Fields(s) {
		m[k] = true
	}
	return m
}

type definition struct {
	path string
}
type schemaChecker struct {
	nodes, properties int
	locations         map[string]any
	definitionsList   []definition
	used              map[string]bool
}

func (c *schemaChecker) count() error {
	c.nodes++
	if c.nodes > 1024 {
		return errSchema
	}
	return nil
}

// syntax catalogs only schema-bearing locations. Annotation/enum/const values
// cannot hide a referenced schema outside the checked subset.
func (c *schemaChecker) syntax(value any, root bool) error {
	c.locations = map[string]any{}
	c.used = map[string]bool{}
	if err := c.scan(value, root, ""); err != nil {
		return err
	}
	for _, value := range c.locations {
		if s, ok := value.(map[string]any); ok {
			if ref, ok := s["$ref"].(string); ok {
				path, err := referencePath(ref)
				if err != nil {
					return err
				}
				if _, ok := c.locations[path]; !ok {
					return errSchema
				}
			}
		}
	}
	return nil
}
func (c *schemaChecker) scan(value any, root bool, path string) error {
	c.locations[path] = value
	if _, ok := value.(bool); ok {
		if root {
			return errSchema
		}
		return nil
	}
	s, ok := value.(map[string]any)
	if !ok {
		return errSchema
	}
	for key, v := range s {
		if !keywords[key] {
			return errSchema
		}
		switch key {
		case "$id":
			if !root {
				return errSchema
			}
		case "$schema":
			if v != "https://json-schema.org/draft/2020-12/schema" {
				return errSchema
			}
		case "$ref":
			ref, ok := v.(string)
			if !ok {
				return errSchema
			}
			if _, err := referencePath(ref); err != nil {
				return err
			}
			for _, k := range []string{"type", "properties", "items", "additionalProperties"} {
				if _, ok := s[k]; ok {
					return errSchema
				}
			}
		case "type":
			typ, ok := v.(string)
			if !ok || !types[typ] {
				return errSchema
			}
		case "format":
			f, ok := v.(string)
			if !ok || !formats[f] {
				return errSchema
			}
		case "pattern":
			pattern, ok := v.(string)
			if !ok {
				return errSchema
			}
			if _, err := regexp.Compile(pattern); err != nil {
				return errSchema
			}
		case "additionalProperties":
			// This subset only permits closed objects. Enforce the value even
			// in unused definitions, before a schema could hide unchecked
			// keywords, formats or references in this schema-bearing position.
			if v != false {
				return errSchema
			}
		case "properties", "$defs":
			m, ok := v.(map[string]any)
			if !ok {
				return errSchema
			}
			for name, item := range m {
				if key == "properties" && (len(name) > 64 || !propertyPattern.MatchString(name)) {
					return errSchema
				}
				childPath := pointer(pointer(path, key), name)
				if key == "$defs" {
					c.definitionsList = append(c.definitionsList, definition{path: childPath})
				}
				if err := c.scan(item, false, childPath); err != nil {
					return err
				}
			}
		case "items", "not", "if", "then", "else":
			if err := c.scan(v, false, pointer(path, key)); err != nil {
				return err
			}
		case "allOf", "anyOf", "oneOf":
			arr, ok := v.([]any)
			if !ok {
				return errSchema
			}
			for i, item := range arr {
				if err := c.scan(item, false, pointer(pointer(path, key), strconv.Itoa(i))); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func referencePath(ref string) (string, error) {
	if !strings.HasPrefix(ref, "#") {
		return "", errSchema
	}
	path, err := url.PathUnescape(ref[1:])
	if err != nil || !strings.HasPrefix(path, "/$defs/") {
		return "", errSchema
	}
	return path, nil
}
func (c *schemaChecker) resolve(ref string) (any, error) {
	path, err := referencePath(ref)
	if err != nil {
		return nil, err
	}
	v, ok := c.locations[path]
	if !ok {
		return nil, errSchema
	}
	c.used[path] = true
	return v, nil
}

func (c *schemaChecker) structure(value any, stack map[string]bool, top bool) (*declaration, error) {
	if err := c.count(); err != nil {
		return nil, err
	}
	s, ok := value.(map[string]any)
	if !ok {
		return nil, errSchema
	}
	if ref, ok := s["$ref"].(string); ok {
		if stack[ref] {
			return nil, errSchema
		}
		stack[ref] = true
		target, err := c.resolve(ref)
		if err != nil {
			return nil, err
		}
		d, err := c.structure(target, stack, top)
		if err == nil {
			err = c.constraints(s, d, stack, false)
		}
		delete(stack, ref)
		return d, err
	}
	typ, ok := s["type"].(string)
	if !ok {
		return nil, errSchema
	}
	d := &declaration{kind: typ}
	switch typ {
	case "object":
		props, ok := s["properties"].(map[string]any)
		if !ok || s["additionalProperties"] != false {
			return nil, errSchema
		}
		d.properties = make(map[string]*declaration, len(props))
		for name, item := range props {
			c.properties++
			if c.properties > 128 || (top && reserved[name]) {
				return nil, errSchema
			}
			child, err := c.structure(item, stack, false)
			if err != nil {
				return nil, err
			}
			d.properties[name] = child
		}
		if _, ok := s["items"]; ok {
			return nil, errSchema
		}
	case "array":
		item, ok := s["items"]
		if !ok {
			return nil, errSchema
		}
		var err error
		d.items, err = c.structure(item, stack, false)
		if err != nil {
			return nil, err
		}
		for _, key := range []string{"properties", "additionalProperties"} {
			if _, ok := s[key]; ok {
				return nil, errSchema
			}
		}
	default:
		for _, key := range []string{"properties", "items", "additionalProperties"} {
			if _, ok := s[key]; ok {
				return nil, errSchema
			}
		}
	}
	if err := c.constraints(s, d, stack, false); err != nil {
		return nil, err
	}
	return d, nil
}

// constraints binds fragments to the fixed directory without satisfiability
// analysis. Boolean schemas are valid only as validation fragments.
func (c *schemaChecker) constraints(value any, d *declaration, stack map[string]bool, fragment bool) error {
	if fragment {
		if err := c.count(); err != nil {
			return err
		}
	}
	if _, ok := value.(bool); ok {
		return nil
	}
	s := value.(map[string]any)
	if fragment {
		if ref, ok := s["$ref"].(string); ok {
			if stack[ref] {
				return errSchema
			}
			stack[ref] = true
			target, err := c.resolve(ref)
			if err != nil {
				return err
			}
			err = c.constraints(target, d, stack, true)
			delete(stack, ref)
			if err != nil {
				return err
			}
		}
		if typ, ok := s["type"]; ok && typ != d.kind {
			return errSchema
		}
		if props, ok := s["properties"].(map[string]any); ok {
			if d.kind != "object" {
				return errSchema
			}
			for name, item := range props {
				child, ok := d.properties[name]
				if !ok {
					return errSchema
				}
				if err := c.constraints(item, child, stack, true); err != nil {
					return err
				}
			}
		}
		if item, ok := s["items"]; ok {
			if d.items == nil {
				return errSchema
			}
			if err := c.constraints(item, d.items, stack, true); err != nil {
				return err
			}
		}
		if ap, ok := s["additionalProperties"]; ok && (d.kind != "object" || ap != false) {
			return errSchema
		}
	}
	if required, ok := s["required"].([]any); ok {
		if d.kind != "object" {
			return errSchema
		}
		for _, name := range required {
			key, ok := name.(string)
			if !ok || d.properties[key] == nil {
				return errSchema
			}
		}
	}
	if deps, ok := s["dependentRequired"].(map[string]any); ok {
		if d.kind != "object" {
			return errSchema
		}
		for name, v := range deps {
			if d.properties[name] == nil {
				return errSchema
			}
			arr, ok := v.([]any)
			if !ok {
				return errSchema
			}
			for _, item := range arr {
				key, ok := item.(string)
				if !ok || d.properties[key] == nil {
					return errSchema
				}
			}
		}
	}
	for _, key := range []string{"allOf", "anyOf", "oneOf"} {
		if arr, ok := s[key].([]any); ok {
			for _, item := range arr {
				if err := c.constraints(item, d, stack, true); err != nil {
					return err
				}
			}
		}
	}
	for _, key := range []string{"not", "if", "then", "else"} {
		if item, ok := s[key]; ok {
			if err := c.constraints(item, d, stack, true); err != nil {
				return err
			}
		}
	}
	return nil
}

// Actual consuming positions are expanded and billed by structure/constraints.
// The remaining definition graph is checked once per schema location, with a
// separate DFS state so map order cannot cause repeated safety-scan billing.
func (c *schemaChecker) definitions(_ map[string]any) error {
	state := map[string]uint8{}
	for _, def := range c.definitionsList {
		if err := c.safety(def.path, state); err != nil {
			return err
		}
	}
	return nil
}
func (c *schemaChecker) safety(path string, state map[string]uint8) error {
	if c.used[path] || state[path] == 2 {
		return nil
	}
	if state[path] == 1 {
		return errSchema
	}
	state[path] = 1
	if err := c.count(); err != nil {
		return err
	}
	s, ok := c.locations[path].(map[string]any)
	if !ok {
		state[path] = 2
		return nil
	}
	if ref, ok := s["$ref"].(string); ok {
		targetPath, err := referencePath(ref)
		if err != nil {
			return err
		}
		if err := c.safety(targetPath, state); err != nil {
			return err
		}
	}
	for key, v := range s {
		switch key {
		case "properties":
			for name := range v.(map[string]any) {
				if err := c.safety(pointer(pointer(path, key), name), state); err != nil {
					return err
				}
			}
		case "items", "not", "if", "then", "else":
			if err := c.safety(pointer(path, key), state); err != nil {
				return err
			}
		case "allOf", "anyOf", "oneOf":
			for i := range v.([]any) {
				if err := c.safety(pointer(pointer(path, key), strconv.Itoa(i)), state); err != nil {
					return err
				}
			}
		}
	}
	state[path] = 2
	return nil
}
