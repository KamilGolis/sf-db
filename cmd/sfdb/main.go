package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/KamilGolis/sf-db/internal/cli"
)

var version = "0.1.0-dev"

func main() {
	level := new(slog.LevelVar)
	app := &cli.App{
		Version: version,
		Out:     os.Stdout,
		Err:     os.Stderr,
		Level:   level,
		Logger:  slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})),
	}
	if err := cli.Execute(app); err != nil {
		fmt.Fprintln(os.Stderr, "sfdb:", err)
		os.Exit(1)
	}
}
