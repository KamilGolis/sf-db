package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/KamilGolis/sf-db/internal/schema"
)

func TestLoadCSVAndRecordObject(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir + "/clone.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	obj := schema.Object{Name: "Account", Label: "Account", Fields: []schema.Field{
		{Name: "Id", Type: "id"},
		{Name: "Name", Type: "string"},
		{Name: "Active", Type: "boolean"},
		{Name: "Amount", Type: "currency"},
		{Name: "CreatedAt", Type: "date"},
	}}
	ddl, err := obj.CreateTable(map[string]bool{"Account": true})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateSchema(context.Background(), append(schema.MetaTables(), ddl.CreateTable)); err != nil {
		t.Fatal(err)
	}
	fields := schema.ExportableFields(obj)
	csvData := "Id,Name,Active,Amount,CreatedAt\n001,Acme,true,12.50,2026-01-02\n002,,false,,\n"
	rows, err := db.LoadCSV(context.Background(), obj.Name, fields, strings.NewReader(csvData))
	if err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("LoadCSV rows = %d, want 2", rows)
	}
	if err := db.RecordObject(context.Background(), obj, rows); err != nil {
		t.Fatal(err)
	}

	var name, amount, createdAt any
	var active int
	if err := db.db.QueryRow(`SELECT "Name", "Active", "Amount", "CreatedAt" FROM "Account" WHERE "Id" = '001'`).
		Scan(&name, &active, &amount, &createdAt); err != nil {
		t.Fatal(err)
	}
	if name != "Acme" || active != 1 {
		t.Fatalf("unexpected row: name=%v active=%v", name, active)
	}
	if amount != 12.5 {
		t.Fatalf("amount = %v, want 12.5", amount)
	}
	if createdAt != "2026-01-02" {
		t.Fatalf("createdAt = %v", createdAt)
	}

	var nullName any
	if err := db.db.QueryRow(`SELECT "Name" FROM "Account" WHERE "Id" = '002'`).Scan(&nullName); err != nil {
		t.Fatal(err)
	}
	if nullName != nil {
		t.Fatalf("empty CSV field should be NULL, got %v", nullName)
	}

	var rowCount int64
	if err := db.db.QueryRow(`SELECT "row_count" FROM "sf_meta_object" WHERE "object_name" = 'Account'`).Scan(&rowCount); err != nil {
		t.Fatal(err)
	}
	if rowCount != 2 {
		t.Fatalf("meta row_count = %d, want 2", rowCount)
	}
}

func TestLoadRecordsPreservesNullAndEmpty(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir + "/clone.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	obj := schema.Object{Name: "Account", Label: "Account", Fields: []schema.Field{
		{Name: "Id", Type: "id"},
		{Name: "Name", Type: "string"},
		{Name: "Active", Type: "boolean"},
		{Name: "Amount", Type: "currency"},
		{Name: "CreatedAt", Type: "date"},
	}}
	ddl, err := obj.CreateTable(map[string]bool{"Account": true})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateSchema(context.Background(), append(schema.MetaTables(), ddl.CreateTable)); err != nil {
		t.Fatal(err)
	}

	fields := schema.ExportableFields(obj)
	records := []map[string]any{
		{"Id": "001", "Name": "Acme", "Active": true, "Amount": json.Number("12.50"), "CreatedAt": "2026-01-02"},
		{"Id": "002", "Name": "", "Active": false, "Amount": nil, "CreatedAt": nil},
	}
	rows, err := db.LoadRecords(context.Background(), obj.Name, fields, records)
	if err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("rows = %d, want 2", rows)
	}

	var name string
	var amount any
	if err := db.db.QueryRow(`SELECT "Name", "Amount" FROM "Account" WHERE "Id" = '001'`).Scan(&name, &amount); err != nil {
		t.Fatal(err)
	}
	if name != "Acme" || amount != 12.5 {
		t.Fatalf("unexpected row: name=%q amount=%v", name, amount)
	}

	var emptyName string
	var nullAmount any
	if err := db.db.QueryRow(`SELECT "Name", "Amount" FROM "Account" WHERE "Id" = '002'`).Scan(&emptyName, &nullAmount); err != nil {
		t.Fatal(err)
	}
	if emptyName != "" {
		t.Fatalf("empty string should stay empty, got %q", emptyName)
	}
	if nullAmount != nil {
		t.Fatalf("nil should stay NULL, got %v", nullAmount)
	}
}
