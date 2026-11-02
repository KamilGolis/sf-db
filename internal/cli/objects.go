package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/KamilGolis/sf-db/internal/salesforce"
	"github.com/KamilGolis/sf-db/internal/selection"
)

// objectSelector is the shared --objects / --all-objects / --custom-objects /
// --standard-objects input for clone and schema.
type objectSelector struct {
	Objects       []string
	All           bool
	Custom        bool
	Standard      bool
	IncludeSystem bool
}

func (s objectSelector) hasSelector() bool {
	return s.All || s.Custom || s.Standard
}

func (s objectSelector) validate() error {
	if s.All && (s.Custom || s.Standard) {
		return errors.New("--all-objects cannot be combined with --custom-objects or --standard-objects")
	}
	if s.IncludeSystem && !s.hasSelector() {
		return errors.New("--include-system-objects requires --all-objects, --custom-objects, or --standard-objects")
	}
	if len(s.Objects) == 0 && !s.hasSelector() {
		return errors.New("at least one of --objects, --all-objects, --custom-objects, or --standard-objects is required")
	}

	return nil
}

func (s objectSelector) strictSet() map[string]bool {
	out := make(map[string]bool, len(s.Objects))

	for _, name := range s.Objects {
		name = strings.TrimSpace(name)
		if name != "" {
			out[strings.ToLower(name)] = true
		}
	}

	return out
}

// resolve returns the final object set. Explicit-only selection does not need
// the org object list; a selector does.
func (s objectSelector) resolve(ctx context.Context, client *salesforce.Client) ([]string, error) {
	opts := selection.Options{
		Explicit:      s.Objects,
		All:           s.All,
		Custom:        s.Custom,
		Standard:      s.Standard,
		IncludeSystem: s.IncludeSystem,
	}

	if !s.hasSelector() {
		return selection.Resolve(nil, opts)
	}
	if client == nil {
		return nil, errors.New("object selectors require an authenticated Salesforce client")
	}

	infos, err := client.ListObjects(ctx)
	if err != nil {
		return nil, err
	}

	return selection.Resolve(infos, opts)
}

func printObjectSet(w io.Writer, names []string) error {
	if _, err := fmt.Fprintf(w, "resolved %d object(s)\n", len(names)); err != nil {
		return err
	}
	
	for _, name := range names {
		if _, err := fmt.Fprintf(w, "  %s\n", name); err != nil {
			return err
		}
	}

	return nil
}
