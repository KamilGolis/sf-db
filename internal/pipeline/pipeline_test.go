package pipeline

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/KamilGolis/sf-db/internal/schema"
	"github.com/KamilGolis/sf-db/internal/store"
)

type fakeSource struct {
	count         int
	describe      atomic.Int64
	queryCalls    atomic.Int64
	bulkCalls     atomic.Int64
	pages         [][]map[string]any
	bulkCSV       string
	describeErr   map[string]error
	queryErrFor   string
	failAfterPage bool
	mu            sync.Mutex
	lastSOQL      string
}

func (f *fakeSource) Describe(ctx context.Context, object string) (schema.Object, error) {
	f.describe.Add(1)
	if err, ok := f.describeErr[object]; ok {
		return schema.Object{}, err
	}
	return schema.Object{Name: object, Label: object, Fields: []schema.Field{
		{Name: "Id", Type: "id"},
		{Name: "Name", Type: "string"},
	}}, nil
}

func (f *fakeSource) Count(ctx context.Context, object string) (int, error) {
	return f.count, nil
}

func (f *fakeSource) Query(ctx context.Context, soql string, allRows bool, onPage func([]map[string]any) error) error {
	f.queryCalls.Add(1)
	fail := f.queryErrFor != "" && strings.Contains(soql, "FROM "+f.queryErrFor)
	if fail && !f.failAfterPage {
		return os.ErrDeadlineExceeded
	}
	f.mu.Lock()
	f.lastSOQL = soql
	f.mu.Unlock()
	for _, page := range f.pages {
		if err := onPage(page); err != nil {
			return err
		}
	}
	if fail {
		return os.ErrDeadlineExceeded
	}
	return nil
}

func (f *fakeSource) BulkQuery(ctx context.Context, soql string, allRows bool, outputFile string) (int64, error) {
	f.bulkCalls.Add(1)
	f.mu.Lock()
	f.lastSOQL = soql
	f.mu.Unlock()
	if err := os.WriteFile(outputFile, []byte(f.bulkCSV), 0o644); err != nil {
		return 0, err
	}
	return int64(strings.Count(f.bulkCSV, "\n") - 1), nil
}

func (f *fakeSource) lastQuery() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastSOQL
}

func newTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(t.TempDir() + "/clone.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestRunREST(t *testing.T) {
	db := newTestDB(t)
	source := &fakeSource{
		count: 2,
		pages: [][]map[string]any{
			{{"Id": "001", "Name": "Acme"}},
			{{"Id": "002", "Name": "Globex"}},
		},
	}
	svc := &Service{Source: source, Sink: db, TempDir: t.TempDir()}
	result, err := svc.Run(context.Background(), Options{
		Org: "dev", Objects: []string{"Account"}, Workers: 2, Engine: EngineAuto,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows["Account"] != 2 {
		t.Fatalf("rows = %d, want 2", result.Rows["Account"])
	}
	if source.queryCalls.Load() != 1 || source.bulkCalls.Load() != 0 {
		t.Fatalf("queryCalls=%d bulkCalls=%d, want 1/0", source.queryCalls.Load(), source.bulkCalls.Load())
	}
	if !strings.Contains(source.lastQuery(), "SELECT Id, Name FROM Account") {
		t.Fatalf("unexpected SOQL: %s", source.lastQuery())
	}
	var count int
	if err := db.SQL().QueryRow(`SELECT COUNT(*) FROM "Account"`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
}

func TestRunBulk(t *testing.T) {
	db := newTestDB(t)
	source := &fakeSource{
		count:   60_000,
		bulkCSV: "Id,Name\n001,Acme\n002,Globex\n",
	}
	svc := &Service{Source: source, Sink: db, TempDir: t.TempDir()}
	result, err := svc.Run(context.Background(), Options{
		Org: "dev", Objects: []string{"Account"}, Workers: 1, Engine: EngineAuto, BulkThreshold: 50_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows["Account"] != 2 {
		t.Fatalf("rows = %d, want 2", result.Rows["Account"])
	}
	if source.bulkCalls.Load() != 1 || source.queryCalls.Load() != 0 {
		t.Fatalf("bulkCalls=%d queryCalls=%d, want 1/0", source.bulkCalls.Load(), source.queryCalls.Load())
	}
}

func TestRunMultiObjectWorkers(t *testing.T) {
	db := newTestDB(t)
	source := &fakeSource{
		count: 1,
		pages: [][]map[string]any{
			{{"Id": "001", "Name": "Acme"}},
		},
	}
	svc := &Service{Source: source, Sink: db, TempDir: t.TempDir()}
	result, err := svc.Run(context.Background(), Options{
		Org: "dev", Objects: []string{"Account", "Contact"}, Workers: 2, Engine: EngineAuto,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows["Account"] != 1 || result.Rows["Contact"] != 1 {
		t.Fatalf("unexpected rows: %+v", result.Rows)
	}
	if source.describe.Load() != 2 || source.queryCalls.Load() != 2 {
		t.Fatalf("describe=%d query=%d, want 2/2", source.describe.Load(), source.queryCalls.Load())
	}
}

func TestRunEngineRESTOverridesBulk(t *testing.T) {
	db := newTestDB(t)
	source := &fakeSource{
		count:   60_000,
		bulkCSV: "Id,Name\n001,Acme\n",
		pages:   [][]map[string]any{{{"Id": "001", "Name": "Acme"}}},
	}
	svc := &Service{Source: source, Sink: db, TempDir: t.TempDir()}
	if _, err := svc.Run(context.Background(), Options{
		Org: "dev", Objects: []string{"Account"}, Workers: 1, Engine: EngineREST, BulkThreshold: 50_000,
	}); err != nil {
		t.Fatal(err)
	}
	if source.bulkCalls.Load() != 0 || source.queryCalls.Load() != 1 {
		t.Fatalf("bulkCalls=%d queryCalls=%d, want 0/1", source.bulkCalls.Load(), source.queryCalls.Load())
	}
}

func TestRunRejectsBadEngine(t *testing.T) {
	db := newTestDB(t)
	svc := &Service{Source: &fakeSource{}, Sink: db}
	if _, err := svc.Run(context.Background(), Options{Org: "dev", Objects: []string{"Account"}, Engine: "nope"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestRunStopsOnError(t *testing.T) {
	db := newTestDB(t)
	source := &fakeSource{count: 1, queryErrFor: "Account"}
	svc := &Service{Source: source, Sink: db, TempDir: t.TempDir()}
	if _, err := svc.Run(context.Background(), Options{
		Org: "dev", Objects: []string{"Account", "Contact"}, Workers: 2, Engine: EngineREST, Strict: true,
	}); err == nil {
		t.Fatal("expected error")
	}
}

func TestRunSkipsSelectorFailure(t *testing.T) {
	db := newTestDB(t)
	source := &fakeSource{
		count:       1,
		describeErr: map[string]error{"Contact": os.ErrPermission},
		pages:       [][]map[string]any{{{"Id": "001", "Name": "Acme"}}},
	}
	svc := &Service{Source: source, Sink: db, TempDir: t.TempDir()}
	result, err := svc.Run(context.Background(), Options{
		Org: "dev", Objects: []string{"Account", "Contact"}, Workers: 2, Engine: EngineAuto,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows["Account"] != 1 {
		t.Fatalf("Account rows = %d, want 1", result.Rows["Account"])
	}
	if len(result.Skipped) != 1 || result.Skipped[0] != "Contact" {
		t.Fatalf("skipped = %v, want [Contact]", result.Skipped)
	}
}

func TestRunSkipsQueryFailure(t *testing.T) {
	db := newTestDB(t)
	source := &fakeSource{
		count:       1,
		queryErrFor: "Contact",
		pages:       [][]map[string]any{{{"Id": "001", "Name": "Acme"}}},
	}
	svc := &Service{Source: source, Sink: db, TempDir: t.TempDir()}
	result, err := svc.Run(context.Background(), Options{
		Org: "dev", Objects: []string{"Account", "Contact"}, Workers: 2, Engine: EngineREST,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows["Account"] != 1 {
		t.Fatalf("Account rows = %d, want 1", result.Rows["Account"])
	}
	if len(result.Skipped) != 1 || result.Skipped[0] != "Contact" {
		t.Fatalf("skipped = %v, want [Contact]", result.Skipped)
	}
}

func TestRunExplicitFailureIsStrict(t *testing.T) {
	db := newTestDB(t)
	source := &fakeSource{
		count:       1,
		describeErr: map[string]error{"Contact": os.ErrPermission},
	}
	svc := &Service{Source: source, Sink: db, TempDir: t.TempDir()}
	_, err := svc.Run(context.Background(), Options{
		Org: "dev", Objects: []string{"Account", "Contact"}, Workers: 2, Engine: EngineREST,
		StrictObjects: map[string]bool{"contact": true},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRunClearsPartialRowsOnQueryFailure(t *testing.T) {
	db := newTestDB(t)
	source := &fakeSource{
		count:         1,
		queryErrFor:   "Contact",
		failAfterPage: true,
		pages:         [][]map[string]any{{{"Id": "001", "Name": "Acme"}}},
	}
	svc := &Service{Source: source, Sink: db, TempDir: t.TempDir()}
	result, err := svc.Run(context.Background(), Options{
		Org: "dev", Objects: []string{"Account", "Contact"}, Workers: 2, Engine: EngineREST,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Skipped) != 1 || result.Skipped[0] != "Contact" {
		t.Fatalf("skipped = %v, want [Contact]", result.Skipped)
	}
	var count int
	if err := db.SQL().QueryRow(`SELECT COUNT(*) FROM "Contact"`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("partial Contact rows = %d, want 0", count)
	}
}

func TestDescribeAllLogsProgress(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	source := &fakeSource{}
	if _, _, err := DescribeAll(context.Background(), source, []string{"Account", "Contact"}, 2, DescribeOptions{Logger: logger}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"describing objects", "describe progress", "described objects"} {
		if !strings.Contains(out, want) {
			t.Fatalf("log missing %q:\n%s", want, out)
		}
	}
}

func TestRunLogsLoadProgress(t *testing.T) {
	db := newTestDB(t)
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	source := &fakeSource{count: 0}
	svc := &Service{Source: source, Sink: db, Logger: logger, TempDir: t.TempDir()}
	if _, err := svc.Run(context.Background(), Options{
		Org: "dev", Objects: []string{"Account", "Contact"}, Workers: 2, Engine: EngineREST,
	}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"loading objects", "load progress"} {
		if !strings.Contains(out, want) {
			t.Fatalf("log missing %q:\n%s", want, out)
		}
	}
}

func TestRunEmitsProgressStages(t *testing.T) {
	db := newTestDB(t)
	source := &fakeSource{count: 0}
	var mu sync.Mutex
	var stages []string
	svc := &Service{Source: source, Sink: db, TempDir: t.TempDir()}
	if _, err := svc.Run(context.Background(), Options{
		Org: "dev", Objects: []string{"Account", "Contact"}, Workers: 2, Engine: EngineREST,
		Progress: func(stage string, done, total int) {
			mu.Lock()
			stages = append(stages, stage)
			mu.Unlock()
		},
	}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, want := range []string{"Describing objects", "Creating schema", "Loading objects", "Creating indexes", "Finalizing database"} {
		found := false
		for _, stage := range stages {
			if stage == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("progress stages missing %q: %v", want, stages)
		}
	}
}
