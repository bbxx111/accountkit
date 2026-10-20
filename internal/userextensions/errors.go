package userextensions

import "errors"

// Violation contains only a declared JSON pointer and a supported keyword.
type Violation struct{ Path, Rule string }

// ValidationError deliberately does not retain or wrap validator errors.
type ValidationError struct{ Violations []Violation }

func (*ValidationError) Error() string { return "user extensions validation failed" }

var errSchema = errors.New("invalid user extensions schema configuration")
var errDocument = errors.New("invalid user extensions JSON document")

func invalid(path, rule string) error {
	return &ValidationError{Violations: []Violation{{Path: path, Rule: rule}}}
}
