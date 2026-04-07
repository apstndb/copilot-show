package analyze

import (
	"testing"
)

func TestEstimateAPICost(t *testing.T) {
	tests := []struct {
		name             string
		model            string
		stat             *ModelStat
		wantUSD          float64
		wantNil          bool
		wantCatalogModel string
	}{
		{
			name:  "GPT-5.4 Basic",
			model: "gpt-5.4",
			stat: &ModelStat{
				Input:  1_000_000, // $2.50
				Output: 1_000_000, // $15.00
			},
			wantUSD: 17.50,
		},
		{
			name:  "GPT-5.4 with Cache Read",
			model: "gpt-5.4",
			stat: &ModelStat{
				Input:     1_000_000, // Inclusive total input
				CacheRead: 1_000_000, // $0.25
				Output:    0,
			},
			wantUSD: 0.25,
		},
		{
			name:  "GPT-5.4 with Mixed Cached and Uncached Input",
			model: "gpt-5.4",
			stat: &ModelStat{
				Input:     3_000_000, // Inclusive total input
				CacheRead: 2_000_000, // $0.50
				Output:    1_000_000, // $15.00
			},
			wantUSD: 18.0, // (3M-2M)*$2.50 + 2M*$0.25 + 1M*$15
		},
		{
			name:  "Unknown Model",
			model: "unknown-model",
			stat: &ModelStat{
				Input: 1000,
			},
			wantNil: true,
		},
		{
			name:  "Gemini 3 Pro Preview",
			model: "gemini-3-pro-preview",
			stat: &ModelStat{
				Input:     1_000_000, // Inclusive total input
				CacheRead: 1_000_000, // $0.20
				Output:    1_000_000, // $12.00
			},
			wantUSD:          12.20,
			wantCatalogModel: "gemini-3-pro-preview",
		},
		{
			name:  "Gemini 3 Pro Alias Without Preview",
			model: "gemini-3-pro",
			stat: &ModelStat{
				Input:  1_000_000, // $2.00
				Output: 1_000_000, // $12.00
			},
			wantUSD:          14.00,
			wantCatalogModel: "gemini-3-pro-preview",
		},
		{
			name:  "GPT-5 mini",
			model: "gpt-5-mini",
			stat: &ModelStat{
				Input:  1_000_000, // $0.25
				Output: 1_000_000, // $2.00
			},
			wantUSD:          2.25,
			wantCatalogModel: "gpt-5-mini",
		},
		{
			name:  "GPT-4.1 Current Pricing",
			model: "gpt-4.1",
			stat: &ModelStat{
				Input:     1_000_000, // Inclusive total input
				CacheRead: 1_000_000, // $0.50
				Output:    1_000_000, // $8.00
			},
			wantUSD:          8.50,
			wantCatalogModel: "gpt-4.1",
		},
		{
			name:    "Empty Stat",
			model:   "gpt-5.4",
			stat:    &ModelStat{},
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EstimateAPICost(tt.model, tt.stat)
			if tt.wantNil {
				if got != nil {
					t.Errorf("EstimateAPICost() = %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("EstimateAPICost() = nil, want non-nil")
			}

			// Check total cost with small epsilon for float comparison
			if abs(got.TotalUSD-tt.wantUSD) > 1e-9 {
				t.Errorf("EstimateAPICost().TotalUSD = %v, want %v", got.TotalUSD, tt.wantUSD)
			}
			if tt.wantCatalogModel != "" && got.PriceCatalogModel != tt.wantCatalogModel {
				t.Errorf("EstimateAPICost().PriceCatalogModel = %q, want %q", got.PriceCatalogModel, tt.wantCatalogModel)
			}
			if got.InputUSDPerMTok <= 0 || got.OutputUSDPerMTok <= 0 {
				t.Errorf("EstimateAPICost() missing required rate fields: %#v", got)
			}
		})
	}
}

func TestEstimateAPICostInclusiveInputMismatchIsIncomplete(t *testing.T) {
	got := EstimateAPICost("gpt-5.4", &ModelStat{
		Input:     1_000_000,
		CacheRead: 2_000_000,
	})
	if got == nil {
		t.Fatal("EstimateAPICost() = nil, want non-nil")
	}
	if got.IsComplete {
		t.Fatal("EstimateAPICost().IsComplete = true, want false")
	}
	if diff := abs(got.TotalUSD - 0.5); diff > 1e-9 {
		t.Fatalf("EstimateAPICost().TotalUSD = %v, want 0.5", got.TotalUSD)
	}
	if got.InputUSD != 0 {
		t.Fatalf("EstimateAPICost().InputUSD = %v, want 0", got.InputUSD)
	}
	if got.UncachedInputTokens != 0 {
		t.Fatalf("EstimateAPICost().UncachedInputTokens = %d, want 0", got.UncachedInputTokens)
	}
	if len(got.MissingPriceComponents) != 1 || got.MissingPriceComponents[0] != "inclusiveInputAssumptionMismatch" {
		t.Fatalf("EstimateAPICost().MissingPriceComponents = %#v, want inclusiveInputAssumptionMismatch", got.MissingPriceComponents)
	}
}

func TestEstimateAPICostTracksUncachedInputTokens(t *testing.T) {
	got := EstimateAPICost("gpt-5.4", &ModelStat{
		Input:     3_000_000,
		CacheRead: 2_000_000,
		Output:    1_000_000,
	})
	if got == nil {
		t.Fatal("EstimateAPICost() = nil, want non-nil")
	}
	if got.UncachedInputTokens != 1_000_000 {
		t.Fatalf("EstimateAPICost().UncachedInputTokens = %d, want 1000000", got.UncachedInputTokens)
	}
	if got.CacheReadUSDPerMTok == nil || abs(*got.CacheReadUSDPerMTok-0.25) > 1e-9 {
		t.Fatalf("EstimateAPICost().CacheReadUSDPerMTok = %#v, want 0.25", got.CacheReadUSDPerMTok)
	}
}

func TestEstimateAPICostExtraUsageIsIncomplete(t *testing.T) {
	got := EstimateAPICost("gpt-5.4", &ModelStat{
		Input:  1_000_000,
		Output: 1_000_000,
		ExtraUsageTokens: map[string]int64{
			"cacheOutputTokens": 250_000,
		},
	})
	if got == nil {
		t.Fatal("EstimateAPICost() = nil, want non-nil")
	}
	if got.IsComplete {
		t.Fatal("EstimateAPICost().IsComplete = true, want false")
	}
	if diff := abs(got.TotalUSD - 17.5); diff > 1e-9 {
		t.Fatalf("EstimateAPICost().TotalUSD = %v, want 17.5", got.TotalUSD)
	}
	if len(got.MissingPriceComponents) != 1 || got.MissingPriceComponents[0] != "cacheOutputTokens" {
		t.Fatalf("EstimateAPICost().MissingPriceComponents = %#v, want [cacheOutputTokens]", got.MissingPriceComponents)
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
