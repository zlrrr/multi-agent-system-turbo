package knowledge

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
	"gopkg.in/yaml.v3"
)

//go:embed packs/*.yaml
var embeddedPacks embed.FS

// EmbeddedFS exposes the packs compiled into the binary, so the tool works with
// no configuration at all.
func EmbeddedFS() fs.FS { return embeddedPacks }

// Library holds every loaded pack.
type Library struct {
	packs    []*Pack
	problems []error
}

// LoadDefault loads the embedded packs plus any user directories.
func LoadDefault(extraDirs []string) (*Library, error) {
	return Load(embeddedPacks, extraDirs)
}

// Load reads packs from an embedded filesystem and from user directories. A user
// pack with the same id as an embedded one replaces it, so an operator can
// correct shipped knowledge without forking the binary.
//
// One invalid pack does not prevent the others from loading: it is recorded in
// Problems and reported by `mas doctor`.
func Load(embedded fs.FS, extraDirs []string) (*Library, error) {
	l := &Library{}
	byID := map[string]*Pack{}

	if embedded != nil {
		entries, err := fs.ReadDir(embedded, "packs")
		if err == nil {
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				if !e.IsDir() && isPackFile(e.Name()) {
					names = append(names, e.Name())
				}
			}
			sort.Strings(names)
			for _, name := range names {
				b, rerr := fs.ReadFile(embedded, "packs/"+name)
				if rerr != nil {
					l.problems = append(l.problems, errs.Wrap(rerr, "MAS-5005", name, rerr.Error()))
					continue
				}
				p, perr := Parse(b, "embedded:"+name)
				if perr != nil {
					l.problems = append(l.problems, perr)
					continue
				}
				byID[p.ID()] = p
			}
		}
	}

	for _, dir := range extraDirs {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // an unreadable subtree is reported once, below
			}
			if d.IsDir() || !isPackFile(d.Name()) {
				return nil
			}
			b, rerr := os.ReadFile(path) //nolint:gosec // operator-supplied directory
			if rerr != nil {
				l.problems = append(l.problems, errs.Wrap(rerr, "MAS-5005", path, rerr.Error()))
				return nil
			}
			p, perr := Parse(b, path)
			if perr != nil {
				l.problems = append(l.problems, perr)
				return nil
			}
			byID[p.ID()] = p // user packs override embedded ones
			return nil
		})
		if err != nil {
			l.problems = append(l.problems, errs.Wrap(err, "MAS-5005", dir, err.Error()))
		}
	}

	for _, p := range byID {
		l.packs = append(l.packs, p)
	}
	sort.Slice(l.packs, func(i, j int) bool { return l.packs[i].ID() < l.packs[j].ID() })
	return l, nil
}

func isPackFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".yaml" || ext == ".yml"
}

// Parse decodes and validates one pack document.
func Parse(b []byte, source string) (*Pack, error) {
	var p Pack
	if err := yaml.Unmarshal(b, &p); err != nil {
		return nil, errs.Wrap(err, "MAS-5001", source, "(document)", err.Error())
	}
	p.Metadata.Source = source
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// For returns the pack covering a middleware at a version. When several packs
// name the same middleware, the one whose version range applies wins; ties go to
// the pack with the more specific range.
func (l *Library) For(kind core.MiddlewareKind, version string) (*Pack, error) {
	var candidates []*Pack
	for _, p := range l.packs {
		if !strings.EqualFold(p.Metadata.Middleware, string(kind)) {
			continue
		}
		if p.AppliesTo(version) {
			candidates = append(candidates, p)
		}
	}
	if len(candidates) == 0 {
		return nil, errs.New("MAS-5003", string(kind))
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		li, lj := len(candidates[i].Metadata.VersionRange), len(candidates[j].Metadata.VersionRange)
		if li != lj {
			return li > lj // a narrower range is more specific
		}
		return candidates[i].ID() < candidates[j].ID()
	})
	return candidates[0], nil
}

// All returns every loaded pack, sorted by id.
func (l *Library) All() []*Pack { return l.packs }

// Middlewares lists the middleware kinds covered.
func (l *Library) Middlewares() []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range l.packs {
		if !seen[p.Metadata.Middleware] {
			seen[p.Metadata.Middleware] = true
			out = append(out, p.Metadata.Middleware)
		}
	}
	sort.Strings(out)
	return out
}

// Problems returns the packs that failed to load, for `mas doctor`.
func (l *Library) Problems() []error { return l.problems }

// Len reports how many packs loaded successfully.
func (l *Library) Len() int { return len(l.packs) }
