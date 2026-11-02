package cli

import (
	"bytes"
	"log/slog"
	"testing"
)

func TestVerboseFlagSwitchesLogLevel(t *testing.T) {
	for _, tt := range []struct {
		name  string
		args  []string
		level slog.Level
	}{
		{name: "default", args: []string{"version"}, level: slog.LevelError},
		{name: "verbose", args: []string{"--verbose", "version"}, level: slog.LevelDebug},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			level := new(slog.LevelVar)
			app := &App{Version: "test", Out: &out, Err: &out, Level: level}
			cmd := NewRootCmd(app)
			cmd.SetArgs(tt.args)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if got := level.Level(); got != tt.level {
				t.Fatalf("level = %v, want %v", got, tt.level)
			}
		})
	}
}
