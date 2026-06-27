package analyze

import (
	"sort"
	"strings"
)

const PricingCatalogVersion = "public-token-pricing-2026-06-10-github-docs-e635641"

type ModelStat struct {
	Requests                int64            `json:"requests" yaml:"requests"`
	Cost                    float64          `json:"cost" yaml:"cost"`
	Input                   int64            `json:"inputTokens" yaml:"inputTokens"`
	CacheRead               int64            `json:"cacheReadTokens,omitempty" yaml:"cacheReadTokens,omitempty"`
	CacheWrite              int64            `json:"cacheWriteTokens,omitempty" yaml:"cacheWriteTokens,omitempty"`
	Output                  int64            `json:"outputTokens" yaml:"outputTokens"`
	ExtraUsageTokens        map[string]int64 `json:"extraUsageTokens,omitempty" yaml:"extraUsageTokens,omitempty"`
	EstimatedOverageCostUSD float64          `json:"estimatedOverageCostUsd,omitempty" yaml:"estimatedOverageCostUsd,omitempty"`
	EstimatedAPICost        *APICostEstimate `json:"estimatedApiCost,omitempty" yaml:"estimatedApiCost,omitempty"`
}

type priceCatalogEntry struct {
	ModelID              string   `json:"modelId" yaml:"modelId"`
	InputUSDPerMTok      float64  `json:"inputUsdPerMToken" yaml:"inputUsdPerMToken"`
	CacheReadUSDPerMTok  *float64 `json:"cacheReadUsdPerMToken,omitempty" yaml:"cacheReadUsdPerMToken,omitempty"`
	CacheWriteUSDPerMTok *float64 `json:"cacheWriteUsdPerMToken,omitempty" yaml:"cacheWriteUsdPerMToken,omitempty"`
	OutputUSDPerMTok     float64  `json:"outputUsdPerMToken" yaml:"outputUsdPerMToken"`
	Source               string   `json:"source" yaml:"source"`
}

type APICostEstimate struct {
	UncachedInputTokens    int64    `json:"uncachedInputTokens,omitempty" yaml:"uncachedInputTokens,omitempty"`
	InputUSDPerMTok        float64  `json:"inputUsdPerMToken" yaml:"inputUsdPerMToken"`
	CacheReadUSDPerMTok    *float64 `json:"cacheReadUsdPerMToken,omitempty" yaml:"cacheReadUsdPerMToken,omitempty"`
	CacheWriteUSDPerMTok   *float64 `json:"cacheWriteUsdPerMToken,omitempty" yaml:"cacheWriteUsdPerMToken,omitempty"`
	OutputUSDPerMTok       float64  `json:"outputUsdPerMToken" yaml:"outputUsdPerMToken"`
	InputUSD               float64  `json:"inputUsd" yaml:"inputUsd"`
	CacheReadUSD           float64  `json:"cacheReadUsd,omitempty" yaml:"cacheReadUsd,omitempty"`
	CacheWriteUSD          float64  `json:"cacheWriteUsd,omitempty" yaml:"cacheWriteUsd,omitempty"`
	OutputUSD              float64  `json:"outputUsd" yaml:"outputUsd"`
	TotalUSD               float64  `json:"totalUsd" yaml:"totalUsd"`
	IsComplete             bool     `json:"isComplete" yaml:"isComplete"`
	MissingPriceComponents []string `json:"missingPriceComponents,omitempty" yaml:"missingPriceComponents,omitempty"`
	PriceCatalogModel      string   `json:"priceCatalogModel" yaml:"priceCatalogModel"`
	Source                 string   `json:"source" yaml:"source"`
}

// Public API token prices for the optional `stats --api-costs` estimate.
// This is separate from Copilot premium-request multipliers, which are plan-dependent
// and should come from local shutdown metrics, live model metadata, or GitHub Docs.
var apiPricingCatalog = map[string]priceCatalogEntry{
	NormalizeModelKey("claude-haiku-4.5"): {
		ModelID:              "claude-haiku-4.5",
		InputUSDPerMTok:      1.00,
		CacheReadUSDPerMTok:  float64Ptr(0.10),
		CacheWriteUSDPerMTok: float64Ptr(1.25),
		OutputUSDPerMTok:     5.00,
		Source:               "https://platform.claude.com/docs/en/about-claude/pricing",
	},
	NormalizeModelKey("claude-sonnet-4"): {
		ModelID:              "claude-sonnet-4",
		InputUSDPerMTok:      3.00,
		CacheReadUSDPerMTok:  float64Ptr(0.30),
		CacheWriteUSDPerMTok: float64Ptr(3.75),
		OutputUSDPerMTok:     15.00,
		Source:               "https://platform.claude.com/docs/en/about-claude/pricing",
	},
	NormalizeModelKey("claude-sonnet-4.5"): {
		ModelID:              "claude-sonnet-4.5",
		InputUSDPerMTok:      3.00,
		CacheReadUSDPerMTok:  float64Ptr(0.30),
		CacheWriteUSDPerMTok: float64Ptr(3.75),
		OutputUSDPerMTok:     15.00,
		Source:               "https://platform.claude.com/docs/en/about-claude/pricing",
	},
	NormalizeModelKey("claude-sonnet-4.6"): {
		ModelID:              "claude-sonnet-4.6",
		InputUSDPerMTok:      3.00,
		CacheReadUSDPerMTok:  float64Ptr(0.30),
		CacheWriteUSDPerMTok: float64Ptr(3.75),
		OutputUSDPerMTok:     15.00,
		Source:               "https://platform.claude.com/docs/en/about-claude/pricing",
	},
	NormalizeModelKey("claude-opus-4.5"): {
		ModelID:              "claude-opus-4.5",
		InputUSDPerMTok:      5.00,
		CacheReadUSDPerMTok:  float64Ptr(0.50),
		CacheWriteUSDPerMTok: float64Ptr(6.25),
		OutputUSDPerMTok:     25.00,
		Source:               "https://platform.claude.com/docs/en/about-claude/pricing",
	},
	NormalizeModelKey("claude-opus-4.6"): {
		ModelID:              "claude-opus-4.6",
		InputUSDPerMTok:      5.00,
		CacheReadUSDPerMTok:  float64Ptr(0.50),
		CacheWriteUSDPerMTok: float64Ptr(6.25),
		OutputUSDPerMTok:     25.00,
		Source:               "https://platform.claude.com/docs/en/about-claude/pricing",
	},
	NormalizeModelKey("claude-opus-4.7"): {
		ModelID:              "claude-opus-4.7",
		InputUSDPerMTok:      5.00,
		CacheReadUSDPerMTok:  float64Ptr(0.50),
		CacheWriteUSDPerMTok: float64Ptr(6.25),
		OutputUSDPerMTok:     25.00,
		Source:               "https://raw.githubusercontent.com/github/docs/main/data/tables/copilot/models-and-pricing.yml",
	},
	NormalizeModelKey("claude-opus-4.8"): {
		ModelID:              "claude-opus-4.8",
		InputUSDPerMTok:      5.00,
		CacheReadUSDPerMTok:  float64Ptr(0.50),
		CacheWriteUSDPerMTok: float64Ptr(6.25),
		OutputUSDPerMTok:     25.00,
		Source:               "https://raw.githubusercontent.com/github/docs/main/data/tables/copilot/models-and-pricing.yml",
	},
	NormalizeModelKey("claude-fable-5"): {
		ModelID:              "claude-fable-5",
		InputUSDPerMTok:      10.00,
		CacheReadUSDPerMTok:  float64Ptr(1.00),
		CacheWriteUSDPerMTok: float64Ptr(12.50),
		OutputUSDPerMTok:     50.00,
		Source:               "https://raw.githubusercontent.com/github/docs/main/data/tables/copilot/models-and-pricing.yml",
	},
	NormalizeModelKey("gemini-2.5-pro"): {
		ModelID:             "gemini-2.5-pro",
		InputUSDPerMTok:     1.25,
		CacheReadUSDPerMTok: float64Ptr(0.125),
		OutputUSDPerMTok:    10.00,
		Source:              "https://raw.githubusercontent.com/github/docs/main/data/tables/copilot/models-and-pricing.yml",
	},
	NormalizeModelKey("gemini-3-flash"): {
		ModelID:             "gemini-3-flash",
		InputUSDPerMTok:     0.50,
		CacheReadUSDPerMTok: float64Ptr(0.05),
		OutputUSDPerMTok:    3.00,
		Source:              "https://raw.githubusercontent.com/github/docs/main/data/tables/copilot/models-and-pricing.yml",
	},
	NormalizeModelKey("gemini-3-pro-preview"): {
		ModelID:             "gemini-3-pro-preview",
		InputUSDPerMTok:     2.00,
		CacheReadUSDPerMTok: float64Ptr(0.20),
		OutputUSDPerMTok:    12.00,
		Source:              "https://blog.google/innovation-and-ai/technology/developers-tools/gemini-3-developers/",
	},
	NormalizeModelKey("gemini-3.1-pro"): {
		ModelID:             "gemini-3.1-pro",
		InputUSDPerMTok:     2.00,
		CacheReadUSDPerMTok: float64Ptr(0.20),
		OutputUSDPerMTok:    12.00,
		Source:              "https://raw.githubusercontent.com/github/docs/main/data/tables/copilot/models-and-pricing.yml",
	},
	NormalizeModelKey("gemini-3.5-flash"): {
		ModelID:             "gemini-3.5-flash",
		InputUSDPerMTok:     1.50,
		CacheReadUSDPerMTok: float64Ptr(0.15),
		OutputUSDPerMTok:    9.00,
		Source:              "https://raw.githubusercontent.com/github/docs/main/data/tables/copilot/models-and-pricing.yml",
	},
	NormalizeModelKey("mai-code-1-flash"): {
		ModelID:             "mai-code-1-flash",
		InputUSDPerMTok:     0.75,
		CacheReadUSDPerMTok: float64Ptr(0.075),
		OutputUSDPerMTok:    4.50,
		Source:              "https://raw.githubusercontent.com/github/docs/main/data/tables/copilot/models-and-pricing.yml",
	},
	NormalizeModelKey("gpt-5.4"): {
		ModelID:             "gpt-5.4",
		InputUSDPerMTok:     2.50,
		CacheReadUSDPerMTok: float64Ptr(0.25),
		OutputUSDPerMTok:    15.00,
		Source:              "https://developers.openai.com/api/docs/pricing",
	},
	NormalizeModelKey("gpt-5.4-mini"): {
		ModelID:             "gpt-5.4-mini",
		InputUSDPerMTok:     0.75,
		CacheReadUSDPerMTok: float64Ptr(0.075),
		OutputUSDPerMTok:    4.50,
		Source:              "https://developers.openai.com/api/docs/pricing",
	},
	NormalizeModelKey("gpt-5.4-nano"): {
		ModelID:             "gpt-5.4-nano",
		InputUSDPerMTok:     0.20,
		CacheReadUSDPerMTok: float64Ptr(0.02),
		OutputUSDPerMTok:    1.25,
		Source:              "https://raw.githubusercontent.com/github/docs/main/data/tables/copilot/models-and-pricing.yml",
	},
	NormalizeModelKey("gpt-5.3-codex"): {
		ModelID:             "gpt-5.3-codex",
		InputUSDPerMTok:     1.75,
		CacheReadUSDPerMTok: float64Ptr(0.175),
		OutputUSDPerMTok:    14.00,
		Source:              "https://developers.openai.com/api/docs/pricing",
	},
	NormalizeModelKey("gpt-5.2-codex"): {
		ModelID:             "gpt-5.2-codex",
		InputUSDPerMTok:     1.75,
		CacheReadUSDPerMTok: float64Ptr(0.175),
		OutputUSDPerMTok:    14.00,
		Source:              "https://developers.openai.com/api/docs/pricing",
	},
	NormalizeModelKey("gpt-5.2"): {
		ModelID:             "gpt-5.2",
		InputUSDPerMTok:     1.75,
		CacheReadUSDPerMTok: float64Ptr(0.175),
		OutputUSDPerMTok:    14.00,
		Source:              "https://developers.openai.com/api/docs/models/gpt-5.2",
	},
	NormalizeModelKey("gpt-5.1-codex-max"): {
		ModelID:             "gpt-5.1-codex-max",
		InputUSDPerMTok:     1.25,
		CacheReadUSDPerMTok: float64Ptr(0.125),
		OutputUSDPerMTok:    10.00,
		Source:              "https://developers.openai.com/api/docs/pricing",
	},
	NormalizeModelKey("gpt-5.1-codex"): {
		ModelID:             "gpt-5.1-codex",
		InputUSDPerMTok:     1.25,
		CacheReadUSDPerMTok: float64Ptr(0.125),
		OutputUSDPerMTok:    10.00,
		Source:              "https://developers.openai.com/api/docs/pricing",
	},
	NormalizeModelKey("gpt-5.1"): {
		ModelID:             "gpt-5.1",
		InputUSDPerMTok:     1.25,
		CacheReadUSDPerMTok: float64Ptr(0.125),
		OutputUSDPerMTok:    10.00,
		Source:              "https://developers.openai.com/api/docs/models/gpt-5.1",
	},
	NormalizeModelKey("gpt-5.1-codex-mini"): {
		ModelID:             "gpt-5.1-codex-mini",
		InputUSDPerMTok:     0.25,
		CacheReadUSDPerMTok: float64Ptr(0.025),
		OutputUSDPerMTok:    2.00,
		Source:              "https://developers.openai.com/api/docs/pricing",
	},
	NormalizeModelKey("gpt-5-mini"): {
		ModelID:             "gpt-5-mini",
		InputUSDPerMTok:     0.25,
		CacheReadUSDPerMTok: float64Ptr(0.025),
		OutputUSDPerMTok:    2.00,
		Source:              "https://developers.openai.com/api/docs/models/gpt-5-mini",
	},
	NormalizeModelKey("gpt-5.5"): {
		ModelID:             "gpt-5.5",
		InputUSDPerMTok:     5.00,
		CacheReadUSDPerMTok: float64Ptr(0.50),
		OutputUSDPerMTok:    30.00,
		Source:              "https://raw.githubusercontent.com/github/docs/main/data/tables/copilot/models-and-pricing.yml",
	},
	NormalizeModelKey("gpt-4.1"): {
		ModelID:             "gpt-4.1",
		InputUSDPerMTok:     2.00,
		CacheReadUSDPerMTok: float64Ptr(0.50),
		OutputUSDPerMTok:    8.00,
		Source:              "https://developers.openai.com/api/docs/models/gpt-4.1",
	},
	NormalizeModelKey("gpt-4o"): {
		ModelID:             "gpt-4o",
		InputUSDPerMTok:     2.50,
		CacheReadUSDPerMTok: float64Ptr(0.25),
		OutputUSDPerMTok:    10.00,
		Source:              "https://raw.githubusercontent.com/github/docs/main/data/tables/copilot/models-and-pricing.yml",
	},
	NormalizeModelKey("grok-code-fast-1"): {
		ModelID:             "grok-code-fast-1",
		InputUSDPerMTok:     0.20,
		CacheReadUSDPerMTok: float64Ptr(0.02),
		OutputUSDPerMTok:    1.50,
		Source:              "https://raw.githubusercontent.com/github/docs/main/data/tables/copilot/models-and-pricing.yml",
	},
	NormalizeModelKey("raptor-mini"): {
		ModelID:             "raptor-mini",
		InputUSDPerMTok:     0.25,
		CacheReadUSDPerMTok: float64Ptr(0.025),
		OutputUSDPerMTok:    2.00,
		Source:              "https://raw.githubusercontent.com/github/docs/main/data/tables/copilot/models-and-pricing.yml",
	},
	NormalizeModelKey("goldeneye"): {
		ModelID:             "goldeneye",
		InputUSDPerMTok:     1.25,
		CacheReadUSDPerMTok: float64Ptr(0.125),
		OutputUSDPerMTok:    10.00,
		Source:              "https://raw.githubusercontent.com/github/docs/main/data/tables/copilot/models-and-pricing.yml",
	},
}

func float64Ptr(v float64) *float64 {
	return &v
}

func NormalizeModelKey(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(strings.ToLower(s), " ", ""), "-", "")
	return strings.TrimSuffix(s, "preview")
}

func (stat *ModelStat) AddExtraUsage(key string, tokens int64) {
	if stat == nil || key == "" || tokens == 0 {
		return
	}
	if stat.ExtraUsageTokens == nil {
		stat.ExtraUsageTokens = make(map[string]int64)
	}
	stat.ExtraUsageTokens[key] += tokens
	if stat.ExtraUsageTokens[key] == 0 {
		delete(stat.ExtraUsageTokens, key)
	}
}

func (stat *ModelStat) SortedExtraUsageKeys() []string {
	if stat == nil || len(stat.ExtraUsageTokens) == 0 {
		return nil
	}
	keys := make([]string, 0, len(stat.ExtraUsageTokens))
	for key, tokens := range stat.ExtraUsageTokens {
		if tokens == 0 {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (stat *ModelStat) HasTokenUsage() bool {
	if stat == nil {
		return false
	}
	if stat.Input != 0 || stat.CacheRead != 0 || stat.CacheWrite != 0 || stat.Output != 0 {
		return true
	}
	for _, tokens := range stat.ExtraUsageTokens {
		if tokens != 0 {
			return true
		}
	}
	return false
}

func EstimateAPICost(model string, stat *ModelStat) *APICostEstimate {
	return EstimateAPICostWithOverrides(model, stat, nil)
}

func effectiveUncachedInputTokens(stat *ModelStat) (int64, bool) {
	if stat.CacheRead <= 0 {
		return stat.Input, true
	}
	// Local session.shutdown usage appears to report inputTokens as total input
	// that already includes cacheReadTokens. Price only the uncached remainder
	// at the full input rate and bill cache reads separately.
	if stat.Input < stat.CacheRead {
		return 0, false
	}
	return stat.Input - stat.CacheRead, true
}

func EstimateAPICostWithOverrides(model string, stat *ModelStat, overrides *APIPricingOverrides) *APICostEstimate {
	if !stat.HasTokenUsage() {
		return nil
	}
	price, ok := resolveAPIPricingEntry(model, overrides)
	if !ok {
		return nil
	}
	uncachedInputTokens, uncachedInputOK := effectiveUncachedInputTokens(stat)
	estimate := &APICostEstimate{
		UncachedInputTokens:  uncachedInputTokens,
		InputUSDPerMTok:      price.InputUSDPerMTok,
		CacheReadUSDPerMTok:  price.CacheReadUSDPerMTok,
		CacheWriteUSDPerMTok: price.CacheWriteUSDPerMTok,
		OutputUSDPerMTok:     price.OutputUSDPerMTok,
		InputUSD:             float64(uncachedInputTokens) / 1_000_000 * price.InputUSDPerMTok,
		OutputUSD:            float64(stat.Output) / 1_000_000 * price.OutputUSDPerMTok,
		IsComplete:           true,
		PriceCatalogModel:    price.ModelID,
		Source:               price.Source,
	}
	if !uncachedInputOK {
		estimate.IsComplete = false
		estimate.MissingPriceComponents = append(estimate.MissingPriceComponents, "inclusiveInputAssumptionMismatch")
	}
	estimate.TotalUSD = estimate.InputUSD + estimate.OutputUSD

	if stat.CacheRead > 0 {
		if price.CacheReadUSDPerMTok != nil {
			estimate.CacheReadUSD = float64(stat.CacheRead) / 1_000_000 * *price.CacheReadUSDPerMTok
			estimate.TotalUSD += estimate.CacheReadUSD
		} else {
			estimate.IsComplete = false
			estimate.MissingPriceComponents = append(estimate.MissingPriceComponents, "cacheReadTokens")
		}
	}
	if stat.CacheWrite > 0 {
		if price.CacheWriteUSDPerMTok != nil {
			estimate.CacheWriteUSD = float64(stat.CacheWrite) / 1_000_000 * *price.CacheWriteUSDPerMTok
			estimate.TotalUSD += estimate.CacheWriteUSD
		} else {
			estimate.IsComplete = false
			estimate.MissingPriceComponents = append(estimate.MissingPriceComponents, "cacheWriteTokens")
		}
	}
	for _, key := range stat.SortedExtraUsageKeys() {
		estimate.IsComplete = false
		estimate.MissingPriceComponents = append(estimate.MissingPriceComponents, key)
	}

	return estimate
}
