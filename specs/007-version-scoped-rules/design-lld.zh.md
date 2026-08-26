# 详细设计（LLD）：版本区间限定的知识包规则

> **特性 ID**：`007-version-scoped-rules` · **版本**：1.0.0 · **状态**：已批准
> **双语对应文件**：[`design-lld.md`](./design-lld.md) · **上游**：[`design-hld.zh.md`](./design-hld.zh.md) v1.0.0 · **下游**：[`tasks.zh.md`](./tasks.zh.md)、代码

## 1. 文件

```
internal/knowledge/
  pack.go        + 在 Signal、LogPattern、FailureMode、Playbook、Step、Inspect
                   上新增 VersionRange；区间与变体的校验
  resolve.go     新增：Pack.Resolve、传递式丢弃、缺口
  overlap.go     新增：versionRange 的区间形式，以及重叠检测
internal/service/
  service.go     准入阶段解析一次；把缺口带入报告
internal/cli/
  commands.go    `mas packs --show <id> --version <v>`
pkg/errs/
  registry.go    MAS-5016…MAS-5019
```

## 2. 这个字段

一个字段，加在六个类型上，名字与语法都与 `metadata.versionRange` 相同：

```go
type Signal struct {
    ID           string `yaml:"id" json:"id"`
    VersionRange string `yaml:"versionRange" json:"version_range,omitempty"`
    // …
}
```

`Step` 也带上它，这样作者就能在一个整体处处适用的 playbook 内部，
只限定其中一条检查。`Inspect` 带上它，是因为命令行参数在版本间的变化，
比知识包里其他任何东西都频繁。

留空表示"所有版本"，这正是现有每一个知识包"什么都不写"所表达的含义。
这就是 NFR-004：对没有任何区间的知识包调用 `Resolve`，
返回的包规则完全相同，因此 case 语料库就是"什么都没变"的证明。

## 3. 区间与重叠

一个 `versionRange` 是若干比较的合取 —— `">=3.3"`、`">=4.0 <5.0"`。
做重叠检测时，每个区间被归约为版本向量上的一个半开区间：

```go
type interval struct {
    lo, hi    []int  // nil 表示无界
    loOpen    bool   // ">" 为 true，">=" 为 false
    hiOpen    bool   // "<" 为 true，"<=" 为 false
    exact     []int  // 由 "==" 设置，把区间钉在一个点上
    hasHoles  bool   // 由 "!=" 设置，我们不对其建模
}
```

`==` 把 `lo` 与 `hi` 钉在同一点。`!=` **不**建模：
它只能在区间上打一个洞，因此忽略它只会让两个区间看起来**更**重叠。
这个偏置是刻意的（RSK-2）：误判为"重叠"是作者下一次执行 `mas packs` 就会看到的加载错误，
而误判为"不相交"是一个歧义 —— 它会在故障处置中以"用错了指标名"的形式浮现。

`overlaps(a, b)` 就是常规的区间相交判定，并尊重开闭端点。
两个空区间彼此重叠 —— 这正是 FR-003 要求某个 id 的**每一个**变体都必须带区间的原因：
一个未限定的声明与一切都重叠。

## 4. 加载时的校验

在 `Pack.Validate` 中新增：

1. 每个非空 `versionRange` 都能解析，否则报 `MAS-5001` 并给出路径
   （`signals[3].versionRange`）。
2. id 只能以**变体**形式重复：每一次声明都带非空区间，且任意两个不重叠。
   否则报 `MAS-5016`，并指出该 id 与两个区间。
3. 现有的唯一性检查改为"变体感知"，而不是被删掉 ——
   不带区间的重复 id 仍然是它一直以来的那个错误。

校验在加载时执行，因此一个"对某些版本存在歧义"的包会立刻对所有人失败（D-4）。

## 5. `Resolve`

```go
// Resolve 返回该知识包在某个已部署版本下的形态，以及它丢弃掉的一切所对应的缺口。
func (p *Pack) Resolve(version string) (*Pack, []core.Gap)
```

做一次浅拷贝并使用新的切片；接收者永不被修改，
因为知识库中的一个 `*Pack` 会被该中间件的所有目标共享。

各遍处理，按顺序：

**5.1 变体。** 把每一类规则按 id 分组。只有一个成员的组，区间适用则保留。
成员多于一个的组：

- 版本已知 → 保留区间适用的那唯一一个变体；若没有任何一个适用，
  丢弃该 id 并逐条记录 `MAS-5017`：该知识包无法为它自称覆盖的版本定位这条规则，
  而作者需要看到是哪个 id；
- 版本未知 → 丢弃该 id 并逐条记录 `MAS-5018`，
  其处置建议为"设置 `targets[].version`"（D-5）。

**5.2 失去依赖的步骤。** 对每个存活的 playbook，按顺序遍历步骤：

- 若某个 `collect` 的参数引用了 `{{signal:x}}` 而 `x` 已不存在 →
  丢弃该步骤，并把它的 `as` 槽位记为未绑定；
- 若某个 `conclude` 指向的故障模式已不存在 → 丢弃该步骤；
- 若某个步骤自身的区间排除了该版本 → 丢弃该步骤；若它是 `collect`，
  其槽位同样记为未绑定。

**5.3 读取未绑定槽位的步骤。** 带着未绑定集合再遍历一次，
丢弃任何 `evaluate` 或 `conclude.when` 表达式引用了其中槽位的步骤。
丢弃一个步骤不会再解绑任何东西 —— 只有 `collect` 会绑定 ——
因此 5.2 之后一遍即可，遍历复杂度为 `O(步骤数 × 槽位数)`。

槽位引用由规则引擎自己的标识符扫描器找出，而不是子串搜索：
`identifiers()` 已经会跳过带引号的字符串字面量 ——
这正是特性 002 在"正则字面量被读成槽位名"之后所做的修复。
在这里换用别的东西，等于把同一个缺陷搬到一个新地方重现一遍。

**5.4 已无结论可下的 playbook。** 一个没有任何存活 `conclude` 步骤的 playbook
无法抵达任何故障模式；它会花掉查询，返回一堆发现却没有结论。
丢弃它，并在下面的汇总中列出它，连同引发这次级联的那条规则。

**5.5 记账，分两种音量**（HLD §3.1）。所有因**已知**版本将其排除而被丢弃的东西 ——
规则本身，以及由此级联出的一切 —— 汇总为恰好一条缺口：

```go
core.Gap{
    Intent: "version scoping for kafka/kafka-core",
    Reason: core.GapNotApplicable,
    Code:   "MAS-5019",
    Detail: "7 rule(s) do not apply to version 4.0.1: logPattern zk_session_expired, playbook kafka.zookeeper-health, …",
    Impact: "these checks do not exist for this version and were not run; nothing was lost",
}
```

`core.GapNotApplicable` 是新增的原因，而它之所以存在，原因就在它的 `Impact` 上。
`GapUnavailable` 说的是"证据没能取到"，那既是另一个说法，也是更令人警觉的说法。
这里的情况是：这份证据对该版本根本不存在，一切正常 —— 这条汇总是一条注记，而不是一条警告。

那两个逐条记录的错误码之所以保持逐条，是因为它们各自指出了一件人可以据以行动的事：
`MAS-5018` 指出缺少 `targets[].version`，`MAS-5017` 指出某个知识包无法为自己的规则定位。

已解析的包会记录它是为什么版本解析的：

```go
out.Metadata.ResolvedFor = version   // 从未解析过时为 ""
```

## 6. service 在何处解析

在 `Diagnose` 中，紧接 `s.library.For(...)` 之后：

```go
pack, packErr := s.library.For(target.Kind, target.Version)
if packErr == nil {
    var scopeGaps []core.Gap
    pack, scopeGaps = pack.Resolve(target.Version)
    prepGaps = append(prepGaps, scopeGaps...)
}
```

`target.Version` 是环境适配器发表意见之后的版本，
因此从运行中的集群探测到的版本会优先于"没有版本"。
从这里往后，规则引擎、提示词与 inspect 注册全都使用已解析的包 ——
因为它们用的还是那个一直在用的变量。

FR-012 以结构方式断言：一个测试解析 `internal/service/service.go`，
要求流入 `rules.New` 的那个值来自一次 `Resolve` 调用。

## 7. `mas packs --show`

```
mas packs --show kafka                 # 每条规则，附带其区间
mas packs --show kafka --version 4.0.1 # 一次诊断实际会用到的内容
```

不带 `--version` 时，摘要新增一列区间，作者可以看到自己写下的限定。
带上它时，先解析知识包，并在下方列出被跳过的规则及其缺口 ——
与报告中会出现的是同样的句子，这正是它成为"预览"而不是"第二套实现"的原因。

## 8. 错误码

| 错误码 | 含义 |
|---|---|
| `MAS-5016` | 同一规则 id 的两次声明版本区间重叠 |
| `MAS-5017` | 某规则的所有变体中，没有一个适用于该版本 |
| `MAS-5018` | 某规则存在版本相关的变体，而目标版本未知 |
| `MAS-5019` | 有规则不适用于所部署的版本而被跳过（每个知识包汇总为一条缺口） |

## 9. 测试

| 测试 | 它钉住了什么 |
|---|---|
| `TestEveryRuleKindAcceptsAVersionRange` | 六类规则上该字段都存在且可解析 |
| `TestOutOfRangeRulesAreDropped` | FR-002 |
| `TestVariantsWithDisjointRangesAreAccepted` | FR-003 |
| `TestOverlappingVariantsAreRejected` | FR-004，含两个未限定声明的情形 |
| `TestVariantMatchingTheVersionIsChosen` | FR-005 |
| `TestUnknownVersionDropsVariantsWithAGap` | FR-006，以及缺口是否给出了处置建议 |
| `TestStepsFollowTheRulesTheyDependOn` | FR-007 |
| `TestStepsFollowTheSlotsTheyRead` | FR-008，含槽位名出现在正则字面量中的情形 |
| `TestEmptyPlaybooksAreDropped` | FR-009 |
| `TestSkippedRulesAreRecordedAsGaps` | FR-010 |
| `TestResolutionNeverWidens` | FR-011，覆盖一张版本表 |
| `TestDiagnosisUsesTheResolvedPack` | FR-012，以结构方式 |
| `TestPacksCommandShowsVersionScoping` | FR-013 |
| `TestKafkaPackScopesZooKeeperRules` | FR-014 |
| `TestUnscopedPackResolvesToItself` | NFR-004 |

## Change Log

| 版本 | 日期 | 变更 | 影响 |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | 初版详细设计 | tasks、代码 |
