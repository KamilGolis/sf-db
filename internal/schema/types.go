package schema

import "strings"

// SQLiteType maps a Salesforce field type to a SQLite declared type.
//
// SQLite uses type affinity rather than strict types, so these are hints for
// the IDE and for readers, not constraints.
func SQLiteType(f Field) string {
	switch strings.ToLower(f.Type) {
	case "id", "string", "textarea", "email", "phone", "url", "picklist",
		"multipicklist", "combobox", "reference", "encryptedstring", "anytype",
		"json", "complexvalue", "floatarray", "textarray",
		"datacategorygroupreference":
		return "TEXT"
	case "boolean", "int", "long":
		return "INTEGER"
	case "double", "percent":
		return "REAL"
	case "currency":
		return "NUMERIC"
	case "date", "datetime", "time":
		return "TEXT"
	case "base64":
		// The Bulk API export carries base64 text. Decode to BLOB later if needed.
		return "TEXT"
	default:
		return "TEXT"
	}
}

// IsCompound reports whether the field is an address or location compound
// field whose components are exported separately.
func IsCompound(f Field) bool {
	switch strings.ToLower(f.Type) {
	case "address", "location":
		return true
	default:
		return false
	}
}

// ExportableFields returns the fields that can be selected as plain columns.
// Compound fields and deprecated fields are skipped.
func ExportableFields(o Object) []Field {
	out := make([]Field, 0, len(o.Fields))
	seen := make(map[string]bool, len(o.Fields))
	for _, f := range o.Fields {
		if f.DeprecatedAndHidden || IsCompound(f) {
			continue
		}
		key := strings.ToLower(f.Name)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, f)
	}
	return out
}

// ColumnNames returns field names in order.
func ColumnNames(fields []Field) []string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, f.Name)
	}
	return out
}

// ForeignKeyTarget returns the single referenced object when the reference is
// unambiguous and the target is in the objectSet.
func ForeignKeyTarget(f Field, objectSet map[string]bool) (string, bool) {
	if len(f.ReferenceTo) != 1 {
		return "", false
	}
	target := f.ReferenceTo[0]
	if !objectSet[target] {
		return "", false
	}
	return target, true
}

// SelectSOQL builds the bulk-export query. Id is always first.
func SelectSOQL(o Object, fields []Field) string {
	names := make([]string, 0, len(fields)+1)
	seen := make(map[string]bool, len(fields)+1)
	add := func(name string) {
		key := strings.ToLower(name)
		if name == "" || seen[key] {
			return
		}
		seen[key] = true
		names = append(names, name)
	}
	add("Id")
	for _, f := range fields {
		add(f.Name)
	}
	return "SELECT " + strings.Join(names, ", ") + " FROM " + o.Name
}
