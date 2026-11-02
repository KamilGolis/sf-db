package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/KamilGolis/sf-db/internal/pipeline"
	"github.com/KamilGolis/sf-db/internal/salesforce"
	"github.com/KamilGolis/sf-db/internal/schema"
)

type schemaOptions struct {
	org      string
	out      string
	meta     bool
	workers  int
	dryRun   bool
	strict   bool
	selector objectSelector
}

func newSchemaCmd(app *App, sfBin *string) *cobra.Command {
	opts := &schemaOptions{}
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Print the SQLite DDL for Salesforce objects",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSchema(cmd.Context(), app, *sfBin, opts)
		},
	}
	cmd.Flags().StringVar(&opts.org, "org", "", "Salesforce org alias or username")
	cmd.Flags().StringSliceVar(&opts.selector.Objects, "objects", nil, "objects to describe (comma-separated or repeated)")
	cmd.Flags().BoolVar(&opts.selector.All, "all-objects", false, "add every queryable standard and custom business object")
	cmd.Flags().BoolVar(&opts.selector.Custom, "custom-objects", false, "add every queryable custom object")
	cmd.Flags().BoolVar(&opts.selector.Standard, "standard-objects", false, "add every queryable standard business object")
	cmd.Flags().BoolVar(&opts.selector.IncludeSystem, "include-system-objects", false, "include History, Share, Feed, Tag, and ChangeEvent objects")
	cmd.Flags().StringVar(&opts.out, "out", "", "write DDL to this file instead of stdout")
	cmd.Flags().BoolVar(&opts.meta, "meta", false, "include the sf_meta_* sidecar tables")
	cmd.Flags().IntVar(&opts.workers, "workers", pipeline.DefaultWorkers, "parallel describe workers (1-32)")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "list the resolved objects and exit")
	cmd.Flags().BoolVar(&opts.strict, "strict", false, "abort on the first object that fails, including selector-derived objects")

	return cmd
}

func runSchema(ctx context.Context, app *App, sfBin string, opts *schemaOptions) error {
	if err := opts.selector.validate(); err != nil {
		return err
	}
	if opts.org == "" && (opts.selector.hasSelector() || !opts.dryRun) {
		return errors.New("--org is required unless --dry-run is used with --objects")
	}
	if opts.workers < 1 || opts.workers > 32 {
		return errors.New("workers must be between 1 and 32")
	}
	if opts.dryRun && opts.out != "" {
		return errors.New("--out cannot be used with --dry-run")
	}

	ui := newProgressUI(app.Err, !app.verboseMode())
	defer ui.Close()
	app.Logger.Info("starting schema", "org", opts.org, "dry_run", opts.dryRun, "workers", opts.workers)

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

	described, skipped, err := pipeline.DescribeAll(ctx, source, objects, opts.workers, pipeline.DescribeOptions{
		Strict:        opts.strict,
		StrictObjects: opts.selector.strictSet(),
		Logger:        app.Logger,
		Progress:      ui.Update,
	})

	printSkipped := func() error {
		for _, name := range skipped {
			if _, printErr := fmt.Fprintf(app.Err, "skipped %s\n", name); printErr != nil {
				return printErr
			}
		}
		return nil
	}

	if err != nil {
		ui.Close()
		if printErr := printSkipped(); printErr != nil {
			return printErr
		}
		return err
	}

	if len(described) == 0 {
		ui.Close()
		if printErr := printSkipped(); printErr != nil {
			return printErr
		}
		return fmt.Errorf("no objects could be described (%d skipped)", len(skipped))
	}

	ui.Done("Described " + pluralize(len(described), "object", "objects"))

	if printErr := printSkipped(); printErr != nil {
		return printErr
	}

	app.Logger.Info("planning tables", "objects", len(described))
	ui.Status("Planning tables...")

	tables, err := pipeline.PlanTables(described)
	if err != nil {
		return err
	}

	ui.Done("Planned " + pluralize(len(tables), "table", "tables"))

	var statements []string

	if opts.meta {
		statements = append(statements, schema.MetaTables()...)
	}

	for _, ddl := range tables {
		statements = append(statements, ddl.CreateTable)
		statements = append(statements, ddl.Indexes...)
	}

	text := renderSQL(statements)

	if opts.out == "" {
		ui.Done("Generated DDL")
		_, err := fmt.Fprint(app.Out, text)
		return err
	}

	if dir := filepath.Dir(opts.out); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create output directory: %w", err)
		}
	}

	app.Logger.Info("writing DDL", "path", opts.out)
	ui.Status("Writing DDL...")

	if err := os.WriteFile(opts.out, []byte(text), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", opts.out, err)
	}

	ui.Done("Wrote " + opts.out)
	_, err = fmt.Fprintf(app.Out, "wrote %s\n", opts.out)

	return err
}

func renderSQL(statements []string) string {
	var b strings.Builder

	for i, stmt := range statements {
		if i > 0 {
			b.WriteString("\n\n")
		}

		b.WriteString(strings.TrimRight(stmt, "; \n"))
		b.WriteString(";")
	}

	b.WriteString("\n")

	return b.String()
}
