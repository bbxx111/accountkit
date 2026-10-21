// Package profileattributes implements immutable bounded schema policies and patches.
package profileattributes

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/bbxx111/accountkit/internal/boundedjson"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

type Policy struct {
	version, fingerprint string
	schema               *jsonschema.Schema
	root                 *declaration
}
type declaration struct {
	kind       string
	properties map[string]*declaration
	items      *declaration
}

var versionPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

type offlineLoader struct{}

func (offlineLoader) Load(string) (any, error) { return nil, errSchema }

// Compile validates the supported schema subset before invoking the offline
// Draft 2020-12 compiler. No caller-owned bytes or mutable trees are retained.
func Compile(version string, raw json.RawMessage) (*Policy, error) {
	if !versionPattern.MatchString(version) {
		return nil, errSchema
	}
	value, err := boundedjson.Decode(raw, boundedjson.Limits{MaxBytes: 65536, MaxDepth: 32})
	if err != nil {
		return nil, errSchema
	}
	root, ok := value.(map[string]any)
	if !ok || root["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		return nil, errSchema
	}
	checker := schemaChecker{}
	if err := checker.syntax(root, true); err != nil {
		return nil, errSchema
	}
	directory, err := checker.structure(root, map[string]bool{}, true)
	if err != nil || directory.kind != "object" {
		return nil, errSchema
	}
	// Definitions unused by the main tree still must obey the supported subset
	// and cannot conceal cyclic references or unsupported constraints.
	if err := checker.definitions(root); err != nil {
		return nil, errSchema
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	c.UseLoader(offlineLoader{})
	c.UseRegexpEngine(func(s string) (jsonschema.Regexp, error) { return regexp.Compile(s) })
	const location = "urn:accountkit:user-profile-attributes"
	if err := c.AddResource(location, value); err != nil {
		return nil, errSchema
	}
	compiled, err := c.Compile(location)
	if err != nil {
		return nil, errSchema
	}
	if err := compiled.Validate(map[string]any{}); err != nil {
		return nil, errSchema
	}
	canonical, err := boundedjson.Canonical(value)
	if err != nil {
		return nil, errSchema
	}
	return &Policy{version: version, fingerprint: fmt.Sprintf("%x", sha256.Sum256(canonical)), schema: compiled, root: directory}, nil
}
func (p *Policy) Version() string     { return p.version }
func (p *Policy) Fingerprint() string { return p.fingerprint }
