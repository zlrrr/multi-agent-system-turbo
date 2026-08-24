// Package core holds the diagnostic domain model. Per Constitution Art. VII.4 it
// depends on the standard library and pkg/errs only, so every other package can
// speak this vocabulary without creating a cycle.
//
// Governs: specs/001-mvp-core/design-lld.md §2.2
package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// ReportSchema is the wire-format version of Report. Machine consumers pin it.
const ReportSchema = "report/v1"

// MiddlewareKind identifies a middleware family. It is an open vocabulary:
// adding a kind requires only a knowledge pack (G2.1).
type MiddlewareKind string

const (
	KindRedis     MiddlewareKind = "redis"
	KindKafka     MiddlewareKind = "kafka"
	KindMongoDB   MiddlewareKind = "mongodb"
	KindPulsar    MiddlewareKind = "pulsar"
	KindMilvus    MiddlewareKind = "milvus"
	KindOceanBase MiddlewareKind = "oceanbase"
)

// Mode selects how much of the live environment a run may read.
//
// ModeOffline restricts the run to telemetry backends. ModeOnline additionally
// permits read-only reads against the environment itself. Neither mode permits
// any mutation — that is a constitutional invariant, not a mode (Art. IV.1).
type Mode string

const (
	ModeOffline Mode = "offline"
	ModeOnline  Mode = "online"
)

// Window is a closed time interval.
type Window struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// Duration returns the window length.
func (w Window) Duration() time.Duration { return w.To.Sub(w.From) }

// Validate reports whether the window is usable.
func (w Window) Validate() error {
	if w.From.IsZero() || w.To.IsZero() {
		return errs.New("MAS-1010", "both from and to must be set")
	}
	if !w.From.Before(w.To) {
		return errs.New("MAS-1010", fmt.Sprintf("from (%s) must precede to (%s)",
			w.From.Format(time.RFC3339), w.To.Format(time.RFC3339)))
	}
	return nil
}

// EnvBinding names the environment a target lives in.
type EnvBinding struct {
	Name      string `json:"name"`
	Type      string `json:"type"` // "kubernetes" | "local"
	Namespace string `json:"namespace,omitempty"`
	Selector  string `json:"selector,omitempty"`
}

// EndpointOverrides lets a target pin specific telemetry sources by name.
type EndpointOverrides struct {
	MetricsSource string `json:"metrics_source,omitempty"`
	LogsSource    string `json:"logs_source,omitempty"`
}

// Instance is one concrete process or pod backing a target.
type Instance struct {
	Name    string            `json:"name"`
	Address string            `json:"address,omitempty"`
	Node    string            `json:"node,omitempty"`
	Status  string            `json:"status,omitempty"`
	Labels  map[string]string `json:"labels,omitempty"`
}

// Target is a resolved middleware deployment under diagnosis.
type Target struct {
	ID        string            `json:"id"`
	Kind      MiddlewareKind    `json:"kind"`
	Version   string            `json:"version,omitempty"`
	Env       EnvBinding        `json:"env"`
	Labels    map[string]string `json:"labels,omitempty"`
	Selector  string            `json:"selector,omitempty"`
	Instances []Instance        `json:"instances,omitempty"`
	Endpoints EndpointOverrides `json:"endpoints,omitempty"`
}

// Budget caps what one run may consume. Exceeding a cap truncates the run and is
// reported; it is never a hard failure (HLD §8).
type Budget struct {
	MaxSteps     int           `json:"max_steps"`
	MaxToolCalls int           `json:"max_tool_calls"`
	MaxTokens    int           `json:"max_tokens"`
	MaxWall      time.Duration `json:"max_wall"`
}

// DiagnoseRequest is one diagnostic ask.
type DiagnoseRequest struct {
	Target   string            `json:"target"`
	Symptom  string            `json:"symptom"`
	Window   Window            `json:"window"`
	Mode     Mode              `json:"mode"`
	Topology string            `json:"topology"`
	Budget   Budget            `json:"budget"`
	Language string            `json:"language,omitempty"` // report language: "en" | "zh"
	Options  map[string]string `json:"options,omitempty"`
}

// EvidenceKind types the payload an Evidence carries.
type EvidenceKind string

const (
	EvidenceMetricSeries  EvidenceKind = "metric_series"
	EvidenceLogLines      EvidenceKind = "log_lines"
	EvidenceKubeObject    EvidenceKind = "kube_object"
	EvidenceHostState     EvidenceKind = "host_state"
	EvidenceCommandOutput EvidenceKind = "command_output"
	EvidenceSourceRef     EvidenceKind = "source_ref"
	EvidenceNote          EvidenceKind = "note"
)

// Evidence is the single envelope every collector produces and every reasoner
// consumes. Nothing downstream sees a raw client response (HLD §1).
type Evidence struct {
	ID          string       `json:"id"`
	Kind        EvidenceKind `json:"kind"`
	Source      string       `json:"source"`
	Query       string       `json:"query,omitempty"`
	CollectedAt time.Time    `json:"collected_at"`
	Payload     any          `json:"payload,omitempty"`
	Summary     string       `json:"summary"`
	Truncated   bool         `json:"truncated,omitempty"`
	Digest      string       `json:"digest"`
}

// ComputeDigest sets Digest from the canonical JSON encoding of the payload. It
// backs deduplication and replay verification.
func (e *Evidence) ComputeDigest() {
	b, err := json.Marshal(e.Payload)
	if err != nil {
		b = []byte(e.Summary)
	}
	sum := sha256.Sum256(append([]byte(string(e.Kind)+"|"+e.Query+"|"), b...))
	e.Digest = hex.EncodeToString(sum[:])
}

// GapReason explains why an intended piece of evidence is missing.
type GapReason string

const (
	GapUnavailable   GapReason = "unavailable"
	GapRefused       GapReason = "refused"
	GapTruncated     GapReason = "truncated"
	GapNotConfigured GapReason = "not_configured"
	GapUnsupported   GapReason = "unsupported"
)

// Gap records evidence that could not be collected. A Gap never aborts a run
// (FR-013); it is reported alongside its effect on confidence.
type Gap struct {
	ID     string    `json:"id"`
	Intent string    `json:"intent"`
	Reason GapReason `json:"reason"`
	Code   string    `json:"code,omitempty"`
	Detail string    `json:"detail,omitempty"`
	Impact string    `json:"impact,omitempty"`
}

// Severity grades a finding.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityMajor    Severity = "major"
	SeverityMinor    Severity = "minor"
	SeverityInfo     Severity = "info"
)

var severityRank = map[Severity]int{
	SeverityCritical: 0, SeverityMajor: 1, SeverityMinor: 2, SeverityInfo: 3,
}

// Finding is an observation attributable to a rule or an agent. Origin is
// "rule:<playbook-id>/<step-id>" or "agent:<role>", so every statement in a
// report can be traced to what produced it.
type Finding struct {
	ID         string   `json:"id"`
	Origin     string   `json:"origin"`
	Severity   Severity `json:"severity"`
	Statement  string   `json:"statement"`
	Detail     string   `json:"detail,omitempty"`
	Evidence   []string `json:"evidence,omitempty"`
	Confidence float64  `json:"confidence"`
}

// HypothesisStatus tracks a hypothesis through critique.
type HypothesisStatus string

const (
	HypothesisProposed     HypothesisStatus = "proposed"
	HypothesisSupported    HypothesisStatus = "supported"
	HypothesisRefuted      HypothesisStatus = "refuted"
	HypothesisInconclusive HypothesisStatus = "inconclusive"
)

// Hypothesis is a candidate explanation with the evidence for and against it.
type Hypothesis struct {
	ID            string           `json:"id"`
	Statement     string           `json:"statement"`
	Status        HypothesisStatus `json:"status"`
	Confidence    float64          `json:"confidence"`
	Supporting    []string         `json:"supporting,omitempty"`
	Contradicting []string         `json:"contradicting,omitempty"`
	Rationale     string           `json:"rationale,omitempty"`
	Rank          int              `json:"rank"`
}

// Risk grades how dangerous a recommended action is for a human to perform.
type Risk string

const (
	RiskLow    Risk = "low"
	RiskMedium Risk = "medium"
	RiskHigh   Risk = "high"
)

// Recommendation is advice for a human operator. Advisory is always true: the
// system never performs the action (CON-003).
type Recommendation struct {
	Statement string   `json:"statement"`
	Risk      Risk     `json:"risk"`
	Rationale string   `json:"rationale,omitempty"`
	Refs      []string `json:"refs,omitempty"`
	Advisory  bool     `json:"advisory"`
}

// NewRecommendation constructs a recommendation with the advisory invariant set.
func NewRecommendation(statement string, risk Risk, rationale string, refs ...string) Recommendation {
	return Recommendation{Statement: statement, Risk: risk, Rationale: rationale, Refs: refs, Advisory: true}
}

// Usage accounts for what a run consumed (FR-019).
type Usage struct {
	LLMCalls         int     `json:"llm_calls"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	ToolCalls        int     `json:"tool_calls"`
	CostUSD          float64 `json:"cost_usd,omitempty"`
	WallMillis       int64   `json:"wall_millis"`
}

// Add accumulates another usage record into this one.
func (u *Usage) Add(o Usage) {
	u.LLMCalls += o.LLMCalls
	u.PromptTokens += o.PromptTokens
	u.CompletionTokens += o.CompletionTokens
	u.ToolCalls += o.ToolCalls
	u.CostUSD += o.CostUSD
}

// Report is what an operator reads and what a machine consumer parses.
type Report struct {
	Schema          string           `json:"schema"`
	RunID           string           `json:"run_id"`
	GeneratedAt     time.Time        `json:"generated_at"`
	Target          Target           `json:"target"`
	Request         DiagnoseRequest  `json:"request"`
	Topology        string           `json:"topology"`
	Summary         string           `json:"summary"`
	Hypotheses      []Hypothesis     `json:"hypotheses"`
	Findings        []Finding        `json:"findings"`
	ChecksPassed    []string         `json:"checks_passed"`
	Gaps            []Gap            `json:"gaps"`
	Recommendations []Recommendation `json:"recommendations"`
	Evidence        []Evidence       `json:"evidence"`
	Usage           Usage            `json:"usage"`
	Truncated       bool             `json:"truncated"`
	Notes           []string         `json:"notes,omitempty"`
}

// SortHypotheses orders hypotheses by descending confidence, refuted ones last,
// and assigns Rank. Ordering is total and deterministic (NFR-010).
func (r *Report) SortHypotheses() {
	sort.SliceStable(r.Hypotheses, func(i, j int) bool {
		a, b := r.Hypotheses[i], r.Hypotheses[j]
		ar, br := a.Status == HypothesisRefuted, b.Status == HypothesisRefuted
		if ar != br {
			return br
		}
		if a.Confidence != b.Confidence {
			return a.Confidence > b.Confidence
		}
		return a.ID < b.ID
	})
	for i := range r.Hypotheses {
		r.Hypotheses[i].Rank = i + 1
	}
}

// SortFindings orders findings by severity then confidence then ID.
func (r *Report) SortFindings() {
	sort.SliceStable(r.Findings, func(i, j int) bool {
		a, b := r.Findings[i], r.Findings[j]
		if severityRank[a.Severity] != severityRank[b.Severity] {
			return severityRank[a.Severity] < severityRank[b.Severity]
		}
		if a.Confidence != b.Confidence {
			return a.Confidence > b.Confidence
		}
		return a.ID < b.ID
	})
}

// Validate asserts the report invariants from LLD §2.2.
func (r *Report) Validate() error {
	var problems []string
	if r.Schema != ReportSchema {
		problems = append(problems, fmt.Sprintf("schema is %q, want %q", r.Schema, ReportSchema))
	}
	if r.RunID == "" {
		problems = append(problems, "run_id is empty")
	}
	ids := map[string]bool{}
	for _, e := range r.Evidence {
		if e.ID == "" {
			problems = append(problems, "evidence with empty id")
			continue
		}
		if ids[e.ID] {
			problems = append(problems, "duplicate evidence id "+e.ID)
		}
		ids[e.ID] = true
	}
	for _, h := range r.Hypotheses {
		if h.Confidence < 0 || h.Confidence > 1 {
			problems = append(problems, fmt.Sprintf("hypothesis %s confidence %v out of [0,1]", h.ID, h.Confidence))
		}
	}
	for _, f := range r.Findings {
		if f.Confidence < 0 || f.Confidence > 1 {
			problems = append(problems, fmt.Sprintf("finding %s confidence %v out of [0,1]", f.ID, f.Confidence))
		}
	}
	for i, rec := range r.Recommendations {
		if !rec.Advisory {
			problems = append(problems, fmt.Sprintf("recommendation %d is not marked advisory", i))
		}
	}
	if len(problems) > 0 {
		return errs.New("MAS-9001", strings.Join(problems, "; "))
	}
	return nil
}

// StepKind types a run-record step.
type StepKind string

const (
	StepToolCall StepKind = "tool_call"
	StepLLMCall  StepKind = "llm_call"
	StepRuleEval StepKind = "rule_eval"
	StepPhase    StepKind = "phase"
	StepNote     StepKind = "note"
)

// Step is one append-only entry in a run record.
type Step struct {
	ID             string    `json:"id"`
	Kind           StepKind  `json:"kind"`
	At             time.Time `json:"at"`
	DurationMillis int64     `json:"duration_millis"`
	Actor          string    `json:"actor"`
	Name           string    `json:"name"`
	Input          any       `json:"input,omitempty"`
	Output         any       `json:"output,omitempty"`
	Code           string    `json:"code,omitempty"`
	Err            string    `json:"error,omitempty"`
}

// RunStatus tracks a run's lifecycle.
type RunStatus string

const (
	RunRunning   RunStatus = "running"
	RunCompleted RunStatus = "completed"
	RunFailed    RunStatus = "failed"
)

// RunRecord is the auditable, replayable trace of one diagnostic run
// (Constitution Art. V.3).
type RunRecord struct {
	ID         string            `json:"id"`
	Status     RunStatus         `json:"status"`
	Request    DiagnoseRequest   `json:"request"`
	Target     Target            `json:"target"`
	StartedAt  time.Time         `json:"started_at"`
	FinishedAt time.Time         `json:"finished_at,omitempty"`
	Steps      []Step            `json:"steps"`
	Report     *Report           `json:"report,omitempty"`
	Usage      Usage             `json:"usage"`
	Versions   map[string]string `json:"versions,omitempty"`
}

// RunSummary is the listing projection of a run.
type RunSummary struct {
	ID         string    `json:"id"`
	Status     RunStatus `json:"status"`
	Target     string    `json:"target"`
	Symptom    string    `json:"symptom"`
	Topology   string    `json:"topology"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	Hypotheses int       `json:"hypotheses"`
}

// Summarise projects a run record into its listing form.
func (r *RunRecord) Summarise() RunSummary {
	s := RunSummary{
		ID: r.ID, Status: r.Status, Target: r.Request.Target, Symptom: r.Request.Symptom,
		Topology: r.Request.Topology, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
	}
	if r.Report != nil {
		s.Hypotheses = len(r.Report.Hypotheses)
	}
	return s
}

// SeriesStats is the collector-independent numeric summary of a metric result.
//
// Latest is the maximum of each series' most recent value, because threshold
// checks nearly always mean "is any instance over the line"; LatestMin gives the
// other end for "have all instances recovered" checks.
type SeriesStats struct {
	Empty     bool               `json:"empty"`
	Series    int                `json:"series"`
	Count     int                `json:"count"`
	Latest    float64            `json:"latest"`
	LatestMin float64            `json:"latest_min"`
	Min       float64            `json:"min"`
	Max       float64            `json:"max"`
	Avg       float64            `json:"avg"`
	Sum       float64            `json:"sum"`
	Delta     float64            `json:"delta"`
	ByLabel   map[string]float64 `json:"by_label"`
}

// SeriesPayload is implemented by evidence payloads carrying numeric series.
// Reasoning code depends on this interface rather than on any collector, which
// is what keeps the layering rule in design-hld.md §3 enforceable.
type SeriesPayload interface {
	Stats() SeriesStats
}

// LinesPayload is implemented by evidence payloads carrying log lines.
type LinesPayload interface {
	LogLines() []string
}
