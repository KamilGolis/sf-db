package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/KamilGolis/sf-db/internal/pipeline"
	"github.com/KamilGolis/sf-db/internal/salesforce"
	"github.com/KamilGolis/sf-db/internal/store"
)

type cloneOptions struct {
	org           string
	out           string
	allRows       bool
	workers       int
	engine        string
	bulkThreshold int
	force         bool
	dryRun        bool
	strict        bool
	selector      objectSelector
}

func newCloneCmd(app *App, sfBin *string) *cobra.Command {
	opts := &cloneOptions{}
	cmd := &cobra.Command{
		Use:   "clone",
		Short: "Clone Salesforce objects into a SQLite file",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClone(cmd.Context(), app, *sfBin, opts)
		},
	}
	cmd.Flags().StringVar(&opts.org, "org", "", "Salesforce org alias or username")
	cmd.Flags().StringSliceVar(&opts.selector.Objects, "objects", nil, "objects to clone (comma-separated or repeated)")
	cmd.Flags().BoolVar(&opts.selector.All, "all-objects", false, "add every queryable standard and custom business object")
	cmd.Flags().BoolVar(&opts.selector.Custom, "custom-objects", false, "add every queryable custom object")
	cmd.Flags().BoolVar(&opts.selector.Standard, "standard-objects", false, "add every queryable standard business object")
	cmd.Flags().BoolVar(&opts.selector.IncludeSystem, "include-system-objects", false, "include History, Share, Feed, Tag, and ChangeEvent objects")
	cmd.Flags().StringVar(&opts.out, "out", "", "output SQLite file")
	cmd.Flags().BoolVar(&opts.allRows, "all-rows", false, "include soft-deleted records")
	cmd.Flags().IntVar(&opts.workers, "workers", pipeline.DefaultWorkers, "parallel object workers (1-32)")
	cmd.Flags().StringVar(&opts.engine, "engine", pipeline.EngineAuto, "query engine: auto, rest, or bulk")
	cmd.Flags().IntVar(&opts.bulkThreshold, "bulk-threshold", pipeline.DefaultBulkThreshold, "auto switches to Bulk above this record count (0 uses the default)")
	cmd.Flags().BoolVar(&opts.force, "force", false, "replace an existing output file")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "list the resolved objects and exit")
	cmd.Flags().BoolVar(&opts.strict, "strict", false, "abort on the first object that fails, including selector-derived objects")
	return cmd
}

func runClone(ctx context.Context, app *App, sfBin string, opts *cloneOptions) error {
	if err := opts.selector.validate(); err != nil {
		return err
	}
	if opts.org == "" && (opts.selector.hasSelector() || !opts.dryRun) {
		return errors.New("--org is required unless --dry-run is used with --objects")
	}
	if !opts.dryRun && opts.out == "" {
		return errors.New("--out is required unless --dry-run")
	}
	if opts.workers < 1 || opts.workers > 32 {
		return errors.New("workers must be between 1 and 32")
	}

	engine := strings.ToLower(opts.engine)

	switch engine {
	case pipeline.EngineAuto, pipeline.EngineREST, pipeline.EngineBulk:
	default:
		return fmt.Errorf("unknown engine %q (want auto, rest, or bulk)", opts.engine)
	}

	if opts.bulkThreshold < 0 {
		return errors.New("bulk-threshold must not be negative")
	}

	if opts.dryRun && opts.out != "" {
		return errors.New("--out cannot be used with --dry-run")
	}

	ui := newProgressUI(app.Err, !app.verboseMode())
	defer ui.Close()

	app.Logger.Info("starting clone", "org", opts.org, "dry_run", opts.dryRun, "workers", opts.workers)

	var source *salesforce.Client

	if opts.selector.hasSelector() || !opts.dryRun {
		provider := salesforce.Provider{Runner: salesforce.ExecRunner{}, Bin: sfBin, Org: opts.org}

		app.Logger.Info("reading org credentials", "org", opts.org)
		ui.Status(fmt.Sprintf("Reading Salesforce credentials for %s...", opts.org))

		var err error
		source, err = salesforce.New(ctx, provider.Token, app.Logger)

		if err != nil {
			return err
		}

		app.Logger.Info("connected to org", "org", opts.org)
		ui.Done(fmt.Sprintf("Connected to %s", opts.org))
	}

	if opts.selector.hasSelector() {
		app.Logger.Info("listing org objects")
		ui.Status("Listing Salesforce objects...")
	}

	objects, err := opts.selector.resolve(ctx, source)
	if err != nil {
		return err
	}

	app.Logger.Info("resolved objects", "count", len(objects))
	ui.Done("Resolved " + pluralize(len(objects), "object", "objects"))

	if opts.dryRun {
		return printObjectSet(app.Out, objects)
	}

	if _, err := os.Stat(opts.out); err == nil && !opts.force {
		return fmt.Errorf("%s already exists; use --force to replace it", opts.out)
	}
	if opts.force {
		removeSQLite(opts.out)
	}

	tmp := opts.out + ".tmp"
	removeSQLite(tmp)

	app.Logger.Info("creating local database", "path", tmp)
	ui.Status("Creating local database...")

	db, err := store.Open(tmp)
	if err != nil {
		return err
	}

	ui.Done("Created local database")

	svc := &pipeline.Service{
		Source:  source,
		Sink:    db,
		Logger:  app.Logger,
		TempDir: filepath.Dir(opts.out),
	}

	start := time.Now()

	result, runErr := svc.Run(ctx, pipeline.Options{
		Org:           opts.org,
		Objects:       objects,
		AllRows:       opts.allRows,
		Workers:       opts.workers,
		Engine:        engine,
		BulkThreshold: opts.bulkThreshold,
		Strict:        opts.strict,
		StrictObjects: opts.selector.strictSet(),
		Progress:      ui.Update,
	})

	closeErr := db.Close()
	
	if runErr != nil {
		removeSQLite(tmp)
		return runErr
	}
	if closeErr != nil {
		removeSQLite(tmp)
		return fmt.Errorf("close database: %w", closeErr)
	}

	ui.Done(fmt.Sprintf("Loaded %s, skipped %d", pluralize(len(result.Rows), "object", "objects"), len(result.Skipped)))
	app.Logger.Info("publishing database", "path", opts.out)
	ui.Status("Publishing database...")

	if err := os.Rename(tmp, opts.out); err != nil {
		removeSQLite(tmp)
		return fmt.Errorf("publish database: %w", err)
	}

	ui.Done("Published " + opts.out)

	if _, err := fmt.Fprintf(app.Out, "resolved %d object(s), loaded %d, skipped %d into %s in %s\n",
		len(objects), len(result.Rows), len(result.Skipped), opts.out, time.Since(start).Round(time.Second)); err != nil {
		return err
	}

	names := make([]string, 0, len(result.Rows))
	for name := range result.Rows {
		names = append(names, name)
	}

	sort.Strings(names)

	for _, name := range names {
		if _, err := fmt.Fprintf(app.Out, "  %-30s %d rows\n", name, result.Rows[name]); err != nil {
			return err
		}
	}

	if len(result.Skipped) > 0 {
		if _, err := fmt.Fprintf(app.Err, "skipped %d object(s):\n", len(result.Skipped)); err != nil {
			return err
		}

		for _, name := range result.Skipped {
			if _, err := fmt.Fprintf(app.Err, "  %s\n", name); err != nil {
				return err
			}
		}
	}

	_, err = fmt.Fprintln(app.Out, "\nOpen it with File | New | Data Source | SQLite.")
	return err
}

func removeSQLite(path string) {
	_ = os.Remove(path)
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")
}
