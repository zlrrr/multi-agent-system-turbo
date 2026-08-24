// Package report renders a diagnosis for the two audiences that read it: an
// operator during an incident, and a machine consuming report/v1.
//
// Governs: specs/001-mvp-core/design-lld.md §2.16
package report

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// JSON renders the machine-readable form.
func JSON(r *core.Report) ([]byte, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, errs.Wrap(err, "MAS-9002", err.Error())
	}
	return append(b, '\n'), nil
}

// phrases holds the bilingual chrome of the report. Findings, hypotheses and
// recommendations are already in the run's language; only the structure needs
// translating here.
type phrases struct {
	title, generated, target, window, mode, topology            string
	summary, hypotheses, findings, checks, gaps, recs, evidence string
	usage, noHypotheses, noFindings, noGaps, noRecs, noEvidence string
	run, symptom, cost                                          string
	confidence, status, severity, origin, risk, advisoryNote    string
	truncatedNote, supporting, contradicting, rationale, impact string
	llmCalls, toolCalls, tokens, duration, rank, checksNone     string
}

var en = phrases{
	title: "Diagnostic report", generated: "Generated", target: "Target", window: "Window",
	mode: "Mode", topology: "Topology", summary: "Summary", hypotheses: "Hypotheses",
	findings: "Findings", checks: "Checks that passed", gaps: "Gaps in the evidence",
	recs: "Recommended next steps", evidence: "Evidence", usage: "Run accounting",
	noHypotheses: "No hypothesis could be formed from the available evidence.",
	noFindings:   "No findings were produced.",
	noGaps:       "Every attempted collection succeeded.",
	noRecs:       "No recommendations were produced.",
	noEvidence:   "No evidence was collected.",
	confidence:   "confidence", status: "status", severity: "severity", origin: "from",
	risk: "risk",
	advisoryNote: "These are recommendations for a human operator. MAS-Turbo is read-only: " +
		"it has not performed, scheduled or arranged any of them.",
	truncatedNote: "This run was truncated before it finished",
	supporting:    "Supporting", contradicting: "Contradicting", rationale: "Reasoning",
	impact:   "Effect on this analysis",
	llmCalls: "Model calls", toolCalls: "Tool calls", tokens: "Tokens", duration: "Duration",
	rank: "Rank", checksNone: "No checks completed successfully.",
	run: "Run", symptom: "Symptom", cost: "Cost",
}

var zh = phrases{
	title: "诊断报告", generated: "生成时间", target: "诊断目标", window: "时间窗口",
	mode: "运行模式", topology: "拓扑", summary: "结论摘要", hypotheses: "假设",
	findings: "发现", checks: "已通过的检查", gaps: "证据缺口",
	recs: "建议的后续动作", evidence: "证据", usage: "运行统计",
	noHypotheses: "现有证据不足以形成任何假设。",
	noFindings:   "未产生任何发现。",
	noGaps:       "所有尝试的采集均成功。",
	noRecs:       "未产生任何建议。",
	noEvidence:   "未采集到任何证据。",
	confidence:   "置信度", status: "状态", severity: "严重级别", origin: "来源",
	risk:          "风险",
	advisoryNote:  "以下均为给人类运维人员的建议。MAS-Turbo 是只读的：它没有执行、也没有安排其中任何一项操作。",
	truncatedNote: "本次运行在完成之前被截断",
	supporting:    "支持证据", contradicting: "反对证据", rationale: "推理过程",
	impact:   "对本次分析的影响",
	llmCalls: "模型调用", toolCalls: "工具调用", tokens: "Token", duration: "耗时",
	rank: "排名", checksNone: "没有检查成功完成。",
	run: "运行编号", symptom: "症状描述", cost: "成本",
}

func phrasesFor(lang string) phrases {
	if lang == "zh" {
		return zh
	}
	return en
}

var statusText = map[string]map[core.HypothesisStatus]string{
	"en": {
		core.HypothesisSupported:    "supported by the evidence",
		core.HypothesisRefuted:      "refuted by the evidence",
		core.HypothesisInconclusive: "inconclusive — the deciding evidence was not available",
		core.HypothesisProposed:     "proposed, not yet challenged",
	},
	"zh": {
		core.HypothesisSupported:    "证据支持",
		core.HypothesisRefuted:      "证据反驳",
		core.HypothesisInconclusive: "无法定论 —— 决定性证据未能获取",
		core.HypothesisProposed:     "已提出，尚未被挑战",
	},
}

var riskText = map[string]map[core.Risk]string{
	"en": {core.RiskLow: "low", core.RiskMedium: "medium", core.RiskHigh: "high"},
	"zh": {core.RiskLow: "低", core.RiskMedium: "中", core.RiskHigh: "高"},
}

var severityText = map[string]map[core.Severity]string{
	"en": {core.SeverityCritical: "critical", core.SeverityMajor: "major",
		core.SeverityMinor: "minor", core.SeverityInfo: "info"},
	"zh": {core.SeverityCritical: "严重", core.SeverityMajor: "主要",
		core.SeverityMinor: "次要", core.SeverityInfo: "提示"},
}

var reasonText = map[string]map[core.GapReason]string{
	"en": {
		core.GapUnavailable:   "source unavailable",
		core.GapRefused:       "refused by the safety guard",
		core.GapTruncated:     "truncated at a budget",
		core.GapNotConfigured: "not configured",
		core.GapUnsupported:   "unsupported",
	},
	"zh": {
		core.GapUnavailable:   "数据源不可用",
		core.GapRefused:       "被安全守卫拒绝",
		core.GapTruncated:     "因预算被截断",
		core.GapNotConfigured: "未配置",
		core.GapUnsupported:   "不支持",
	},
}

// Markdown renders the operator-facing form.
//
// The section order is the order an on-call engineer needs them: the conclusion
// first, then what supports it, then — deliberately before the recommendations —
// what could not be checked, so nobody acts on advice without seeing its limits.
func Markdown(r *core.Report, lang string) ([]byte, error) {
	if r == nil {
		return nil, errs.New("MAS-9001", "cannot render a nil report")
	}
	p := phrasesFor(lang)
	var b strings.Builder

	fmt.Fprintf(&b, "# %s: %s\n\n", p.title, r.Target.ID)

	fmt.Fprintf(&b, "| | |\n|---|---|\n")
	fmt.Fprintf(&b, "| %s | `%s` |\n", p.run, r.RunID)
	fmt.Fprintf(&b, "| %s | %s |\n", p.target, describeTarget(r.Target))
	fmt.Fprintf(&b, "| %s | %s |\n", p.symptom, r.Request.Symptom)
	fmt.Fprintf(&b, "| %s | %s → %s (%s) |\n", p.window,
		r.Request.Window.From.Format(time.RFC3339), r.Request.Window.To.Format(time.RFC3339),
		r.Request.Window.Duration())
	fmt.Fprintf(&b, "| %s | %s |\n", p.mode, r.Request.Mode)
	fmt.Fprintf(&b, "| %s | %s |\n", p.topology, r.Topology)
	fmt.Fprintf(&b, "| %s | %s |\n\n", p.generated, r.GeneratedAt.Format(time.RFC3339))

	if r.Truncated {
		fmt.Fprintf(&b, "> ⚠️ %s.\n\n", p.truncatedNote)
	}

	fmt.Fprintf(&b, "## %s\n\n%s\n\n", p.summary, orText(r.Summary, p.noHypotheses))

	fmt.Fprintf(&b, "## %s\n\n", p.hypotheses)
	if len(r.Hypotheses) == 0 {
		fmt.Fprintf(&b, "%s\n\n", p.noHypotheses)
	}
	for _, h := range r.Hypotheses {
		fmt.Fprintf(&b, "### %d. %s\n\n", h.Rank, h.Statement)
		fmt.Fprintf(&b, "- **%s**: %s\n", p.status, statusFor(lang, h.Status))
		fmt.Fprintf(&b, "- **%s**: %.0f%%\n", p.confidence, h.Confidence*100)
		if h.Rationale != "" {
			fmt.Fprintf(&b, "- **%s**: %s\n", p.rationale, h.Rationale)
		}
		if len(h.Supporting) > 0 {
			fmt.Fprintf(&b, "- **%s**: %s\n", p.supporting, strings.Join(h.Supporting, ", "))
		}
		if len(h.Contradicting) > 0 {
			fmt.Fprintf(&b, "- **%s**: %s\n", p.contradicting, strings.Join(h.Contradicting, ", "))
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "## %s\n\n", p.findings)
	if len(r.Findings) == 0 {
		fmt.Fprintf(&b, "%s\n\n", p.noFindings)
	}
	for _, f := range r.Findings {
		fmt.Fprintf(&b, "- **[%s]** %s\n", severityFor(lang, f.Severity), f.Statement)
		if f.Detail != "" {
			fmt.Fprintf(&b, "  - %s\n", f.Detail)
		}
		fmt.Fprintf(&b, "  - %s `%s`, %s %.0f%%", p.origin, f.Origin, p.confidence, f.Confidence*100)
		if len(f.Evidence) > 0 {
			fmt.Fprintf(&b, ", %s: %s", p.evidence, strings.Join(f.Evidence, ", "))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "## %s\n\n", p.checks)
	if len(r.ChecksPassed) == 0 {
		fmt.Fprintf(&b, "%s\n\n", p.checksNone)
	}
	for _, c := range r.ChecksPassed {
		fmt.Fprintf(&b, "- %s\n", c)
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "## %s\n\n", p.gaps)
	if len(r.Gaps) == 0 {
		fmt.Fprintf(&b, "%s\n\n", p.noGaps)
	}
	for _, g := range r.Gaps {
		fmt.Fprintf(&b, "- **%s** — %s", g.Intent, reasonFor(lang, g.Reason))
		if g.Code != "" {
			fmt.Fprintf(&b, " (`%s`)", g.Code)
		}
		b.WriteString("\n")
		if g.Impact != "" {
			fmt.Fprintf(&b, "  - %s: %s\n", p.impact, g.Impact)
		}
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "## %s\n\n", p.recs)
	fmt.Fprintf(&b, "> %s\n\n", p.advisoryNote)
	if len(r.Recommendations) == 0 {
		fmt.Fprintf(&b, "%s\n\n", p.noRecs)
	}
	for i, rec := range r.Recommendations {
		fmt.Fprintf(&b, "%d. **[%s: %s]** %s\n", i+1, p.risk, riskFor(lang, rec.Risk), rec.Statement)
		if rec.Rationale != "" {
			fmt.Fprintf(&b, "   - %s\n", rec.Rationale)
		}
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "## %s\n\n", p.evidence)
	if len(r.Evidence) == 0 {
		fmt.Fprintf(&b, "%s\n\n", p.noEvidence)
	}
	for _, e := range r.Evidence {
		fmt.Fprintf(&b, "- `%s` (%s, %s) %s\n", e.ID, e.Kind, e.Source, e.Summary)
		if e.Query != "" {
			fmt.Fprintf(&b, "  - `%s`\n", e.Query)
		}
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "## %s\n\n", p.usage)
	fmt.Fprintf(&b, "| | |\n|---|---|\n")
	fmt.Fprintf(&b, "| %s | %d |\n", p.llmCalls, r.Usage.LLMCalls)
	fmt.Fprintf(&b, "| %s | %d |\n", p.toolCalls, r.Usage.ToolCalls)
	fmt.Fprintf(&b, "| %s | %d |\n", p.tokens, r.Usage.PromptTokens+r.Usage.CompletionTokens)
	fmt.Fprintf(&b, "| %s | %s |\n", p.duration, time.Duration(r.Usage.WallMillis)*time.Millisecond)
	if r.Usage.CostUSD > 0 {
		fmt.Fprintf(&b, "| %s | $%.4f |\n", p.cost, r.Usage.CostUSD)
	}

	return []byte(b.String()), nil
}

func describeTarget(t core.Target) string {
	s := fmt.Sprintf("`%s` (%s", t.ID, t.Kind)
	if t.Version != "" {
		s += " " + t.Version
	}
	s += ")"
	if t.Env.Type != "" {
		s += ", " + t.Env.Type
		if t.Env.Namespace != "" {
			s += "/" + t.Env.Namespace
		}
	}
	return s
}

func statusFor(lang string, s core.HypothesisStatus) string {
	if v, ok := statusText[langKey(lang)][s]; ok {
		return v
	}
	return string(s)
}

func riskFor(lang string, r core.Risk) string {
	if v, ok := riskText[langKey(lang)][r]; ok {
		return v
	}
	return string(r)
}

func severityFor(lang string, s core.Severity) string {
	if v, ok := severityText[langKey(lang)][s]; ok {
		return v
	}
	return string(s)
}

func reasonFor(lang string, r core.GapReason) string {
	if v, ok := reasonText[langKey(lang)][r]; ok {
		return v
	}
	return string(r)
}

func langKey(lang string) string {
	if lang == "zh" {
		return "zh"
	}
	return "en"
}

func orText(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

// Text renders a terse console form for `mas diagnose` on a narrow terminal.
func Text(r *core.Report, lang string) ([]byte, error) {
	p := phrasesFor(lang)
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s (%s)\n", p.title, r.Target.ID, r.RunID)
	fmt.Fprintf(&b, "%s\n\n", strings.Repeat("=", 60))
	fmt.Fprintf(&b, "%s\n%s\n\n", p.summary, orText(r.Summary, p.noHypotheses))
	for _, h := range r.Hypotheses {
		fmt.Fprintf(&b, "%d. [%s, %.0f%%] %s\n", h.Rank, statusFor(lang, h.Status), h.Confidence*100, h.Statement)
	}
	if len(r.Gaps) > 0 {
		fmt.Fprintf(&b, "\n%s: %d\n", p.gaps, len(r.Gaps))
	}
	if len(r.Recommendations) > 0 {
		fmt.Fprintf(&b, "\n%s\n", p.recs)
		for i, rec := range r.Recommendations {
			fmt.Fprintf(&b, "  %d. [%s] %s\n", i+1, riskFor(lang, rec.Risk), rec.Statement)
		}
	}
	return []byte(b.String()), nil
}
