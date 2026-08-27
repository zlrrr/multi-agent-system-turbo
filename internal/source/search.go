package source

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// Match is one search hit with the context an operator needs to read it.
type Match struct {
	File   string   `json:"file"`
	Line   int      `json:"line"`
	Text   string   `json:"text"`
	Before []string `json:"before,omitempty"`
	After  []string `json:"after,omitempty"`
}

// SearchOptions bounds a search so a large repository cannot exhaust memory or
// time.
type SearchOptions struct {
	MaxMatches    int
	MaxFileBytes  int64
	ContextLines  int
	Extensions    []string
	CaseSensitive bool
}

// DefaultSearchOptions are tuned for reading source, not for grepping binaries.
func DefaultSearchOptions() SearchOptions {
	return SearchOptions{
		MaxMatches: 50, MaxFileBytes: 2 << 20, ContextLines: 2,
		Extensions: []string{
			".c", ".h", ".cc", ".cpp", ".hpp", ".go", ".java", ".scala", ".py",
			".rs", ".rb", ".js", ".ts", ".proto", ".md", ".conf", ".yaml", ".yml",
		},
	}
}

var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "target": true,
	"build": true, "dist": true, ".idea": true, "__pycache__": true,
}

// Search finds a pattern in an acquired source tree.
//
// The pattern is an RE2 regular expression: RE2 has no backtracking, so a
// hostile or careless pattern cannot cause exponential blow-up.
func Search(root, pattern string, opts SearchOptions) ([]Match, error) {
	if opts.MaxMatches <= 0 {
		opts = DefaultSearchOptions()
	}
	expr := pattern
	if !opts.CaseSensitive {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, errs.Wrap(err, "MAS-4404", err.Error())
	}
	if strings.Contains(root, "..") {
		return nil, errs.New("MAS-4404", "search root contains path traversal")
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return nil, errs.New("MAS-4402", "search root", "no source tree at "+root)
	}

	wanted := map[string]bool{}
	for _, e := range opts.Extensions {
		wanted[e] = true
	}

	var matches []Match
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable subtree is skipped, not fatal
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if len(wanted) > 0 && !wanted[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > opts.MaxFileBytes {
			return nil
		}
		found, ferr := searchFile(root, path, re, opts, opts.MaxMatches-len(matches))
		if ferr != nil {
			return nil
		}
		matches = append(matches, found...)
		if len(matches) >= opts.MaxMatches {
			return filepath.SkipAll
		}
		return nil
	})
	if walkErr != nil {
		return nil, errs.Wrap(walkErr, "MAS-4402", "search", walkErr.Error())
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].File != matches[j].File {
			return matches[i].File < matches[j].File
		}
		return matches[i].Line < matches[j].Line
	})
	return matches, nil
}

func searchFile(root, path string, re *regexp.Regexp, opts SearchOptions, budget int) ([]Match, error) {
	if budget <= 0 {
		return nil, nil
	}
	f, err := os.Open(path) //nolint:gosec // path comes from walking a fetched tree
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}

	var (
		out     []Match
		window  []string
		lineNo  int
		pending []*Match
	)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		lineNo++
		line := sc.Text()

		// Attach trailing context to matches still awaiting it.
		for _, m := range pending {
			if len(m.After) < opts.ContextLines {
				m.After = append(m.After, line)
			}
		}
		pending = filterComplete(pending, opts.ContextLines)

		if re.MatchString(line) && len(out)+len(pending) < budget {
			m := Match{File: filepath.ToSlash(rel), Line: lineNo, Text: line}
			m.Before = append(m.Before, window...)
			out = append(out, m)
			pending = append(pending, &out[len(out)-1])
		}

		window = append(window, line)
		if len(window) > opts.ContextLines {
			window = window[1:]
		}
		if len(out) >= budget && len(pending) == 0 {
			break
		}
	}
	if err := sc.Err(); err != nil {
		return out, nil // a binary or malformed file yields what was found
	}
	return out, nil
}

func filterComplete(pending []*Match, want int) []*Match {
	kept := pending[:0]
	for _, m := range pending {
		if len(m.After) < want {
			kept = append(kept, m)
		}
	}
	return kept
}
