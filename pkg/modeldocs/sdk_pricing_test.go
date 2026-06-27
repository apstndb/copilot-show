package modeldocs

import (
	"testing"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

func TestSDKTokenPricingFromRPCMatchesDocsStyleUSD(t *testing.T) {
	t.Parallel()

	pricing := SDKTokenPricingFromRPC(&rpc.ModelBillingTokenPrices{
		BatchSize:       ptrInt64(1_000_000),
		InputPrice:      ptrFloat64(300),
		CacheReadPrice:  ptrFloat64(30),
		CacheWritePrice: ptrFloat64(375),
		OutputPrice:     ptrFloat64(1500),
	})
	if pricing == nil {
		t.Fatal("SDKTokenPricingFromRPC() = nil, want pricing")
	}
	if pricing.InputUSDPerMTok == nil || *pricing.InputUSDPerMTok != 3 {
		t.Fatalf("InputUSDPerMTok = %#v, want 3", pricing.InputUSDPerMTok)
	}
	if pricing.CachedInputUSDPerMTok == nil || *pricing.CachedInputUSDPerMTok != 0.3 {
		t.Fatalf("CachedInputUSDPerMTok = %#v, want 0.3", pricing.CachedInputUSDPerMTok)
	}
	if pricing.CacheWriteUSDPerMTok == nil || *pricing.CacheWriteUSDPerMTok != 3.75 {
		t.Fatalf("CacheWriteUSDPerMTok = %#v, want 3.75", pricing.CacheWriteUSDPerMTok)
	}
	if pricing.OutputUSDPerMTok == nil || *pricing.OutputUSDPerMTok != 15 {
		t.Fatalf("OutputUSDPerMTok = %#v, want 15", pricing.OutputUSDPerMTok)
	}
	if got, want := SDKTokenPricingIOSummary(pricing), "3/15"; got != want {
		t.Fatalf("SDKTokenPricingIOSummary() = %q, want %q", got, want)
	}
}

func TestSDKTokenPricingFromModelUsesBillingTokenPrices(t *testing.T) {
	t.Parallel()

	pricing := SDKTokenPricingFromModel(copilot.ModelInfo{
		Billing: &copilot.ModelBilling{
			TokenPrices: &rpc.ModelBillingTokenPrices{
				BatchSize:  ptrInt64(1_000_000),
				InputPrice: ptrFloat64(250),
				OutputPrice: ptrFloat64(1500),
			},
		},
	})
	if got, want := SDKTokenPricingIOSummary(pricing), "2.5/15"; got != want {
		t.Fatalf("SDKTokenPricingIOSummary() = %q, want %q", got, want)
	}
}

func ptrFloat64(v float64) *float64 {
	return &v
}

func ptrInt64(v int64) *int64 {
	return &v
}
