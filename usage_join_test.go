package main

import (
	"testing"

	"github.com/apstndb/copilot-show/pkg/modeldocs"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

func TestUsageModelPricingKeysDedupesNormalizeVariants(t *testing.T) {
	t.Parallel()

	keys := usageModelPricingKeys("claude-sonnet-4.6")
	if len(keys) == 0 {
		t.Fatal("usageModelPricingKeys() = empty")
	}
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if _, ok := seen[key]; ok {
			t.Fatalf("duplicate key %q in %v", key, keys)
		}
		seen[key] = struct{}{}
	}
}

func TestUsageModelLookupNamesStripsAutoPrefix(t *testing.T) {
	t.Parallel()

	names := usageModelLookupNames("Auto: GPT-5.4 mini")
	if len(names) < 2 || names[1] != "GPT-5.4 mini" {
		t.Fatalf("usageModelLookupNames() = %#v, want stripped Auto prefix", names)
	}
}

func TestBuildModelPricingLookupMatchesNameAndID(t *testing.T) {
	t.Parallel()

	lookup := buildModelPricingLookup([]copilot.ModelInfo{{
		ID:   "claude-sonnet-4.6",
		Name: "Claude Sonnet 4.6",
		Billing: &copilot.ModelBilling{
			TokenPrices: &rpc.ModelBillingTokenPrices{
				BatchSize:   ptrInt64(1_000_000),
				InputPrice:  ptrFloat64(300),
				OutputPrice: ptrFloat64(1500),
			},
		},
	}})

	for _, modelName := range []string{"claude-sonnet-4.6", "Claude Sonnet 4.6", "Auto: Claude Sonnet 4.6"} {
		pricing, ok := lookupUsageModelPricing(modelName, lookup)
		if !ok || pricing == nil {
			t.Fatalf("lookupUsageModelPricing(%q) = %#v, ok=%v", modelName, pricing, ok)
		}
		if got, want := modeldocs.SDKTokenPricingIOSummary(pricing), "3/15"; got != want {
			t.Fatalf("SDKTokenPricingIOSummary(%q) = %q, want %q", modelName, got, want)
		}
	}
}

func TestFormatUsageItemIOSummaryUnmatched(t *testing.T) {
	t.Parallel()

	if got := formatUsageItemIOSummary("unknown-model", nil); got != "-" {
		t.Fatalf("formatUsageItemIOSummary() = %q, want -", got)
	}
}

func TestUsageTableHeaderIncludesPricingColumn(t *testing.T) {
	t.Parallel()

	header, rightAligned := usageTableHeader(usageBillingAICredits, false)
	want := []string{"SKU", "Model", "Used (credits)", modelsAPIPricingColumnHeader}
	if len(header) != len(want) {
		t.Fatalf("header = %#v, want %#v", header, want)
	}
	for i, column := range want {
		if header[i] != column {
			t.Fatalf("header[%d] = %q, want %q", i, header[i], column)
		}
	}
	if len(rightAligned) != 2 || rightAligned[0] != 2 || rightAligned[1] != 3 {
		t.Fatalf("rightAligned = %#v, want [2 3]", rightAligned)
	}
	for _, column := range header {
		if column == "Billed (credits)" || column == "Amount (USD)" {
			t.Fatalf("default usage header unexpectedly includes %q: %#v", column, header)
		}
	}
}

func TestEnrichUsageResponsesForYAMLAddsPricing(t *testing.T) {
	t.Parallel()

	responses := []*usageResponse{{
		User: "octocat",
		UsageItems: []struct {
			Product          string  `json:"product" yaml:"product"`
			SKU              string  `json:"sku" yaml:"sku"`
			Model            string  `json:"model" yaml:"model"`
			UnitType         string  `json:"unitType" yaml:"unitType"`
			PricePerUnit     float64 `json:"pricePerUnit" yaml:"pricePerUnit"`
			GrossQuantity    float64 `json:"grossQuantity" yaml:"grossQuantity"`
			GrossAmount      float64 `json:"grossAmount" yaml:"grossAmount"`
			DiscountQuantity float64 `json:"discountQuantity" yaml:"discountQuantity"`
			DiscountAmount   float64 `json:"discountAmount" yaml:"discountAmount"`
			NetQuantity      float64 `json:"netQuantity" yaml:"netQuantity"`
			NetAmount        float64 `json:"netAmount" yaml:"netAmount"`
		}{{
			Model:         "gpt-5.4",
			GrossQuantity: 12.5,
		}},
	}}
	lookup := buildModelPricingLookup([]copilot.ModelInfo{{
		ID: "gpt-5.4",
		Billing: &copilot.ModelBilling{
			TokenPrices: &rpc.ModelBillingTokenPrices{
				BatchSize:   ptrInt64(1_000_000),
				InputPrice:  ptrFloat64(250),
				OutputPrice: ptrFloat64(1500),
			},
		},
	}})

	enriched, ok := enrichUsageResponsesForYAML(responses, lookup).(usageResponseEnriched)
	if !ok {
		t.Fatalf("enrichUsageResponsesForYAML() = %T, want usageResponseEnriched", enrichUsageResponsesForYAML(responses, lookup))
	}
	if len(enriched.UsageItems) != 1 {
		t.Fatalf("usageItems len = %d, want 1", len(enriched.UsageItems))
	}
	item := enriched.UsageItems[0]
	if item.IOSummary != "2.5/15" {
		t.Fatalf("ioSummary = %q, want 2.5/15", item.IOSummary)
	}
	if item.TokenPricing == nil || item.TokenPricing.InputUSDPerMTok == nil {
		t.Fatalf("tokenPricing = %#v, want input pricing", item.TokenPricing)
	}
}

func TestFlattenUsageResponses(t *testing.T) {
	t.Parallel()

	month := 6
	day := 15
	items := flattenUsageResponses([]*usageResponse{
		{
			TimePeriod: struct {
				Year  int  `json:"year" yaml:"year"`
				Month *int `json:"month" yaml:"month"`
				Day   *int `json:"day" yaml:"day"`
			}{Year: 2026, Month: &month, Day: &day},
			UsageItems: []struct {
				Product          string  `json:"product" yaml:"product"`
				SKU              string  `json:"sku" yaml:"sku"`
				Model            string  `json:"model" yaml:"model"`
				UnitType         string  `json:"unitType" yaml:"unitType"`
				PricePerUnit     float64 `json:"pricePerUnit" yaml:"pricePerUnit"`
				GrossQuantity    float64 `json:"grossQuantity" yaml:"grossQuantity"`
				GrossAmount      float64 `json:"grossAmount" yaml:"grossAmount"`
				DiscountQuantity float64 `json:"discountQuantity" yaml:"discountQuantity"`
				DiscountAmount   float64 `json:"discountAmount" yaml:"discountAmount"`
				NetQuantity      float64 `json:"netQuantity" yaml:"netQuantity"`
				NetAmount        float64 `json:"netAmount" yaml:"netAmount"`
			}{
				{SKU: "copilot-chat", Model: "gpt-5.4", GrossQuantity: 10, NetQuantity: 0, NetAmount: 0},
			},
		},
	})

	if len(items) != 1 {
		t.Fatalf("flattened len = %d, want 1", len(items))
	}
	if items[0].Period != "2026-06-15" || items[0].Model != "gpt-5.4" || items[0].GrossQuantity != 10 {
		t.Fatalf("flattened item = %#v", items[0])
	}
}

func ptrFloat64(v float64) *float64 {
	return &v
}

func ptrInt64(v int64) *int64 {
	return &v
}
