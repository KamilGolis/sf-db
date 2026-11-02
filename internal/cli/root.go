// Package cli defines the sfdb command tree.
package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"

	"github.com/spf13/cobra"
)

// App carries process-level dependencies into the command tree.
type App struct {
	Version string
	Out     io.Writer
	Err     io.Writer
	Logger  *slog.Logger
	Level   *slog.LevelVar
}

// verboseMode reports whether structured debug logging is enabled. The
// progress display uses the same switch so the two never write over each other.
func (a *App) verboseMode() bool {
	return a.Level != nil && a.Level.Level() <= slog.LevelDebug
}

// Execute builds and runs the root command with interrupt cancellation.
func Execute(app *App) error {
	if app == nil {
		app = &App{}
	}
	if app.Out == nil {
		app.Out = os.Stdout
	}
	if app.Err == nil {
		app.Err = os.Stderr
	}
	if app.Level == nil {
		app.Level = new(slog.LevelVar)
	}
	if app.Logger == nil {
		app.Logger = slog.New(slog.NewTextHandler(app.Err, &slog.HandlerOptions{Level: app.Level}))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	return NewRootCmd(app).ExecuteContext(ctx)
}

// NewRootCmd builds the sfdb command tree.
func NewRootCmd(app *App) *cobra.Command {
	var (
		verbose bool
		sfBin   string
	)

	cmd := &cobra.Command{
		Use:           "sfdb",
		Short:         "Clone Salesforce data into a local SQLite database",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if verbose {
				app.Level.Set(slog.LevelDebug)
				return
			}
			app.Level.Set(slog.LevelError)
		},
	}
	cmd.PersistentFlags().BoolVar(&verbose, "verbose", false, "show structured logs instead of the progress display")
	cmd.PersistentFlags().StringVar(&sfBin, "sf-bin", "sf", "path to the sf CLI")

	cmd.AddCommand(
		newCloneCmd(app, &sfBin),
		newSchemaCmd(app, &sfBin),
		newRefreshCmd(),
		newVersionCmd(app),
	)

	return cmd
}

func newVersionCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the sfdb version",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintf(app.Out, "sfdb %s\n", app.Version)
			return err
		},
	}
}
