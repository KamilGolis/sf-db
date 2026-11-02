package schema

import (
	"fmt"
	"strings"
)

// DDL is the SQL needed to create one object table and its indexes.
type DDL struct {
	Table       string
	CreateTable string
	Indexes     []string
}

// QuoteIdent quotes a SQLite identifier, escaping embedded double quotes.
func QuoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// CreateTable builds the table and index statements for an object. Foreign
// keys are emitted only for unambiguous references whose target is in the
// objectSet; every reference column still gets an index.
func (o Object) CreateTable(objectSet map[string]bool) (DDL, error) {
	if err := o.Validate(); err != nil {
		return DDL{}, err
	}

	if _, ok := o.Field("Id"); !ok {
		return DDL{}, fmt.Errorf("object %q has no Id field", o.Name)
	}

	fields := make([]Field, 0, len(o.Fields)+2)
	seen := make(map[string]bool, len(o.Fields)+2)
	add := func(f Field) {
		key := strings.ToLower(f.Name)

		if key == "" || seen[key] {
			return
		}

		seen[key] = true
		fields = append(fields, f)
	}

	// Id first, then the describe order. Compound fields are skipped; their
	// component fields remain.
	id, _ := o.Field("Id")
	add(id)

	for _, f := range o.Fields {
		if IsCompound(f) {
			continue
		}

		add(f)
	}

	if _, ok := o.Field("IsDeleted"); !ok {
		add(Field{Name: "IsDeleted", Type: "boolean"})
	}

	if _, ok := o.Field("SystemModstamp"); !ok {
		add(Field{Name: "SystemModstamp", Type: "datetime", Nillable: true})
	}

	columns := make([]string, 0, len(fields))

	for _, f := range fields {
		def := QuoteIdent(f.Name) + " " + SQLiteType(f)

		if strings.EqualFold(f.Name, "Id") {
			def += " PRIMARY KEY NOT NULL"
		}
		if strings.EqualFold(f.Name, "IsDeleted") {
			def += " NOT NULL DEFAULT 0"
		}
		if target, ok := ForeignKeyTarget(f, objectSet); ok {
			def += " REFERENCES " + QuoteIdent(target) + "(" + QuoteIdent("Id") + ")"
		}

		columns = append(columns, def)
	}

	table := QuoteIdent(o.Name)
	create := fmt.Sprintf("CREATE TABLE %s (\n  %s\n)", table, strings.Join(columns, ",\n  "))

	indexes := make([]string, 0, len(fields))
	indexSeen := make(map[string]bool, len(fields))
	addIndex := func(column string) {
		key := strings.ToLower(column)

		if indexSeen[key] {
			return
		}

		indexSeen[key] = true
		name := "ix_" + o.Name + "_" + column
		indexes = append(indexes, fmt.Sprintf(
			"CREATE INDEX %s ON %s(%s)",
			QuoteIdent(name), table, QuoteIdent(column),
		))
	}

	for _, f := range fields {
		if strings.EqualFold(f.Name, "SystemModstamp") || len(f.ReferenceTo) > 0 {
			addIndex(f.Name)
		}
	}

	return DDL{Table: o.Name, CreateTable: create, Indexes: indexes}, nil
}

// MetaTables returns the sidecar tables that make the clone self-describing.
func MetaTables() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS "sf_meta_object" (
  "object_name" TEXT PRIMARY KEY NOT NULL,
  "label" TEXT,
  "field_count" INTEGER NOT NULL DEFAULT 0,
  "row_count" INTEGER NOT NULL DEFAULT 0,
  "last_synced_at" TEXT
)`,
		`CREATE TABLE IF NOT EXISTS "sf_meta_field" (
  "object_name" TEXT NOT NULL,
  "field_name" TEXT NOT NULL,
  "type" TEXT,
  "length" INTEGER,
  "precision" INTEGER,
  "scale" INTEGER,
  "nillable" INTEGER,
  "reference_to" TEXT,
  "calculated" INTEGER,
  PRIMARY KEY ("object_name", "field_name")
)`,
		`CREATE TABLE IF NOT EXISTS "sf_meta_sync" (
  "object_name" TEXT PRIMARY KEY NOT NULL,
  "last_system_modstamp" TEXT,
  "last_run_at" TEXT,
  "row_count" INTEGER NOT NULL DEFAULT 0
)`,
	}
}
