# 任务拆解：给目标与凭据加上租户维度

> **特性 ID**：`011-tenant-registry` · **版本**：1.0.0
> **双语对应文件**：[`tasks.md`](./tasks.md) · **上游**：[`design-lld.zh.md`](./design-lld.zh.md) v1.0.0

## 图例
`status` ∈ `todo | doing | done | blocked`。每个任务在实现之前先声明其测试
（宪法第六条第 1 款），且只有当该测试通过时才标记为 `done`。
此处提到的每一个测试都必须真实存在：`sddctl verify` 会检查这一点。

## 阶段 A —— 注册表

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| TA01 | `TargetConfig.Tenant`、`APIToken.Tenants`、`MultiTenant()`、`Tenants()` | FR-001 | `TestTargetsCarryATenant` | — | done |
| TA02 | 租户关闭时，现有的每一项行为都不受影响 | FR-002、NFR-003 | `TestTenancyOffChangesNothing` | TA01 | done |
| TA03 | 部分启用租户会在加载时被拒绝 | FR-003 | `TestPartialTenancyIsRefused` | TA01 | done |
| TA04 | 未写 tenants、或写了未知租户的凭据会被拒绝 | FR-004、FR-012、CON-001 | `TestCredentialWithoutTenantsIsRefused`、`TestCredentialNamingAnUnknownTenantIsRefused` | TA03 | done |
| TA05 | 错误码 `MAS-1013`、`MAS-7015`，双语，并重新生成文档 | CON-004 | `mas errcodes` 输出为最新 | TA03 | done |
| **G-A** | **闸门 A** | | 不自洽的租户配置无法加载 | | done |

## 阶段 B —— 强制执行

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| TB01 | `Principal.Tenants` 与 `MayReach` —— 租户判定的唯一场所 | FR-010、CON-002 | `TestTenancyIsEnforcedInOnePlace` | G-A | todo |
| TB02 | 对其他租户目标发起诊断会被拒绝 | FR-005 | `TestDiagnosingAnotherTenantIsRefused` | TB01 | todo |
| TB03 | 该拒绝与"未知目标"不可区分 | FR-009、CON-003 | `TestCrossTenantRefusalRevealsNothing` | TB02 | todo |
| TB04 | 目标列举按租户限定 | FR-006 | `TestTargetListingIsTenantScoped` | TB01 | todo |
| TB05 | 运行记录的列举与读取按租户限定 | FR-007 | `TestRunAccessIsTenantScoped` | TB01 | todo |
| **G-B** | **闸门 B** | | `go test ./internal/httpapi/...` | | todo |

## 阶段 C —— 归属与文档

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| TC01 | 在准入阶段把租户记录到运行上 | FR-008 | `TestRunRecordCarriesTheTenant` | G-B | todo |
| TC02 | `mas doctor` 报告租户状态与每个凭据的触达范围 | FR-011 | `TestDoctorReportsTenancy` | TC01 | todo |
| TC03 | 双语文档：配置参考、用户手册、README | NFR-002、NFR-001 | `sddctl verify` 对等检查；`go.mod` 未变 | TC02 | todo |
| **G-C** | **闸门 C —— 特性出口** | | `make ci` 全绿；`make demo` 行为不变 | | todo |

## 检查点闸门

| 闸门 | 任务 | 验证命令 |
|---|---|---|
| G-A | TA01–TA05 | `go test ./internal/config/...` |
| G-B | TB01–TB05 | `go test ./internal/httpapi/...` |
| G-C | TC01–TC03 | `make ci && make demo` |

## Change Log

| 版本 | 日期 | 变更 | 影响 |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | 初版任务拆解 | 代码、配置、文档 |
