package modeldocs

import (
	"strconv"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

// SDK credits are expressed per billing batch; one USD equals this many credits.
const sdkCreditsPerUSD = 100.0

const defaultTokenPricingBatchSize = int64(1_000_000)

// SDKTokenPricing is USD-per-million-token pricing from Copilot SDK ListModels billing.tokenPrices.
type SDKTokenPricing struct {
	InputUSDPerMTok       *float64 `json:"inputUsdPerMToken,omitempty" yaml:"inputUsdPerMToken,omitempty"`
	CachedInputUSDPerMTok *float64 `json:"cachedInputUsdPerMToken,omitempty" yaml:"cachedInputUsdPerMToken,omitempty"`
	CacheWriteUSDPerMTok  *float64 `json:"cacheWriteUsdPerMToken,omitempty" yaml:"cacheWriteUsdPerMToken,omitempty"`
	OutputUSDPerMTok      *float64 `json:"outputUsdPerMToken,omitempty" yaml:"outputUsdPerMToken,omitempty"`
}

func SDKTokenPricingFromModel(model copilot.ModelInfo) *SDKTokenPricing {
	if model.Billing == nil || model.Billing.TokenPrices == nil {
		return nil
	}
	return SDKTokenPricingFromRPC(model.Billing.TokenPrices)
}

func SDKTokenPricingFromRPC(tokenPrices *rpc.ModelBillingTokenPrices) *SDKTokenPricing {
	if tokenPrices == nil {
		return nil
	}
	return &SDKTokenPricing{
		InputUSDPerMTok:       creditsPerBatchToUSDPerMTok(tokenPrices.InputPrice, tokenPrices.BatchSize),
		CachedInputUSDPerMTok: creditsPerBatchToUSDPerMTok(cacheReadCredits(tokenPrices), tokenPrices.BatchSize),
		CacheWriteUSDPerMTok:  creditsPerBatchToUSDPerMTok(tokenPrices.CacheWritePrice, tokenPrices.BatchSize),
		OutputUSDPerMTok:      creditsPerBatchToUSDPerMTok(tokenPrices.OutputPrice, tokenPrices.BatchSize),
	}
}

func SDKTokenPricingIOSummary(pricing *SDKTokenPricing) string {
	if pricing == nil || (pricing.InputUSDPerMTok == nil && pricing.OutputUSDPerMTok == nil) {
		return "-"
	}
	input := formatUSDPerMTok(pricing.InputUSDPerMTok)
	output := formatUSDPerMTok(pricing.OutputUSDPerMTok)
	return input + "/" + output
}

func creditsPerBatchToUSDPerMTok(credits *float64, batchSize *int64) *float64 {
	if credits == nil {
		return nil
	}
	batch := defaultTokenPricingBatchSize
	if batchSize != nil && *batchSize > 0 {
		batch = *batchSize
	}
	usd := *credits / sdkCreditsPerUSD
	if batch != defaultTokenPricingBatchSize {
		usd *= float64(defaultTokenPricingBatchSize) / float64(batch)
	}
	return &usd
}

func cacheReadCredits(tokenPrices *rpc.ModelBillingTokenPrices) *float64 {
	if tokenPrices.CacheReadPrice != nil {
		return tokenPrices.CacheReadPrice
	}
	return tokenPrices.CachePrice
}

func formatUSDPerMTok(value *float64) string {
	if value == nil {
		return "-"
	}
	if *value == 0 {
		return "0"
	}
	return strconv.FormatFloat(*value, 'f', -1, 64)
}

func cloneSDKTokenPricing(pricing *SDKTokenPricing) *SDKTokenPricing {
	if pricing == nil {
		return nil
	}
	cloned := &SDKTokenPricing{}
	if pricing.InputUSDPerMTok != nil {
		value := *pricing.InputUSDPerMTok
		cloned.InputUSDPerMTok = &value
	}
	if pricing.CachedInputUSDPerMTok != nil {
		value := *pricing.CachedInputUSDPerMTok
		cloned.CachedInputUSDPerMTok = &value
	}
	if pricing.CacheWriteUSDPerMTok != nil {
		value := *pricing.CacheWriteUSDPerMTok
		cloned.CacheWriteUSDPerMTok = &value
	}
	if pricing.OutputUSDPerMTok != nil {
		value := *pricing.OutputUSDPerMTok
		cloned.OutputUSDPerMTok = &value
	}
	return cloned
}
