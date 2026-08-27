package eval

import (
	"encoding/json"
	"os"
	"sort"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// Class is what an outcome amounts to, in the vocabulary a baseline records.
//
// Deliberately a small closed set rather than a number. A baseline of counts
// would compare a change that turns one hit into a false conclusion and one
// miss into a hit as *unchanged*, and those two movements are the exact pair
// feature 006 refuses to average: one leaves an operator where they started,
// the other sends them somewhere wrong with confidence
// (specs/008-regression-baselines/design-hld.md §2).
type Class string

const (
	ClassHit       Class = "hit"
	ClassMiss      Class = "miss"       // expected modes were not concluded
	ClassWrong     Class = "wrong"      // a mode the case rules out was concluded
	ClassGapMissed Class = "gap-missed" // an expected gap was not declared
	ClassError     Class = "error"      // the run itself failed
)

// Class reduces an outcome to the one word a baseline records.
//
// `wrong` outranks `miss` when both apply: a run that missed the answer and
// reached a ruled-out one is the more serious of the two, and a class has to
// pick one. Nothing is lost by the ordering — the ids are recorded alongside.
func (o Outcome) Class() Class {
	switch {
	case o.Err != nil || o.ErrText != "":
		return ClassError
	case len(o.False) > 0:
		return ClassWrong
	case len(o.Missing) > 0:
		return ClassMiss
	case len(o.MissingGaps) > 0:
		return ClassGapMissed
	default:
		return ClassHit
	}
}

// Cell is one (case, topology, model) result, as recorded.
type Cell struct {
	Case       string   `json:"case"`
	Topology   string   `json:"topology"`
	Model      string   `json:"model"`
	Class      Class    `json:"class"`
	Missing    []string `json:"missing,omitempty"`
	False      []string `json:"false_conclusions,omitempty"`
	GapsMissed []string `json:"gaps_missed,omitempty"`
}

// Key identifies the cell across runs.
func (c Cell) Key() string { return c.Case + "|" + c.Topology + "|" + c.Model }

// sameFailure reports whether two cells fail for the same reasons.
//
// The ids and not the class alone: a cell that was missing one mode and now
// reaches a wrong one has moved, even though both are "not a hit"
// (design-hld.md §3).
func (c Cell) sameFailure(other Cell) bool {
	return c.Class == other.Class &&
		equalIDs(c.Missing, other.Missing) &&
		equalIDs(c.False, other.False) &&
		equalIDs(c.GapsMissed, other.GapsMissed)
}

func equalIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Baseline is a recorded run: one cell per (case, topology, model).
type Baseline struct {
	Version  int    `json:"version"`
	Provider string `json:"provider"`
	// Recorded is a date, not a timestamp. A baseline rewritten with no change
	// of content should produce no diff at all (FR-012).
	Recorded string `json:"recorded"`
	Corpus   int    `json:"corpus"`
	Cells    []Cell `json:"cells"`
}

// baselineVersion is the file's schema version.
const baselineVersion = 1

// NewBaseline records a summary.
func NewBaseline(s Summary) Baseline {
	b := Baseline{
		Version:  baselineVersion,
		Provider: s.Provider,
		Recorded: time.Now().UTC().Format("2006-01-02"),
		Corpus:   s.Cases,
	}
	for _, o := range s.Outcomes {
		b.Cells = append(b.Cells, Cell{
			Case: o.Case, Topology: o.Topology, Model: o.Model, Class: o.Class(),
			Missing: o.Missing, False: o.False, GapsMissed: o.MissingGaps,
		})
	}
	b.sortCells()
	return b
}

func (b *Baseline) sortCells() {
	sort.Slice(b.Cells, func(i, j int) bool { return b.Cells[i].Key() < b.Cells[j].Key() })
}

// Encode renders the baseline as it is stored: indented JSON with cells in key
// order, so the file is reviewed as a diff rather than as a blob.
func (b Baseline) Encode() ([]byte, error) {
	b.sortCells()
	out, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// Save writes the baseline. It is called by `--write-baseline` and by nothing
// else: a baseline that writes itself records whatever happened and can never
// fail (CON-003).
func (b Baseline) Save(path string) error {
	out, err := b.Encode()
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600) //nolint:gosec // operator-named path
}

// ParseBaseline decodes a baseline, refusing a file that is not one.
func ParseBaseline(raw []byte) (Baseline, error) {
	var b Baseline
	if err := json.Unmarshal(raw, &b); err != nil {
		return Baseline{}, errs.Wrap(err, "MAS-9106", "<bytes>", err.Error())
	}
	// A file that merely parses as JSON is not a baseline. Treating it as an
	// empty one would make every cell "new", which reads as a clean comparison.
	if b.Version == 0 || b.Cells == nil {
		return Baseline{}, errs.New("MAS-9106", "<bytes>",
			"no version or cells: this is not a baseline")
	}
	b.sortCells()
	return b, nil
}

// LoadBaseline reads a baseline from disk.
func LoadBaseline(path string) (Baseline, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // operator-named path
	if err != nil {
		return Baseline{}, errs.Wrap(err, "MAS-9106", path, err.Error())
	}
	b, err := ParseBaseline(raw)
	if err != nil {
		return Baseline{}, errs.New("MAS-9106", path, err.Error())
	}
	return b, nil
}

// index keys a baseline's cells for comparison.
func (b Baseline) index() map[string]Cell {
	out := make(map[string]Cell, len(b.Cells))
	for _, c := range b.Cells {
		out[c.Key()] = c
	}
	return out
}
