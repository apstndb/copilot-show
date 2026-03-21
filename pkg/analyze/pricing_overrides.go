package analyze

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
)

var BuiltInAPIPricingSources = []string{
	"https://developers.openai.com/api/docs/pricing",
	"https://platform.claude.com/docs/en/about-claude/pricing",
	"https://ai.google.dev/gemini-api/docs/pricing?hl=en",
}

type APIPricingOverride struct {
	InputUSDPerMTok      *float64 `json:"inputUsdPerMToken,omitempty" yaml:"inputUsdPerMToken,omitempty"`
	CacheReadUSDPerMTok  *float64 `json:"cacheReadUsdPerMToken,omitempty" yaml:"cacheReadUsdPerMToken,omitempty"`
	CacheWriteUSDPerMTok *float64 `json:"cacheWriteUsdPerMToken,omitempty" yaml:"cacheWriteUsdPerMToken,omitempty"`
	OutputUSDPerMTok     *float64 `json:"outputUsdPerMToken,omitempty" yaml:"outputUsdPerMToken,omitempty"`
}

type apiPricingOverrideFile struct {
	Models map[string]APIPricingOverride `json:"models" yaml:"models"`
}

type APIPricingOverrides struct {
	Path     string   `json:"path" yaml:"path"`
	ModelIDs []string `json:"modelIds" yaml:"modelIds"`

	models map[string]APIPricingOverride
}

func LoadAPIPricingOverrides(path string) (*APIPricingOverrides, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read API pricing override %q: %w", path, err)
	}

	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse API pricing override %q: %w", path, err)
	}
	if _, ok := root["models"]; !ok {
		return nil, fmt.Errorf("parse API pricing override %q: missing top-level models key", path)
	}

	var file apiPricingOverrideFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse API pricing override %q: %w", path, err)
	}
	overrides := &APIPricingOverrides{
		Path:   path,
		models: make(map[string]APIPricingOverride, len(file.Models)),
	}
	for modelID, override := range file.Models {
		key := NormalizeModelKey(modelID)
		if key == "" {
			return nil, fmt.Errorf("parse API pricing override %q: model ID %q becomes empty after normalization", path, modelID)
		}
		if _, exists := overrides.models[key]; exists {
			return nil, fmt.Errorf("parse API pricing override %q: duplicate normalized model key %q", path, modelID)
		}
		if !override.hasValues() {
			return nil, fmt.Errorf("parse API pricing override %q: model %q does not override any pricing fields", path, modelID)
		}
		if _, exists := apiPricingCatalog[key]; !exists {
			if override.InputUSDPerMTok == nil || override.OutputUSDPerMTok == nil {
				return nil, fmt.Errorf("parse API pricing override %q: model %q is not in the built-in catalog and must define both inputUsdPerMToken and outputUsdPerMToken", path, modelID)
			}
		}
		overrides.models[key] = override
		overrides.ModelIDs = append(overrides.ModelIDs, modelID)
	}
	sort.Strings(overrides.ModelIDs)

	return overrides, nil
}

func APIPricingTemplate() string {
	entries := builtInAPIPricingEntries()
	var b strings.Builder
	b.WriteString("# API pricing override template for `copilot-show stats --api-pricing <file>`\n")
	b.WriteString("# Uncomment only the models and fields you want to override.\n")
	b.WriteString("# Omitted fields inherit the built-in catalog.\n")
	b.WriteString("# New models are allowed, but they should define at least\n")
	b.WriteString("# inputUsdPerMToken and outputUsdPerMToken.\n")
	b.WriteString("# Built-in catalog version: " + PricingCatalogVersion + "\n")
	b.WriteString("models:\n")
	for _, entry := range entries {
		b.WriteString("  # " + entry.ModelID + ":\n")
		b.WriteString("  #   inputUsdPerMToken: " + formatTemplateFloat(entry.InputUSDPerMTok) + "\n")
		writeCommentedTemplateFloat(&b, "cacheReadUsdPerMToken", entry.CacheReadUSDPerMTok)
		writeCommentedTemplateFloat(&b, "cacheWriteUsdPerMToken", entry.CacheWriteUSDPerMTok)
		b.WriteString("  #   outputUsdPerMToken: " + formatTemplateFloat(entry.OutputUSDPerMTok) + "\n")
	}
	b.WriteString("  # custom-model:\n")
	b.WriteString("  #   inputUsdPerMToken:\n")
	b.WriteString("  #   cacheReadUsdPerMToken:\n")
	b.WriteString("  #   cacheWriteUsdPerMToken:\n")
	b.WriteString("  #   outputUsdPerMToken:\n")
	return b.String()
}

func (o *APIPricingOverrides) CatalogVersion() string {
	if o == nil || !o.HasActiveModels() {
		return PricingCatalogVersion
	}
	return PricingCatalogVersion + "+local-yaml"
}

func (o *APIPricingOverrides) Sources() []string {
	sources := append([]string(nil), BuiltInAPIPricingSources...)
	if o == nil || !o.HasActiveModels() {
		return sources
	}
	return append([]string{"local override: " + o.Path}, sources...)
}

func (o *APIPricingOverrides) HasActiveModels() bool {
	return o != nil && len(o.ModelIDs) > 0
}

func (o *APIPricingOverrides) hasModel(model string) bool {
	if o == nil {
		return false
	}
	_, ok := o.models[NormalizeModelKey(model)]
	return ok
}

func resolveAPIPricingEntry(model string, overrides *APIPricingOverrides) (priceCatalogEntry, bool) {
	key := NormalizeModelKey(model)
	price, ok := apiPricingCatalog[key]
	if overrides == nil {
		return price, ok
	}

	override, hasOverride := overrides.models[key]
	if !ok && !hasOverride {
		return priceCatalogEntry{}, false
	}
	if !ok {
		price = priceCatalogEntry{
			ModelID: model,
		}
	}
	if !hasOverride {
		return price, ok
	}

	overrideApplied := false
	if override.InputUSDPerMTok != nil {
		price.InputUSDPerMTok = *override.InputUSDPerMTok
		overrideApplied = true
	}
	if override.CacheReadUSDPerMTok != nil {
		price.CacheReadUSDPerMTok = override.CacheReadUSDPerMTok
		overrideApplied = true
	}
	if override.CacheWriteUSDPerMTok != nil {
		price.CacheWriteUSDPerMTok = override.CacheWriteUSDPerMTok
		overrideApplied = true
	}
	if override.OutputUSDPerMTok != nil {
		price.OutputUSDPerMTok = *override.OutputUSDPerMTok
		overrideApplied = true
	}
	if overrideApplied || !ok {
		price.Source = "local override: " + overrides.Path
	}
	if price.ModelID == "" {
		price.ModelID = model
	}
	return price, ok || hasOverride
}

func (o APIPricingOverride) hasValues() bool {
	return o.InputUSDPerMTok != nil ||
		o.CacheReadUSDPerMTok != nil ||
		o.CacheWriteUSDPerMTok != nil ||
		o.OutputUSDPerMTok != nil
}

func builtInAPIPricingEntries() []priceCatalogEntry {
	entries := make([]priceCatalogEntry, 0, len(apiPricingCatalog))
	for _, entry := range apiPricingCatalog {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].ModelID < entries[j].ModelID
	})
	return entries
}

func formatTemplateFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func writeCommentedTemplateFloat(b *strings.Builder, key string, value *float64) {
	if value == nil {
		b.WriteString("  #   " + key + ":\n")
		return
	}
	b.WriteString("  #   " + key + ": " + formatTemplateFloat(*value) + "\n")
}
