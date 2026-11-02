package schema

import (
	"strings"
	"testing"
)

func TestSQLiteType(t *testing.T) {
	tests := []struct {
		sfType string
		want   string
	}{
		{"id", "TEXT"},
		{"string", "TEXT"},
		{"boolean", "INTEGER"},
		{"int", "INTEGER"},
		{"long", "INTEGER"},
		{"double", "REAL"},
		{"currency", "NUMERIC"},
		{"datetime", "TEXT"},
		{"base64", "TEXT"},
		{"unknown", "TEXT"},
	}
	for _, tt := range tests {
		t.Run(tt.sfType, func(t *testing.T) {
			if got := SQLiteType(Field{Type: tt.sfType}); got != tt.want {
				t.Errorf("SQLiteType(%q) = %q, want %q", tt.sfType, got, tt.want)
			}
		})
	}
}

func TestCreateTable(t *testing.T) {
	obj := Object{
		Name:  "Account",
		Label: "Account",
		Fields: []Field{
			{Name: "Id", Type: "id"},
			{Name: "Name", Type: "string"},
			{Name: "ParentId", Type: "reference", ReferenceTo: []string{"Account"}},
			{Name: "OwnerId", Type: "reference", ReferenceTo: []string{"User", "Group"}},
			{Name: "BillingAddress", Type: "address"},
			{Name: "BillingCity", Type: "string"},
			{Name: "IsDeleted", Type: "boolean"},
			{Name: "SystemModstamp", Type: "datetime"},
		},
	}
	ddl, err := obj.CreateTable(map[string]bool{"Account": true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"Id" TEXT PRIMARY KEY NOT NULL`,
		`"ParentId" TEXT REFERENCES "Account"("Id")`,
		`"OwnerId" TEXT`,
		`"BillingCity" TEXT`,
		`CREATE INDEX "ix_Account_ParentId"`,
		`CREATE INDEX "ix_Account_OwnerId"`,
		`CREATE INDEX "ix_Account_SystemModstamp"`,
	} {
		if !strings.Contains(ddl.CreateTable+"\n"+strings.Join(ddl.Indexes, "\n"), want) {
			t.Errorf("DDL missing %q:\n%s", want, ddl.CreateTable+"\n"+strings.Join(ddl.Indexes, "\n"))
		}
	}
	if strings.Contains(ddl.CreateTable, `"BillingAddress"`) {
		t.Errorf("compound field should not become a column:\n%s", ddl.CreateTable)
	}
	if strings.Contains(ddl.CreateTable, `"OwnerId" TEXT REFERENCES`) {
		t.Errorf("polymorphic reference should not become a foreign key:\n%s", ddl.CreateTable)
	}
}

func TestCreateTableRequiresId(t *testing.T) {
	obj := Object{Name: "Broken", Fields: []Field{{Name: "Name", Type: "string"}}}
	if _, err := obj.CreateTable(nil); err == nil {
		t.Fatal("expected error for object without Id")
	}
}

func TestSelectSOQL(t *testing.T) {
	obj := Object{Name: "Contact", Fields: []Field{
		{Name: "Name", Type: "string"},
		{Name: "Id", Type: "id"},
		{Name: "AccountId", Type: "reference", ReferenceTo: []string{"Account"}},
	}}
	got := SelectSOQL(obj, ExportableFields(obj))
	want := "SELECT Id, Name, AccountId FROM Contact"
	if got != want {
		t.Errorf("SelectSOQL() = %q, want %q", got, want)
	}
}
