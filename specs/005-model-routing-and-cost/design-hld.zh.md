# 概要设计（HLD）：模型路由与如实的成本核算

> **特性 ID**：`005-model-routing-and-cost` · **版本**：1.0.0 · **状态**：已批准
> **双语对应文件**：[`design-hld.md`](./design-hld.md) · **上游**：[`plan.zh.md`](./plan.zh.md) v1.0.0 · **下游**：[`design-lld.zh.md`](./design-lld.zh.md)

## 1. 它处在哪个位置

```
config.LLMConfig ──► llm.Router ──┬─► provider "default"（anthropic、opus）
     + per_agent                  ├─► provider "cheap"（OpenAI 兼容、本地）
     + pricing                    └─► provider "mock"（测试与演示）
                                        │
              agent 角色 ── Router.For(role) ─┘  → provider + 模型 + 温度
                                        │
                                  llm.Counting（按角色）
                                        │
                        Usage{调用数、token、墙钟、Cost{USD, Known}}
                                        │
                              报告 · 运行记录 · mas models
```

`Provider` 接口不变。路由是它前面的一次查找，核算则是本就存在的那个包装器，只是加了角色这个键。

## 2. 成本是一个类型，而不是一个数字

这是整个特性的支点。

`CostUSD float64` 没有任何取值能表示"未被测量"。0 是一个真实的成本 ——
mock provider 确实不花钱 —— 因此渲染器在没有约定的情况下无法区分"免费"与"未定价"，
而约定正是维护者在某个周二顺手删掉的那种东西。

```
Cost{ USD float64, Known bool, Unpriced []string }
```

三种状态，都可渲染：

| 状态 | 渲染为 |
|---|---|
| 已知、已定价 | `$0.0412` |
| 已知、未花费 | `$0.0000` —— 这是如实的：本次运行确实没花钱 |
| 未知 | "未定价 —— 请为 claude-opus-5 设置 `llm.pricing`" |

部分定价的运行会同时保留两半：已定价部分的金额，加上未定价模型的名字。
隐去数字会丢弃真实信息；只给数字而不加限定则会低估这次运行。任何一半单独出现都不算如实。

## 3. 路由

角色的 provider 与它的模型以相同方式解析，并继承一切它没有覆盖的配置：

```yaml
llm:
  provider: anthropic
  model: claude-opus-5
  api_key: ${env:ANTHROPIC_API_KEY}

  providers:                       # 具名的备选项
    local:
      provider: openai
      base_url: http://127.0.0.1:11434/v1
      model: qwen2.5:14b

  per_agent:
    investigator: { provider: local }        # 廉价抽取
    executor:     { provider: local }
    correlator:   { temperature: 0.1 }       # 默认 provider，温度更低
```

继承比看上去更要紧。只覆盖温度的角色不能因此丢掉端点与密钥；
只覆盖 provider 的角色不能因此丢掉超时。
按角色把配置重述一遍，正是生产运行栽在"某人漏掉的那一个字段"上的方式。

一次运行所需的每个不同 provider 都在准入阶段只打开一次。
因此凭据错误会以"带错误码的运行被拒"的形式出现 ——
而不是在三分钟后、调查员们已经花掉 token 之后，由 correlator 发现的一条缺口。

## 4. 归属

计数包装器本就位于每个角色与其 provider 之间。
给它加上角色这个键，就把一个总量变成了明细，而结构上零代价 ——
并且因为键是在本就保护计数器的那把锁内应用的，明细不可能与它所汇总的总量发生漂移。

这让特性 003 无法回答的那个问题变得可答。那个特性让拓扑之间可比，
演示也会打印各自花了多少次调用。有了归属，答案变得具体：
*debate 比 supervisor 贵，而全部差额都在 advocate 身上* ——
这是运维人员能据以行动的事实：要么换一个拓扑，要么把那一个角色路由到更便宜的模型。

## 5. 本设计刻意不做的事

- **内置价格。** 它们会变、随合同而异，而一个看起来权威的过期数字就是一句假话（CON-002）。
- **以货币为单位强制预算。** 运行已有 token、步数与墙钟上限，那些都是被测量出来的。
  成本上限则会把安全控制建在运维人员手打的数字上。
- **自动选模。** 预测哪个提示词需要强模型是一项优化，而没有语料库可用于评估它 ——
  未经评估的优化，就是一个套着自信接口的猜测。

## Change Log

| 版本 | 日期 | 变更 | 影响 |
|---|---|---|---|
| 1.0.0 | 2026-08-25 | 初版概要设计 | LLD、tasks、代码 |
