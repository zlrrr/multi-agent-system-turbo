package llm

import (
	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
)

// Pricing turns tokens into money.
//
// This project ships no price list, and that is a decision rather than an
// omission (specs/005 CON-002). Prices change, differ by contract and region,
// and go stale in a repository without anyone noticing. A number that looks
// authoritative and is wrong is worse than an admitted gap — so prices are
// operator-supplied configuration, and a model nobody priced yields an unknown
// cost naming it, never a zero that reads as free.
//
// Governs: specs/005-model-routing-and-cost/design-lld.md §3
type Pricing map[string]config.ModelPrice

// CostOf prices one exchange.
//
// A configured price of exactly zero stays *known*: a self-hosted model really
// is free at the margin, and an operator who wrote 0 meant it. Absence is the
// unknown case. That distinction is the entire reason core.Cost carries a Known
// flag instead of being a float.
func (p Pricing) CostOf(model string, promptTokens, completionTokens int) core.Cost {
	price, ok := p[model]
	if !ok {
		return core.UnpricedCost(model)
	}
	const perMillion = 1_000_000.0
	usd := float64(promptTokens)/perMillion*price.InputPerMTok +
		float64(completionTokens)/perMillion*price.OutputPerMTok
	return core.KnownCost(usd)
}

// Priced reports whether a model has a configured price, for `mas doctor`.
func (p Pricing) Priced(model string) bool {
	_, ok := p[model]
	return ok
}
