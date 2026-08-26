# Feature Specification: Case Corpus and Evaluation Harness

> **Feature ID**: `006-eval-harness` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`spec.zh.md`](./spec.zh.md) · **Upstream**: [`docs/en/project-goals.md`](../../docs/en/project-goals.md) v1.1.4
> **Constitution**: `.specify/memory/constitution.md` v1.0.0 · **Downstream**: `plan.md`

## 1. Problem statement

Three features have now ended with the same sentence: *this project compares
topologies, it does not score them, because scoring would need a corpus of cases
with known causes.* That sentence has been honest each time and it is now the
thing standing between this project and the goal that motivated the switchable
architecture — settling a design question by experiment rather than by taste.

What exists is a comparison of **cost**: the demo runs one case through five
topologies and prints model calls, tool calls and money. What is missing is a
comparison of **outcome**. Nothing today can answer "does `debate` reach the
right conclusion more often than `supervisor`, and on which kinds of incident?"

There is a second use, less glamorous and more likely to matter day to day. Six
knowledge packs now contain roughly two hundred thresholds, and nothing checks
that a pack still concludes what it used to. A corpus is a regression suite for
domain knowledge, which no unit test can be.

The risk this feature must not walk into is producing a number that looks like
accuracy and is not. A synthetic corpus measures agreement with its own labels;
a scorer built on text similarity rewards parroting; and a run against the mock
provider measures the script, not the model. Each of those would yield a
confident figure that means something other than what a reader would assume, and
a misleading measurement is worse than none — it ends arguments that should
continue.

## 2. Users & scenarios

| Persona | Goal | Trigger |
|---|---|---|
| Platform engineer | Choose a default topology on evidence rather than taste | Evaluating the tool |
| Pack author | Know their change did not break a conclusion that used to hold | Editing thresholds |
| Maintainer | Catch a regression in the rule engine that no unit test covers | Every CI run |
| Researcher | Compare model × topology on identical cases | Studying the design |

## 3. Scope

### In scope
- A **case** as versioned data: stub telemetry, logs, target, symptom, and the
  outcome a correct diagnosis reaches.
- A corpus shipped with the project, derived from the packs' failure modes.
- A harness that runs a case end to end through the real stack — real
  collectors, real guard, real engine — and scores it.
- Scoring on **machine-checkable facts only**: which failure mode was concluded,
  whether a conclusion the case rules out was reached, whether an expected gap
  was declared.
- A matrix run across topologies, and a stable report of the result.
- Loud, unavoidable statements of what the number does *not* mean.
- A CI gate that fails when the corpus regresses.

### Out of scope
- Scoring free text by similarity to a reference answer. It rewards a model that
  restates the prompt, and it would make the corpus impossible to extend without
  a reference writer.
- A "quality score" collapsing correctness, cost and latency into one number.
  The trade-off between them is the reader's to make; hiding it inside a weight
  is a way of making it for them silently.
- Real incident data. Recording production telemetry is a privacy question this
  project has not asked.
- Tuning prompts against the corpus. A corpus used to fit is no longer a measure.

## 4. Functional requirements

| ID | Requirement | Priority | Acceptance signal |
|---|---|---|---|
| FR-001 | A case MUST be data: adding one MUST need no recompilation | P0 | `TestCorpusLoadsFromDirectory` |
| FR-002 | A case MUST declare the failure mode a correct diagnosis reaches, and MAY declare modes it must not reach | P0 | `TestCaseSchemaRequiresAnExpectedOutcome` |
| FR-003 | The harness MUST run a case through the real service, collectors and guard, not a simulation of them | P0 | `TestHarnessUsesTheRealPipeline` |
| FR-004 | Scoring MUST use only machine-checkable facts; no text-similarity scoring | P0 | `TestScoringUsesNoTextSimilarity` |
| FR-005 | A false conclusion — a mode the case rules out — MUST be reported separately from a miss | P0 | `TestFalseConclusionIsScoredSeparately` |
| FR-006 | A case MAY withhold a telemetry source, and the harness MUST check the run declared the resulting gap | P0 | `TestWithheldSourceMustProduceADeclaredGap` |
| FR-007 | The harness MUST run the matrix: every case × selected topologies | P0 | `TestMatrixRunsEveryCaseAgainstEveryTopology` |
| FR-008 | Results MUST be deterministic for a deterministic provider, so a regression is a real change | P0 | `TestResultsAreDeterministic` |
| FR-009 | A run against the mock provider MUST state that it measures the script, not a model, and MUST NOT present agent-phase results as model quality | P0 | `TestMockRunRefusesToClaimModelQuality` |
| FR-010 | Output MUST report per-case outcomes and totals, and never a single collapsed score | P0 | `TestReportKeepsOutcomesSeparate` |
| FR-011 | `mas eval` MUST run the corpus from the CLI, in both languages | P1 | CLI test |
| FR-012 | CI MUST fail when a shipped case regresses | P1 | `make ci` runs the corpus |
| FR-013 | The corpus MUST cover every shipped pack | P1 | `TestEveryPackHasACase` |

## 5. Non-functional requirements

| ID | Requirement | Measure |
|---|---|---|
| NFR-001 | The whole corpus MUST run in CI in under 60 seconds with the deterministic provider | Timed test |
| NFR-002 | No new module dependency | `go.mod` unchanged |
| NFR-003 | Every operator-facing string bilingual | `sddctl verify` |
| NFR-004 | A case MUST NOT be able to widen the guard or reach a real network | Structural audit; stub servers only |
| NFR-005 | The corpus's synthetic nature MUST be stated wherever a result is displayed | Output test in both languages |

## 6. Constraints

| ID | Constraint | Source |
|---|---|---|
| CON-001 | A measurement must never be presented as more than it is | Constitution Art. IX |
| CON-002 | No single collapsed score | §3, out of scope |
| CON-003 | The corpus is synthetic and says so, every time | NFR-005 |
| CON-004 | Cases are data under the same bilingual rule as packs | Art. III |
| CON-005 | The harness may not tune anything; it only measures | §3, out of scope |

## 7. Acceptance

The feature is done when `mas eval` runs a shipped corpus through the real
pipeline across every topology, reports per-case outcomes with false conclusions
counted separately, states plainly what the numbers do not mean, fails CI on a
regression, and `make ci` is green.

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-25 | Initial specification | plan, HLD, LLD, tasks, code |
