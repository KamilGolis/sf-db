package salesforce

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/KamilGolis/sf-db/internal/schema"
	"github.com/KamilGolis/sf-db/internal/selection"
)

const defaultAPIVersion = "68.0"

var objectNameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

func validateObjectName(name string) error {
	if !objectNameRE.MatchString(name) {
		return fmt.Errorf("invalid Salesforce object name %q", name)
	}

	return nil
}

// TokenProvider returns a fresh token. It is called once at startup and again
// after a 401.
type TokenProvider func(context.Context) (Token, error)

// Client talks to the Salesforce REST and Bulk APIs with one keep-alive HTTP
// client.
type Client struct {
	http       *http.Client
	base       *url.URL
	apiVersion string
	logger     *slog.Logger

	mu       sync.Mutex
	token    Token
	version  int
	provider TokenProvider
	refresh  sync.Mutex
}

// New bootstraps the first token and builds the HTTP client.
func New(ctx context.Context, provider TokenProvider, logger *slog.Logger) (*Client, error) {
	if provider == nil {
		return nil, fmt.Errorf("token provider is required")
	}

	if logger == nil {
		logger = slog.Default()
	}

	tok, err := provider(ctx)
	if err != nil {
		return nil, err
	}

	tok.InstanceURL = strings.TrimRight(tok.InstanceURL, "/")
	base, err := url.Parse(tok.InstanceURL)
	if err != nil {
		return nil, fmt.Errorf("parse instance URL: %w", err)
	}

	if base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("invalid instance URL %q", tok.InstanceURL)
	}

	tok.APIVersion = strings.TrimPrefix(tok.APIVersion, "v")
	if tok.APIVersion == "" {
		tok.APIVersion = defaultAPIVersion
	}

	return &Client{
		http:       newHTTPClient(),
		base:       base,
		apiVersion: tok.APIVersion,
		logger:     logger,
		token:      tok,
		provider:   provider,
	}, nil
}

func newHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   8,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
			ExpectContinueTimeout: time.Second,
		},
	}
}

// ListObjects returns Describe Global metadata for selector resolution.
func (c *Client) ListObjects(ctx context.Context) ([]selection.Info, error) {
	var resp globalDescribeResponse

	if err := c.getJSON(ctx, "/sobjects", &resp); err != nil {
		return nil, fmt.Errorf("list objects: %w", err)
	}

	out := make([]selection.Info, 0, len(resp.SObjects))

	for _, o := range resp.SObjects {
		out = append(out, selection.Info{
			Name:                o.Name,
			Custom:              o.Custom,
			Queryable:           o.Queryable,
			DeprecatedAndHidden: o.DeprecatedAndHidden,
		})
	}

	return out, nil
}

// Describe returns one object and its fields.
func (c *Client) Describe(ctx context.Context, object string) (schema.Object, error) {
	if err := validateObjectName(object); err != nil {
		return schema.Object{}, err
	}

	var resp describeResponse
	path := "/sobjects/" + url.PathEscape(object) + "/describe"

	if err := c.getJSON(ctx, path, &resp); err != nil {
		return schema.Object{}, fmt.Errorf("describe %s: %w", object, err)
	}

	obj := schema.Object{Name: resp.Name, Label: resp.Label}
	if obj.Name == "" {
		obj.Name = object
	}
	if obj.Label == "" {
		obj.Label = obj.Name
	}

	obj.Fields = make([]schema.Field, 0, len(resp.Fields))

	for _, f := range resp.Fields {
		refs := f.ReferenceTo

		if refs == nil {
			refs = []string{}
		}

		obj.Fields = append(obj.Fields, schema.Field{
			Name:                f.Name,
			Label:               f.Label,
			Type:                f.Type,
			Length:              f.Length,
			Precision:           f.Precision,
			Scale:               f.Scale,
			Nillable:            f.Nillable,
			Unique:              f.Unique,
			ReferenceTo:         refs,
			RelationshipName:    f.RelationshipName,
			Calculated:          f.Calculated,
			CompoundFieldName:   f.CompoundFieldName,
			AutoNumber:          f.AutoNumber,
			HTMLFormatted:       f.HTMLFormatted,
			DeprecatedAndHidden: f.DeprecatedAndHidden,
		})
	}

	return obj, nil
}

// Count returns the number of visible records in an object.
func (c *Client) Count(ctx context.Context, object string) (int, error) {
	if err := validateObjectName(object); err != nil {
		return 0, err
	}

	var page queryResponse
	q := "SELECT COUNT() FROM " + object

	if err := c.getJSON(ctx, "/query?q="+url.QueryEscape(q), &page); err != nil {
		return 0, fmt.Errorf("count %s: %w", object, err)
	}

	return page.TotalSize, nil
}

// Query follows REST query pages and hands each page to onPage.
func (c *Client) Query(ctx context.Context, soql string, allRows bool, onPage func([]map[string]any) error) error {
	endpoint := "query"

	if allRows {
		endpoint = "queryAll"
	}

	var page queryResponse

	if err := c.getJSON(ctx, "/"+endpoint+"?q="+url.QueryEscape(soql), &page); err != nil {
		return err
	}

	fetched := 0

	if len(page.Records) > 0 {
		if err := onPage(page.Records); err != nil {
			return err
		}

		fetched += len(page.Records)
	}

	for !page.Done && page.NextRecordsURL != "" {
		var next queryResponse

		if err := c.getJSONAbsolute(ctx, page.NextRecordsURL, &next); err != nil {
			return err
		}

		page = next

		if len(page.Records) > 0 {
			if err := onPage(page.Records); err != nil {
				return err
			}

			fetched += len(page.Records)
		}
	}

	if !page.Done {
		return fmt.Errorf("query %s: incomplete page (totalSize %d, fetched %d)", soql, page.TotalSize, fetched)
	}

	return nil
}

// BulkQuery runs a Bulk API 2.0 query job and writes its CSV result to
// outputFile. It returns the record count reported by the job.
func (c *Client) BulkQuery(ctx context.Context, soql string, allRows bool, outputFile string) (int64, error) {
	operation := "query"

	if allRows {
		operation = "queryAll"
	}

	var job jobInfo
	body := jobCreateRequest{
		Operation:       operation,
		Query:           soql,
		ContentType:     "CSV",
		ColumnDelimiter: "COMMA",
		LineEnding:      "LF",
	}

	if err := c.postJSON(ctx, "/jobs/query", body, &job); err != nil {
		return 0, fmt.Errorf("create bulk query: %w", err)
	}

	if job.ID == "" {
		return 0, fmt.Errorf("create bulk query: empty job id")
	}

	completed := false

	defer func() {
		if !completed {
			c.abortJob(job.ID)
		}
	}()

	var err error
	job, err = c.waitForJob(ctx, job)
	if err != nil {
		return 0, err
	}

	f, err := os.Create(outputFile)
	if err != nil {
		return 0, fmt.Errorf("create bulk result file: %w", err)
	}

	defer f.Close()

	locator := ""
	first := true

	for {
		path := "/jobs/query/" + url.PathEscape(job.ID) + "/results"

		if locator != "" {
			path += "?locator=" + url.QueryEscape(locator)
		}

		resp, err := c.request(ctx, http.MethodGet, path, nil, "text/csv")
		if err != nil {
			return 0, fmt.Errorf("download bulk result: %w", err)
		}

		if err := copyCSVPage(f, resp.Body, first); err != nil {
			resp.Body.Close()
			return 0, fmt.Errorf("write bulk result: %w", err)
		}

		resp.Body.Close()
		first = false
		locator = resp.Header.Get("Sforce-Locator")

		if locator == "" || strings.EqualFold(locator, "null") {
			break
		}
	}

	completed = true

	return int64(job.NumberRecordsProcessed), nil
}

func (c *Client) abortJob(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = c.request(ctx, http.MethodPatch, "/jobs/query/"+url.PathEscape(id), map[string]string{"state": "Aborted"}, "application/json")
}

func (c *Client) waitForJob(ctx context.Context, job jobInfo) (jobInfo, error) {
	deadline := time.Now().Add(30 * time.Minute)

	for {
		var current jobInfo

		if err := c.getJSON(ctx, "/jobs/query/"+url.PathEscape(job.ID), &current); err != nil {
			return job, fmt.Errorf("poll bulk query: %w", err)
		}

		switch current.State {
		case stateJobComplete:
			return current, nil
		case stateFailed, stateAborted:
			return job, fmt.Errorf("bulk query %s: %s", current.State, current.ErrorMessage)
		}

		if time.Now().After(deadline) {
			return job, fmt.Errorf("bulk query did not finish within 30 minutes")
		}

		select {
		case <-ctx.Done():
			return job, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

func copyCSVPage(dst io.Writer, src io.Reader, includeHeader bool) error {
	br := bufio.NewReader(src)
	header, err := br.ReadString('\n')

	if err != nil && err != io.EOF {
		return err
	}

	if includeHeader && header != "" {
		if _, err := io.WriteString(dst, header); err != nil {
			return err
		}
	}

	_, err = io.Copy(dst, br)

	return err
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	resp, err := c.request(ctx, http.MethodGet, path, nil, "application/json")
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	return decodeJSON(resp, out)
}

func (c *Client) getJSONAbsolute(ctx context.Context, absolute string, out any) error {
	resp, err := c.requestURL(ctx, http.MethodGet, c.base.String()+absolute, nil, "application/json")
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	return decodeJSON(resp, out)
}

func (c *Client) postJSON(ctx context.Context, path string, body, out any) error {
	resp, err := c.request(ctx, http.MethodPost, path, body, "application/json")
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	return decodeJSON(resp, out)
}

func decodeJSON(resp *http.Response, out any) error {
	if out == nil {
		_, err := io.Copy(io.Discard, resp.Body)
		return err
	}

	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()

	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	return nil
}

func (c *Client) request(ctx context.Context, method, path string, body any, accept string) (*http.Response, error) {
	return c.do(ctx, method, path, true, body, accept)
}

func (c *Client) requestURL(ctx context.Context, method, full string, body any, accept string) (*http.Response, error) {
	return c.do(ctx, method, full, false, body, accept)
}

func (c *Client) do(ctx context.Context, method, path string, relative bool, body any, accept string) (*http.Response, error) {
	var payload []byte

	if body != nil {
		var err error

		payload, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request: %w", err)
		}
	}

	refreshed := false

	for attempt := 0; attempt < 5; attempt++ {
		tok, version, base, apiVersion := c.snapshot()
		full := path

		if relative {
			full = base.String() + "/services/data/v" + apiVersion + path
		}

		req, err := http.NewRequestWithContext(ctx, method, full, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}

		req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
		if accept == "" {
			accept = "application/json"
		}

		req.Header.Set("Accept", accept)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if method != http.MethodGet {
				return nil, fmt.Errorf("request %s: %w", full, err)
			}
			if attempt == 4 {
				return nil, fmt.Errorf("request %s: %w", full, err)
			}

			c.logger.Debug("retrying salesforce request", "error", err, "attempt", attempt+1)

			if err := sleepContext(ctx, retryDelay("", attempt)); err != nil {
				return nil, err
			}

			continue
		}

		if resp.StatusCode == http.StatusUnauthorized {
			data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()

			if refreshed {
				return nil, apiError(resp.StatusCode, data)
			}

			refreshed = true

			c.logger.Debug("refreshing salesforce token", "attempt", attempt+1)

			if err := c.refreshToken(ctx, version); err != nil {
				return nil, fmt.Errorf("refresh token: %w", err)
			}

			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()

			if method != http.MethodGet || attempt == 4 {
				return nil, apiError(resp.StatusCode, data)
			}

			c.logger.Debug("retrying salesforce request", "status", resp.StatusCode, "attempt", attempt+1)

			if err := sleepContext(ctx, retryDelay(resp.Header.Get("Retry-After"), attempt)); err != nil {
				return nil, err
			}

			continue
		}

		if resp.StatusCode >= 400 {
			data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()

			return nil, apiError(resp.StatusCode, data)
		}

		return resp, nil
	}

	return nil, fmt.Errorf("request %s: retry budget exhausted", path)
}

func (c *Client) snapshot() (Token, int, *url.URL, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token, c.version, c.base, c.apiVersion
}

func (c *Client) refreshToken(ctx context.Context, failedVersion int) error {
	c.refresh.Lock()
	defer c.refresh.Unlock()

	c.mu.Lock()

	if c.version != failedVersion {
		c.mu.Unlock()
		return nil
	}

	c.mu.Unlock()

	tok, err := c.provider(ctx)
	if err != nil {
		return err
	}

	base, err := url.Parse(strings.TrimRight(tok.InstanceURL, "/"))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return fmt.Errorf("refresh returned an invalid instance URL %q", tok.InstanceURL)
	}

	if tok.APIVersion != "" {
		tok.APIVersion = strings.TrimPrefix(tok.APIVersion, "v")
	}

	c.mu.Lock()
	c.token = tok
	c.version++
	c.base = base

	if tok.APIVersion != "" {
		c.apiVersion = tok.APIVersion
	}

	c.mu.Unlock()

	return nil
}

func retryDelay(header string, attempt int) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(header)); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}

	if at, err := http.ParseTime(strings.TrimSpace(header)); err == nil {
		if delay := time.Until(at); delay > 0 {
			return delay
		}
	}

	delay := 500 * time.Millisecond << attempt
	if delay > 8*time.Second {
		delay = 8 * time.Second
	}

	if spread := int64(delay / 5); spread > 0 {
		delay += time.Duration(rand.Int63n(2*spread) - spread)
	}

	return delay
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)

	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func apiError(status int, data []byte) error {
	if len(data) > 0 {
		var list []salesforceError

		if json.Unmarshal(data, &list) == nil && len(list) > 0 && list[0].Message != "" {
			return fmt.Errorf("salesforce %d: %s (%s)", status, list[0].Message, list[0].ErrorCode)
		}

		var single salesforceError

		if json.Unmarshal(data, &single) == nil && single.Message != "" {
			return fmt.Errorf("salesforce %d: %s (%s)", status, single.Message, single.ErrorCode)
		}

		text := strings.TrimSpace(string(data))
		if len(text) > 500 {
			text = text[:500]
		}

		return fmt.Errorf("salesforce %d: %s", status, text)
	}

	return fmt.Errorf("salesforce %d", status)
}
