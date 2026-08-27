// MAS-Turbo console — the whole client.
//
// It is a client of the HTTP API, not a second implementation of it: every
// value it shows crosses the same authorizer, the same scope check and the same
// tenant filter that `curl` crosses, because it is the same code path
// (specs/012-web-console/design-hld.md §1).
//
// Two rules hold everywhere in this file:
//   1. No value ever becomes HTML. Report prose is written by a language model
//      and quoted from estate logs; `el` sets textContent and nothing else
//      touches text (§4). The sinks that could do otherwise are absent, and a
//      Go test refuses them so they stay absent.
//   2. No operator-facing prose lives here. Every label is `t(<id>)` against a
//      bilingual table that lives in Go, where its parity is checked (§6).
"use strict";

const TOKEN_KEY = "mas.token";
const POLL_MS = 4000;

const state = {
  token: null,
  lang: "en",
  strings: {},
  langPinned: false,
  // A failure raised before a view exists — a rejected credential on the very
  // first read — has nowhere to render itself yet. It waits here and the
  // credential gate shows it, so "your token was refused" arrives as MAS-7012
  // with its remedy rather than as a blank prompt.
  problem: null,
  timer: null,
};

// ── plumbing ────────────────────────────────────────────────────────────────

// el is the only way a value reaches the page. There is no second function
// that touches text, which is what makes the XSS argument a short one.
function el(tag, cls, text) {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text !== undefined && text !== null) n.textContent = String(text);
  return n;
}

function add(parent, child) { parent.appendChild(child); return child; }

function clear(node) { while (node.firstChild) node.removeChild(node.firstChild); }

// t falls back to the id so a missing string is visible rather than blank.
function t(id) {
  const s = state.strings[id];
  return s === undefined ? id : s;
}

function guessLang() {
  const nav = (navigator.language || "en").toLowerCase();
  return nav.indexOf("zh") === 0 ? "zh" : "en";
}

async function loadStrings(lang) {
  const res = await fetch("strings.json?lang=" + encodeURIComponent(lang), {
    headers: { "Accept": "application/json" },
  });
  const body = await res.json();
  state.strings = body.strings || {};
  state.lang = body.lang || lang;
  document.documentElement.lang = state.lang === "zh" ? "zh-Hans" : "en";
  document.title = t("app.title");
}

// api performs one authorised read. The credential travels in the header and
// nowhere else: never a query parameter, never a cookie.
async function api(path) {
  const headers = { "Accept": "application/json" };
  if (state.token) headers["Authorization"] = "Bearer " + state.token;
  let res;
  try {
    res = await fetch(path, { headers: headers, credentials: "omit" });
  } catch (e) {
    throw { code: "MAS-7003", message: t("err.network"), remedy: t("err.network.remedy") };
  }
  if (res.status === 401) {
    forgetToken();
    throw { code: "MAS-7012", message: t("err.unauthorised"), remedy: t("err.unauthorised.remedy") };
  }
  const body = await res.json().catch(function () { return {}; });
  if (!res.ok) {
    throw {
      code: body.code || String(res.status),
      message: body.message || t("err.request"),
      remedy: body.remedy || "",
    };
  }
  return body;
}

function rememberToken(tok) {
  state.token = tok;
  try { sessionStorage.setItem(TOKEN_KEY, tok); } catch (e) { /* private mode */ }
}

function forgetToken() {
  state.token = null;
  try { sessionStorage.removeItem(TOKEN_KEY); } catch (e) { /* private mode */ }
}

function readToken() {
  try { return sessionStorage.getItem(TOKEN_KEY); } catch (e) { return null; }
}

function secureOrigin() {
  const h = location.hostname;
  return location.protocol === "https:" ||
    h === "localhost" || h === "127.0.0.1" || h === "::1" || h === "[::1]";
}

// ── small renderers ─────────────────────────────────────────────────────────

function when(iso) {
  if (!iso || iso.indexOf("0001-01-01") === 0) return "—";
  const d = new Date(iso);
  return isNaN(d.getTime()) ? iso : d.toLocaleString();
}

function pct(x) { return Math.round((Number(x) || 0) * 100) + "%"; }

function statusLabel(s) {
  if (s === "supported") return t("hyp.supported");
  if (s === "refuted") return t("hyp.refuted");
  if (s === "inconclusive") return t("hyp.inconclusive");
  if (s === "proposed") return t("hyp.proposed");
  return s || "";
}

function severityLabel(s) {
  if (s === "critical") return t("sev.critical");
  if (s === "major") return t("sev.major");
  if (s === "minor") return t("sev.minor");
  if (s === "info") return t("sev.info");
  return s || "";
}

function severityClass(s) {
  if (s === "critical") return "tag critical";
  if (s === "major") return "tag warn";
  return "tag";
}

function riskLabel(r) {
  if (r === "high") return t("risk.high");
  if (r === "medium") return t("risk.medium");
  if (r === "low") return t("risk.low");
  return r || "";
}

function gapReasonLabel(r) {
  if (r === "unavailable") return t("gap.unavailable");
  if (r === "refused") return t("gap.refused");
  if (r === "truncated") return t("gap.truncated");
  if (r === "not_configured") return t("gap.not_configured");
  if (r === "unsupported") return t("gap.unsupported");
  if (r === "not_applicable") return t("gap.not_applicable");
  return r || "";
}

function runStatusLabel(s) {
  if (s === "running") return t("run.running");
  if (s === "completed") return t("run.completed");
  if (s === "failed") return t("run.failed");
  return s || "";
}

function runStatusClass(s) {
  if (s === "failed") return "tag critical";
  if (s === "running") return "tag warn";
  if (s === "completed") return "tag ok";
  return "tag";
}

function confidenceBar(value, refuted) {
  const track = el("div", "bar-track");
  const fill = el("div", refuted ? "bar-fill refuted" : "bar-fill");
  fill.style.width = Math.max(0, Math.min(100, Math.round((Number(value) || 0) * 100))) + "%";
  add(track, fill);
  return track;
}

function kv(parent, label, value) {
  if (value === undefined || value === null || value === "") return;
  add(parent, el("dt", null, label));
  add(parent, el("dd", "wrap", value));
}

function section(parent, title) {
  add(parent, el("h2", null, title));
  return parent;
}

// counted puts the size in the heading. It is not a way to hide anything —
// nothing here is collapsed — but a reader scrolling a run with thirty gaps
// deserves to know that before they start scrolling.
function counted(title, list) {
  return title + " (" + ((list && list.length) || 0) + ")";
}

// costLine never renders an unpriced run as zero: a cost that says nothing is
// honest, and one that says $0.00 when nothing was priced is a lie an operator
// will believe.
function costLine(cost) {
  if (!cost) return t("cost.unknown");
  if (cost.known) return "$" + (Number(cost.usd) || 0).toFixed(4);
  if (cost.unpriced && cost.unpriced.length) {
    if (Number(cost.usd) > 0) {
      return t("cost.partial") + " $" + (Number(cost.usd) || 0).toFixed(4) +
        " · " + t("cost.unpriced") + ": " + cost.unpriced.join(", ");
    }
    return t("cost.unpriced") + ": " + cost.unpriced.join(", ");
  }
  return t("cost.unknown");
}

// withoutCode drops a leading "MAS-NNNN: " from a detail that already has its
// code rendered above it. Nothing is lost and the line stops saying it twice.
function withoutCode(detail, code) {
  const prefix = code + ": ";
  return code && detail.indexOf(prefix) === 0 ? detail.slice(prefix.length) : detail;
}

function failureCard(err) {
  const card = el("div", "card failure");
  add(card, el("div", "mono small muted", err.code || ""));
  add(card, el("p", null, err.message || ""));
  if (err.remedy) add(card, el("p", "small muted", err.remedy));
  return card;
}

// ── chrome ──────────────────────────────────────────────────────────────────

function renderChrome() {
  const nav = document.getElementById("nav");
  clear(nav);
  const here = (location.hash || "#/runs").split("/")[1] || "runs";
  const items = [
    ["#/runs", t("nav.runs"), "runs"],
    ["#/targets", t("nav.targets"), "targets"],
    ["#/system", t("nav.system"), "system"],
  ];
  if (state.token) {
    items.forEach(function (it) {
      const a = el("a", here === it[2] ? "on" : null, it[1]);
      a.href = it[0];
      add(nav, a);
    });
  }

  const toggle = document.getElementById("lang-toggle");
  toggle.textContent = state.lang === "zh" ? "English" : "中文";

  const out = document.getElementById("signout");
  out.textContent = t("action.signout");
  out.hidden = !state.token;

  const foot = document.getElementById("foot");
  clear(foot);
  add(foot, el("p", null, t("foot.advisory")));
  add(foot, el("p", null, t("foot.readonly")));
}

// ── views ───────────────────────────────────────────────────────────────────

function viewGate(view) {
  if (state.problem) {
    add(view, failureCard(state.problem));
    state.problem = null;
  }
  const card = el("div", "card gate");
  add(card, el("h1", null, t("gate.title")));
  add(card, el("p", "muted", t("gate.body")));

  if (!secureOrigin()) {
    const warn = el("div", "card notice small");
    add(warn, el("p", null, t("gate.insecure")));
    add(card, warn);
  }

  const input = el("input");
  input.type = "password";
  input.autocomplete = "off";
  input.spellcheck = false;
  input.placeholder = t("gate.placeholder");
  add(card, input);

  const row = el("div", "row");
  row.style.marginTop = "0.7rem";
  const go = el("button", "primary", t("gate.submit"));
  add(row, go);
  add(card, row);

  const problem = el("div");
  add(card, problem);

  function submit() {
    const tok = input.value.trim();
    if (!tok) return;
    rememberToken(tok);
    clear(problem);
    route();
  }
  go.addEventListener("click", submit);
  input.addEventListener("keydown", function (e) { if (e.key === "Enter") submit(); });

  add(view, card);
  input.focus();
}

async function viewRuns(view) {
  add(view, el("h1", null, t("runs.title")));
  add(view, el("p", "muted small", t("runs.subtitle")));

  const body = await api("/api/v1/diagnoses?limit=100");
  const runs = body.runs || [];
  if (!runs.length) {
    add(view, el("p", "muted", t("runs.empty")));
    return;
  }

  const box = el("div", "scroll");
  const table = el("table");
  const thead = el("thead");
  const hr = el("tr");
  [t("col.run"), t("col.status"), t("col.target"), t("col.symptom"),
   t("col.topology"), t("col.started"), t("col.hypotheses")].forEach(function (h) {
    add(hr, el("th", null, h));
  });
  add(thead, hr);
  add(table, thead);

  const tbody = el("tbody");
  runs.forEach(function (r) {
    const tr = el("tr");
    const idCell = el("td");
    const a = el("a", "mono", r.id);
    a.href = "#/runs/" + encodeURIComponent(r.id);
    add(idCell, a);
    if (r.tenant) add(idCell, el("span", "tag", r.tenant));
    add(tr, idCell);

    const st = el("td");
    add(st, el("span", runStatusClass(r.status), runStatusLabel(r.status)));
    add(tr, st);

    add(tr, el("td", "wrap", r.target || "—"));
    add(tr, el("td", "wrap", r.symptom || "—"));
    add(tr, el("td", null, r.topology || "—"));
    add(tr, el("td", "small", when(r.started_at)));
    add(tr, el("td", null, r.hypotheses || 0));
    add(tbody, tr);
  });
  add(table, tbody);
  add(box, table);
  add(view, box);
}

async function viewRun(view, id) {
  const rec = await api("/api/v1/diagnoses/" + encodeURIComponent(id));
  const rep = rec.report;

  const head = el("div", "card");
  const top = el("div", "row spread");
  add(top, el("h1", "mono wrap", rec.id));
  add(top, el("span", runStatusClass(rec.status), runStatusLabel(rec.status)));
  add(head, top);

  const facts = el("dl", "kv");
  if (rep) {
    kv(facts, t("field.target"), (rep.target && rep.target.id) || "");
    kv(facts, t("field.kind"), (rep.target && rep.target.kind) || "");
    kv(facts, t("field.version"), (rep.target && rep.target.version) || "");
    kv(facts, t("field.symptom"), (rep.request && rep.request.symptom) || "");
    kv(facts, t("field.topology"), rep.topology);
  }
  kv(facts, t("field.tenant"), rec.tenant);
  kv(facts, t("field.principal"), rec.principal);
  kv(facts, t("field.started"), when(rec.started_at));
  kv(facts, t("field.finished"), when(rec.finished_at));
  add(head, facts);

  const links = el("div", "row");
  links.style.marginTop = "0.6rem";
  const steps = el("a", null, t("action.steps"));
  steps.href = "#/runs/" + encodeURIComponent(rec.id) + "/steps";
  add(links, steps);
  add(head, links);
  add(view, head);

  if (rec.status === "running") {
    const note = el("div", "card notice");
    add(note, el("p", null, t("run.polling")));
    add(view, note);
    schedulePoll();
  }

  if (!rep) {
    add(view, el("p", "muted", t("run.noreport")));
    return;
  }

  // Gaps come before the summary, deliberately: a reader who stops after the
  // summary must still have seen what the diagnosis could not look at.
  if (rep.gaps && rep.gaps.length) {
    section(view, counted(t("gaps.title"), rep.gaps));
    add(view, el("p", "muted small", t("gaps.note")));
    rep.gaps.forEach(function (g) {
      const c = el("div", "card gaps");
      const r = el("div", "row spread");
      add(r, el("strong", "wrap", g.intent || ""));
      add(r, el("span", "tag gap", gapReasonLabel(g.reason)));
      add(c, r);
      if (g.code) add(c, el("div", "mono small muted", g.code));
      if (g.detail) add(c, el("p", "small wrap", withoutCode(g.detail, g.code)));
      if (g.impact) add(c, el("p", "small muted", t("gaps.impact") + " " + g.impact));
      add(view, c);
    });
  }

  section(view, t("summary.title"));
  add(view, el("p", "wrap", rep.summary || t("summary.none")));

  if (rep.conclusions && rep.conclusions.length) {
    const row = el("div", "row");
    add(row, el("span", "muted small", t("summary.conclusions")));
    rep.conclusions.forEach(function (c) { add(row, el("span", "tag", c)); });
    add(view, row);
  }

  if (rep.truncated) {
    const c = el("div", "card notice");
    add(c, el("p", null, t("run.truncated")));
    add(view, c);
  }

  if (rep.hypotheses && rep.hypotheses.length) {
    section(view, counted(t("hyp.title"), rep.hypotheses));
    add(view, el("p", "muted small", t("hyp.note")));
    rep.hypotheses.forEach(function (h) {
      const refuted = h.status === "refuted";
      const c = el("div", refuted ? "card hyp refuted" : "card hyp");
      const r = el("div", "row spread");
      add(r, el("strong", "wrap", (h.rank ? h.rank + ". " : "") + (h.statement || "")));
      add(c, r);
      const meta = el("div", "row");
      add(meta, el("span", "tag", statusLabel(h.status)));
      add(meta, confidenceBar(h.confidence, refuted));
      add(meta, el("span", "small muted", pct(h.confidence)));
      add(c, meta);
      if (h.rationale) add(c, el("p", "small wrap", h.rationale));
      if (h.supporting && h.supporting.length) {
        add(c, el("p", "small muted", t("hyp.supporting") + " " + h.supporting.join(", ")));
      }
      if (h.contradicting && h.contradicting.length) {
        add(c, el("p", "small muted", t("hyp.contradicting") + " " + h.contradicting.join(", ")));
      }
      add(view, c);
    });
  }

  if (rep.findings && rep.findings.length) {
    section(view, counted(t("find.title"), rep.findings));
    rep.findings.forEach(function (f) {
      const c = el("div", "card");
      const r = el("div", "row spread");
      add(r, el("strong", "wrap", f.statement || ""));
      add(r, el("span", severityClass(f.severity), severityLabel(f.severity)));
      add(c, r);
      if (f.detail) add(c, el("p", "small wrap", f.detail));
      const meta = el("div", "row small muted");
      add(meta, el("span", null, t("find.confidence") + " " + pct(f.confidence)));
      if (f.origin) add(meta, el("span", "mono", f.origin));
      if (f.evidence && f.evidence.length) {
        add(meta, el("span", null, t("find.evidence") + " " + f.evidence.join(", ")));
      }
      add(c, meta);
      add(view, c);
    });
  }

  if (rep.checks_passed && rep.checks_passed.length) {
    section(view, counted(t("checks.title"), rep.checks_passed));
    const ul = el("ul");
    rep.checks_passed.forEach(function (s) { add(ul, el("li", "small", s)); });
    add(view, ul);
  }

  if (rep.recommendations && rep.recommendations.length) {
    section(view, counted(t("rec.title"), rep.recommendations));
    add(view, el("p", "muted small", t("rec.note")));
    rep.recommendations.forEach(function (rc) {
      const c = el("div", "card");
      const r = el("div", "row spread");
      add(r, el("strong", "wrap", rc.statement || ""));
      const tags = el("div", "row");
      if (rc.advisory) add(tags, el("span", "tag advisory", t("rec.advisory")));
      add(tags, el("span", "tag", t("rec.risk") + " " + riskLabel(rc.risk)));
      add(r, tags);
      add(c, r);
      if (rc.rationale) add(c, el("p", "small wrap", rc.rationale));
      if (rc.refs && rc.refs.length) {
        add(c, el("p", "small muted", t("rec.refs") + " " + rc.refs.join(", ")));
      }
      add(view, c);
    });
  }

  if (rep.evidence && rep.evidence.length) {
    section(view, counted(t("ev.title"), rep.evidence));
    const box = el("div", "scroll");
    const table = el("table");
    const thead = el("thead");
    const hr = el("tr");
    [t("col.id"), t("col.kind"), t("col.source"), t("col.summary"), t("col.collected")]
      .forEach(function (h) { add(hr, el("th", null, h)); });
    add(thead, hr);
    add(table, thead);
    const tbody = el("tbody");
    rep.evidence.forEach(function (e) {
      const tr = el("tr");
      add(tr, el("td", "mono small", e.id || ""));
      add(tr, el("td", "small", e.kind || ""));
      add(tr, el("td", "small wrap", e.source || ""));
      const sum = el("td", "wrap small");
      sum.textContent = e.summary || "";
      if (e.truncated) add(sum, el("span", "tag warn", t("ev.truncated")));
      add(tr, sum);
      add(tr, el("td", "small", when(e.collected_at)));
      add(tbody, tr);
    });
    add(table, tbody);
    add(box, table);
    add(view, box);
  }

  section(view, t("usage.title"));
  const u = rec.usage || {};
  const ul = el("dl", "kv");
  kv(ul, t("usage.llm_calls"), u.llm_calls || 0);
  kv(ul, t("usage.tool_calls"), u.tool_calls || 0);
  kv(ul, t("usage.prompt_tokens"), u.prompt_tokens || 0);
  kv(ul, t("usage.completion_tokens"), u.completion_tokens || 0);
  kv(ul, t("usage.wall"), Math.round((u.wall_millis || 0) / 100) / 10 + "s");
  kv(ul, t("usage.cost"), costLine(u.cost));
  add(view, ul);

  if (rep.role_usage && rep.role_usage.length) {
    add(view, el("h3", null, t("usage.by_role")));
    const box = el("div", "scroll");
    const table = el("table");
    const thead = el("thead");
    const hr = el("tr");
    [t("col.role"), t("col.model"), t("col.calls"), t("col.tokens"), t("col.cost")]
      .forEach(function (h) { add(hr, el("th", null, h)); });
    add(thead, hr);
    add(table, thead);
    const tbody = el("tbody");
    rep.role_usage.forEach(function (ru) {
      const tr = el("tr");
      add(tr, el("td", "small", ru.role || ""));
      add(tr, el("td", "small mono wrap", (ru.provider || "") + "/" + (ru.model || "")));
      add(tr, el("td", "small", ru.calls || 0));
      add(tr, el("td", "small", (ru.prompt_tokens || 0) + " / " + (ru.completion_tokens || 0)));
      add(tr, el("td", "small", costLine(ru.cost)));
      add(tbody, tr);
    });
    add(table, tbody);
    add(box, table);
    add(view, box);
  }

  if (rep.notes && rep.notes.length) {
    section(view, t("notes.title"));
    const nl = el("ul");
    rep.notes.forEach(function (n) { add(nl, el("li", "small wrap", n)); });
    add(view, nl);
  }

  if (rec.versions) {
    section(view, t("versions.title"));
    const vl = el("dl", "kv");
    Object.keys(rec.versions).sort().forEach(function (k) {
      kv(vl, k, rec.versions[k]);
    });
    add(view, vl);
  }
}

async function viewSteps(view, id) {
  const rec = await api("/api/v1/diagnoses/" + encodeURIComponent(id) + "?steps=true");
  const back = el("a", null, t("action.back"));
  back.href = "#/runs/" + encodeURIComponent(id);
  add(view, back);
  add(view, el("h1", null, counted(t("steps.title"), rec.steps)));
  add(view, el("p", "muted small", t("steps.note")));

  const steps = rec.steps || [];
  if (!steps.length) {
    add(view, el("p", "muted", t("steps.empty")));
    return;
  }
  steps.forEach(function (s) {
    const c = el("div", s.error ? "card failure" : "card");
    const r = el("div", "row spread");
    add(r, el("strong", "wrap", (s.name || "") || s.kind));
    const tags = el("div", "row");
    add(tags, el("span", "tag", s.kind || ""));
    if (s.actor) add(tags, el("span", "tag", s.actor));
    add(tags, el("span", "small muted", (s.duration_millis || 0) + "ms"));
    add(r, tags);
    add(c, r);
    if (s.code) add(c, el("div", "mono small muted", s.code));
    if (s.error) add(c, el("p", "small wrap", withoutCode(s.error, s.code)));
    if (s.input !== undefined && s.input !== null) {
      add(c, el("div", "small muted", t("steps.input")));
      add(c, el("pre", null, JSON.stringify(s.input, null, 2)));
    }
    if (s.output !== undefined && s.output !== null) {
      add(c, el("div", "small muted", t("steps.output")));
      add(c, el("pre", null, JSON.stringify(s.output, null, 2)));
    }
    add(view, c);
  });
}

async function viewTargets(view) {
  add(view, el("h1", null, t("targets.title")));
  add(view, el("p", "muted small", t("targets.note")));
  const body = await api("/api/v1/targets");
  const targets = body.targets || [];
  if (!targets.length) {
    add(view, el("p", "muted", t("targets.empty")));
    return;
  }
  const box = el("div", "scroll");
  const table = el("table");
  const thead = el("thead");
  const hr = el("tr");
  [t("col.id"), t("col.kind"), t("col.version"), t("col.env"),
   t("col.tenant"), t("col.metrics"), t("col.logs")].forEach(function (h) {
    add(hr, el("th", null, h));
  });
  add(thead, hr);
  add(table, thead);
  const tbody = el("tbody");
  targets.forEach(function (tg) {
    const tr = el("tr");
    add(tr, el("td", "mono small wrap", tg.id || ""));
    add(tr, el("td", "small", tg.kind || ""));
    add(tr, el("td", "small", tg.version || "—"));
    add(tr, el("td", "small", tg.env || ""));
    add(tr, el("td", "small", tg.tenant || "—"));
    add(tr, el("td", "small", tg.metrics_source || "—"));
    add(tr, el("td", "small", tg.logs_source || "—"));
    add(tbody, tr);
  });
  add(table, tbody);
  add(box, table);
  add(view, box);
}

async function viewSystem(view) {
  add(view, el("h1", null, t("system.title")));

  const index = await api("/");
  const meta = el("dl", "kv");
  kv(meta, t("system.version"), index.version);
  kv(meta, t("system.language"), index.language);
  add(view, meta);

  const health = await api("/healthz");
  const hm = el("dl", "kv");
  kv(hm, t("system.uptime"), (health.uptime_seconds || 0) + "s");
  add(view, hm);

  section(view, t("system.packs"));
  const packs = await api("/api/v1/packs");
  const box = el("div", "scroll");
  const table = el("table");
  const thead = el("thead");
  const hr = el("tr");
  [t("col.pack"), t("col.middleware"), t("col.version"), t("col.range"),
   t("col.signals"), t("col.patterns"), t("col.modes"), t("col.playbooks")]
    .forEach(function (h) { add(hr, el("th", null, h)); });
  add(thead, hr);
  add(table, thead);
  const tbody = el("tbody");
  (packs.packs || []).forEach(function (p) {
    const tr = el("tr");
    add(tr, el("td", "mono small wrap", p.id || ""));
    add(tr, el("td", "small", p.middleware || ""));
    add(tr, el("td", "small", p.version || ""));
    add(tr, el("td", "small", p.version_range || "—"));
    add(tr, el("td", "small", p.signals || 0));
    add(tr, el("td", "small", p.log_patterns || 0));
    add(tr, el("td", "small", p.failure_modes || 0));
    add(tr, el("td", "small", p.playbooks || 0));
    add(tbody, tr);
  });
  add(table, tbody);
  add(box, table);
  add(view, box);

  section(view, t("system.topologies"));
  const topo = await api("/api/v1/topologies");
  const names = topo.topologies || [];
  const desc = topo.descriptions || {};
  names.forEach(function (n) {
    const c = el("div", "card");
    const r = el("div", "row spread");
    add(r, el("strong", null, n));
    if (n === topo.default) add(r, el("span", "tag ok", t("system.default")));
    add(c, r);
    if (desc[n]) add(c, el("p", "small wrap", desc[n]));
    add(view, c);
  });
}

// ── routing ─────────────────────────────────────────────────────────────────

function stopPoll() {
  if (state.timer) { clearTimeout(state.timer); state.timer = null; }
}

// The timer is cleared on every route change, so a stale detail view cannot
// keep polling behind a newer one.
function schedulePoll() {
  stopPoll();
  const at = location.hash;
  state.timer = setTimeout(function () {
    if (location.hash === at) route();
  }, POLL_MS);
}

async function route() {
  stopPoll();
  state.token = state.token || readToken();
  const view = document.getElementById("view");
  clear(view);
  renderChrome();

  if (!state.token) {
    viewGate(view);
    return;
  }

  const parts = (location.hash || "#/runs").replace(/^#\/?/, "").split("/");
  try {
    if (parts[0] === "runs" && parts[1] && parts[2] === "steps") {
      await viewSteps(view, decodeURIComponent(parts[1]));
    } else if (parts[0] === "runs" && parts[1]) {
      await viewRun(view, decodeURIComponent(parts[1]));
    } else if (parts[0] === "targets") {
      await viewTargets(view);
    } else if (parts[0] === "system") {
      await viewSystem(view);
    } else {
      await viewRuns(view);
    }
  } catch (err) {
    clear(view);
    renderChrome();
    add(view, failureCard(err));
    if (!state.token) viewGate(view);
  }
}

async function switchLang(lang) {
  state.langPinned = true;
  await loadStrings(lang);
  route();
}

async function boot() {
  state.token = readToken();
  await loadStrings(guessLang());

  document.getElementById("lang-toggle").addEventListener("click", function () {
    switchLang(state.lang === "zh" ? "en" : "zh");
  });
  document.getElementById("signout").addEventListener("click", function () {
    forgetToken();
    location.hash = "#/runs";
    route();
  });
  window.addEventListener("hashchange", route);

  // The server's configured language outranks the browser's guess and is
  // outranked by the reader's own choice (design-hld.md §6). It is only
  // knowable once a credential exists, so this runs after the first read.
  if (state.token) {
    try {
      const index = await api("/");
      if (index.language && !state.langPinned && index.language !== state.lang) {
        await loadStrings(index.language);
      }
    } catch (err) {
      // Kept, not swallowed: identify() has already forgotten the credential,
      // so route() is about to render the gate and this is the only thing that
      // can explain why.
      state.problem = err;
    }
  }
  route();
}

boot();
