package eval

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
)

// Caveats are what the numbers do not mean.
//
// They are produced by the code and travel with every result — printed under
// the table, and carried as fields in the JSON — rather than living in the
// manual. A caveat in the manual is absent at the moment someone screenshots
// the table, and the screenshot is what gets forwarded (plan.md D-7).
type Caveats struct {
	Synthetic core.Text `json:"-"`
	Scripted  core.Text `json:"-"`
	Pricing   core.Text `json:"-"`

	// Rendered carries the same statements as plain strings so a JSON consumer
	// cannot drop them by choosing a different formatter.
	Rendered []string `json:"caveats"`
}

func caveatsFor(s Summary, lang string) Caveats {
	c := Caveats{
		Synthetic: core.Text{
			EN: "This corpus is synthetic: it measures agreement with its own labels, " +
				"not accuracy on real incidents. A perfect result means the system behaves " +
				"as the corpus authors expected, which is a weaker claim and a useful one.",
			ZH: "本语料库是合成的：它度量的是与其自身标签的一致程度，而不是在真实故障上的准确率。" +
				"满分意味着系统的行为符合语料库作者的预期 —— 这是一个更弱、但有用的主张。",
		},
		Pricing: core.Text{
			EN: "Cost is only as good as the prices you configured; an unpriced model " +
				"leaves it unknown rather than zero.",
			ZH: "成本的可靠程度不会高于你配置的价格；未定价的模型会让它成为未知，而不是 0。",
		},
	}
	if scripted(s.Provider) {
		c.Scripted = core.Text{
			EN: "The provider is `" + s.Provider + "`, which replays a script that already " +
				"contains the answer. These results say nothing about a model's quality — " +
				"what they exercise is the deterministic phase and the pipeline around it.",
			ZH: "当前 provider 是 `" + s.Provider + "`，它重放的脚本中本就写着答案。" +
				"这些结果对模型质量不说明任何事情 —— 它们检验的是确定性阶段及其周边的流水线。",
		}
	}

	c.Rendered = append(c.Rendered, c.Synthetic.In(lang))
	if !c.Scripted.Empty() {
		c.Rendered = append(c.Rendered, c.Scripted.In(lang))
	}
	c.Rendered = append(c.Rendered, c.Pricing.In(lang))
	return c
}

type evalPhrases struct {
	header, caseCol, topologyCol, result, falseCol, gapsCol, callsCol, costCol string
	hit, miss, failed, wrong, ok, missing                                      string
	corpus, totalsLine                                                         string
}

var evalEN = evalPhrases{
	header: "Corpus", caseCol: "CASE", topologyCol: "TOPOLOGY", result: "RESULT",
	falseCol: "FALSE", gapsCol: "GAPS", callsCol: "CALLS", costCol: "COST",
	hit: "hit", miss: "MISS", failed: "ERROR", wrong: "WRONG", ok: "ok", missing: "missing",
	corpus:     "%d case(s) × %s · provider %s",
	totalsLine: "%-14s %d/%d hit · %d miss · %d false conclusion(s) · %d gap(s) missed",
}

var evalZH = evalPhrases{
	header: "语料库", caseCol: "CASE", topologyCol: "拓扑", result: "结果",
	falseCol: "错误", gapsCol: "缺口", callsCol: "调用", costCol: "成本",
	hit: "命中", miss: "漏判", failed: "错误", wrong: "错误", ok: "ok", missing: "缺失",
	corpus:     "%d 个 case × %s · provider %s",
	totalsLine: "%-14s %d/%d 命中 · %d 漏判 · %d 个错误结论 · %d 个缺口缺失",
}

func phrasesFor(lang string) evalPhrases {
	if lang == "zh" {
		return evalZH
	}
	return evalEN
}

// Render writes the summary as a table followed by its caveats.
func Render(w io.Writer, s Summary, lang string) error {
	p := phrasesFor(lang)

	fmt.Fprintf(w, "%s: "+p.corpus+"\n\n", p.header,
		s.Cases, describeTopologies(s.Topologies), s.Provider)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
		p.caseCol, p.topologyCol, p.result, p.falseCol, p.gapsCol, p.callsCol, p.costCol)
	for _, o := range s.Outcomes {
		result := p.hit
		switch {
		case o.Err != nil:
			result = p.failed
		case len(o.False) > 0:
			result = p.wrong
		case !o.Hit():
			result = p.miss
		}
		gaps := p.ok
		if len(o.MissingGaps) > 0 {
			gaps = p.missing
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%d\t%s\n",
			o.Case, o.Topology, result, len(o.False), gaps,
			o.Usage.LLMCalls, renderCost(o.Usage.Cost, lang))
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	fmt.Fprintln(w)
	for _, t := range s.ByTopology() {
		fmt.Fprintf(w, p.totalsLine+"\n",
			t.Topology, t.Hits, t.Cases, t.Misses, t.False, t.GapsMissed)
	}

	// The detail behind a failure, so a reader does not have to re-run to learn
	// which mode was missed.
	for _, o := range s.Outcomes {
		if o.Hit() && o.Err == nil {
			continue
		}
		fmt.Fprintf(w, "\n  %s / %s:\n", o.Case, o.Topology)
		if o.Err != nil {
			fmt.Fprintf(w, "    error: %v\n", o.Err)
		}
		if len(o.Missing) > 0 {
			fmt.Fprintf(w, "    not concluded: %s\n", strings.Join(o.Missing, ", "))
		}
		if len(o.False) > 0 {
			fmt.Fprintf(w, "    concluded but ruled out by the case: %s\n", strings.Join(o.False, ", "))
		}
		if len(o.MissingGaps) > 0 {
			fmt.Fprintf(w, "    gap not declared: %s\n", strings.Join(o.MissingGaps, ", "))
		}
	}

	fmt.Fprintln(w)
	for _, line := range caveatsFor(s, lang).Rendered {
		fmt.Fprintf(w, "%s\n", wrapText(line, 92))
	}
	return nil
}

// RenderJSON writes the summary with its caveats as fields, so an integration
// cannot drop them by formatting.
func RenderJSON(w io.Writer, s Summary, lang string) error {
	type outcome struct {
		Outcome
		Hit bool `json:"hit"`
	}
	outcomes := make([]outcome, 0, len(s.Outcomes))
	for _, o := range s.Outcomes {
		if o.Err != nil {
			o.ErrText = o.Err.Error()
		}
		outcomes = append(outcomes, outcome{Outcome: o, Hit: o.Hit()})
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{
		"cases":      s.Cases,
		"topologies": s.Topologies,
		"provider":   s.Provider,
		"outcomes":   outcomes,
		"totals":     s.ByTopology(),
		"caveats":    caveatsFor(s, lang).Rendered,
	})
}

// renderCost states an amount or states that nobody knows it, matching the
// report's rule: an unknown cost is never rendered as a number.
func renderCost(c core.Cost, lang string) string {
	switch {
	case c.Known:
		return fmt.Sprintf("$%.4f", c.USD)
	case c.USD > 0:
		if lang == "zh" {
			return fmt.Sprintf("$%.4f·部分未定价", c.USD)
		}
		return fmt.Sprintf("$%.4f·partly", c.USD)
	default:
		if lang == "zh" {
			return "未定价"
		}
		return "unpriced"
	}
}

// wrapText breaks a caveat to a readable width, counting terminal columns so a
// Chinese caveat wraps where a reader expects.
func wrapText(s string, width int) string {
	var b strings.Builder
	col := 0
	for _, r := range s {
		w := 1
		if r >= 0x2E80 && r <= 0xFFE6 {
			w = 2
		}
		if col+w > width && r == ' ' {
			b.WriteString("\n")
			col = 0
			continue
		}
		if col+w > width && w == 2 {
			b.WriteString("\n")
			col = 0
		}
		b.WriteRune(r)
		col += w
	}
	return b.String()
}

var _ = time.Second

// RenderComparisonJSON writes a run and its comparison as one document, so a
// consumer that wants both does not have to parse two.
func RenderComparisonJSON(w io.Writer, s Summary, d Delta, lang string) error {
	d.Caveats = translateCaveats(d, lang)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{
		"cases":      s.Cases,
		"topologies": s.Topologies,
		"provider":   s.Provider,
		"totals":     s.ByTopology(),
		"comparison": d,
	})
}

// deltaPhrases are the words a comparison uses, per language.
type deltaPhrases struct {
	header     string
	none       string
	cellCol    string
	wasCol     string
	detailCol  string
	transition map[Transition]string
}

var deltaEN = deltaPhrases{
	header:    "Compared against the baseline recorded %s under provider %s",
	none:      "Nothing moved: every cell matches the baseline.",
	cellCol:   "CASE / TOPOLOGY / MODEL",
	wasCol:    "WAS",
	detailCol: "WHAT CHANGED",
	transition: map[Transition]string{
		Regressed:      "REGRESSED",
		ChangedFailure: "changed failure",
		Improved:       "improved",
		KnownBad:       "known-bad",
		New:            "new",
		NotRun:         "not run",
	},
}

var deltaZH = deltaPhrases{
	header:    "与 %s 在 provider %s 下记录的基线相比",
	none:      "没有任何变化：每个格子都与基线一致。",
	cellCol:   "CASE / 拓扑 / 模型",
	wasCol:    "原本",
	detailCol: "变化内容",
	transition: map[Transition]string{
		Regressed:      "回归",
		ChangedFailure: "失败方式改变",
		Improved:       "改善",
		KnownBad:       "已知为坏",
		New:            "新增",
		NotRun:         "未运行",
	},
}

func deltaPhrasesFor(lang string) deltaPhrases {
	if lang == "zh" {
		return deltaZH
	}
	return deltaEN
}

// RenderDelta writes a comparison: what regressed, what improved, what is
// known-bad, and what the numbers do not support.
//
// Transitions are grouped and never summed. A single figure here would let a
// fix and a break cancel, which is the collapse this project has refused at
// every layer (specs/008-regression-baselines/design-hld.md §2).
func RenderDelta(w io.Writer, d Delta, lang string) error {
	p := deltaPhrasesFor(lang)

	fmt.Fprintf(w, p.header+"\n", orUnknown(d.Baseline), orUnknown(d.Provider))
	if d.Mismatch != "" {
		fmt.Fprintf(w, "\n! %s\n", wrapText(d.Mismatch, 92))
	}
	fmt.Fprintln(w)

	if len(d.Changes) == 0 {
		fmt.Fprintf(w, "%s\n\n", p.none)
	} else {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.cellCol, p.wasCol, "NOW", p.detailCol)
		for _, c := range d.Changes {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
				c.Cell.Key(), orDashText(string(c.Was)),
				p.transition[c.Transition], c.Detail)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
		fmt.Fprintln(w)
	}

	for _, line := range translateCaveats(d, lang) {
		fmt.Fprintf(w, "%s\n", wrapText(line, 92))
	}
	return nil
}

// RenderDeltaJSON writes the comparison with its caveats as fields, so an
// integration cannot drop them by formatting.
func RenderDeltaJSON(w io.Writer, d Delta, lang string) error {
	d.Caveats = translateCaveats(d, lang)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(d)
}

// translateCaveats re-renders a delta's caveats in the requested language.
// Compare builds them in English because it has no language of its own; the
// renderer is where an operator's language is known.
func translateCaveats(d Delta, lang string) []string {
	if lang != "zh" {
		return d.Caveats
	}
	return deltaCaveats(Summary{Provider: d.Provider}, lang)
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(unknown)"
	}
	return s
}

func orDashText(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
