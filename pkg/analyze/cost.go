package analyze

import (
	"strings"
)

const PricingCatalogVersion = "public-token-pricing-2026-03-20-google-audit"

type ModelStat struct {
	Requests                int64            `json:"requests" yaml:"requests"`
	Cost                    float64          `json:"cost" yaml:"cost"`
	Input                   int64            `json:"inputTokens" yaml:"inputTokens"`
	CacheRead               int64            `json:"cacheReadTokens,omitempty" yaml:"cacheReadTokens,omitempty"`
	CacheWrite              int64            `json:"cacheWriteTokens,omitempty" yaml:"cacheWriteTokens,omitempty"`
	Output                  int64            `json:"outputTokens" yaml:"outputTokens"`
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
		ModelID:             "claude-haiku-4.5",
		InputUSDPerMTok:     1.00,
		CacheReadUSDPerMTok: float64Ptr(0.10),
		OutputUSDPerMTok:    5.00,
		Source:              "https://platform.claude.com/docs/en/about-claude/pricing",
	},
	NormalizeModelKey("claude-sonnet-4"): {
		ModelID:             "claude-sonnet-4",
		InputUSDPerMTok:     3.00,
		CacheReadUSDPerMTok: float64Ptr(0.30),
		OutputUSDPerMTok:    15.00,
		Source:              "https://platform.claude.com/docs/en/about-claude/pricing",
	},
	NormalizeModelKey("claude-sonnet-4.5"): {
		ModelID:             "claude-sonnet-4.5",
		InputUSDPerMTok:     3.00,
		CacheReadUSDPerMTok: float64Ptr(0.30),
		OutputUSDPerMTok:    15.00,
		Source:              "https://platform.claude.com/docs/en/about-claude/pricing",
	},
	NormalizeModelKey("claude-sonnet-4.6"): {
		ModelID:             "claude-sonnet-4.6",
		InputUSDPerMTok:     3.00,
		CacheReadUSDPerMTok: float64Ptr(0.30),
		OutputUSDPerMTok:    15.00,
		Source:              "https://platform.claude.com/docs/en/about-claude/pricing",
	},
	NormalizeModelKey("claude-opus-4.5"): {
		ModelID:             "claude-opus-4.5",
		InputUSDPerMTok:     5.00,
		CacheReadUSDPerMTok: float64Ptr(0.50),
		OutputUSDPerMTok:    25.00,
		Source:              "https://platform.claude.com/docs/en/about-claude/pricing",
	},
	NormalizeModelKey("claude-opus-4.6"): {
		ModelID:             "claude-opus-4.6",
		InputUSDPerMTok:     5.00,
		CacheReadUSDPerMTok: float64Ptr(0.50),
		OutputUSDPerMTok:    25.00,
		Source:              "https://platform.claude.com/docs/en/about-claude/pricing",
	},
	NormalizeModelKey("gemini-3-pro-preview"): {
		ModelID:             "gemini-3-pro-preview",
		InputUSDPerMTok:     2.00,
		CacheReadUSDPerMTok: float64Ptr(0.20),
		OutputUSDPerMTok:    12.00,
		Source:              "https://blog.google/innovation-and-ai/technology/developers-tools/gemini-3-developers/",
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
	NormalizeModelKey("gpt-4.1"): {
		ModelID:             "gpt-4.1",
		InputUSDPerMTok:     2.00,
		CacheReadUSDPerMTok: float64Ptr(0.50),
		OutputUSDPerMTok:    8.00,
		Source:              "https://developers.openai.com/api/docs/models/gpt-4.1",
	},
}

func float64Ptr(v float64) *float64 {
	return &v
}

func NormalizeModelKey(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(strings.ToLower(s), " ", ""), "-", "")
	return strings.TrimSuffix(s, "preview")
}

func EstimateAPICost(model string, stat *ModelStat) *APICostEstimate {
	if stat.Input == 0 && stat.CacheRead == 0 && stat.CacheWrite == 0 && stat.Output == 0 {
		return nil
	}
	price, ok := apiPricingCatalog[NormalizeModelKey(model)]
	if !ok {
		return nil
	}
	estimate := &APICostEstimate{
		InputUSD:          float64(stat.Input) / 1_000_000 * price.InputUSDPerMTok,
		OutputUSD:         float64(stat.Output) / 1_000_000 * price.OutputUSDPerMTok,
		IsComplete:        true,
		PriceCatalogModel: price.ModelID,
		Source:            price.Source,
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

	return estimate
}
