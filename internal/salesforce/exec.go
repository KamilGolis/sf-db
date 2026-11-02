package salesforce

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Runner executes the sf CLI. It is injectable for tests.
type Runner interface {
	Run(ctx context.Context, bin string, args ...string) ([]byte, error)
}

// ExecRunner runs the real sf binary.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, bin string, args ...string) ([]byte, error) {
	if bin == "" {
		bin = "sf"
	}

	path, err := exec.LookPath(bin)
	if err != nil {
		return nil, fmt.Errorf("sf CLI not found (%q): %w", bin, err)
	}

	cmd := commandContext(ctx, path, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()

	if err != nil {
		return nil, fmt.Errorf("sf %s: %s", strings.Join(args, " "), commandError(string(out), stderr.String(), err))
	}

	return out, nil
}

func commandContext(ctx context.Context, bin string, args ...string) *exec.Cmd {
	lower := strings.ToLower(bin)

	if runtime.GOOS == "windows" && (strings.HasSuffix(lower, ".cmd") || strings.HasSuffix(lower, ".bat")) {
		return exec.CommandContext(ctx, "cmd.exe", append([]string{"/c", bin}, args...)...)
	}

	return exec.CommandContext(ctx, bin, args...)
}

// commandError keeps the useful part of an sf failure.
func commandError(stdout, stderr string, err error) string {
	var named struct {
		Name    string `json:"name"`
		Message string `json:"message"`
	}

	if json.Unmarshal([]byte(strings.TrimSpace(stdout)), &named) == nil && named.Message != "" {
		if named.Name != "" {
			return named.Name + ": " + named.Message
		}

		return named.Message
	}

	var list []salesforceError

	if json.Unmarshal([]byte(strings.TrimSpace(stdout)), &list) == nil && len(list) > 0 && list[0].Message != "" {
		return list[0].Message
	}

	parts := make([]string, 0, 2)

	if s := strings.TrimSpace(stderr); s != "" {
		parts = append(parts, s)
	}

	if s := strings.TrimSpace(stdout); s != "" {
		parts = append(parts, s)
	}

	if len(parts) == 0 {
		return err.Error()
	}
	
	return strings.Join(parts, "\n")
}
