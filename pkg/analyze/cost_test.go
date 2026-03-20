package analyze

import (
	"testing"
)

func TestEstimateAPICost(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		stat     *ModelStat
		wantUSD  float64
		wantNil  bool
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
				Input:     1_000_000, // $2.50
				CacheRead: 1_000_000, // $0.25
				Output:    0,
			},
			wantUSD: 2.75,
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
			name:  "Empty Stat",
			model: "gpt-5.4",
			stat:  &ModelStat{},
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
		})
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
