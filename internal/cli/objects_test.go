package cli

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
)

func testApp() (*App, *bytes.Buffer, *bytes.Buffer) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	return &App{
		Out:    out,
		Err:    errOut,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, out, errOut
}

func TestSelectorValidate(t *testing.T) {
	tests := []struct {
		name string
		s    objectSelector
		want string
	}{
		{"no source", objectSelector{}, "at least one"},
		{"all conflicts custom", objectSelector{All: true, Custom: true}, "cannot be combined"},
		{"all conflicts standard", objectSelector{All: true, Standard: true}, "cannot be combined"},
		{"system needs selector", objectSelector{Objects: []string{"Account"}, IncludeSystem: true}, "requires"},
		{"custom standard is fine", objectSelector{Custom: true, Standard: true}, ""},
		{"explicit is fine", objectSelector{Objects: []string{"Account"}}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.s.validate()
			if tt.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want contains %q", err, tt.want)
			}
		})
	}
}

func TestSelectorResolveExplicitNoClient(t *testing.T) {
	got, err := (objectSelector{Objects: []string{"Contact", "Account"}}).resolve(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "Account,Contact" {
		t.Fatalf("got %v", got)
	}
}

func TestSelectorResolveSelectorRequiresClient(t *testing.T) {
	if _, err := (objectSelector{Custom: true}).resolve(context.Background(), nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestSelectorStrictSet(t *testing.T) {
	got := (objectSelector{Objects: []string{"Account", "Contact"}}).strictSet()
	if !got["account"] || !got["contact"] {
		t.Fatalf("strict set = %v", got)
	}
}

func TestRunCloneDryRunExplicitNoAuth(t *testing.T) {
	app, out, _ := testApp()
	err := runClone(context.Background(), app, "sf", &cloneOptions{
		selector: objectSelector{Objects: []string{"Account", "Contact"}},
		dryRun:   true,
		workers:  4,
		engine:   "auto",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "resolved 2 object(s)") || !strings.Contains(out.String(), "Account") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestRunCloneDryRunConflict(t *testing.T) {
	app, _, _ := testApp()
	err := runClone(context.Background(), app, "sf", &cloneOptions{
		selector: objectSelector{All: true, Custom: true},
		dryRun:   true,
		workers:  4,
		engine:   "auto",
	})
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunCloneRequiresOut(t *testing.T) {
	app, _, _ := testApp()
	err := runClone(context.Background(), app, "sf", &cloneOptions{
		org:      "dev",
		selector: objectSelector{Objects: []string{"Account"}},
		workers:  4,
		engine:   "auto",
	})
	if err == nil || !strings.Contains(err.Error(), "--out is required") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunCloneDryRunRejectsOut(t *testing.T) {
	app, _, _ := testApp()
	err := runClone(context.Background(), app, "sf", &cloneOptions{
		selector: objectSelector{Objects: []string{"Account"}},
		out:      "clone.sqlite",
		dryRun:   true,
		workers:  4,
		engine:   "auto",
	})
	if err == nil || !strings.Contains(err.Error(), "--out cannot be used") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunSchemaDryRunExplicitNoAuth(t *testing.T) {
	app, out, _ := testApp()
	err := runSchema(context.Background(), app, "sf", &schemaOptions{
		selector: objectSelector{Objects: []string{"Account"}},
		dryRun:   true,
		workers:  4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "resolved 1 object(s)") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestRunSchemaDryRunRejectsOut(t *testing.T) {
	app, _, _ := testApp()
	err := runSchema(context.Background(), app, "sf", &schemaOptions{
		selector: objectSelector{Objects: []string{"Account"}},
		out:      "schema.sql",
		dryRun:   true,
		workers:  4,
	})
	if err == nil || !strings.Contains(err.Error(), "--out cannot be used") {
		t.Fatalf("error = %v", err)
	}
}
