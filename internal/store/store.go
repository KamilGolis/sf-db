// Package store writes the clone into a SQLite database.
package store

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/KamilGolis/sf-db/internal/schema"
)

const batchSize = 5000

// DB wraps the SQLite connection.
type DB struct {
	db   *sql.DB
	path string
}

// Open opens or creates a SQLite database. The caller owns Close.
func Open(path string) (*DB, error) {
	if path == "" {
		return nil, fmt.Errorf("database path is empty")
	}

	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// One connection keeps the per-connection pragmas in force and serializes
	// writes; the clone is a single-writer workload.
	db.SetMaxOpenConns(1)

	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=OFF",
		"PRAGMA foreign_keys=OFF",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("%s: %w", pragma, err)
		}
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	return &DB{db: db, path: path}, nil
}

// Close closes the database.
func (d *DB) Close() error { return d.db.Close() }

// SQL exposes the underlying handle for read-only queries and tests.
func (d *DB) SQL() *sql.DB { return d.db }

// CreateSchema executes DDL statements in one transaction.
func (d *DB) CreateSchema(ctx context.Context, statements []string) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	defer tx.Rollback()

	for _, stmt := range statements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("create schema: %w\n%s", err, stmt)
		}
	}

	return tx.Commit()
}

// LoadCSV streams a Bulk API CSV file into a table. Empty fields become NULL;
// Bulk API 2.0 cannot distinguish NULL from an empty string.
func (d *DB) LoadCSV(ctx context.Context, table string, fields []schema.Field, r io.Reader) (int64, error) {
	reader := csv.NewReader(r)
	reader.LazyQuotes = true

	header, err := reader.Read()
	if err != nil {
		return 0, fmt.Errorf("read %s CSV header: %w", table, err)
	}
	if len(header) == 0 {
		return 0, fmt.Errorf("read %s CSV header: empty", table)
	}

	header[0] = strings.TrimPrefix(header[0], "\ufeff")
	reader.FieldsPerRecord = len(header)
	index := make(map[string]int, len(header))

	for i, name := range header {
		index[name] = i
	}

	names := make([]string, 0, len(fields))
	types := make([]string, 0, len(fields))
	positions := make([]int, 0, len(fields))

	for _, f := range fields {
		pos, ok := index[f.Name]

		if !ok {
			return 0, fmt.Errorf("CSV for %s is missing column %q", table, f.Name)
		}

		names = append(names, schema.QuoteIdent(f.Name))
		types = append(types, schema.SQLiteType(f))
		positions = append(positions, pos)
	}

	if len(names) == 0 {
		return 0, fmt.Errorf("CSV for %s has no loadable columns", table)
	}

	placeholders := make([]string, len(names))
	for i := range placeholders {
		placeholders[i] = "?"
	}

	insert := fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (%s)",
		schema.QuoteIdent(table), strings.Join(names, ", "), strings.Join(placeholders, ", "),
	)

	var (
		total int64
		tx    *sql.Tx
		stmt  *sql.Stmt
	)

	begin := func() error {
		var err error
		tx, err = d.db.BeginTx(ctx, nil)

		if err != nil {
			return err
		}

		stmt, err = tx.PrepareContext(ctx, insert)
		if err != nil {
			_ = tx.Rollback()
			tx = nil
			return fmt.Errorf("prepare insert into %s: %w", table, err)
		}

		return nil
	}

	commit := func() error {
		if stmt != nil {
			if err := stmt.Close(); err != nil {
				return err
			}

			stmt = nil
		}
		if tx == nil {
			return nil
		}
		if err := tx.Commit(); err != nil {
			return err
		}

		tx = nil

		return nil
	}

	if err := begin(); err != nil {
		return 0, err
	}

	defer func() {
		if stmt != nil {
			_ = stmt.Close()
		}
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	values := make([]any, len(names))

	for {
		record, err := reader.Read()

		if err == io.EOF {
			break
		}
		if err != nil {
			return total, fmt.Errorf("read %s CSV row %d: %w", table, total+1, err)
		}

		for i, pos := range positions {
			values[i] = convertValue(record[pos], types[i])
		}

		if _, err := stmt.ExecContext(ctx, values...); err != nil {
			return total, fmt.Errorf("insert %s row %d: %w", table, total+1, err)
		}

		total++

		if total%batchSize == 0 {
			if err := commit(); err != nil {
				return total, err
			}
			if err := begin(); err != nil {
				return total, err
			}
		}
	}

	if err := commit(); err != nil {
		return total, err
	}

	return total, nil
}

// LoadRecords inserts one REST query page. REST preserves NULL versus empty
// string, unlike the Bulk API CSV path.
func (d *DB) LoadRecords(ctx context.Context, table string, fields []schema.Field, records []map[string]any) (int64, error) {
	if len(records) == 0 {
		return 0, nil
	}

	names := make([]string, 0, len(fields))
	types := make([]string, 0, len(fields))

	for _, f := range fields {
		names = append(names, schema.QuoteIdent(f.Name))
		types = append(types, schema.SQLiteType(f))
	}

	if len(names) == 0 {
		return 0, fmt.Errorf("table %s has no loadable columns", table)
	}

	placeholders := make([]string, len(names))

	for i := range placeholders {
		placeholders[i] = "?"
	}

	insert := fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (%s)",
		schema.QuoteIdent(table), strings.Join(names, ", "), strings.Join(placeholders, ", "),
	)

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}

	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, insert)

	if err != nil {
		return 0, fmt.Errorf("prepare insert into %s: %w", table, err)
	}

	defer stmt.Close()

	values := make([]any, len(names))
	var total int64

	for _, record := range records {
		for i, f := range fields {
			values[i] = convertJSONValue(record[f.Name], types[i])
		}

		if _, err := stmt.ExecContext(ctx, values...); err != nil {
			return total, fmt.Errorf("insert %s row %d: %w", table, total+1, err)
		}

		total++
	}

	if err := stmt.Close(); err != nil {
		return total, err
	}

	if err := tx.Commit(); err != nil {
		return total, err
	}

	return total, nil
}

// Finalize restores the safe synchronous mode and checkpoints the WAL before
// the file is published.
func (d *DB) Finalize(ctx context.Context) error {
	if _, err := d.db.ExecContext(ctx, "PRAGMA synchronous=NORMAL"); err != nil {
		return fmt.Errorf("restore synchronous mode: %w", err)
	}
	if _, err := d.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return fmt.Errorf("checkpoint WAL: %w", err)
	}

	return nil
}

// ClearTable removes rows committed before a failed load.
func (d *DB) ClearTable(ctx context.Context, table string) error {
	if _, err := d.db.ExecContext(ctx, "DELETE FROM "+schema.QuoteIdent(table)); err != nil {
		return fmt.Errorf("clear %s: %w", table, err)
	}

	return nil
}

// CreateIndexes executes index DDL after the bulk load.
func (d *DB) CreateIndexes(ctx context.Context, statements []string) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	defer tx.Rollback()
	
	for _, stmt := range statements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("create index: %w\n%s", err, stmt)
		}
	}

	return tx.Commit()
}

// RecordObject writes the object, field, and sync metadata.
func (d *DB) RecordObject(ctx context.Context, object schema.Object, rows int64) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)

	if _, err := tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO "sf_meta_object"
		 ("object_name", "label", "field_count", "row_count", "last_synced_at")
		 VALUES (?, ?, ?, ?, ?)`,
		object.Name, object.Label, len(object.Fields), rows, now,
	); err != nil {
		return fmt.Errorf("record object meta: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM "sf_meta_field" WHERE "object_name" = ?`, object.Name,
	); err != nil {
		return fmt.Errorf("clear field meta: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO "sf_meta_field"
		 ("object_name", "field_name", "type", "length", "precision", "scale",
		  "nillable", "reference_to", "calculated")
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
	)

	if err != nil {
		return fmt.Errorf("prepare field meta: %w", err)
	}

	defer stmt.Close()

	for _, f := range object.Fields {
		if _, err := stmt.ExecContext(ctx,
			object.Name, f.Name, f.Type, f.Length, f.Precision, f.Scale,
			boolInt(f.Nillable), strings.Join(f.ReferenceTo, ","), boolInt(f.Calculated),
		); err != nil {
			return fmt.Errorf("record field %s.%s: %w", object.Name, f.Name, err)
		}
	}

	var watermark sql.NullString
	_ = tx.QueryRowContext(ctx,
		`SELECT MAX("SystemModstamp") FROM `+schema.QuoteIdent(object.Name),
	).Scan(&watermark)

	if _, err := tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO "sf_meta_sync"
		 ("object_name", "last_system_modstamp", "last_run_at", "row_count")
		 VALUES (?, ?, ?, ?)`,
		object.Name, watermark, now, rows,
	); err != nil {
		return fmt.Errorf("record sync meta: %w", err)
	}

	return tx.Commit()
}

func convertJSONValue(v any, sqlType string) any {
	if v == nil {
		return nil
	}

	switch sqlType {
	case "INTEGER":
		switch x := v.(type) {
		case json.Number:
			if n, err := x.Int64(); err == nil {
				return n
			}
			return x.String()
		case float64:
			return int64(x)
		case bool:
			if x {
				return int64(1)
			}
			return int64(0)
		case string:
			switch strings.ToLower(x) {
			case "true":
				return int64(1)
			case "false":
				return int64(0)
			}
			if n, err := strconv.ParseInt(x, 10, 64); err == nil {
				return n
			}
			return x
		}
	case "REAL", "NUMERIC":
		switch x := v.(type) {
		case json.Number:
			if n, err := x.Float64(); err == nil {
				return n
			}
			return x.String()
		case float64:
			return x
		case string:
			if n, err := strconv.ParseFloat(x, 64); err == nil {
				return n
			}
			return x
		}
	case "TEXT":
		switch x := v.(type) {
		case string:
			return x
		case json.Number:
			return x.String()
		case bool:
			return strconv.FormatBool(x)
		case float64:
			return strconv.FormatFloat(x, 'f', -1, 64)
		default:
			b, err := json.Marshal(x)
			if err != nil {
				return fmt.Sprint(x)
			}
			return string(b)
		}
	}
	return v
}

func convertValue(raw, sqlType string) any {
	if raw == "" {
		return nil
	}

	switch sqlType {
	case "INTEGER":
		switch strings.ToLower(raw) {
		case "true":
			return int64(1)
		case "false":
			return int64(0)
		}
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return v
		}
		return raw
	case "REAL", "NUMERIC":
		if v, err := strconv.ParseFloat(raw, 64); err == nil {
			return v
		}
		return raw
	default:
		return raw
	}
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
