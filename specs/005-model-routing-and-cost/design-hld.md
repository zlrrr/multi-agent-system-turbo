# High-Level Design: Model Routing and Honest Cost Accounting

> **Feature ID**: `005-model-routing-and-cost` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`design-hld.zh.md`](./design-hld.zh.md) · **Upstream**: [`plan.md`](./plan.md) v1.0.0 · **Downstream**: [`design-lld.md`](./design-lld.md)

## 1. Where this sits

```
config.LLMConfig ──► llm.Router ──┬─► provider "default"  (anthropic, opus)
     + per_agent                  ├─► provider "cheap"    (openai-compatible, local)
     + pricing                    └─► provider "mock"     (tests, demo)
                                        │
              agent role ── Router.For(role) ─┘  → provider + model + temperature
                                        │
                                  llm.Counting (per role)
                                        │
                        Usage{calls, tokens, wall, Cost{USD, Known}}
                                        │
                              report · run record · mas models
```

The `Provider` interface is unchanged. Routing is a lookup in front of it, and
accounting is the wrapper that was already there, keyed by role.

## 2. Cost is a type, not a number

This is the decision the feature turns on.

`CostUSD float64` has no value meaning "not measured". Zero is a real cost —
the mock provider genuinely costs nothing — so a renderer cannot distinguish
"free" from "unpriced" without a convention, and conventions are what a
maintainer edits away on a Tuesday.

```
Cost{ USD float64, Known bool, Unpriced []string }
```

Three states, all renderable:

| State | Renders as |
|---|---|
| Known, priced | `$0.0412` |
| Known, nothing spent | `$0.0000` — honest: the run really did cost nothing |
| Unknown | "not priced — set `llm.pricing` for claude-opus-5" |

A partly priced run keeps both halves: the amount for what was priced, plus the
names of the models that were not. Suppressing the figure would discard real
information; showing it bare would understate the run. Neither alone is honest.

## 3. Routing

A role's provider is resolved the way its model already is, and inherits
everything it does not override:

```yaml
llm:
  provider: anthropic
  model: claude-opus-5
  api_key: ${env:ANTHROPIC_API_KEY}

  providers:                       # named alternatives
    local:
      provider: openai
      base_url: http://127.0.0.1:11434/v1
      model: qwen2.5:14b

  per_agent:
    investigator: { provider: local }        # cheap extraction
    executor:     { provider: local }
    correlator:   { temperature: 0.1 }       # default provider, cooler
```

Inheritance matters more than it looks. A role that overrides only the
temperature must not lose the endpoint and the key; a role that overrides only
the provider must not lose the timeout. Restating settings per role is how a
production run fails on the one field somebody forgot.

Every distinct provider a run needs is opened once, at admission. A credential
error therefore surfaces as a refused run with a code — not as a gap discovered
by the correlator, three minutes in, after the investigators have already spent
their tokens.

## 4. Attribution

The counting wrapper already sits between every role and its provider. Keying it
by role turns one total into a breakdown at no structural cost, and — because
the key is applied inside the mutex that already guards the counter — the
breakdown cannot drift from the total it sums to.

What this makes possible is the question feature 003 could not answer. That
feature made topologies comparable and the demo prints what each one cost in
calls. With attribution the answer becomes specific: *debate costs more than
supervisor, and all of the difference is in the advocates* — which is a fact an
operator can act on, either by choosing a different topology or by routing that
one role to a cheaper model.

## 5. What this deliberately does not do

- **Ship prices.** They change, they differ by contract, and a stale number that
  looks authoritative is a false claim (CON-002).
- **Enforce a budget in currency.** The run already has token, step and
  wall-clock ceilings, which are measured. A cost ceiling would be a safety
  control resting on numbers an operator typed.
- **Choose models automatically.** Predicting which prompt needs the strong
  model is an optimisation with no corpus to evaluate it against, and an
  unevaluated optimisation is a guess with a confident interface.

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-25 | Initial high-level design | LLD, tasks, code |
