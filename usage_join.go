package main

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/apstndb/copilot-show/pkg/analyze"
	"github.com/apstndb/copilot-show/pkg/modeldocs"
	copilot "github.com/github/copilot-sdk/go"
)

type usageFlatItem struct {
	Period        string
	SKU           string
	Model         string
	GrossQuantity float64
	NetQuantity   float64
	NetAmount     float64
}

type usageItemEnriched struct {
	Product          string                     `json:"product" yaml:"product"`
	SKU              string                     `json:"sku" yaml:"sku"`
	Model            string                     `json:"model" yaml:"model"`
	UnitType         string                     `json:"unitType" yaml:"unitType"`
	PricePerUnit     float64                    `json:"pricePerUnit" yaml:"pricePerUnit"`
	GrossQuantity    float64                    `json:"grossQuantity" yaml:"grossQuantity"`
	GrossAmount      float64                    `json:"grossAmount" yaml:"grossAmount"`
	DiscountQuantity float64                    `json:"discountQuantity" yaml:"discountQuantity"`
	DiscountAmount   float64                    `json:"discountAmount" yaml:"discountAmount"`
	NetQuantity      float64                    `json:"netQuantity" yaml:"netQuantity"`
	NetAmount        float64                    `json:"netAmount" yaml:"netAmount"`
	IOSummary        string                     `json:"ioSummary,omitempty" yaml:"ioSummary,omitempty"`
	TokenPricing     *modeldocs.SDKTokenPricing `json:"tokenPricing,omitempty" yaml:"tokenPricing,omitempty"`
}

type usageResponseEnriched struct {
	TimePeriod struct {
		Year  int  `json:"year" yaml:"year"`
		Month *int `json:"month" yaml:"month"`
		Day   *int `json:"day" yaml:"day"`
	} `json:"timePeriod" yaml:"timePeriod"`
	User       string              `json:"user" yaml:"user"`
	UsageItems []usageItemEnriched `json:"usageItems" yaml:"usageItems"`
}

func usageTableHeader(billingMode string, multiPeriod bool) ([]string, []int) {
	usedHeader, _ := usageQuantityColumnHeaders(billingMode)
	header := []string{"Period", "SKU", "Model", usedHeader, modelsAPIPricingColumnHeader}
	rightAligned := []int{len(header) - 2, len(header) - 1}
	if !multiPeriod {
		header = header[1:]
		rightAligned = []int{len(header) - 2, len(header) - 1}
	}
	return header, rightAligned
}

func usageModelLookupNames(modelName string) []string {
	candidates := []string{modelName}
	if trimmed := strings.TrimSpace(modelName); trimmed != modelName {
		candidates = append(candidates, trimmed)
	}
	for _, prefix := range []string{"Auto: ", "auto: "} {
		if after, ok := strings.CutPrefix(modelName, prefix); ok {
			candidates = append(candidates, strings.TrimSpace(after))
		}
	}
	return candidates
}

func usageModelPricingKeys(name string) []string {
	keys := make([]string, 0, 4)
	for _, candidate := range usageModelLookupNames(name) {
		for _, key := range []string{
			modeldocs.NormalizeModelNameKey(candidate),
			analyze.NormalizeModelKey(candidate),
		} {
			if key == "" {
				continue
			}
			duplicate := false
			for _, existing := range keys {
				if existing == key {
					duplicate = true
					break
				}
			}
			if !duplicate {
				keys = append(keys, key)
			}
		}
	}
	return keys
}

func buildModelPricingLookup(models []copilot.ModelInfo) map[string]*modeldocs.SDKTokenPricing {
	lookup := make(map[string]*modeldocs.SDKTokenPricing)
	for _, model := range models {
		pricing := modeldocs.SDKTokenPricingFromModel(model)
		if pricing == nil {
			continue
		}
		cloned := cloneSDKTokenPricing(pricing)
		for _, name := range []string{model.Name, model.ID} {
			for _, key := range usageModelPricingKeys(name) {
				lookup[key] = cloned
			}
		}
	}
	return lookup
}

func lookupUsageModelPricing(modelName string, lookup map[string]*modeldocs.SDKTokenPricing) (*modeldocs.SDKTokenPricing, bool) {
	if lookup == nil {
		return nil, false
	}
	for _, key := range usageModelPricingKeys(modelName) {
		if pricing, ok := lookup[key]; ok {
			return pricing, true
		}
	}
	return nil, false
}

func formatUsageItemIOSummary(modelName string, lookup map[string]*modeldocs.SDKTokenPricing) string {
	if pricing, ok := lookupUsageModelPricing(modelName, lookup); ok {
		return modeldocs.SDKTokenPricingIOSummary(pricing)
	}
	return "-"
}

func enrichUsageItem(item struct {
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
}, lookup map[string]*modeldocs.SDKTokenPricing) usageItemEnriched {
	enriched := usageItemEnriched{
		Product:          item.Product,
		SKU:              item.SKU,
		Model:            item.Model,
		UnitType:         item.UnitType,
		PricePerUnit:     item.PricePerUnit,
		GrossQuantity:    item.GrossQuantity,
		GrossAmount:      item.GrossAmount,
		DiscountQuantity: item.DiscountQuantity,
		DiscountAmount:   item.DiscountAmount,
		NetQuantity:      item.NetQuantity,
		NetAmount:        item.NetAmount,
	}
	if pricing, ok := lookupUsageModelPricing(item.Model, lookup); ok {
		enriched.IOSummary = modeldocs.SDKTokenPricingIOSummary(pricing)
		enriched.TokenPricing = cloneSDKTokenPricing(pricing)
	}
	return enriched
}

func enrichUsageResponsesForYAML(responses []*usageResponse, lookup map[string]*modeldocs.SDKTokenPricing) any {
	enriched := make([]usageResponseEnriched, 0, len(responses))
	for _, res := range responses {
		if res == nil {
			continue
		}
		var out usageResponseEnriched
		out.TimePeriod = res.TimePeriod
		out.User = res.User
		for _, item := range res.UsageItems {
			out.UsageItems = append(out.UsageItems, enrichUsageItem(item, lookup))
		}
		enriched = append(enriched, out)
	}
	if len(enriched) == 1 {
		return enriched[0]
	}
	return enriched
}

func formatUsageUsedValue(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func flattenUsageResponses(responses []*usageResponse) []usageFlatItem {
	var items []usageFlatItem
	for _, res := range responses {
		periodStr := strconv.Itoa(res.TimePeriod.Year)
		if res.TimePeriod.Month != nil {
			periodStr = fmt.Sprintf("%d-%02d", res.TimePeriod.Year, *res.TimePeriod.Month)
			if res.TimePeriod.Day != nil {
				periodStr = fmt.Sprintf("%d-%02d-%02d", res.TimePeriod.Year, *res.TimePeriod.Month, *res.TimePeriod.Day)
			}
		}
		for _, item := range res.UsageItems {
			items = append(items, usageFlatItem{
				Period:        periodStr,
				SKU:           item.SKU,
				Model:         item.Model,
				GrossQuantity: item.GrossQuantity,
				NetQuantity:   item.NetQuantity,
				NetAmount:     item.NetAmount,
			})
		}
	}
	return items
}

func fetchUsageModelPricingLookup(ctx context.Context, client *copilot.Client) (map[string]*modeldocs.SDKTokenPricing, error) {
	models, err := client.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	return buildModelPricingLookup(models), nil
}

func usageModelPricingLookupOrWarn(ctx context.Context, client *copilot.Client) map[string]*modeldocs.SDKTokenPricing {
	lookup, err := fetchUsageModelPricingLookup(ctx, client)
	if err != nil {
		log.Printf("Warning: could not fetch model pricing for usage table: %v", err)
		return nil
	}
	return lookup
}
