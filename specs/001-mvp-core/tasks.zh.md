# 任务分解：MVP 内核

> **特性 ID**：`001-mvp-core` · **版本**：1.0.0
> **双语对应文件**：[`tasks.md`](./tasks.md) · **上游**：[`design-lld.zh.md`](./design-lld.zh.md) v1.0.0

## 图例
`[P]` = 可与相邻任务并行 · `status` ∈ `todo | doing | done | blocked`
每个任务都必须在实现**之前**声明其测试（宪章 VI.1）。
只有当任务的测试通过时，该任务才算 `done`（VI.2）。

## 阶段 A —— 基础

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T001 | Go 模块、`Makefile`、`.golangci.yml`、`internal/version` | — | `make build` 产出 `mas`；`mas version` 打印构建信息 | — | done |
| T002 | `pkg/errs`：注册表、`Error`、查询、双语定义 | FR-017 | `TestRegistryUnique`、`TestAllCodesRegistered`、`TestBilingualComplete`、`TestCodeOfThroughWrap` | T001 | done |
| T003 | `internal/core`：领域模型 + 不变量 + JSON 往返 | FR-011、FR-012 | `TestReportRoundTrip`、`TestInvariants`、`TestNoUpwardImports` | T002 | done |
| T004 | `internal/config`：模型、加载/合并优先级、`Secret`、校验 | FR-001、FR-016 | `TestPrecedence`、`TestValidateCodes`、`TestSecretNeverSerialises`、`TestResolveRefs` | T002 | done |
| T005 | `internal/safety`：`Redactor` | FR-016 | `TestRedactPatterns`、`TestRedactNestedAny` | T004 | done |
| T006 | `internal/safety`：`Guard` —— 六道检查、默认拒绝 | FR-006、CON-001、CON-002 | `TestGuardAdversarial`（≥30 条恶意输入）、`TestGuardCannotBeWidened` | T005 | done |
| T007 | `internal/obs`：slog 初始化、脱敏 handler、运行上下文、自身指标 | FR-017、G11.4 | `TestRunIDPropagates`、`TestHandlerRedacts`、`TestPromExposition` | T005 | done |
| **G-A** | **闸门 A** | | `go test ./pkg/... ./internal/errs/... ./internal/core/... ./internal/config/... ./internal/safety/... ./internal/obs/...` 全绿 | | done |

## 阶段 B —— 能力层

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T010 | `internal/tool`：`Tool`、`Schema`、`Registry`、受守卫的 `Invoker` | FR-006 | `TestInvokerValidatesArgs`、`TestGuardRefusalBecomesGap`、`TestTimeoutBecomesCeilingCode` | T006 | done |
| T011 | 结构性安全测试：`TestNoUnguardedIO`、代码树中不存在 `sh -c` | NFR-003 | 两项测试全绿 | T010 | done |
| T012 | `collector/promql` 客户端 + 3 个工具 [P] | FR-003 | `TestInstant`、`TestRange`、`TestSeries`、`TestAuthHeaders`、`TestTruncation`、`TestErrorMapping` | T010 | done |
| T013 | `collector/loki` 客户端 + 2 个工具 [P] | FR-004 | `TestQuery`、`TestLimit`、`TestLabels`、`TestErrorMapping` | T010 | done |
| T014 | `envadapter/kube` 只读 REST 客户端 + 5 个工具 [P] | FR-005 | `TestListPods`、`TestPodLogs`、`TestEvents`、`TestNodes`、`TestAuthModes`、`TestKubeClientHasNoMutatingMethods` | T010 | done |
| T015 | `envadapter/local` 主机巡检 + 4 个工具 [P] | FR-021 | `TestProcesses`、`TestPorts`、`TestInspectAllowListed`、`TestInspectRefusesMutating` | T010 | done |
| T016 | `internal/source` 网络→本地回退获取 + 检索 + 2 个工具 | FR-022、FR-023 | `TestFallbackOnUnreachable`、`TestNoMirrorGap`、`TestCacheHitSkipsNetwork`、`TestSearchFixture` | T010 | done |
| **G-B** | **闸门 B** | | `go test ./internal/tool/... ./internal/collector/... ./internal/envadapter/... ./internal/source/...` 全绿 | | done |

## 阶段 C —— 知识与规则

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T020 | `internal/knowledge`：知识包类型、Schema 校验、加载器、embed | FR-007 | `TestSchemaViolations`、`TestUserDirOverrides`、`TestVersionRange`、`TestBilingualPackFields` | T003 | done |
| T021 | Redis 知识包（信号、日志模式、失效模式、剧本、巡检命令） | G2.2 | `TestEmbeddedPacksValid`、`TestRedisPackConformance` | T020 | done |
| T022 | Kafka 知识包 | G2.2 | `TestKafkaPackConformance` | T020 | done |
| T023 | `internal/rules`：剧本引擎、沙箱表达式、结论产出 | FR-008 | `TestPlaybookHappyPath`、`TestMissingEvidenceSkips`、`TestExpressionErrorsCoded`、`TestZeroLLMCalls`、`TestUnder2Seconds` | T020、T010 | done |
| **G-C** | **闸门 C** | | `go test ./internal/knowledge/... ./internal/rules/...` 全绿 | | done |

## 阶段 D —— 推理层

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T030 | `internal/llm`：类型、`Provider`、注册表、预算统计 | FR-010、FR-019 | `TestRegistryOpen`、`TestUnknownProviderCoded` | T004 | done |
| T031 | `llm/mock` 脚本化确定性 provider | 第六条 VI.3、NFR-010 | `TestMockDeterminism`、`TestMockToolSequence` | T030 | done |
| T032 | `llm/anthropic` [P] | FR-010 | `TestAnthropicToolRoundTrip`、`TestAnthropicErrorMapping`、`TestAPIKeyRedactedInErrors` | T030 | done |
| T033 | `llm/openai`（OpenAI 兼容） [P] | FR-010 | `TestOpenAIToolRoundTrip`、`TestBaseURLOverride`、`TestOpenAIErrorMapping` | T030 | done |
| T034 | `internal/agent`：`State`、预算、`toolLoop`、提示词模板 | FR-009、FR-019 | `TestBudgetEnforced`、`TestInvalidToolCallRepairThenGap` | T031、T010 | done |
| T035 | 角色：规划、调查、关联、批判、报告 | G7.1 | 每个角色一个针对脚本化 mock 的行为测试 | T034 | done |
| T036 | `internal/orchestrator`：接口、注册表、`single` | FR-009 | `TestSingleProducesReport`、`TestRegistryRejectsDuplicate` | T035 | done |
| T037 | `orchestrator/supervisor`，含并发调查者 | FR-009 | `TestSupervisorProducesReport`、`-race` 干净 | T036 | done |
| **G-D** | **闸门 D** | | `go test -race ./internal/llm/... ./internal/agent/... ./internal/orchestrator/...` 全绿 | | done |

## 阶段 E —— 输出与持久化

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T040 | `internal/report`：Markdown（en/zh） + JSON 渲染器 | FR-011 | 四种输出的黄金文件测试 | T003 | todo |
| T041 | `internal/store`：`RunStore`、`fs`、`memory` | FR-012 | `TestFSRoundTrip`、`TestAppendOnly`、`TestCorruptDetected`、`TestList` | T003 | todo |
| T042 | `internal/service`：准入、两阶段流水线、短路、降级、统计 | FR-001、FR-002、FR-008、FR-013、FR-019 | `TestAdmissionCodes`、`TestShortCircuit`、`TestAllSourcesDownStillCompletes`、`TestEndToEndUnder5s`、`TestDeterminism` | T023、T037、T041 | todo |
| T043 | 重放 | FR-012 | `TestReplayWithoutNetwork` | T042 | todo |
| **G-E** | **闸门 E** | | `go test ./internal/report/... ./internal/store/... ./internal/service/...` 全绿 | | todo |

## 阶段 F —— 操作面

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T050 | `internal/cli`：全部子命令、全局参数、输出格式 | FR-014 | 每个子命令的冒烟测试 | T042 | todo |
| T051 | `mas doctor` 覆盖配置、遥测、环境、LLM、知识包、源码 | FR-018 | `TestDoctorAgainstStubs` | T050 | todo |
| T052 | `internal/httpapi`：端点、健康检查、`/metrics`、错误映射 | FR-015 | 每个端点一个测试，含 4xx 路径 | T042 | todo |
| **G-F** | **闸门 F** | | `go test ./internal/cli/... ./internal/httpapi/...` 全绿 | | todo |

## 阶段 G —— 交付

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T060 | `cmd/sddctl`：双语对等、可追溯陈旧检测、需求覆盖 | NFR-009、G13 | `TestParityDetectsMissingZH`、`TestStalenessDetected`、`TestCoverageGap`；`make sdd-verify` 全绿 | T001 | todo |
| T061 | 多阶段 `Dockerfile`、非 root、`docker-compose` 示例 | FR-020、NFR-005 | 镜像可构建；`docker run … version` 与 `… diagnose` 成功 | T050 | todo |
| T062 | `.github/workflows/ci.yml`：fmt、vet、lint、`-race` 测试、构建矩阵、sdd-verify | 第八条 VIII.2 | 工作流全绿 | T060 | todo |
| T063 | `.github/workflows/release.yml`：tag → 二进制 + 校验和 + 镜像 | FR-020、G12.2 | 对 tag 的试运行产出制品 | T062 | todo |
| T064 | 双语用户手册、配置参考、错误码参考、README、快速上手 | G12.3、NFR-009 | `sddctl verify` 对等全绿；按手册操作可复现演示 | T050 | todo |
| T065 | 示例配置与演示桩数据（`examples/`），让新用户一条命令拿到报告 | G12.1 | `make demo` 产出报告 | T064 | todo |
| **G-G** | **闸门 G —— M1 出口** | | `make ci` 全绿；镜像可运行；发布制品产出 | | todo |

## 检查点闸门

| 闸门 | 必须为 `done` 的任务 | 验证命令 |
|---|---|---|
| G-A | T001–T007 | `make test-foundation` |
| G-B | T010–T016 | `make test-capability` |
| G-C | T020–T023 | `make test-knowledge` |
| G-D | T030–T037 | `make test-reasoning` |
| G-E | T040–T043 | `make test-output` |
| G-F | T050–T052 | `make test-surfaces` |
| G-G | T060–T065 | `make ci && make docker && make demo` |

## Change Log

| 版本 | 日期 | 变更 | 影响 |
|---|---|---|---|
| 1.0.0 | 2026-08-23 | 依据 LLD v1.0.0 产出初版任务分解 | 代码 |
