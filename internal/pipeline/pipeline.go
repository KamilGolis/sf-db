// Package pipeline orchestrates describe, query, and SQLite load.
package pipeline

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/KamilGolis/sf-db/internal/schema"
)

const (
	EngineAuto = "auto"
	EngineREST = "rest"
	EngineBulk = "bulk"

	DefaultWorkers       = 4
	DefaultBulkThreshold = 50_000
)

// Source is the Salesforce side of the pipeline.
type Source interface {
	Describe(ctx context.Context, object string) (schema.Object, error)
	Count(ctx context.Context, object string) (int, error)
	Query(ctx context.Context, soql string, allRows bool, onPage func([]map[string]any) error) error
	BulkQuery(ctx context.Context, soql string, allRows bool, outputFile string) (int64, error)
}

// Sink is the SQLite side of the pipeline.
type Sink interface {
	CreateSchema(ctx context.Context, statements []string) error
	LoadCSV(ctx context.Context, table string, fields []schema.Field, r io.Reader) (int64, error)
	LoadRecords(ctx context.Context, table string, fields []schema.Field, records []map[string]any) (int64, error)
	CreateIndexes(ctx context.Context, statements []string) error
	ClearTable(ctx context.Context, table string) error
	RecordObject(ctx context.Context, object schema.Object, rows int64) error
	Finalize(ctx context.Context) error
}

// ProgressFunc receives a stage name and, for measurable stages, done/total.
// total == 0 marks an indeterminate stage.
type ProgressFunc func(stage string, done, total int)

// Service runs the clone pipeline.
type Service struct {
	Source  Source
	Sink    Sink
	Logger  *slog.Logger
	TempDir string
}

// Options controls one clone run.
type Options struct {
	Org           string
	Objects       []string
	AllRows       bool
	Workers       int
	Engine        string
	BulkThreshold int
	Strict        bool
	StrictObjects map[string]bool
	Progress      ProgressFunc
}

// Result reports what was loaded.
type Result struct {
	Rows    map[string]int64
	Skipped []string
}

// Run describes every object, creates the schema, queries each object, and
// loads it. It stops at the first error; the caller owns the database file.
func (s *Service) Run(ctx context.Context, opts Options) (Result, error) {
	result := Result{Rows: make(map[string]int64, len(opts.Objects))}

	if strings.TrimSpace(opts.Org) == "" {
		return result, fmt.Errorf("org is required")
	}

	if len(opts.Objects) == 0 {
		return result, fmt.Errorf("at least one object is required")
	}

	if s.Source == nil || s.Sink == nil {
		return result, fmt.Errorf("source and sink are required")
	}

	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}

	workers := opts.Workers
	if workers <= 0 {
		workers = DefaultWorkers
	}
	if workers > 32 {
		return result, fmt.Errorf("workers must be between 1 and 32")
	}

	engine := strings.ToLower(strings.TrimSpace(opts.Engine))
	if engine == "" {
		engine = EngineAuto
	}

	switch engine {
	case EngineAuto, EngineREST, EngineBulk:
	default:
		return result, fmt.Errorf("unknown engine %q (want auto, rest, or bulk)", opts.Engine)
	}

	if opts.BulkThreshold < 0 {
		return result, fmt.Errorf("bulk threshold must not be negative")
	}

	threshold := opts.BulkThreshold
	if threshold == 0 {
		threshold = DefaultBulkThreshold
	}

	objects := uniqueObjects(opts.Objects)
	if len(objects) == 0 {
		return result, fmt.Errorf("at least one object is required")
	}

	described, skipped, err := DescribeAll(ctx, s.Source, objects, workers, DescribeOptions{
		Strict:        opts.Strict,
		StrictObjects: opts.StrictObjects,
		Logger:        logger,
		Progress:      opts.Progress,
	})

	if err != nil {
		return result, err
	}

	if len(described) == 0 {
		return result, fmt.Errorf("no objects could be described (%d skipped)", len(skipped))
	}

	result.Skipped = append(result.Skipped, skipped...)

	tables, err := PlanTables(described)

	if err != nil {
		return result, err
	}

	statements := schema.MetaTables()
	var indexes []string

	for _, ddl := range tables {
		statements = append(statements, ddl.CreateTable)
		indexes = append(indexes, ddl.Indexes...)
	}

	logger.Info("creating schema", "tables", len(tables))

	if opts.Progress != nil {
		opts.Progress("Creating schema", 0, 0)
	}

	if err := s.Sink.CreateSchema(ctx, statements); err != nil {
		return result, fmt.Errorf("create schema: %w", err)
	}

	rows := make([]int64, len(described))
	loaded := make([]bool, len(described))
	var skippedMu sync.Mutex
	
	logger.Info("loading objects", "count", len(described), "workers", workers)

	if opts.Progress != nil {
		opts.Progress("Loading objects", 0, len(described))
	}

	loadStep := progressInterval(len(described))
	var loadDone atomic.Int64

	err = runPool(ctx, workers, len(described), func(ctx context.Context, i int) error {
		defer func() {
			done := int(loadDone.Add(1))

			if opts.Progress != nil {
				opts.Progress("Loading objects", done, len(described))
			}

			if done == len(described) || done%loadStep == 0 {
				logger.Info("load progress", "done", done, "total", len(described))
			}
		}()

		n, err := s.loadObject(ctx, logger, described[i], engine, threshold, opts.AllRows)

		if err != nil {
			if isStrict(described[i].Name, opts) {
				return err
			}

			logger.Warn("skipping object", "object", described[i].Name, "error", err)

			if clearErr := s.Sink.ClearTable(ctx, described[i].Name); clearErr != nil {
				return fmt.Errorf("clear partial rows for %s: %w", described[i].Name, clearErr)
			}

			skippedMu.Lock()
			result.Skipped = append(result.Skipped, described[i].Name)
			skippedMu.Unlock()

			return nil
		}

		rows[i] = n
		loaded[i] = true

		return nil
	})

	if err != nil {
		return result, err
	}

	for i, obj := range described {
		if loaded[i] {
			result.Rows[obj.Name] = rows[i]
		}
	}

	if len(result.Rows) == 0 {
		return result, fmt.Errorf("no objects were loaded (%d skipped)", len(result.Skipped))
	}

	result.Skipped = uniqueSorted(result.Skipped)

	logger.Info("creating indexes", "indexes", len(indexes))

	if opts.Progress != nil {
		opts.Progress("Creating indexes", 0, 0)
	}

	if err := s.Sink.CreateIndexes(ctx, indexes); err != nil {
		return result, fmt.Errorf("create indexes: %w", err)
	}

	if opts.Progress != nil {
		opts.Progress("Finalizing database", 0, 0)
	}

	if err := s.Sink.Finalize(ctx); err != nil {
		return result, fmt.Errorf("finalize database: %w", err)
	}

	return result, nil
}

func (s *Service) loadObject(ctx context.Context, logger *slog.Logger, obj schema.Object, engine string, threshold int, allRows bool) (int64, error) {
	fields := schema.ExportableFields(obj)
	if len(fields) == 0 {
		return 0, fmt.Errorf("object %s has no exportable fields", obj.Name)
	}

	soql := schema.SelectSOQL(obj, fields)

	selected, err := s.chooseEngine(ctx, logger, obj, engine, threshold)
	if err != nil {
		return 0, err
	}

	var (
		rows    int64
		loadErr error
	)

	switch selected {
	case EngineBulk:
		rows, loadErr = s.loadBulk(ctx, logger, obj, fields, soql, allRows)
	default:
		rows, loadErr = s.loadREST(ctx, logger, obj, fields, soql, allRows)
	}

	if loadErr != nil {
		return 0, fmt.Errorf("load %s: %w", obj.Name, loadErr)
	}

	if err := s.Sink.RecordObject(ctx, obj, rows); err != nil {
		return 0, fmt.Errorf("record metadata for %s: %w", obj.Name, err)
	}

	logger.Debug("loaded", "object", obj.Name, "rows", rows, "engine", selected)

	return rows, nil
}

func (s *Service) chooseEngine(ctx context.Context, logger *slog.Logger, obj schema.Object, engine string, threshold int) (string, error) {
	if engine != EngineAuto {
		return engine, nil
	}

	count, err := s.Source.Count(ctx, obj.Name)
	if err != nil {
		logger.Warn("count failed; using REST", "object", obj.Name, "error", err)
		return EngineREST, nil
	}
	if count > threshold {
		logger.Info("using bulk", "object", obj.Name, "count", count, "threshold", threshold)
		return EngineBulk, nil
	}

	return EngineREST, nil
}

func (s *Service) loadREST(ctx context.Context, logger *slog.Logger, obj schema.Object, fields []schema.Field, soql string, allRows bool) (int64, error) {
	var total int64

	err := s.Source.Query(ctx, soql, allRows, func(page []map[string]any) error {
		n, err := s.Sink.LoadRecords(ctx, obj.Name, fields, page)
		total += n

		return err
	})

	if err != nil {
		return total, err
	}
	
	return total, nil
}

func (s *Service) loadBulk(ctx context.Context, logger *slog.Logger, obj schema.Object, fields []schema.Field, soql string, allRows bool) (int64, error) {
	tempDir := s.TempDir
	if tempDir == "" {
		tempDir = os.TempDir()
	}

	tmp, err := os.CreateTemp(tempDir, "sfdb-*.csv")

	if err != nil {
		return 0, fmt.Errorf("create temp file: %w", err)
	}

	tmpName := tmp.Name()
	if err := tmp.Close(); err != nil {
		return 0, fmt.Errorf("close temp file: %w", err)
	}

	defer os.Remove(tmpName)

	logger.Info("bulk exporting", "object", obj.Name, "fields", len(fields))

	jobRows, err := s.Source.BulkQuery(ctx, soql, allRows, tmpName)
	if err != nil {
		return 0, err
	}

	if info, err := os.Stat(tmpName); err == nil && info.Size() == 0 {
		return 0, nil
	}

	f, err := os.Open(tmpName)
	if err != nil {
		return 0, fmt.Errorf("open bulk export: %w", err)
	}

	rows, err := s.Sink.LoadCSV(ctx, obj.Name, fields, f)
	closeErr := f.Close()

	if err != nil {
		return 0, err
	}
	if closeErr != nil {
		return 0, fmt.Errorf("close bulk export: %w", closeErr)
	}
	if jobRows > 0 && rows != jobRows {
		return 0, fmt.Errorf("bulk row count mismatch: job=%d csv=%d", jobRows, rows)
	}

	return rows, nil
}

// PlanTables builds the table and index DDL for a described object set.
func PlanTables(described []schema.Object) ([]schema.DDL, error) {
	objectSet := make(map[string]bool, len(described))

	for _, obj := range described {
		objectSet[obj.Name] = true
	}

	tables := make([]schema.DDL, 0, len(described))

	for _, obj := range described {
		ddl, err := obj.CreateTable(objectSet)

		if err != nil {
			return nil, fmt.Errorf("build table for %s: %w", obj.Name, err)
		}

		tables = append(tables, ddl)
	}

	return tables, nil
}

// DescribeOptions controls describe failure handling.
type DescribeOptions struct {
	Strict        bool
	StrictObjects map[string]bool
	Logger        *slog.Logger
	Progress      ProgressFunc
}

// DescribeAll describes objects in parallel and returns them in input order.
// Selector-derived failures are skipped unless Strict or StrictObjects marks
// the name. A failure for a strict object aborts the run.
func DescribeAll(ctx context.Context, source Source, names []string, workers int, opts DescribeOptions) ([]schema.Object, []string, error) {
	names = uniqueObjects(names)

	if workers <= 0 {
		workers = DefaultWorkers
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Info("describing objects", "count", len(names), "workers", workers)

	if opts.Progress != nil {
		opts.Progress("Describing objects", 0, len(names))
	}

	out := make([]schema.Object, len(names))

	var (
		skipped   []string
		skippedMu sync.Mutex
	)

	describeStep := progressInterval(len(names))
	var describeDone atomic.Int64

	err := runPool(ctx, workers, len(names), func(ctx context.Context, i int) error {
		defer func() {
			done := int(describeDone.Add(1))

			if opts.Progress != nil {
				opts.Progress("Describing objects", done, len(names))
			}

			if done == len(names) || done%describeStep == 0 {
				logger.Info("describe progress", "done", done, "total", len(names))
			}
		}()

		obj, err := source.Describe(ctx, names[i])
		if err != nil {
			if opts.Strict || opts.StrictObjects[strings.ToLower(names[i])] {
				return fmt.Errorf("describe %s: %w", names[i], err)
			}

			logger.Warn("skipping object", "object", names[i], "error", err)

			skippedMu.Lock()
			skipped = append(skipped, names[i])
			skippedMu.Unlock()

			return nil
		}

		if obj.Name == "" {
			return fmt.Errorf("describe %s: empty object name", names[i])
		}

		out[i] = obj

		return nil
	})

	described := compactObjects(out)
	skippedNames := uniqueSorted(skipped)

	logger.Info("described objects", "described", len(described), "skipped", len(skippedNames))

	if err != nil {
		return described, skippedNames, err
	}

	return described, skippedNames, nil
}

// progressInterval keeps progress output bounded for large runs while still
// reporting every object for small ones.
func progressInterval(total int) int {
	if total <= 20 {
		return 1
	}
	if total <= 200 {
		return 10
	}
	if total <= 500 {
		return 25
	}

	return 50
}

func compactObjects(in []schema.Object) []schema.Object {
	out := make([]schema.Object, 0, len(in))

	for _, obj := range in {
		if obj.Name != "" {
			out = append(out, obj)
		}
	}

	return out
}

func isStrict(name string, opts Options) bool {
	if opts.Strict {
		return true
	}

	return opts.StrictObjects[strings.ToLower(name)]
}

func uniqueSorted(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))

	for _, name := range in {
		name = strings.TrimSpace(name)
		key := strings.ToLower(name)

		if name == "" || seen[key] {
			continue
		}

		seen[key] = true
		out = append(out, name)
	}
	sort.Strings(out)

	return out
}

func runPool(ctx context.Context, workers, total int, fn func(context.Context, int) error) error {
	if total == 0 {
		return nil
	}
	if workers <= 0 {
		workers = 1
	}
	if workers > total {
		workers = total
	}

	ctx, cancel := context.WithCancel(ctx)

	defer cancel()

	jobs := make(chan int)
	var wg sync.WaitGroup
	var once sync.Once
	var firstErr error

	fail := func(err error) {
		once.Do(func() {
			firstErr = err
			cancel()
		})
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for index := range jobs {
				if ctx.Err() != nil {
					return
				}
				if err := fn(ctx, index); err != nil {
					fail(err)
					return
				}
			}
		}()
	}

feed:
	for i := 0; i < total; i++ {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break feed
		}
	}

	close(jobs)
	wg.Wait()

	if firstErr != nil {
		return firstErr
	}

	return ctx.Err()
}

func uniqueObjects(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))

	for _, name := range in {
		name = strings.TrimSpace(name)
		key := strings.ToLower(name)

		if name == "" || seen[key] {
			continue
		}

		seen[key] = true
		out = append(out, name)
	}

	return out
}
