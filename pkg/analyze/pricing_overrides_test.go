package analyze

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadAPIPricingOverrides(t *testing.T) {
	path := writePricingOverrideFile(t, `
models:
  gpt-5.4:
    inputUsdPerMToken: 1.5
    outputUsdPerMToken: 12
  custom-model:
    inputUsdPerMToken: 0.9
    outputUsdPerMToken: 4.2
`)

	overrides, err := LoadAPIPricingOverrides(path)
	if err != nil {
		t.Fatalf("LoadAPIPricingOverrides() error = %v", err)
	}

	if got, want := overrides.CatalogVersion(), PricingCatalogVersion+"+local-yaml"; got != want {
		t.Fatalf("CatalogVersion() = %q, want %q", got, want)
	}

	if got, want := overrides.ModelIDs, []string{"custom-model", "gpt-5.4"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ModelIDs = %v, want %v", got, want)
	}

	sources := overrides.Sources()
	if len(sources) == 0 || sources[0] != "local override: "+path {
		t.Fatalf("Sources()[0] = %q, want %q", firstOrEmpty(sources), "local override: "+path)
	}

	if !overrides.hasModel("gpt-5.4") {
		t.Fatalf("hasModel(gpt-5.4) = false, want true")
	}
	if !overrides.hasModel("custom-model") {
		t.Fatalf("hasModel(custom-model) = false, want true")
	}
	if !overrides.HasActiveModels() {
		t.Fatalf("HasActiveModels() = false, want true")
	}
}

func TestLoadAPIPricingOverridesAllowsCommentOnlyTemplate(t *testing.T) {
	path := writePricingOverrideFile(t, `
models:
  # gpt-5.4:
  #   inputUsdPerMToken: 1.5
  #   outputUsdPerMToken: 12
`)

	overrides, err := LoadAPIPricingOverrides(path)
	if err != nil {
		t.Fatalf("LoadAPIPricingOverrides() error = %v", err)
	}
	if overrides.HasActiveModels() {
		t.Fatalf("HasActiveModels() = true, want false")
	}
	if got, want := overrides.CatalogVersion(), PricingCatalogVersion; got != want {
		t.Fatalf("CatalogVersion() = %q, want %q", got, want)
	}
	if got, want := overrides.Sources(), BuiltInAPIPricingSources; !reflect.DeepEqual(got, want) {
		t.Fatalf("Sources() = %v, want %v", got, want)
	}
}

func TestLoadAPIPricingOverridesRejectsIncompleteNewModel(t *testing.T) {
	path := writePricingOverrideFile(t, `
models:
  new-model:
    inputUsdPerMToken: 1.25
`)

	_, err := LoadAPIPricingOverrides(path)
	if err == nil {
		t.Fatal("LoadAPIPricingOverrides() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "must define both inputUsdPerMToken and outputUsdPerMToken") {
		t.Fatalf("LoadAPIPricingOverrides() error = %v, want missing input/output message", err)
	}
}

func TestLoadAPIPricingOverridesRequiresModelsKey(t *testing.T) {
	path := writePricingOverrideFile(t, `
gpt-5.4:
  inputUsdPerMToken: 1.5
  outputUsdPerMToken: 12
`)

	_, err := LoadAPIPricingOverrides(path)
	if err == nil {
		t.Fatal("LoadAPIPricingOverrides() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "missing top-level models key") {
		t.Fatalf("LoadAPIPricingOverrides() error = %v, want missing models key message", err)
	}
}

func TestEstimateAPICostWithOverrides(t *testing.T) {
	path := writePricingOverrideFile(t, `
models:
  gpt-5.4:
    inputUsdPerMToken: 1.5
    outputUsdPerMToken: 12
`)

	overrides, err := LoadAPIPricingOverrides(path)
	if err != nil {
		t.Fatalf("LoadAPIPricingOverrides() error = %v", err)
	}

	got := EstimateAPICostWithOverrides("gpt-5.4", &ModelStat{
		Input:  1_000_000,
		Output: 1_000_000,
	}, overrides)
	if got == nil {
		t.Fatal("EstimateAPICostWithOverrides() = nil, want non-nil")
	}
	if diff := abs(got.TotalUSD - 13.5); diff > 1e-9 {
		t.Fatalf("EstimateAPICostWithOverrides().TotalUSD = %v, want 13.5", got.TotalUSD)
	}
	if got.Source != "local override: "+path {
		t.Fatalf("EstimateAPICostWithOverrides().Source = %q, want %q", got.Source, "local override: "+path)
	}
}

func TestEstimateAPICostWithOverridesAllowsNewModel(t *testing.T) {
	path := writePricingOverrideFile(t, `
models:
  custom-model:
    inputUsdPerMToken: 0.9
    outputUsdPerMToken: 4.2
`)

	overrides, err := LoadAPIPricingOverrides(path)
	if err != nil {
		t.Fatalf("LoadAPIPricingOverrides() error = %v", err)
	}

	got := EstimateAPICostWithOverrides("custom-model", &ModelStat{
		Input:  2_000_000,
		Output: 1_000_000,
	}, overrides)
	if got == nil {
		t.Fatal("EstimateAPICostWithOverrides() = nil, want non-nil")
	}
	if diff := abs(got.TotalUSD - 6.0); diff > 1e-9 {
		t.Fatalf("EstimateAPICostWithOverrides().TotalUSD = %v, want 6.0", got.TotalUSD)
	}
	if got.PriceCatalogModel != "custom-model" {
		t.Fatalf("EstimateAPICostWithOverrides().PriceCatalogModel = %q, want %q", got.PriceCatalogModel, "custom-model")
	}
	if got.Source != "local override: "+path {
		t.Fatalf("EstimateAPICostWithOverrides().Source = %q, want %q", got.Source, "local override: "+path)
	}
}

func TestAPIPricingTemplate(t *testing.T) {
	template := APIPricingTemplate()
	if !strings.Contains(template, "# API pricing override template for `copilot-show stats --api-pricing <file>`") {
		t.Fatalf("APIPricingTemplate() missing header: %q", template)
	}
	if !strings.Contains(template, "models:\n") {
		t.Fatalf("APIPricingTemplate() missing models root: %q", template)
	}
	if !strings.Contains(template, "  # gpt-5.4:\n") {
		t.Fatalf("APIPricingTemplate() missing gpt-5.4 section: %q", template)
	}
	if !strings.Contains(template, "  #   inputUsdPerMToken: 2.5\n") {
		t.Fatalf("APIPricingTemplate() missing commented input price: %q", template)
	}
	if !strings.Contains(template, "  #   cacheWriteUsdPerMToken:\n") {
		t.Fatalf("APIPricingTemplate() missing commented optional field: %q", template)
	}
}

func writePricingOverrideFile(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "api-pricing.yaml")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(body)+"\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", path, err)
	}
	return path
}

func firstOrEmpty(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
