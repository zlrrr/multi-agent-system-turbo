package knowledge

// Version-range overlap detection, used to decide whether two declarations of
// one rule id are variants (allowed) or an ambiguity (refused).
//
// Governs: specs/007-version-scoped-rules/design-lld.md §3

// interval is a versionRange reduced to a range over version vectors.
//
// The reduction is lossy in exactly one direction. `!=` can only punch a hole
// in an interval, and a hole can only make two intervals *less* overlapping —
// so ignoring it can never turn a real overlap into an apparent disjointness.
// That bias is the whole point: a false "overlap" is a load error the author
// sees on their next `mas packs`, while a false "disjoint" is an ambiguity that
// surfaces as the wrong metric name in the middle of an incident (plan.md
// RSK-2).
type interval struct {
	lo, hi         []int // nil means unbounded on that side
	loOpen, hiOpen bool  // true for a strict comparison
	unbounded      bool  // an empty range: matches every version
}

// toInterval reduces a range to an interval. An unparseable range comes back
// unbounded, which makes it overlap everything — the conservative direction.
func toInterval(raw string) interval {
	vr, err := parseVersionRange(raw)
	if err != nil || len(vr.checks) == 0 {
		return interval{unbounded: true}
	}

	iv := interval{}
	for _, c := range vr.checks {
		switch c.op {
		case ">=":
			iv.raiseLo(c.version, false)
		case ">":
			iv.raiseLo(c.version, true)
		case "<=":
			iv.lowerHi(c.version, false)
		case "<":
			iv.lowerHi(c.version, true)
		case "==":
			iv.raiseLo(c.version, false)
			iv.lowerHi(c.version, false)
		case "!=":
			// Not modelled; see the type comment.
		}
	}
	if iv.lo == nil && iv.hi == nil {
		iv.unbounded = true
	}
	return iv
}

func (iv *interval) raiseLo(v []int, open bool) {
	if iv.lo == nil || compareVersions(v, iv.lo) > 0 {
		iv.lo, iv.loOpen = v, open
		return
	}
	if compareVersions(v, iv.lo) == 0 && open {
		iv.loOpen = true
	}
}

func (iv *interval) lowerHi(v []int, open bool) {
	if iv.hi == nil || compareVersions(v, iv.hi) < 0 {
		iv.hi, iv.hiOpen = v, open
		return
	}
	if compareVersions(v, iv.hi) == 0 && open {
		iv.hiOpen = true
	}
}

// rangesOverlap reports whether any version could satisfy both ranges.
//
// An empty range means "every version", so an unscoped declaration overlaps
// everything — which is why a rule id may only repeat when every one of its
// declarations carries a range (FR-003).
func rangesOverlap(a, b string) bool {
	x, y := toInterval(a), toInterval(b)
	if x.unbounded || y.unbounded {
		return true
	}
	return !separated(x, y) && !separated(y, x)
}

// separated reports whether x lies entirely below y.
func separated(x, y interval) bool {
	if x.hi == nil || y.lo == nil {
		return false
	}
	cmp := compareVersions(x.hi, y.lo)
	if cmp < 0 {
		return true
	}
	// They meet at a point: it belongs to both only if both ends include it.
	return cmp == 0 && (x.hiOpen || y.loOpen)
}
