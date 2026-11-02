// Package schema holds the Salesforce object model and the SQLite DDL derived
// from it. It has no I/O and no knowledge of the sf CLI.
package schema

import (
	"fmt"
	"strings"
)

// Field is a Salesforce field reduced to what the clone needs.
type Field struct {
	Name                string
	Label               string
	Type                string
	Length              int
	Precision           int
	Scale               int
	Nillable            bool
	Unique              bool
	ReferenceTo         []string
	RelationshipName    string
	Calculated          bool
	CompoundFieldName   string
	AutoNumber          bool
	HTMLFormatted       bool
	DeprecatedAndHidden bool
}

// Object is a Salesforce object and its fields.
type Object struct {
	Name   string
	Label  string
	Fields []Field
}

// Field returns the named field, case-insensitively.
func (o Object) Field(name string) (Field, bool) {
	for _, f := range o.Fields {
		if strings.EqualFold(f.Name, name) {
			return f, true
		}
	}

	return Field{}, false
}

// Validate checks the minimum needed to build a table.
func (o Object) Validate() error {
	if strings.TrimSpace(o.Name) == "" {
		return fmt.Errorf("object name is empty")
	}
	if len(o.Fields) == 0 {
		return fmt.Errorf("object %q has no fields", o.Name)
	}

	return nil
}
