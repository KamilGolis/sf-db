// Package selection turns object selectors into a deterministic object set.
package selection

import (
	"fmt"
	"sort"
	"strings"
)

// Info is the Describe Global metadata needed to classify an object.
type Info struct {
	Name                string
	Custom              bool
	Queryable           bool
	DeprecatedAndHidden bool
}

// SystemSuffixes are the derived object name suffixes excluded by default.
var SystemSuffixes = []string{"History", "Share", "Feed", "Tag", "ChangeEvent"}

// SystemObjectDescription is the human-readable form used in flag help.
const SystemObjectDescription = "History, Share, Feed, Tag, and ChangeEvent"

// Options describes the requested object set. Explicit names are always
// included; the three selectors add matching org objects.
type Options struct {
	Explicit      []string
	All           bool
	Custom        bool
	Standard      bool
	IncludeSystem bool
}

// Resolve returns sorted, deduplicated API names. A selector without a
// Describe Global list is an error; explicit-only selection does not need one.
func Resolve(infos []Info, opts Options) ([]string, error) {
	if opts.All && (opts.Custom || opts.Standard) {
		return nil, fmt.Errorf("--all-objects cannot be combined with --custom-objects or --standard-objects")
	}
	if len(opts.Explicit) == 0 && !opts.All && !opts.Custom && !opts.Standard {
		return nil, fmt.Errorf("at least one of --objects, --all-objects, --custom-objects, or --standard-objects is required")
	}

	byName := make(map[string]Info, len(infos))
	
	for _, info := range infos {
		byName[strings.ToLower(info.Name)] = info
	}

	selected := make(map[string]string)
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}

		key := strings.ToLower(name)

		if _, exists := selected[key]; exists {
			return
		}

		if info, ok := byName[key]; ok {
			selected[key] = info.Name
			return
		}

		selected[key] = name
	}

	for _, name := range opts.Explicit {
		add(name)
	}

	wantCustom := opts.All || opts.Custom
	wantStandard := opts.All || opts.Standard

	if wantCustom || wantStandard {
		if len(infos) == 0 {
			return nil, fmt.Errorf("object selectors require the org object list")
		}

		for _, info := range infos {
			if !info.Queryable || info.DeprecatedAndHidden {
				continue
			}
			if !opts.IncludeSystem && isSystemObject(info.Name) {
				continue
			}
			if (wantCustom && info.Custom) || (wantStandard && !info.Custom) {
				key := strings.ToLower(info.Name)
				if _, exists := selected[key]; !exists {
					selected[key] = info.Name
				}
			}
		}
	}

	if len(selected) == 0 {
		return nil, fmt.Errorf("object selection resolved to no objects")
	}

	out := make([]string, 0, len(selected))
	for _, name := range selected {
		out = append(out, name)
	}

	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i]) < strings.ToLower(out[j])
	})

	return out, nil
}

func isSystemObject(name string) bool {
	for _, suffix := range SystemSuffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}

	return false
}
