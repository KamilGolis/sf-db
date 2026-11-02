package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestProgressUITTY(t *testing.T) {
	var buf bytes.Buffer
	ui := newProgressUI(&buf, true)
	ui.tty = true
	ui.Update("Describing objects", 0, 10)
	ui.Update("Describing objects", 5, 10)
	ui.Done("Described 10 objects")
	out := buf.String()
	for _, want := range []string{"Describing objects", "5/10", "50%", "ok Described 10 objects"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%q", want, out)
		}
	}
	if !strings.Contains(out, "#") || !strings.Contains(out, "-") {
		t.Fatalf("output missing progress bar:\n%q", out)
	}
}

func TestProgressUINonTTY(t *testing.T) {
	var buf bytes.Buffer
	ui := newProgressUI(&buf, true)
	ui.Update("Loading objects", 0, 100)
	ui.Update("Loading objects", 1, 100)
	ui.Update("Loading objects", 10, 100)
	ui.Done("Loaded 100 objects")
	out := buf.String()
	if strings.Contains(out, "\r") {
		t.Fatalf("non-TTY output contains carriage returns:\n%q", out)
	}
	for _, want := range []string{"Loading objects 0/100 (0%", "Loading objects 10/100 (10%", "ok Loaded 100 objects"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%q", want, out)
		}
	}
	if strings.Contains(out, "1/100") {
		t.Fatalf("non-TTY output should throttle 1%%:\n%q", out)
	}
}

func TestProgressUIDisabled(t *testing.T) {
	var buf bytes.Buffer
	ui := newProgressUI(&buf, false)
	ui.Status("working")
	ui.Update("Describing objects", 1, 2)
	ui.Done("done")
	if buf.Len() != 0 {
		t.Fatalf("disabled UI wrote %q", buf.String())
	}
}
