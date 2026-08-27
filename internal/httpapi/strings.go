package httpapi

import (
	"sort"
	"strings"
)

// consoleStrings is the web console's entire operator-facing vocabulary, in
// both languages: {English, Chinese}.
//
// It lives in Go rather than in the JavaScript for one reason: here its
// bilingual parity is checked by a test that reads the actual table, exactly as
// the error-code registry's is. Prose in a script would be checked by a Go test
// reading JavaScript, or — far more likely — not at all
// (specs/012-web-console/design-hld.md §6).
//
// A two-element array rather than a map keyed by language, so the parity
// question has no way to be answered "the key is missing": every entry has
// exactly two slots and both are checked non-empty.
var consoleStrings = map[string][2]string{
	// chrome
	"app.title":      {"MAS-Turbo console", "MAS-Turbo 控制台"},
	"nav.runs":       {"Diagnoses", "诊断"},
	"nav.targets":    {"Targets", "目标"},
	"nav.system":     {"System", "系统"},
	"action.signout": {"Forget credential", "清除凭据"},
	"action.steps":   {"Step trace →", "步骤轨迹 →"},
	"action.back":    {"← Back to the diagnosis", "← 返回诊断"},
	"foot.advisory": {
		"Every recommendation is advisory. MAS-Turbo analyses and suggests; it never changes anything itself.",
		"所有建议均仅供参考。MAS-Turbo 只做分析与建议，从不亲自变更任何东西。"},
	"foot.readonly": {
		"Read-only by constitution: the system collects evidence and never writes to a target environment.",
		"按宪章只读：本系统只采集证据，绝不写入目标环境。"},

	// the credential gate
	"gate.title": {"Credential required", "需要凭据"},
	"gate.body": {
		"The console reads the API with your bearer token. It is kept for this browser tab only and is never written to a URL or a cookie.",
		"控制台使用你的 Bearer 令牌读取 API。它只保存在当前浏览器标签页内，绝不会写入 URL 或 Cookie。"},
	"gate.placeholder": {"Bearer token", "Bearer 令牌"},
	"gate.submit":      {"Continue", "继续"},
	"gate.insecure": {
		"This page was delivered over plaintext HTTP from a remote host. A credential typed here can be read in transit unless a proxy in front of this server terminates TLS.",
		"本页面是通过明文 HTTP 从远端主机传来的。除非本服务前面有代理终止 TLS，否则在此输入的凭据可能在传输途中被读取。"},

	// run listing
	"runs.title": {"Diagnoses", "诊断"},
	"runs.subtitle": {
		"Runs this credential may see, newest first.",
		"当前凭据可见的运行记录，最新的在前。"},
	"runs.empty": {"No diagnoses have been recorded yet.", "尚无诊断记录。"},

	// run states
	"run.running":   {"running", "进行中"},
	"run.completed": {"completed", "已完成"},
	"run.failed":    {"failed", "已失败"},
	"run.polling": {
		"This diagnosis is still running. The page refreshes itself every few seconds.",
		"该诊断仍在进行中。本页每隔几秒会自行刷新。"},
	"run.noreport": {
		"No report is attached to this run yet.",
		"该运行尚未附带报告。"},
	"run.truncated": {
		"This run hit a configured budget and stopped early, so it produced less than it might have.",
		"该运行触到了配置的预算上限而提前结束，因此产出比它本可以产出的要少。"},

	// run header fields
	"field.target":    {"Target", "目标"},
	"field.kind":      {"Middleware", "中间件"},
	"field.version":   {"Version", "版本"},
	"field.symptom":   {"Symptom", "症状"},
	"field.topology":  {"Topology", "拓扑"},
	"field.tenant":    {"Tenant", "租户"},
	"field.principal": {"Requested by", "发起者"},
	"field.started":   {"Started", "开始"},
	"field.finished":  {"Finished", "结束"},

	// gaps — rendered before the summary, on purpose
	"gaps.title": {"Gaps in the evidence", "证据缺口"},
	"gaps.note": {
		"Evidence the run intended to collect and could not. A conclusion reached without it is a conclusion with a hole in it.",
		"本次运行本打算采集、但没能采集到的证据。缺了它得出的结论，是一个带洞的结论。"},
	"gaps.impact":        {"Impact:", "影响："},
	"gap.unavailable":    {"unavailable", "不可用"},
	"gap.refused":        {"refused", "被拒绝"},
	"gap.truncated":      {"truncated", "被截断"},
	"gap.not_configured": {"not configured", "未配置"},
	"gap.unsupported":    {"unsupported", "不支持"},
	"gap.not_applicable": {"not applicable", "不适用"},

	// summary
	"summary.title":       {"Summary", "摘要"},
	"summary.none":        {"No summary was produced.", "未产出摘要。"},
	"summary.conclusions": {"Failure modes concluded:", "已判定的故障模式："},

	// hypotheses
	"hyp.title": {"Hypotheses", "假设"},
	"hyp.note": {
		"Ranked by confidence. Refuted hypotheses stay in the list: what was ruled out is half of what was learned.",
		"按置信度排序。被否证的假设仍留在列表中：排除了什么，是“学到了什么”的一半。"},
	"hyp.supported":     {"supported by the evidence", "有证据支持"},
	"hyp.refuted":       {"refuted by the evidence", "被证据否证"},
	"hyp.inconclusive":  {"inconclusive", "无法判定"},
	"hyp.proposed":      {"proposed", "已提出"},
	"hyp.supporting":    {"Supporting evidence:", "支持性证据："},
	"hyp.contradicting": {"Contradicting evidence:", "反证："},

	// findings
	"find.title":      {"Findings", "发现"},
	"find.confidence": {"confidence", "置信度"},
	"find.evidence":   {"evidence", "证据"},
	"sev.critical":    {"critical", "严重"},
	"sev.major":       {"major", "重要"},
	"sev.minor":       {"minor", "次要"},
	"sev.info":        {"info", "信息"},

	// checks
	"checks.title": {"Checks that passed", "通过的检查"},

	// recommendations
	"rec.title": {"Recommendations", "建议"},
	"rec.note": {
		"Advisory only. MAS-Turbo does not perform any of these; a human decides and acts.",
		"仅供参考。MAS-Turbo 不会执行其中任何一项；由人来决定并动手。"},
	"rec.advisory": {"advisory", "仅供参考"},
	"rec.risk":     {"risk", "风险"},
	"rec.refs":     {"References:", "参考："},
	"risk.low":     {"low", "低"},
	"risk.medium":  {"medium", "中"},
	"risk.high":    {"high", "高"},

	// evidence
	"ev.title":     {"Evidence", "证据"},
	"ev.truncated": {"truncated", "已截断"},

	// usage and cost
	"usage.title":             {"Usage", "用量"},
	"usage.llm_calls":         {"Model calls", "模型调用"},
	"usage.tool_calls":        {"Tool calls", "工具调用"},
	"usage.prompt_tokens":     {"Prompt tokens", "输入 token"},
	"usage.completion_tokens": {"Completion tokens", "输出 token"},
	"usage.wall":              {"Wall clock", "墙钟耗时"},
	"usage.cost":              {"Cost", "成本"},
	"usage.by_role":           {"By role", "按角色"},
	"cost.unknown": {
		"not measured — no model in this run had a configured price",
		"未测量 —— 本次运行中没有任何模型配置了价格"},
	"cost.unpriced": {"unpriced models", "未定价的模型"},
	"cost.partial":  {"partial:", "部分："},

	// notes, versions, steps
	"notes.title":    {"Notes", "备注"},
	"versions.title": {"Versions", "版本信息"},
	"steps.title":    {"Step trace", "步骤轨迹"},
	"steps.note": {
		"Every tool call, model call and rule evaluation this run made, in order.",
		"本次运行发出的每一次工具调用、模型调用与规则求值，按顺序排列。"},
	"steps.empty":  {"This run recorded no steps.", "该运行没有记录任何步骤。"},
	"steps.input":  {"input", "输入"},
	"steps.output": {"output", "输出"},

	// targets
	"targets.title": {"Targets", "目标"},
	"targets.note": {
		"Configured middleware deployments this credential may reach.",
		"当前凭据可触达的、已配置的中间件部署。"},
	"targets.empty": {"No targets are visible to this credential.", "当前凭据看不到任何目标。"},

	// system
	"system.title":      {"System", "系统"},
	"system.version":    {"Build", "构建版本"},
	"system.language":   {"Configured language", "配置的语言"},
	"system.uptime":     {"Uptime", "运行时长"},
	"system.packs":      {"Knowledge packs", "知识包"},
	"system.topologies": {"Topologies", "拓扑"},
	"system.default":    {"default", "默认"},

	// table columns
	"col.run":        {"Run", "运行"},
	"col.status":     {"Status", "状态"},
	"col.target":     {"Target", "目标"},
	"col.symptom":    {"Symptom", "症状"},
	"col.topology":   {"Topology", "拓扑"},
	"col.started":    {"Started", "开始"},
	"col.hypotheses": {"Hypotheses", "假设数"},
	"col.id":         {"ID", "ID"},
	"col.kind":       {"Kind", "类型"},
	"col.source":     {"Source", "来源"},
	"col.summary":    {"Summary", "摘要"},
	"col.collected":  {"Collected", "采集时间"},
	"col.version":    {"Version", "版本"},
	"col.env":        {"Environment", "环境"},
	"col.tenant":     {"Tenant", "租户"},
	"col.metrics":    {"Metrics source", "指标来源"},
	"col.logs":       {"Log source", "日志来源"},
	"col.role":       {"Role", "角色"},
	"col.model":      {"Model", "模型"},
	"col.calls":      {"Calls", "调用次数"},
	"col.tokens":     {"Tokens in / out", "token 输入 / 输出"},
	"col.cost":       {"Cost", "成本"},
	"col.pack":       {"Pack", "知识包"},
	"col.middleware": {"Middleware", "中间件"},
	"col.range":      {"Version range", "版本区间"},
	"col.signals":    {"Signals", "信号"},
	"col.patterns":   {"Log patterns", "日志模式"},
	"col.modes":      {"Failure modes", "故障模式"},
	"col.playbooks":  {"Playbooks", "处置手册"},

	// failures the console renders itself
	"err.request": {"The request failed.", "请求失败。"},
	"err.network": {"The server could not be reached.", "无法连接到服务端。"},
	"err.network.remedy": {
		"Check that `mas serve` is still running and reachable from this browser.",
		"请确认 `mas serve` 仍在运行，且当前浏览器能够访问到它。"},
	"err.unauthorised": {"The credential was rejected.", "凭据被拒绝。"},
	"err.unauthorised.remedy": {
		"Enter a token configured under `server.auth.tokens` that holds the `read` scope.",
		"请输入一个在 `server.auth.tokens` 中配置、且拥有 `read` scope 的令牌。"},
}

// consoleStringIDs lists the table's ids in a stable order, for tests and for
// anything that needs to enumerate the vocabulary.
func consoleStringIDs() []string {
	out := make([]string, 0, len(consoleStrings))
	for id := range consoleStrings {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// consoleStringsFor projects the table into one language.
func consoleStringsFor(lang string) map[string]string {
	idx := 0
	if consoleLang(lang) == "zh" {
		idx = 1
	}
	out := make(map[string]string, len(consoleStrings))
	for id, pair := range consoleStrings {
		out[id] = pair[idx]
	}
	return out
}

// consoleLang narrows anything an operator or a query parameter might write to
// the two languages this project has.
func consoleLang(lang string) string {
	if len(lang) >= 2 && strings.EqualFold(lang[:2], "zh") {
		return "zh"
	}
	return "en"
}
