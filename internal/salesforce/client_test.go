package salesforce

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, handler http.Handler, provider TokenProvider) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	if provider == nil {
		provider = func(context.Context) (Token, error) {
			return Token{AccessToken: "token", InstanceURL: server.URL, APIVersion: "68.0"}, nil
		}
	}
	client, err := New(context.Background(), provider, nil)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestDescribeAndQuery(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/services/data/v68.0/sobjects/Account/describe":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"name":"Account","label":"Account","fields":[{"name":"Id","type":"id"},{"name":"ParentId","type":"reference","referenceTo":["Account"]}]}`)
		case r.URL.Path == "/services/data/v68.0/query":
			if r.URL.Query().Get("q") == "SELECT COUNT() FROM Account" {
				io.WriteString(w, `{"totalSize":2,"done":true,"records":[]}`)
				return
			}
			io.WriteString(w, `{"totalSize":3,"done":false,"nextRecordsUrl":"/services/data/v68.0/query/next","records":[{"Id":"001","Name":"Acme"},{"Id":"002","Name":"Globex"}]}`)
		case r.URL.Path == "/services/data/v68.0/query/next":
			io.WriteString(w, `{"totalSize":3,"done":true,"records":[{"Id":"003","Name":"Initech"}]}`)
		default:
			http.NotFound(w, r)
		}
	})
	client := testClient(t, handler, nil)

	obj, err := client.Describe(context.Background(), "Account")
	if err != nil {
		t.Fatal(err)
	}
	if obj.Name != "Account" || len(obj.Fields) != 2 || obj.Fields[1].ReferenceTo[0] != "Account" {
		t.Fatalf("unexpected object: %+v", obj)
	}

	count, err := client.Count(context.Background(), "Account")
	if err != nil || count != 2 {
		t.Fatalf("Count() = %d, %v", count, err)
	}

	var pages [][]map[string]any
	if err := client.Query(context.Background(), "SELECT Id, Name FROM Account", false, func(page []map[string]any) error {
		pages = append(pages, page)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 || len(pages[0]) != 2 || len(pages[1]) != 1 {
		t.Fatalf("unexpected pages: %+v", pages)
	}
}

func TestQueryAllPath(t *testing.T) {
	var sawPath string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		io.WriteString(w, `{"totalSize":0,"done":true,"records":[]}`)
	})
	client := testClient(t, handler, nil)
	if err := client.Query(context.Background(), "SELECT Id FROM Account", true, func([]map[string]any) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if sawPath != "/services/data/v68.0/queryAll" {
		t.Fatalf("path = %q", sawPath)
	}
}

func TestBulkQuery(t *testing.T) {
	oldPoll := pollInterval
	pollInterval = time.Millisecond
	defer func() { pollInterval = oldPoll }()

	var polls int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/services/data/v68.0/jobs/query":
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, `{"id":"job1","state":"UploadComplete"}`)
		case r.URL.Path == "/services/data/v68.0/jobs/query/job1":
			if atomic.AddInt32(&polls, 1) == 1 {
				io.WriteString(w, `{"id":"job1","state":"InProgress"}`)
				return
			}
			io.WriteString(w, `{"id":"job1","state":"JobComplete","numberRecordsProcessed":2}`)
		case r.URL.Path == "/services/data/v68.0/jobs/query/job1/results":
			w.Header().Set("Content-Type", "text/csv")
			w.Header().Set("Sforce-Locator", "null")
			w.Header().Set("Sforce-NumberOfRecords", "2")
			io.WriteString(w, "Id,Name\n001,Acme\n002,Globex\n")
		default:
			http.NotFound(w, r)
		}
	})
	client := testClient(t, handler, nil)
	out := t.TempDir() + "/result.csv"
	rows, err := client.BulkQuery(context.Background(), "SELECT Id, Name FROM Account", false, out)
	if err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("rows = %d, want 2", rows)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "Id,Name\n001,Acme\n002,Globex\n" {
		t.Fatalf("unexpected CSV: %q", data)
	}
}

func TestRefreshOn401(t *testing.T) {
	var providerCalls int32
	var requests int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&requests, 1) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Authorization") != "Bearer token-2" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		io.WriteString(w, `{"name":"Account","label":"Account","fields":[{"name":"Id","type":"id"}]}`)
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	provider := func(context.Context) (Token, error) {
		atomic.AddInt32(&providerCalls, 1)
		return Token{AccessToken: "token-" + string(rune('0'+atomic.LoadInt32(&providerCalls))), InstanceURL: server.URL, APIVersion: "68.0"}, nil
	}
	client, err := New(context.Background(), provider, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Describe(context.Background(), "Account"); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&providerCalls) != 2 {
		t.Fatalf("provider calls = %d, want 2", providerCalls)
	}
}

func TestErrorResponse(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `[{"message":"bad field","errorCode":"INVALID_FIELD"}]`)
	})
	client := testClient(t, handler, nil)
	_, err := client.Describe(context.Background(), "Account")
	if err == nil || !strings.Contains(err.Error(), "bad field") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeJSONNumbers(t *testing.T) {
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(`{"totalSize":1,"done":true,"records":[{"N":1.5}]}`))}
	var page queryResponse
	if err := decodeJSON(resp, &page); err != nil {
		t.Fatal(err)
	}
	n, ok := page.Records[0]["N"].(json.Number)
	if !ok || n.String() != "1.5" {
		t.Fatalf("N = %#v, want json.Number(1.5)", page.Records[0]["N"])
	}
}

func TestConcurrent401SingleRefresh(t *testing.T) {
	var requests int32
	var providerCalls int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&requests, 1) <= 2 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Authorization") != "Bearer token-2" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		io.WriteString(w, `{"name":"Account","label":"Account","fields":[{"name":"Id","type":"id"}]}`)
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	provider := func(context.Context) (Token, error) {
		n := atomic.AddInt32(&providerCalls, 1)
		return Token{AccessToken: "token-" + string(rune('0'+n)), InstanceURL: server.URL, APIVersion: "68.0"}, nil
	}
	client, err := New(context.Background(), provider, nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := client.Describe(context.Background(), "Account")
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := atomic.LoadInt32(&providerCalls); got != 2 {
		t.Fatalf("provider calls = %d, want 2 (initial + one refresh)", got)
	}
}

func TestRetryAfter(t *testing.T) {
	var requests int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&requests, 1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		io.WriteString(w, `{"name":"Account","label":"Account","fields":[{"name":"Id","type":"id"}]}`)
	})
	client := testClient(t, handler, nil)
	if _, err := client.Describe(context.Background(), "Account"); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&requests); got != 2 {
		t.Fatalf("requests = %d, want 2", got)
	}
}

func TestBulkCreateNotRetried(t *testing.T) {
	var posts int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&posts, 1)
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `{"message":"boom"}`)
	})
	client := testClient(t, handler, nil)
	if _, err := client.BulkQuery(context.Background(), "SELECT Id FROM Account", false, t.TempDir()+"/out.csv"); err == nil {
		t.Fatal("expected error")
	}
	if got := atomic.LoadInt32(&posts); got != 1 {
		t.Fatalf("POST attempts = %d, want 1", got)
	}
}

func TestQueryIncompletePage(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"totalSize":5,"done":false,"records":[{"Id":"001"}]}`)
	})
	client := testClient(t, handler, nil)
	err := client.Query(context.Background(), "SELECT Id FROM Account", false, func([]map[string]any) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("error = %v", err)
	}
}

func TestRejectsInvalidObjectName(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("HTTP should not be called")
	}), nil)
	if _, err := client.Describe(context.Background(), "Bad Name"); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestBulkQueryPaginationSkipsRepeatedHeader(t *testing.T) {
	oldPoll := pollInterval
	pollInterval = time.Millisecond
	defer func() { pollInterval = oldPoll }()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/services/data/v68.0/jobs/query":
			io.WriteString(w, `{"id":"job2","state":"JobComplete","numberRecordsProcessed":2}`)
		case r.URL.Path == "/services/data/v68.0/jobs/query/job2":
			io.WriteString(w, `{"id":"job2","state":"JobComplete","numberRecordsProcessed":2}`)
		case r.URL.Path == "/services/data/v68.0/jobs/query/job2/results":
			w.Header().Set("Content-Type", "text/csv")
			if r.URL.Query().Get("locator") == "" {
				w.Header().Set("Sforce-Locator", "page2")
				io.WriteString(w, "Id,Name\n001,Acme\n")
				return
			}
			w.Header().Set("Sforce-Locator", "null")
			io.WriteString(w, "Id,Name\n002,Globex\n")
		default:
			http.NotFound(w, r)
		}
	})
	client := testClient(t, handler, nil)
	out := t.TempDir() + "/result.csv"
	if _, err := client.BulkQuery(context.Background(), "SELECT Id, Name FROM Account", false, out); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	want := "Id,Name\n001,Acme\n002,Globex\n"
	if string(data) != want {
		t.Fatalf("CSV = %q, want %q", data, want)
	}
}

func TestListObjects(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/services/data/v68.0/sobjects" {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, `{"sobjects":[{"name":"Account","label":"Account","custom":false,"queryable":true},{"name":"MyObject__c","label":"My Object","custom":true,"queryable":true},{"name":"NotQueryable","queryable":false}]}`)
	})
	client := testClient(t, handler, nil)
	infos, err := client.ListObjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 3 || infos[1].Name != "MyObject__c" || !infos[1].Custom || !infos[0].Queryable || infos[2].Queryable {
		t.Fatalf("unexpected infos: %+v", infos)
	}
}
