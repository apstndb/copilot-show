package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
	"github.com/goccy/go-yaml"
	"github.com/maruel/natural"
	"github.com/olekukonko/tablewriter"
	"github.com/olekukonko/tablewriter/renderer"
	"github.com/olekukonko/tablewriter/tw"
	"github.com/olekukonko/ts"
	"github.com/spf13/cobra"
)

const (
	version      = "0.1.5"
	uiVersionOld = "old"
	uiVersionNew = "new"

	apiPricingCatalogVersion = "public-token-pricing-2026-03"

	historyViewRaw   = "raw"
	historyViewSpans = "spans"

	historyGroupByNone = "none"
	historyGroupByTurn = "turn"

	historyEventLabelWidth = 20
)

var (
	outputFormat string
	tableMode    string
	uiVersion    string
)

func main() {
	// 1. Initialize Copilot CLI client
	client := copilot.NewClient(nil)
	ctx := context.Background()
	if err := client.Start(ctx); err != nil {
		log.Fatalf("Failed to start client: %v", err)
	}
	defer client.Stop()

	rootCmd := &cobra.Command{
		Use:     "copilot-show",
		Short:   "A tool to inspect GitHub Copilot information",
		Version: "0.1.5",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			switch uiVersion {
			case uiVersionOld, uiVersionNew:
				return nil
			default:
				return fmt.Errorf("invalid --ui-version %q: expected %q or %q", uiVersion, uiVersionOld, uiVersionNew)
			}
		},
		Run: func(cmd *cobra.Command, args []string) {
			_ = cmd.Help()
		},
	}

	rootCmd.PersistentFlags().StringVarP(&outputFormat, "format", "f", "table", "Output format (table, yaml)")
	rootCmd.PersistentFlags().StringVar(&tableMode, "table-mode", "default", "Table mode (default, ascii, markdown)")
	rootCmd.PersistentFlags().StringVar(&uiVersion, "ui-version", uiVersionNew, "Hidden UI selector for temporary A/B testing (old, new)")
	if err := rootCmd.PersistentFlags().MarkHidden("ui-version"); err != nil {
		log.Fatalf("Failed to hide ui-version flag: %v", err)
	}

	rootCmd.AddCommand(newQuotaCmd(client))
	rootCmd.AddCommand(newModelsCmd(client))
	rootCmd.AddCommand(newToolsCmd(client))
	rootCmd.AddCommand(newStatsCmd())
	rootCmd.AddCommand(newUsageCmd(client))
	rootCmd.AddCommand(newTurnsCmd(client))

	hiddenCmds := []*cobra.Command{
		newAgentsCmd(client),
		newCurrentModelCmd(client),
		newCurrentAgentCmd(client),
		newModeCmd(client),
		newPlanCmd(client),
		newWorkspaceCmd(client),
		newReadFileCmd(client),
		newPingCmd(client),
		newStatusCmd(client),
		newSessionsCmd(client),
		newHistoryCmd(client),
		newGraphCmd(client),
		newTurnsCmd(client),
	}
	for _, c := range hiddenCmds {
		c.Hidden = true
		rootCmd.AddCommand(c)
	}

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func printYAML(v interface{}) {
	data, err := yaml.MarshalWithOptions(v, yaml.UseJSONMarshaler())
	if err != nil {
		log.Printf("Error marshaling YAML: %v", err)
		return
	}
	fmt.Print(string(data))
}

func createTable(header []string, rightAlignedCols []int, hierarchicalMerge bool, rowLine bool) *tablewriter.Table {
	var opts []tablewriter.Option

	if tableMode == "markdown" {
		opts = append(opts, tablewriter.WithRenderer(renderer.NewMarkdown()))
	} else if tableMode == "ascii" {
		opts = append(opts, tablewriter.WithRenderer(renderer.NewBlueprint(tw.Rendition{
			Symbols: tw.NewSymbols(tw.StyleASCII),
		})))
	}

	if rowLine {
		opts = append(opts, tablewriter.WithRendition(tw.Rendition{
			Settings: tw.Settings{
				Separators: tw.Separators{
					BetweenRows: tw.On,
				},
			},
		}))
	}

	table := tablewriter.NewTable(os.Stdout, opts...)

	table.Configure(func(cfg *tablewriter.Config) {
		cfg.Row.Formatting.AutoWrap = tw.WrapNormal
		cfg.Row.Formatting.AutoFormat = tw.Off
		cfg.Header.Formatting.AutoFormat = tw.Off
		cfg.Header.Alignment.Global = tw.AlignLeft

		if hierarchicalMerge {
			cfg.Row.Merging.Mode = tw.MergeHierarchical
		}

		if len(rightAlignedCols) > 0 {
			cfg.Row.Alignment.PerColumn = make([]tw.Align, len(header))
			for i := range cfg.Row.Alignment.PerColumn {
				cfg.Row.Alignment.PerColumn[i] = tw.AlignLeft
			}
			for _, col := range rightAlignedCols {
				if col >= 0 && col < len(header) {
					cfg.Row.Alignment.PerColumn[col] = tw.AlignRight
				}
			}
		}
	})

	anyHeader := make([]interface{}, len(header))
	for i, v := range header {
		anyHeader[i] = v
	}
	table.Header(anyHeader...)
	return table
}

type statsModelStat struct {
	Requests                int64            `json:"requests" yaml:"requests"`
	Cost                    float64          `json:"cost" yaml:"cost"`
	Input                   int64            `json:"inputTokens" yaml:"inputTokens"`
	CacheRead               int64            `json:"cacheReadTokens,omitempty" yaml:"cacheReadTokens,omitempty"`
	CacheWrite              int64            `json:"cacheWriteTokens,omitempty" yaml:"cacheWriteTokens,omitempty"`
	Output                  int64            `json:"outputTokens" yaml:"outputTokens"`
	EstimatedOverageCostUSD float64          `json:"estimatedOverageCostUsd,omitempty" yaml:"estimatedOverageCostUsd,omitempty"`
	EstimatedAPICost        *apiCostEstimate `json:"estimatedApiCost,omitempty" yaml:"estimatedApiCost,omitempty"`
}

type apiPriceCatalogEntry struct {
	ModelID              string   `json:"modelId" yaml:"modelId"`
	InputUSDPerMTok      float64  `json:"inputUsdPerMToken" yaml:"inputUsdPerMToken"`
	CacheReadUSDPerMTok  *float64 `json:"cacheReadUsdPerMToken,omitempty" yaml:"cacheReadUsdPerMToken,omitempty"`
	CacheWriteUSDPerMTok *float64 `json:"cacheWriteUsdPerMToken,omitempty" yaml:"cacheWriteUsdPerMToken,omitempty"`
	OutputUSDPerMTok     float64  `json:"outputUsdPerMToken" yaml:"outputUsdPerMToken"`
	Source               string   `json:"source" yaml:"source"`
}

type apiCostEstimate struct {
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
var apiPricingCatalog = map[string]apiPriceCatalogEntry{
	normalizeModelKey("claude-haiku-4.5"): {
		ModelID:             "claude-haiku-4.5",
		InputUSDPerMTok:     1.00,
		CacheReadUSDPerMTok: float64Ptr(0.10),
		OutputUSDPerMTok:    5.00,
		Source:              "https://platform.claude.com/docs/en/about-claude/pricing",
	},
	normalizeModelKey("claude-sonnet-4"): {
		ModelID:             "claude-sonnet-4",
		InputUSDPerMTok:     3.00,
		CacheReadUSDPerMTok: float64Ptr(0.30),
		OutputUSDPerMTok:    15.00,
		Source:              "https://platform.claude.com/docs/en/about-claude/pricing",
	},
	normalizeModelKey("claude-sonnet-4.5"): {
		ModelID:             "claude-sonnet-4.5",
		InputUSDPerMTok:     3.00,
		CacheReadUSDPerMTok: float64Ptr(0.30),
		OutputUSDPerMTok:    15.00,
		Source:              "https://platform.claude.com/docs/en/about-claude/pricing",
	},
	normalizeModelKey("claude-sonnet-4.6"): {
		ModelID:             "claude-sonnet-4.6",
		InputUSDPerMTok:     3.00,
		CacheReadUSDPerMTok: float64Ptr(0.30),
		OutputUSDPerMTok:    15.00,
		Source:              "https://platform.claude.com/docs/en/about-claude/pricing",
	},
	normalizeModelKey("claude-opus-4.5"): {
		ModelID:             "claude-opus-4.5",
		InputUSDPerMTok:     5.00,
		CacheReadUSDPerMTok: float64Ptr(0.50),
		OutputUSDPerMTok:    25.00,
		Source:              "https://platform.claude.com/docs/en/about-claude/pricing",
	},
	normalizeModelKey("claude-opus-4.6"): {
		ModelID:             "claude-opus-4.6",
		InputUSDPerMTok:     5.00,
		CacheReadUSDPerMTok: float64Ptr(0.50),
		OutputUSDPerMTok:    25.00,
		Source:              "https://platform.claude.com/docs/en/about-claude/pricing",
	},
	normalizeModelKey("gpt-5.4"): {
		ModelID:             "gpt-5.4",
		InputUSDPerMTok:     2.50,
		CacheReadUSDPerMTok: float64Ptr(0.25),
		OutputUSDPerMTok:    15.00,
		Source:              "https://developers.openai.com/api/docs/pricing",
	},
	normalizeModelKey("gpt-5.4-mini"): {
		ModelID:             "gpt-5.4-mini",
		InputUSDPerMTok:     0.75,
		CacheReadUSDPerMTok: float64Ptr(0.075),
		OutputUSDPerMTok:    4.50,
		Source:              "https://developers.openai.com/api/docs/pricing",
	},
	normalizeModelKey("gpt-5.3-codex"): {
		ModelID:             "gpt-5.3-codex",
		InputUSDPerMTok:     1.75,
		CacheReadUSDPerMTok: float64Ptr(0.175),
		OutputUSDPerMTok:    14.00,
		Source:              "https://developers.openai.com/api/docs/pricing",
	},
	normalizeModelKey("gpt-5.2-codex"): {
		ModelID:             "gpt-5.2-codex",
		InputUSDPerMTok:     1.75,
		CacheReadUSDPerMTok: float64Ptr(0.175),
		OutputUSDPerMTok:    14.00,
		Source:              "https://developers.openai.com/api/docs/pricing",
	},
	normalizeModelKey("gpt-5.1-codex-max"): {
		ModelID:             "gpt-5.1-codex-max",
		InputUSDPerMTok:     1.25,
		CacheReadUSDPerMTok: float64Ptr(0.125),
		OutputUSDPerMTok:    10.00,
		Source:              "https://developers.openai.com/api/docs/pricing",
	},
	normalizeModelKey("gpt-5.1-codex"): {
		ModelID:             "gpt-5.1-codex",
		InputUSDPerMTok:     1.25,
		CacheReadUSDPerMTok: float64Ptr(0.125),
		OutputUSDPerMTok:    10.00,
		Source:              "https://developers.openai.com/api/docs/pricing",
	},
	normalizeModelKey("gpt-5.1-codex-mini"): {
		ModelID:             "gpt-5.1-codex-mini",
		InputUSDPerMTok:     0.25,
		CacheReadUSDPerMTok: float64Ptr(0.025),
		OutputUSDPerMTok:    2.00,
		Source:              "https://developers.openai.com/api/docs/pricing",
	},
	normalizeModelKey("gpt-4.1"): {
		ModelID:             "gpt-4.1",
		InputUSDPerMTok:     3.00,
		CacheReadUSDPerMTok: float64Ptr(0.75),
		OutputUSDPerMTok:    12.00,
		Source:              "https://developers.openai.com/api/docs/pricing",
	},
}

func float64Ptr(v float64) *float64 {
	return &v
}

func normalizeModelKey(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(strings.ToLower(s), " ", ""), "-", "")
	return strings.TrimSuffix(s, "preview")
}

func formatFloatCompact(v float64) string {
	if math.Abs(v-math.Round(v)) < 1e-9 {
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func formatUSD(v float64) string {
	return fmt.Sprintf("$%.2f", v)
}

func estimateAPICost(model string, stat *statsModelStat) *apiCostEstimate {
	if stat.Input == 0 && stat.CacheRead == 0 && stat.CacheWrite == 0 && stat.Output == 0 {
		return nil
	}
	price, ok := apiPricingCatalog[normalizeModelKey(model)]
	if !ok {
		return nil
	}
	estimate := &apiCostEstimate{
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

func withSession(ctx context.Context, client *copilot.Client, fn func(session *copilot.Session) error) error {
	cwd, _ := os.Getwd()
	session, err := client.CreateSession(ctx, &copilot.SessionConfig{
		OnPermissionRequest: copilot.PermissionHandler.ApproveAll,
		WorkingDirectory:    cwd,
	})
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Destroy()
	return fn(session)
}

func newQuotaCmd(client *copilot.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "quota",
		Short: "Show Premium Interactions quota",
		Run: func(cmd *cobra.Command, args []string) {
			showQuota(cmd.Context(), client, outputFormat)
		},
	}
}

func showQuota(ctx context.Context, client *copilot.Client, format string) {
	quota, err := client.RPC.Account.GetQuota(ctx)
	if err != nil {
		log.Printf("Error fetching quota: %v", err)
		return
	}

	if format == "yaml" {
		printYAML(quota)
		return
	}

	header := []string{"Metric", "Included", "Used", "Overage", "Usage %"}
	table := createTable(header, []int{1, 2, 3, 4}, false, false)

	// Sort snapshots by name for consistent output
	var keys []string
	for k := range quota.QuotaSnapshots {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	lastUpdatedSet := make(map[string]struct{})
	for _, k := range keys {
		snap := quota.QuotaSnapshots[k]
		usagePct := "-"
		if snap.EntitlementRequests > 0 {
			usagePct = fmt.Sprintf("%.1f%%", (snap.UsedRequests/snap.EntitlementRequests)*100)
		}
		if snap.ResetDate != nil {
			t, err := time.Parse(time.RFC3339, *snap.ResetDate)
			if err == nil {
				lastUpdatedSet[t.Local().Format(time.RFC3339)] = struct{}{}
			} else {
				lastUpdatedSet[*snap.ResetDate] = struct{}{}
			}
		}
		overageVal := ""
		if snap.OverageAllowedWithExhaustedQuota {
			if snap.Overage > 0 {
				overageVal = strconv.FormatFloat(snap.Overage, 'f', -1, 64)
			} else {
				overageVal = "Allowed"
			}
		} else {
			if snap.Overage > 0 {
				overageVal = fmt.Sprintf("%.0f Disallowed", snap.Overage)
			} else {
				overageVal = "Disallowed"
			}
		}

		table.Append([]string{
			k,
			fmt.Sprintf("%.0f", snap.EntitlementRequests),
			fmt.Sprintf("%.0f", snap.UsedRequests),
			overageVal,
			usagePct,
		})
	}

	if len(keys) == 0 {
		fmt.Println("No quota information found.")
		return
	}

	fmt.Println("--- Quota Information ---")
	table.Render()

	// Show Last Updated information outside the table
	if len(lastUpdatedSet) > 0 {
		var dates []string
		for d := range lastUpdatedSet {
			dates = append(dates, d)
		}
		sort.Strings(dates)
		if len(dates) == 1 {
			fmt.Printf("Last Updated: %s\n", dates[0])
		} else {
			fmt.Printf("Last Updated: %v\n", dates)
		}
	}

	// Add educational notes based on documentation
	fmt.Println("\nPlan Reference (Included Monthly Premium Requests):")
	fmt.Println("- Copilot Free: 50")
	fmt.Println("- Copilot Pro / Business: 300")
	fmt.Println("- Copilot Enterprise: 1,000")
	fmt.Println("- Copilot Pro+: 1,500")

	// Month progress calculation (UTC based, as per GitHub billing docs)
	now := time.Now().UTC()
	year, month, _ := now.Date()
	startOfMonth := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	startOfNextMonth := startOfMonth.AddDate(0, 1, 0)

	totalSecondsInMonth := startOfNextMonth.Sub(startOfMonth).Seconds()
	secondsPassed := now.Sub(startOfMonth).Seconds()
	monthProgress := math.Min(100, math.Max(0, (secondsPassed/totalSecondsInMonth)*100))

	fmt.Printf("\nMonth Progress (UTC): %.1f%%\n", monthProgress)

	// Overage cost estimation based on quota snapshots
	var totalOverage float64
	overagePossible := false
	for _, snap := range quota.QuotaSnapshots {
		if snap.Overage > 0 {
			totalOverage += snap.Overage
		}
		if snap.OverageAllowedWithExhaustedQuota {
			overagePossible = true
		}
	}

	if totalOverage > 0 {
		fmt.Printf("Estimated Overage Cost (at $0.04/req): $%.2f USD\n", totalOverage*0.04)
	} else if overagePossible {
		fmt.Println("Overage is allowed. Future overage cost: $0.04 USD per premium request.")
	}

	fmt.Println("\nNotes:")
	fmt.Println("- Quotas reset on the 1st of each month at 00:00 UTC.")
	fmt.Println("- 'Overage' shows the extra usage after exhausting your included requests.")
	fmt.Println("- Each interaction's cost depends on the model's multiplier.")
	fmt.Println("- Standard models (e.g., GPT-4o, Claude 4.5 Sonnet) are often 'Included' at 0 cost.")
	fmt.Println("- Premium models (e.g., Claude 4.6 Opus, o1) have a multiplier (e.g., 3x).")
}

func newModelsCmd(client *copilot.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "models",
		Short: "List available AI models with details",
		Run: func(cmd *cobra.Command, args []string) {
			showModels(cmd.Context(), client, outputFormat)
		},
	}
}

func showModels(ctx context.Context, client *copilot.Client, format string) {
	models, err := client.RPC.Models.List(ctx)
	if err != nil {
		log.Printf("Error listing models: %v", err)
		return
	}

	if format == "yaml" {
		printYAML(models)
		return
	}

	header := []string{"ID", "Name", "Multiplier", "Context", "Output", "Prompt", "Vision", "Reasoning", "Efforts", "State"}
	table := createTable(header, []int{2, 3, 4, 5}, false, false)

	for _, m := range models.Models {
		multiplier := "-"
		multiplierNum := 0.0
		if m.Billing != nil {
			multiplierNum = m.Billing.Multiplier
			multiplier = strconv.FormatFloat(multiplierNum, 'f', -1, 64)
		}

		ctxTokens := fmt.Sprintf("%.0f", m.Capabilities.Limits.MaxContextWindowTokens)

		outTokens := "-"
		if m.Capabilities.Limits.MaxOutputTokens != nil {
			outTokens = fmt.Sprintf("%.0f", *m.Capabilities.Limits.MaxOutputTokens)
		}

		pmtTokens := "-"
		if m.Capabilities.Limits.MaxPromptTokens != nil {
			pmtTokens = fmt.Sprintf("%.0f", *m.Capabilities.Limits.MaxPromptTokens)
		}

		vision := "No"
		if m.Capabilities.Supports.Vision != nil && *m.Capabilities.Supports.Vision {
			vision = "Yes"
		}

		reasoning := "No"
		if m.Capabilities.Supports.ReasoningEffort != nil && *m.Capabilities.Supports.ReasoningEffort {
			reasoning = "Yes"
			if m.DefaultReasoningEffort != nil {
				reasoning += fmt.Sprintf(" (%s)", *m.DefaultReasoningEffort)
			}
		}

		efforts := "-"
		if len(m.SupportedReasoningEfforts) > 0 {
			efforts = ""
			for i, e := range m.SupportedReasoningEfforts {
				if i > 0 {
					efforts += ", "
				}
				efforts += e
			}
		}

		policyState := "-"
		if m.Policy != nil {
			policyState = m.Policy.State
		}

		row := []string{
			m.ID,
			m.Name,
			multiplier,
			ctxTokens,
			outTokens,
			pmtTokens,
			vision,
			reasoning,
			efforts,
			policyState,
		}

		if multiplierNum == 0 && (policyState == "enabled" || policyState == "default") {
			// Included models (Free/No premium request cost)
			row[2] = "Included (0)"
		}

		table.Append(row)
	}
	table.Render()
	fmt.Println("\nNote: 'Included' models (e.g., GPT-4o, Claude 4.5 Sonnet) consume ZERO premium requests on paid plans.")
	fmt.Println("Note: Premium models (e.g., Claude 4.6 Opus, o1) consume premium requests based on their multiplier.")
}

func newToolsCmd(client *copilot.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "tools",
		Short: "List available built-in tools",
		Run: func(cmd *cobra.Command, args []string) {
			showTools(cmd.Context(), client, outputFormat)
		},
	}
}

func showTools(ctx context.Context, client *copilot.Client, format string) {
	tools, err := client.RPC.Tools.List(ctx, nil)
	if err != nil {
		log.Printf("Error listing tools: %v", err)
		return
	}

	if format == "yaml" {
		printYAML(tools)
		return
	}

	header := []string{"Name", "Description", "Namespaced Name"}
	table := createTable(header, nil, false, false)

	for _, t := range tools.Tools {
		nsName := "-"
		if t.NamespacedName != nil {
			nsName = *t.NamespacedName
		}
		table.Append([]string{t.Name, t.Description, nsName})
	}
	table.Render()
}

func newAgentsCmd(client *copilot.Client) *cobra.Command {
	return &cobra.Command{
		Use:    "agents",
		Short:  "List available custom agents",
		Hidden: true,
		Run: func(cmd *cobra.Command, args []string) {
			showAgents(cmd.Context(), client, outputFormat)
		},
	}
}

func showAgents(ctx context.Context, client *copilot.Client, format string) {
	err := withSession(ctx, client, func(session *copilot.Session) error {
		res, err := session.RPC.Agent.List(ctx)
		if err != nil {
			return err
		}

		if format == "yaml" {
			printYAML(res)
			return nil
		}

		header := []string{"Name", "Display Name", "Description"}
		table := createTable(header, nil, false, false)

		for _, a := range res.Agents {
			table.Append([]string{a.Name, a.DisplayName, a.Description})
		}
		table.Render()
		return nil
	})
	if err != nil {
		log.Printf("Error in agents command: %v", err)
	}
}

func newModeCmd(client *copilot.Client) *cobra.Command {
	return &cobra.Command{
		Use:    "mode",
		Short:  "Show the current agent mode",
		Hidden: true,
		Run: func(cmd *cobra.Command, args []string) {
			showMode(cmd.Context(), client, outputFormat)
		},
	}
}

func showMode(ctx context.Context, client *copilot.Client, format string) {
	err := withSession(ctx, client, func(session *copilot.Session) error {
		res, err := session.RPC.Mode.Get(ctx)
		if err != nil {
			return err
		}

		if format == "yaml" {
			printYAML(res)
			return nil
		}

		fmt.Printf("Current Mode: %s\n", res.Mode)
		return nil
	})
	if err != nil {
		log.Printf("Error in mode command: %v", err)
	}
}

func newPlanCmd(client *copilot.Client) *cobra.Command {
	return &cobra.Command{
		Use:    "plan",
		Short:  "Read the current plan file",
		Hidden: true,
		Run: func(cmd *cobra.Command, args []string) {
			showPlan(cmd.Context(), client, outputFormat)
		},
	}
}

func showPlan(ctx context.Context, client *copilot.Client, format string) {
	err := withSession(ctx, client, func(session *copilot.Session) error {
		res, err := session.RPC.Plan.Read(ctx)
		if err != nil {
			return err
		}

		if format == "yaml" {
			printYAML(res)
			return nil
		}

		fmt.Printf("Exists: %v\n", res.Exists)
		if res.Path != nil {
			fmt.Printf("Path: %s\n", *res.Path)
		}
		if res.Content != nil {
			fmt.Println("Content:")
			fmt.Println(*res.Content)
		}
		return nil
	})
	if err != nil {
		log.Printf("Error in plan command: %v", err)
	}
}

func newWorkspaceCmd(client *copilot.Client) *cobra.Command {
	var showAll bool
	cmd := &cobra.Command{
		Use:    "workspace",
		Short:  "List files in the workspace",
		Hidden: true,
		Run: func(cmd *cobra.Command, args []string) {
			showWorkspace(cmd.Context(), client, outputFormat, showAll)
		},
	}
	cmd.Flags().BoolVarP(&showAll, "all", "a", false, "Show full content of files")
	return cmd
}

func showWorkspace(ctx context.Context, client *copilot.Client, format string, showAll bool) {
	err := withSession(ctx, client, func(session *copilot.Session) error {
		files, err := session.RPC.Workspace.ListFiles(ctx)
		if err != nil {
			return err
		}

		type fileInfo struct {
			Path    string  `yaml:"path"`
			Content *string `yaml:"content,omitempty"`
		}

		var result []fileInfo
		for _, f := range files.Files {
			var content *string
			if showAll {
				c, err := session.RPC.Workspace.ReadFile(ctx, &rpc.SessionWorkspaceReadFileParams{Path: f})
				if err == nil {
					content = &c.Content
				}
			}
			result = append(result, fileInfo{Path: f, Content: content})
		}

		if format == "yaml" {
			printYAML(result)
			return nil
		}

		if len(result) == 0 {
			fmt.Println("No files found in workspace.")
			return nil
		}

		table := tablewriter.NewWriter(os.Stdout)
		if showAll {
			header := []string{"File Path", "Content (Truncated)"}
			table := createTable(header, nil, false, false)
			for _, f := range result {
				c := "-"
				if f.Content != nil {
					c = *f.Content
					if len(c) > 50 {
						c = c[:50] + "..."
					}
				}
				table.Append([]string{f.Path, c})
			}
		} else {
			header := []string{"File Path"}
			table := createTable(header, nil, false, false)
			for _, f := range result {
				table.Append([]string{f.Path})
			}
		}
		table.Render()
		return nil
	})
	if err != nil {
		log.Printf("Error in workspace command: %v", err)
	}
}

func newReadFileCmd(client *copilot.Client) *cobra.Command {
	return &cobra.Command{
		Use:    "read-file <path>",
		Short:  "Read a specific file from the workspace",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			showReadFile(cmd.Context(), client, args[0], outputFormat)
		},
	}
}

func showReadFile(ctx context.Context, client *copilot.Client, path string, format string) {
	err := withSession(ctx, client, func(session *copilot.Session) error {
		res, err := session.RPC.Workspace.ReadFile(ctx, &rpc.SessionWorkspaceReadFileParams{Path: path})
		if err != nil {
			return err
		}

		if format == "yaml" {
			printYAML(res)
			return nil
		}

		fmt.Printf("--- %s ---\n%s\n", path, res.Content)
		return nil
	})
	if err != nil {
		log.Printf("Error in read-file command: %v", err)
	}
}

func newPingCmd(client *copilot.Client) *cobra.Command {
	return &cobra.Command{
		Use:    "ping",
		Short:  "Check connection to the server",
		Hidden: true,
		Run: func(cmd *cobra.Command, args []string) {
			showPing(cmd.Context(), client, outputFormat)
		},
	}
}

func showPing(ctx context.Context, client *copilot.Client, format string) {
	res, err := client.RPC.Ping(ctx, nil)
	if err != nil {
		log.Printf("Error pinging: %v", err)
		return
	}

	if format == "yaml" {
		printYAML(res)
		return
	}

	fmt.Printf("Message: %s\n", res.Message)
	fmt.Printf("Protocol Version: %.1f\n", res.ProtocolVersion)
	fmt.Printf("Timestamp: %.0f\n", res.Timestamp)
}

func newCurrentModelCmd(client *copilot.Client) *cobra.Command {
	return &cobra.Command{
		Use:    "current-model",
		Short:  "Show the currently selected model ID",
		Hidden: true,
		Run: func(cmd *cobra.Command, args []string) {
			showCurrentModel(cmd.Context(), client, outputFormat)
		},
	}
}

func showCurrentModel(ctx context.Context, client *copilot.Client, format string) {
	err := withSession(ctx, client, func(session *copilot.Session) error {
		res, err := session.RPC.Model.GetCurrent(ctx)
		if err != nil {
			return err
		}

		if format == "yaml" {
			printYAML(res)
			return nil
		}

		id := "not set"
		if res.ModelID != nil {
			id = *res.ModelID
		}
		fmt.Printf("Current Model ID: %s\n", id)
		return nil
	})
	if err != nil {
		log.Printf("Error in current-model command: %v", err)
	}
}

func newCurrentAgentCmd(client *copilot.Client) *cobra.Command {
	return &cobra.Command{
		Use:    "current-agent",
		Short:  "Show the currently selected agent",
		Hidden: true,
		Run: func(cmd *cobra.Command, args []string) {
			showCurrentAgent(cmd.Context(), client, outputFormat)
		},
	}
}

func showCurrentAgent(ctx context.Context, client *copilot.Client, format string) {
	err := withSession(ctx, client, func(session *copilot.Session) error {
		res, err := session.RPC.Agent.GetCurrent(ctx)
		if err != nil {
			return err
		}

		if format == "yaml" {
			printYAML(res)
			return nil
		}

		if res.Agent == nil {
			fmt.Println("Current Agent: default")
		} else {
			fmt.Printf("Current Agent: %s (%s)\n", res.Agent.DisplayName, res.Agent.Name)
			fmt.Printf("Description: %s\n", res.Agent.Description)
		}
		return nil
	})
	if err != nil {
		log.Printf("Error in current-agent command: %v", err)
	}
}

func newStatusCmd(client *copilot.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show CLI status and authentication status",
		Run: func(cmd *cobra.Command, args []string) {
			showStatus(cmd.Context(), client, outputFormat)
		},
	}
}

func showStatus(ctx context.Context, client *copilot.Client, format string) {
	status, err := client.GetStatus(ctx)
	if err != nil {
		log.Printf("Error fetching status: %v", err)
		return
	}

	auth, err := client.GetAuthStatus(ctx)
	if err != nil {
		log.Printf("Error fetching auth status: %v", err)
		return
	}

	combined := struct {
		Status *copilot.GetStatusResponse     `json:"status" yaml:"status"`
		Auth   *copilot.GetAuthStatusResponse `json:"auth" yaml:"auth"`
	}{
		Status: status,
		Auth:   auth,
	}

	if format == "yaml" {
		printYAML(combined)
		return
	}

	fmt.Println("--- CLI Status ---")
	table := createTable([]string{"Property", "Value"}, nil, false, false)
	table.Append([]string{"Version", status.Version})
	table.Append([]string{"Protocol Version", fmt.Sprintf("%d", status.ProtocolVersion)})
	table.Render()

	fmt.Println("\n--- Auth Status ---")
	tableAuth := createTable([]string{"Property", "Value"}, nil, false, false)
	tableAuth.Append([]string{"Authenticated", fmt.Sprintf("%v", auth.IsAuthenticated)})
	if auth.Login != nil {
		tableAuth.Append([]string{"Login", *auth.Login})
	}
	if auth.Host != nil {
		tableAuth.Append([]string{"Host", *auth.Host})
	}
	tableAuth.Render()
}

func newSessionsCmd(client *copilot.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "sessions",
		Short: "List all Copilot sessions",
		Run: func(cmd *cobra.Command, args []string) {
			showSessions(cmd.Context(), client, outputFormat)
		},
	}
}

func showSessions(ctx context.Context, client *copilot.Client, format string) {
	sessions, err := client.ListSessions(ctx, nil)
	if err != nil {
		log.Printf("Error listing sessions: %v", err)
		return
	}

	lastID, _ := client.GetLastSessionID(ctx)
	fgID, _ := client.GetForegroundSessionID(ctx)

	// Scan local session-state directory for additional info (e.g., PID locks)
	localStates := make(map[string][]string) // SessionID -> list of PIDs
	home, _ := os.UserHomeDir()
	stateDir := filepath.Join(home, ".copilot", "session-state")
	entries, _ := os.ReadDir(stateDir)
	for _, entry := range entries {
		if entry.IsDir() {
			sessionID := entry.Name()
			subEntries, _ := os.ReadDir(filepath.Join(stateDir, sessionID))
			for _, sub := range subEntries {
				if strings.HasPrefix(sub.Name(), "inuse.") && strings.HasSuffix(sub.Name(), ".lock") {
					pid := strings.TrimSuffix(strings.TrimPrefix(sub.Name(), "inuse."), ".lock")
					localStates[sessionID] = append(localStates[sessionID], pid)
				}
			}
		}
	}

	if format == "yaml" {
		combined := struct {
			Sessions          []copilot.SessionMetadata `json:"sessions" yaml:"sessions"`
			LastSessionID     *string                   `json:"lastSessionId" yaml:"lastSessionId"`
			ForegroundSession *string                   `json:"foregroundSessionId" yaml:"foregroundSessionId"`
			LocalPIDs         map[string][]string       `json:"localPids" yaml:"localPids"`
		}{
			Sessions:          sessions,
			LastSessionID:     lastID,
			ForegroundSession: fgID,
			LocalPIDs:         localStates,
		}
		printYAML(combined)
		return
	}

	header := []string{"ID", "CWD", "Start Time", "Modified Time", "Status", "PIDs"}
	table := createTable(header, nil, false, false)

	for _, s := range sessions {
		cwd := "-"
		if s.Context != nil {
			cwd = s.Context.Cwd
		}
		status := ""
		if lastID != nil && s.SessionID == *lastID {
			status += "[Last]"
		}
		if fgID != nil && s.SessionID == *fgID {
			if status != "" {
				status += " "
			}
			status += "[Foreground]"
		}

		pids := "-"
		if ps, ok := localStates[s.SessionID]; ok {
			pids = strings.Join(ps, ", ")
			// Check if any PID is actually alive
			alive := false
			for _, pidStr := range ps {
				pid, _ := strconv.Atoi(pidStr)
				if pid > 0 {
					// On Unix, signal 0 checks for process existence
					process, err := os.FindProcess(pid)
					if err == nil {
						// On Unix, Signal(0) checks if process is alive
						if err := process.Signal(os.Signal(nil)); err == nil {
							alive = true
							break
						}
					}
				}
			}
			if alive {
				if status != "" {
					status += " "
				}
				status += "[Running]"
			}
		}

		table.Append([]string{
			s.SessionID,
			cwd,
			s.StartTime,
			s.ModifiedTime,
			status,
			pids,
		})
	}
	table.Render()
}

func resolveSessionID(ctx context.Context, client *copilot.Client, args []string) (string, error) {
	if len(args) > 0 {
		return args[0], nil
	}
	if fg, _ := client.GetForegroundSessionID(ctx); fg != nil {
		return *fg, nil
	}
	if last, _ := client.GetLastSessionID(ctx); last != nil {
		return *last, nil
	}
	return "", fmt.Errorf("no session ID provided and no foreground/last session found")
}

func newHistoryCmd(client *copilot.Client) *cobra.Command {
	var historyView string
	var historyGroupBy string
	cmd := &cobra.Command{
		Use:   "history [sessionID]",
		Short: "Show conversation history for a session",
		Args:  cobra.MaximumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			switch historyView {
			case historyViewRaw, historyViewSpans:
			default:
				log.Printf("invalid --view %q: expected %q or %q", historyView, historyViewRaw, historyViewSpans)
				return
			}
			switch historyGroupBy {
			case historyGroupByNone, historyGroupByTurn:
			default:
				log.Printf("invalid --group-by %q: expected %q or %q", historyGroupBy, historyGroupByNone, historyGroupByTurn)
				return
			}
			sessionID, err := resolveSessionID(cmd.Context(), client, args)
			if err != nil {
				log.Printf("%v", err)
				return
			}
			showHistory(cmd.Context(), client, sessionID, outputFormat, historyView, historyGroupBy)
		},
	}
	cmd.Flags().StringVar(&historyView, "view", historyViewRaw, "History projection (raw, spans)")
	cmd.Flags().StringVar(&historyGroupBy, "group-by", historyGroupByNone, "History grouping (none, turn)")
	return cmd
}

func showHistory(ctx context.Context, client *copilot.Client, sessionID string, format string, historyView string, historyGroupBy string) {
	_ = ctx
	_ = client
	if historyView == historyViewSpans {
		showHistorySpans(sessionID, format, historyGroupBy)
		return
	}
	if uiVersion == uiVersionOld {
		showHistoryOld(sessionID, format)
		return
	}
	showHistoryNew(sessionID, format)
}

func newGraphCmd(client *copilot.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "graph [sessionID]",
		Short: "Show graph-oriented event summary for a session",
		Args:  cobra.MaximumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			sessionID, err := resolveSessionID(cmd.Context(), client, args)
			if err != nil {
				log.Printf("%v", err)
				return
			}
			showGraph(cmd.Context(), client, sessionID, outputFormat)
		},
	}
}

func newUsageCmd(client *copilot.Client) *cobra.Command {
	var year, month, day, last int
	var product, model, sortOrder string
	cmd := &cobra.Command{
		Use:   "usage",
		Short: "Show detailed billing usage from GitHub API",
		Run: func(cmd *cobra.Command, args []string) {
			now := time.Now().UTC()
			// Flag parsing logic for drill down
			// If finer grain is specified but coarser is not, use current values
			if cmd.Flags().Changed("day") {
				if !cmd.Flags().Changed("month") {
					month = int(now.Month())
				}
				if !cmd.Flags().Changed("year") {
					year = now.Year()
				}
			} else if cmd.Flags().Changed("month") {
				if !cmd.Flags().Changed("year") {
					year = now.Year()
				}
			} else if !cmd.Flags().Changed("year") {
				// No flags specified: default to current month
				year = now.Year()
				month = int(now.Month())
			}

			// Handle relative dates
			if day < 0 {
				targetDate := now.AddDate(0, 0, day)
				year = targetDate.Year()
				month = int(targetDate.Month())
				day = targetDate.Day()
			} else if month < 0 {
				targetDate := now.AddDate(0, month, 0)
				year = targetDate.Year()
				month = int(targetDate.Month())
				// If month is relative, usually day should be 0 (monthly report)
				// but let's see if we want to keep current day or not.
				// Based on previous drill-down logic:
				if !cmd.Flags().Changed("day") {
					day = 0
				}
			} else if year < 0 {
				targetDate := now.AddDate(year, 0, 0)
				year = targetDate.Year()
				// Usually annual report if only year is specified
				if !cmd.Flags().Changed("month") {
					month = 0
				}
				if !cmd.Flags().Changed("day") {
					day = 0
				}
			}

			showUsage(cmd.Context(), client, outputFormat, year, month, day, product, model, last, sortOrder)
		},
	}
	cmd.Flags().IntVarP(&year, "year", "y", 0, "Year for usage report (positive for absolute, negative for relative)")
	cmd.Flags().IntVarP(&month, "month", "m", 0, "Month for usage report (1-12, or negative for relative)")
	cmd.Flags().IntVarP(&day, "day", "d", 0, "Day for usage report (1-31, or negative for relative)")
	cmd.Flags().IntVarP(&last, "last", "L", 0, "Show reports for the last N periods (days, months, or years)")
	cmd.Flags().StringVarP(&product, "product", "p", "", "Product to filter (e.g., copilot, spark)")
	cmd.Flags().MarkHidden("product")
	cmd.Flags().StringVarP(&model, "model", "M", "", "Model to filter (e.g., gpt-5, claude-opus-4.6)")
	cmd.Flags().MarkHidden("model")
	cmd.Flags().StringVar(&sortOrder, "sort-order", "desc", "Sort order for Period (asc, desc)")
	return cmd
}

type usageResponse struct {
	TimePeriod struct {
		Year  int  `json:"year" yaml:"year"`
		Month *int `json:"month" yaml:"month"`
		Day   *int `json:"day" yaml:"day"`
	} `json:"timePeriod" yaml:"timePeriod"`
	User       string `json:"user" yaml:"user"`
	UsageItems []struct {
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
	} `json:"usageItems" yaml:"usageItems"`
}

func fetchUsage(username string, year, month, day int, product, model string) (*usageResponse, error) {
	// Execute billing usage API command
	path := fmt.Sprintf("/users/%s/settings/billing/premium_request/usage?year=%d", username, year)
	if month > 0 {
		path += fmt.Sprintf("&month=%d", month)
	}
	if day > 0 {
		path += fmt.Sprintf("&day=%d", day)
	}
	if product != "" {
		path += fmt.Sprintf("&product=%s", product)
	}
	if model != "" {
		path += fmt.Sprintf("&model=%s", model)
	}

	cmd := exec.Command("gh", "api", path)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("Error: %s", string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("Error executing gh api: %v", err)
	}

	var res usageResponse
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, fmt.Errorf("Error unmarshaling API response: %v", err)
	}
	return &res, nil
}

func showUsage(ctx context.Context, client *copilot.Client, format string, year, month, day int, product, model string, last int, sortOrder string) {
	// 1. Get current username
	userCmd := exec.Command("gh", "api", "/user", "--jq", ".login")
	userOut, err := userCmd.Output()
	if err != nil {
		log.Printf("Error fetching username: %v", err)
		return
	}
	username := strings.TrimSpace(string(userOut))

	var responses []*usageResponse

	targetDate := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	if month == 0 {
		targetDate = time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	if day > 0 {
		targetDate = time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	}

	if last > 0 {
		for i := 0; i < last; i++ {
			var y, m, d int
			if day > 0 {
				// daily
				date := targetDate.AddDate(0, 0, -i)
				y, m, d = date.Year(), int(date.Month()), date.Day()
			} else if month > 0 {
				// monthly
				date := targetDate.AddDate(0, -i, 0)
				y, m, d = date.Year(), int(date.Month()), 0
			} else {
				// annual
				date := targetDate.AddDate(-i, 0, 0)
				y, m, d = date.Year(), 0, 0
			}
			res, err := fetchUsage(username, y, m, d, product, model)
			if err != nil {
				log.Printf("Failed to fetch usage for %d-%02d-%02d: %v", y, m, d, err)
				continue
			}
			responses = append(responses, res)
		}
	} else {
		res, err := fetchUsage(username, year, month, day, product, model)
		if err != nil {
			log.Print(err)
			return
		}
		responses = append(responses, res)
	}

	if format == "yaml" {
		if len(responses) == 1 {
			printYAML(responses[0])
		} else {
			printYAML(responses)
		}
		return
	}

	if len(responses) == 0 {
		fmt.Println("No usage data found.")
		return
	}

	// Fetch models to join with usage (Left Join Multiplier)
	multiplierMap := make(map[string]float64)
	modelsList, err := client.RPC.Models.List(ctx)
	if err == nil {
		for _, m := range modelsList.Models {
			if m.Billing != nil {
				multiplierMap[normalizeModelKey(m.Name)] = m.Billing.Multiplier
				multiplierMap[normalizeModelKey(m.ID)] = m.Billing.Multiplier
			}
		}
	}

	type usageItem struct {
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
		Period           string  `json:"-" yaml:"-"` // Not in API, used for sorting
		Multiplier       string  `json:"multiplier,omitempty" yaml:"multiplier,omitempty"`
	}

	// Fetch included limit if available (for reference only, as it's for current month)
	var entitlement float64
	quotaRes, err := client.RPC.Account.GetQuota(ctx)
	if err == nil {
		if snap, ok := quotaRes.QuotaSnapshots["premium_interactions"]; ok {
			entitlement = snap.EntitlementRequests
		}
	}

	var usageItems []usageItem
	for _, res := range responses {
		periodStr := strconv.Itoa(res.TimePeriod.Year)
		if res.TimePeriod.Month != nil {
			periodStr = fmt.Sprintf("%d-%02d", res.TimePeriod.Year, *res.TimePeriod.Month)
			if res.TimePeriod.Day != nil {
				periodStr = fmt.Sprintf("%d-%02d-%02d", res.TimePeriod.Year, *res.TimePeriod.Month, *res.TimePeriod.Day)
			}
		}
		for _, item := range res.UsageItems {
			multiplier := "-"
			if m, ok := multiplierMap[normalizeModelKey(item.Model)]; ok {
				multiplier = strconv.FormatFloat(m, 'f', -1, 64)
				if m == 0 {
					multiplier = "Included (0)"
				}
			}

			usageItems = append(usageItems, usageItem{
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
				Period:           periodStr,
				Multiplier:       multiplier,
			})
		}
	}

	// Sort usage items: Period (sortOrder), SKU ASC, Model ASC
	sort.Slice(usageItems, func(i, j int) bool {
		if usageItems[i].Period != usageItems[j].Period {
			if strings.ToLower(sortOrder) == "asc" {
				return usageItems[i].Period < usageItems[j].Period
			}
			return usageItems[i].Period > usageItems[j].Period
		}
		if usageItems[i].SKU != usageItems[j].SKU {
			return natural.Less(strings.ToLower(usageItems[i].SKU), strings.ToLower(usageItems[j].SKU))
		}
		return natural.Less(strings.ToLower(usageItems[i].Model), strings.ToLower(usageItems[j].Model))
	})

	// Group usage items: Period -> []item
	var periods []string
	periodGroups := make(map[string][]usageItem)
	for _, item := range usageItems {
		found := false
		for _, p := range periods {
			if p == item.Period {
				found = true
				break
			}
		}
		if !found {
			periods = append(periods, item.Period)
		}
		periodGroups[item.Period] = append(periodGroups[item.Period], item)
	}

	fmt.Printf("--- Billing Usage for %s (%s) ---\n", username, responses[0].User)
	if entitlement > 0 {
		fmt.Printf("Monthly Included Premium Requests (current plan): %s\n", strconv.FormatFloat(entitlement, 'f', -1, 64))
	}
	header := []string{"Period", "SKU", "Model", "Multiplier", "Used (req.)", "Billed (req.)", "Amount (USD)"}
	if last == 0 {
		header = header[1:] // Remove Period column if only one response
	}
	table := createTable(header, []int{len(header) - 4, len(header) - 3, len(header) - 2, len(header) - 1}, last > 0, last > 0)

	for _, p := range periods {
		items := periodGroups[p]
		// Further group items by SKU within the period
		var skus []string
		skuGroups := make(map[string][]usageItem)
		for _, item := range items {
			found := false
			for _, s := range skus {
				if s == item.SKU {
					found = true
					break
				}
			}
			if !found {
				skus = append(skus, item.SKU)
			}
			skuGroups[item.SKU] = append(skuGroups[item.SKU], item)
		}

		var periodUsedTotal, periodBilledTotal, periodAmountTotal float64
		for i, sku := range skus {
			skuItems := skuGroups[sku]
			var models, multipliers, useds, billeds, amounts []string
			var skuUsedTotal, skuBilledTotal, skuAmountTotal float64
			for _, item := range skuItems {
				models = append(models, item.Model)
				multipliers = append(multipliers, item.Multiplier)
				useds = append(useds, strconv.FormatFloat(item.GrossQuantity, 'f', -1, 64))
				billeds = append(billeds, strconv.FormatFloat(item.NetQuantity, 'f', -1, 64))
				amounts = append(amounts, fmt.Sprintf("$%.2f", item.NetAmount))
				skuUsedTotal += item.GrossQuantity
				skuBilledTotal += item.NetQuantity
				skuAmountTotal += item.NetAmount
			}
			periodUsedTotal += skuUsedTotal
			periodBilledTotal += skuBilledTotal
			periodAmountTotal += skuAmountTotal

			row := []string{
				p,
				sku,
				strings.Join(models, "\n"),
				strings.Join(multipliers, "\n"),
				strings.Join(useds, "\n"),
				strings.Join(billeds, "\n"),
				strings.Join(amounts, "\n"),
			}

			if last == 0 {
				row = row[1:]
			}
			table.Append(row)

			// If this is the last SKU in the period, add the Period Subtotal row
			if i == len(skus)-1 {
				subtotalRow := []string{
					p,
					"Subtotal (All SKUs)",
					"", // Model
					"", // Multiplier
					strconv.FormatFloat(periodUsedTotal, 'f', -1, 64),
					strconv.FormatFloat(periodBilledTotal, 'f', -1, 64),
					fmt.Sprintf("$%.2f", periodAmountTotal),
				}
				if last == 0 {
					subtotalRow = subtotalRow[1:]
				}
				table.Append(subtotalRow)
			}
		}
	}
	table.Render()
	fmt.Println("\nNotes:")
	fmt.Println("- 'Multiplier' is the request consumption rate per interaction for the model.")
	fmt.Println("- 'Used (req.)' is the total premium requests consumed.")
	fmt.Println("- 'Billed (req.)' is the overage amount you are billed for.")
	fmt.Println("- 'Amount (USD)' is the total billed cost in USD.")
	fmt.Println("- 'req.' stands for 'requests'.")
}

type sessionEvent struct {
	ID        string
	Type      string
	ParentID  string
	Timestamp time.Time
	Data      map[string]any
}

type sessionTurnWindow struct {
	TurnNumber        int            `json:"turnNumber" yaml:"turnNumber"`
	SegmentNumber     int            `json:"segmentNumber" yaml:"segmentNumber"`
	TurnID            string         `json:"turnId" yaml:"turnId"`
	InteractionID     string         `json:"interactionId,omitempty" yaml:"interactionId,omitempty"`
	ParentEventID     string         `json:"parentEventId,omitempty" yaml:"parentEventId,omitempty"`
	ParentUserEventID string         `json:"parentUserEventId,omitempty" yaml:"parentUserEventId,omitempty"`
	StartTime         time.Time      `json:"startTime" yaml:"startTime"`
	EndTime           *time.Time     `json:"endTime,omitempty" yaml:"endTime,omitempty"`
	State             string         `json:"state" yaml:"state"`
	ModelCalls        map[string]int `json:"modelCalls,omitempty" yaml:"modelCalls,omitempty"`
	ToolCalls         map[string]int `json:"toolCalls,omitempty" yaml:"toolCalls,omitempty"`
	UserMessage       string         `json:"userMessage,omitempty" yaml:"userMessage,omitempty"`
	Summary           string         `json:"summary,omitempty" yaml:"summary,omitempty"`
	AssistantMessages []string       `json:"assistantMessages,omitempty" yaml:"assistantMessages,omitempty"`
	SkillEvents       int            `json:"skillEvents,omitempty" yaml:"skillEvents,omitempty"`
	SubagentEvents    int            `json:"subagentEvents,omitempty" yaml:"subagentEvents,omitempty"`
	PlanChangeEvents  int            `json:"planChangeEvents,omitempty" yaml:"planChangeEvents,omitempty"`
	AbortEvents       int            `json:"abortEvents,omitempty" yaml:"abortEvents,omitempty"`
	startEventID      string
	endEventID        string
	lastActivityTime  time.Time
}

type historyRenderContext struct {
	events             []*sessionEvent
	turns              []*sessionTurnWindow
	eventMap           map[string]*sessionEvent
	depthCache         map[string]int
	interactionCache   map[string]string
	toolNames          map[string]string
	toolStartByCallID  map[string]*sessionEvent
	turnStartByEventID map[string]*sessionTurnWindow
	turnEndByEventID   map[string]*sessionTurnWindow
	lastEventTime      time.Time
}

type historyDisplayRow struct {
	Time          string
	Delta         string
	Depth         int
	InteractionID string
	Label         string
	Detail        string
	ExtraLines    []string
}

type historySpanProjectionRow struct {
	Timestamp     time.Time `json:"timestamp" yaml:"timestamp"`
	Span          string    `json:"span,omitempty" yaml:"span,omitempty"`
	Depth         int       `json:"depth,omitempty" yaml:"depth,omitempty"`
	InteractionID string    `json:"interactionId,omitempty" yaml:"interactionId,omitempty"`
	UserEventID   string    `json:"userEventId,omitempty" yaml:"userEventId,omitempty"`
	UserText      string    `json:"userText,omitempty" yaml:"userText,omitempty"`
	TurnNumber    int       `json:"turnNumber,omitempty" yaml:"turnNumber,omitempty"`
	SegmentNumber int       `json:"segmentNumber,omitempty" yaml:"segmentNumber,omitempty"`
	TurnID        string    `json:"turnId,omitempty" yaml:"turnId,omitempty"`
	TurnState     string    `json:"turnState,omitempty" yaml:"turnState,omitempty"`
	TurnDuration  string    `json:"turnDuration,omitempty" yaml:"turnDuration,omitempty"`
	Label         string    `json:"label" yaml:"label"`
	Detail        string    `json:"detail,omitempty" yaml:"detail,omitempty"`
	ExtraLines    []string  `json:"extraLines,omitempty" yaml:"extraLines,omitempty"`
	order         int
}

type sessionToolSpan struct {
	ToolCallID       string     `json:"toolCallId" yaml:"toolCallId"`
	ParentToolCallID string     `json:"parentToolCallId,omitempty" yaml:"parentToolCallId,omitempty"`
	InteractionID    string     `json:"interactionId,omitempty" yaml:"interactionId,omitempty"`
	ToolName         string     `json:"toolName,omitempty" yaml:"toolName,omitempty"`
	Model            string     `json:"model,omitempty" yaml:"model,omitempty"`
	StartTime        *time.Time `json:"startTime,omitempty" yaml:"startTime,omitempty"`
	EndTime          *time.Time `json:"endTime,omitempty" yaml:"endTime,omitempty"`
	Success          *bool      `json:"success,omitempty" yaml:"success,omitempty"`
	State            string     `json:"state" yaml:"state"`
	Depth            int        `json:"depth,omitempty" yaml:"depth,omitempty"`
	StartEventID     string     `json:"startEventId,omitempty" yaml:"startEventId,omitempty"`
	EndEventID       string     `json:"endEventId,omitempty" yaml:"endEventId,omitempty"`
	order            int
}

type sessionGraphSummary struct {
	SessionID                 string                    `json:"sessionId" yaml:"sessionId"`
	EventVertices             int                       `json:"eventVertices" yaml:"eventVertices"`
	InteractionVertices       int                       `json:"interactionVertices" yaml:"interactionVertices"`
	ToolCallVertices          int                       `json:"toolCallVertices" yaml:"toolCallVertices"`
	EventParentEdges          int                       `json:"eventParentEdges" yaml:"eventParentEdges"`
	EventInteractionEdges     int                       `json:"eventInteractionEdges" yaml:"eventInteractionEdges"`
	EventToolCallEdges        int                       `json:"eventToolCallEdges" yaml:"eventToolCallEdges"`
	ToolCallParentEdges       int                       `json:"toolCallParentEdges" yaml:"toolCallParentEdges"`
	RowsWithParentID          int                       `json:"rowsWithParentId" yaml:"rowsWithParentId"`
	MissingParentEventRows    int                       `json:"missingParentEventRows" yaml:"missingParentEventRows"`
	RowsWithParentToolCallID  int                       `json:"rowsWithParentToolCallId" yaml:"rowsWithParentToolCallId"`
	MissingParentToolCallRows int                       `json:"missingParentToolCallRows" yaml:"missingParentToolCallRows"`
	MissingParentTypes        []sessionEventTypeCount   `json:"missingParentTypes,omitempty" yaml:"missingParentTypes,omitempty"`
	InteractionHubs           []sessionInteractionHub   `json:"interactionHubs,omitempty" yaml:"interactionHubs,omitempty"`
	NestedToolParents         []sessionNestedToolParent `json:"nestedToolParents,omitempty" yaml:"nestedToolParents,omitempty"`
}

type sessionEventTypeCount struct {
	EventType string `json:"eventType" yaml:"eventType"`
	Rows      int    `json:"rows" yaml:"rows"`
}

type sessionInteractionHub struct {
	InteractionID string   `json:"interactionId" yaml:"interactionId"`
	MatchedEvents int      `json:"matchedEvents" yaml:"matchedEvents"`
	ToolCalls     int      `json:"toolCalls" yaml:"toolCalls"`
	EventTypes    []string `json:"eventTypes,omitempty" yaml:"eventTypes,omitempty"`
}

type sessionNestedToolParent struct {
	ParentToolCallID string              `json:"parentToolCallId" yaml:"parentToolCallId"`
	ParentToolName   string              `json:"parentToolName,omitempty" yaml:"parentToolName,omitempty"`
	InteractionID    string              `json:"interactionId,omitempty" yaml:"interactionId,omitempty"`
	ChildToolCalls   int                 `json:"childToolCalls" yaml:"childToolCalls"`
	ChildTools       []sessionNamedCount `json:"childTools,omitempty" yaml:"childTools,omitempty"`
	ChildEventTypes  []string            `json:"childEventTypes,omitempty" yaml:"childEventTypes,omitempty"`
	ChildToolCallIDs []string            `json:"childToolCallIds,omitempty" yaml:"childToolCallIds,omitempty"`
}

type sessionNamedCount struct {
	Name  string `json:"name" yaml:"name"`
	Count int    `json:"count" yaml:"count"`
}

type interactionHubAccumulator struct {
	matchedEvents int
	toolCalls     map[string]struct{}
	eventTypes    map[string]struct{}
}

type toolCallVertexAccumulator struct {
	ParentToolCallID string
	ToolName         string
	InteractionID    string
	EventTypes       map[string]struct{}
}

type nestedToolParentAccumulator struct {
	ParentToolName  string
	InteractionID   string
	ChildToolCalls  map[string]struct{}
	ChildTools      map[string]int
	ChildEventTypes map[string]struct{}
}

func (t *sessionTurnWindow) effectiveEnd(lastEventTime time.Time) time.Time {
	if t.EndTime != nil {
		return *t.EndTime
	}
	if !t.lastActivityTime.IsZero() {
		return t.lastActivityTime
	}
	return lastEventTime
}

func (t *sessionTurnWindow) durationString(lastEventTime time.Time) string {
	return t.effectiveEnd(lastEventTime).Sub(t.StartTime).Round(time.Millisecond).String()
}

func (s *sessionToolSpan) effectiveTime() time.Time {
	if s.StartTime != nil {
		return *s.StartTime
	}
	if s.EndTime != nil {
		return *s.EndTime
	}
	return time.Time{}
}

func (s *sessionToolSpan) spanString() string {
	switch {
	case s.StartTime != nil && s.EndTime != nil:
		return s.EndTime.Sub(*s.StartTime).Round(time.Millisecond).String()
	case s.StartTime != nil:
		return "Open"
	default:
		return "End-only"
	}
}

func sessionEventsPath(sessionID string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".copilot", "session-state", sessionID, "events.jsonl")
}

var errStopJSONLIteration = errors.New("stop jsonl iteration")

func visitJSONLObjects(path string, fn func(map[string]any) error) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("error opening events file: %w", err)
	}
	defer f.Close()

	decoder := json.NewDecoder(bufio.NewReader(f))
	rowNo := 0
	for {
		var ev map[string]any
		if err := decoder.Decode(&ev); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("error decoding %s row %d: %w", path, rowNo+1, err)
		}
		rowNo++
		if err := fn(ev); err != nil {
			if errors.Is(err, errStopJSONLIteration) {
				return nil
			}
			return err
		}
	}

	return nil
}

func sessionHasShutdown(eventsPath string) (bool, error) {
	hasShutdown := false
	err := visitJSONLObjects(eventsPath, func(ev map[string]any) error {
		if ev["type"] == "session.shutdown" {
			hasShutdown = true
			return errStopJSONLIteration
		}
		return nil
	})
	return hasShutdown, err
}

func loadSessionRawEvents(sessionID string) ([]map[string]any, error) {
	eventsPath := sessionEventsPath(sessionID)
	if _, err := os.Stat(eventsPath); err != nil {
		return nil, fmt.Errorf("no local events found for session %s", sessionID)
	}

	var events []map[string]any
	if err := visitJSONLObjects(eventsPath, func(ev map[string]any) error {
		events = append(events, ev)
		return nil
	}); err != nil {
		return nil, err
	}

	return events, nil
}

func parseSessionEvent(raw map[string]any) *sessionEvent {
	timestampStr, _ := raw["timestamp"].(string)
	ts, err := time.Parse(time.RFC3339, timestampStr)
	if err != nil {
		return nil
	}

	data, _ := raw["data"].(map[string]any)
	id, _ := raw["id"].(string)
	parentID, _ := raw["parentId"].(string)
	evType, _ := raw["type"].(string)

	return &sessionEvent{
		ID:        id,
		Type:      evType,
		ParentID:  parentID,
		Timestamp: ts,
		Data:      data,
	}
}

func loadSessionEvents(sessionID string) ([]*sessionEvent, error) {
	eventsPath := sessionEventsPath(sessionID)
	if _, err := os.Stat(eventsPath); err != nil {
		return nil, fmt.Errorf("no local events found for session %s", sessionID)
	}

	var events []*sessionEvent
	if err := visitJSONLObjects(eventsPath, func(raw map[string]any) error {
		if ev := parseSessionEvent(raw); ev != nil {
			events = append(events, ev)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return events, nil
}

func dataString(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	value, _ := data[key].(string)
	return value
}

func nestedDataString(data map[string]any, keys ...string) string {
	current := data
	for i, key := range keys {
		if current == nil {
			return ""
		}
		if i == len(keys)-1 {
			value, _ := current[key].(string)
			return value
		}
		next, _ := current[key].(map[string]any)
		current = next
	}
	return ""
}

func dataBool(data map[string]any, key string) bool {
	if data == nil {
		return false
	}
	value, _ := data[key].(bool)
	return value
}

func dataBoolPtr(data map[string]any, key string) *bool {
	if data == nil {
		return nil
	}
	value, ok := data[key].(bool)
	if !ok {
		return nil
	}
	return &value
}

func dataMap(data map[string]any, key string) map[string]any {
	if data == nil {
		return nil
	}
	value, _ := data[key].(map[string]any)
	return value
}

func dataFloat(data map[string]any, key string) (float64, bool) {
	if data == nil {
		return 0, false
	}
	value, ok := data[key].(float64)
	return value, ok
}

func normalizeInlineText(text string) string {
	text = strings.TrimSpace(text)
	text = strings.ReplaceAll(text, "\n", " ")
	return strings.Join(strings.Fields(text), " ")
}

func eventText(data map[string]any) string {
	text := dataString(data, "content")
	if text == "" {
		text = dataString(data, "transformedContent")
	}
	return normalizeInlineText(text)
}

func truncateRunes(text string, limit int) string {
	if limit <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "..."
}

func formatCountSummary(counts map[string]int) string {
	if len(counts) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s (%d)", key, counts[key]))
	}
	return strings.Join(parts, ", ")
}

func formatNamedCounts(counts []sessionNamedCount) string {
	if len(counts) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(counts))
	for _, count := range counts {
		parts = append(parts, fmt.Sprintf("%s (%d)", count.Name, count.Count))
	}
	return strings.Join(parts, ", ")
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func buildHistoryRenderContext(events []*sessionEvent) *historyRenderContext {
	turns := buildTurnWindows(events)
	ctx := &historyRenderContext{
		events:             events,
		turns:              turns,
		eventMap:           make(map[string]*sessionEvent, len(events)),
		depthCache:         make(map[string]int),
		interactionCache:   make(map[string]string),
		toolNames:          buildToolNameIndex(events),
		toolStartByCallID:  buildToolStartEventIndex(events),
		turnStartByEventID: make(map[string]*sessionTurnWindow),
		turnEndByEventID:   make(map[string]*sessionTurnWindow),
	}

	if len(events) > 0 {
		ctx.lastEventTime = events[len(events)-1].Timestamp
	}

	for _, ev := range events {
		if ev.ID != "" {
			ctx.eventMap[ev.ID] = ev
		}
	}

	for _, turn := range turns {
		if turn.startEventID != "" {
			ctx.turnStartByEventID[turn.startEventID] = turn
		}
		if turn.endEventID != "" {
			ctx.turnEndByEventID[turn.endEventID] = turn
		}
	}

	return ctx
}

func resolveTurnForTimestamp(ctx *historyRenderContext, ts time.Time) *sessionTurnWindow {
	for _, turn := range ctx.turns {
		if ts.Before(turn.StartTime) {
			continue
		}
		if ts.After(turn.effectiveEnd(ctx.lastEventTime)) {
			continue
		}
		return turn
	}
	return nil
}

func populateHistorySpanTurnFields(row *historySpanProjectionRow, turn *sessionTurnWindow, lastEventTime time.Time) {
	if row == nil || turn == nil {
		return
	}
	row.TurnNumber = turn.TurnNumber
	row.SegmentNumber = turn.SegmentNumber
	row.TurnID = turn.TurnID
	row.TurnState = turn.State
	row.TurnDuration = turn.durationString(lastEventTime)
	if row.InteractionID == "" {
		row.InteractionID = turn.InteractionID
	}
	if row.UserEventID == "" {
		row.UserEventID = turn.ParentUserEventID
	}
	if row.UserText == "" {
		row.UserText = turn.UserMessage
	}
}

func buildHistoryRows(events []*sessionEvent) ([]historyDisplayRow, error) {
	if len(events) == 0 {
		return nil, fmt.Errorf("no parsable events found")
	}

	ctx := buildHistoryRenderContext(events)
	rows := make([]historyDisplayRow, 0, len(events))
	var prevTs time.Time

	for i, ev := range ctx.events {
		var delta string
		if i > 0 {
			delta = fmt.Sprintf("+%v", ev.Timestamp.Sub(prevTs).Round(time.Millisecond))
		}
		if ev.ParentID != "" {
			if parent, ok := ctx.eventMap[ev.ParentID]; ok {
				delta = fmt.Sprintf("(p+%v)", ev.Timestamp.Sub(parent.Timestamp).Round(time.Millisecond))
			}
		}
		prevTs = ev.Timestamp

		label, detail, extraLines := describeHistoryEvent(ctx, ev)
		rows = append(rows, historyDisplayRow{
			Time:          ev.Timestamp.Local().Format("15:04:05.000"),
			Delta:         delta,
			Depth:         eventDepth(ev.ID, ctx.eventMap, ctx.depthCache),
			InteractionID: resolveHistoryInteractionID(ctx, ev),
			Label:         label,
			Detail:        detail,
			ExtraLines:    extraLines,
		})
	}

	return rows, nil
}

func buildHistorySpanProjectionRows(events []*sessionEvent) ([]historySpanProjectionRow, error) {
	if len(events) == 0 {
		return nil, fmt.Errorf("no parsable events found")
	}

	ctx := buildHistoryRenderContext(events)
	toolSpans, consumedEventIDs := buildSessionToolSpans(ctx)
	rows := make([]historySpanProjectionRow, 0, len(events))

	for i, ev := range ctx.events {
		if ev.ID != "" {
			if _, ok := consumedEventIDs[ev.ID]; ok {
				continue
			}
		}
		label, detail, extraLines := describeHistoryEvent(ctx, ev)
		rows = append(rows, historySpanProjectionRow{
			Timestamp:     ev.Timestamp,
			Depth:         eventDepth(ev.ID, ctx.eventMap, ctx.depthCache),
			InteractionID: resolveHistoryInteractionID(ctx, ev),
			UserEventID:   ev.ID,
			UserText:      eventText(ev.Data),
			Label:         label,
			Detail:        detail,
			ExtraLines:    extraLines,
			order:         i,
		})
		if ev.Type != "user.message" {
			rows[len(rows)-1].UserEventID = ""
			rows[len(rows)-1].UserText = ""
		}
		if turn, ok := ctx.turnStartByEventID[ev.ID]; ok {
			populateHistorySpanTurnFields(&rows[len(rows)-1], turn, ctx.lastEventTime)
		} else if turn, ok := ctx.turnEndByEventID[ev.ID]; ok {
			populateHistorySpanTurnFields(&rows[len(rows)-1], turn, ctx.lastEventTime)
		} else if turn := resolveTurnForTimestamp(ctx, ev.Timestamp); turn != nil {
			populateHistorySpanTurnFields(&rows[len(rows)-1], turn, ctx.lastEventTime)
		}
	}

	for _, span := range toolSpans {
		label, detail, extraLines := describeToolSpan(span)
		rows = append(rows, historySpanProjectionRow{
			Timestamp:     span.effectiveTime(),
			Span:          span.spanString(),
			Depth:         span.Depth + 1,
			InteractionID: span.InteractionID,
			Label:         label,
			Detail:        detail,
			ExtraLines:    extraLines,
			order:         span.order,
		})
		if turn := resolveTurnForTimestamp(ctx, span.effectiveTime()); turn != nil {
			populateHistorySpanTurnFields(&rows[len(rows)-1], turn, ctx.lastEventTime)
		}
	}

	sort.SliceStable(rows, func(i, j int) bool {
		if !rows[i].Timestamp.Equal(rows[j].Timestamp) {
			return rows[i].Timestamp.Before(rows[j].Timestamp)
		}
		return rows[i].order < rows[j].order
	})

	return rows, nil
}

func describeToolSpan(span *sessionToolSpan) (string, string, []string) {
	toolName := span.ToolName
	if toolName == "" {
		toolName = "<unknown>"
	}

	detail := toolName
	if span.Model != "" {
		detail = fmt.Sprintf("%s [%s]", detail, span.Model)
	}

	switch span.State {
	case "Complete":
		if span.Success != nil {
			detail = fmt.Sprintf("%s (Success: %v)", detail, *span.Success)
		} else {
			detail = fmt.Sprintf("%s (Complete)", detail)
		}
	case "Open":
		detail = fmt.Sprintf("%s (Open)", detail)
	case "Orphan End":
		if span.Success != nil {
			detail = fmt.Sprintf("%s (End without start, Success: %v)", detail, *span.Success)
		} else {
			detail = fmt.Sprintf("%s (End without start)", detail)
		}
	}
	return "Tool", detail, nil
}

func buildSessionToolSpans(ctx *historyRenderContext) ([]*sessionToolSpan, map[string]struct{}) {
	spansByToolCallID := make(map[string]*sessionToolSpan)
	consumedEventIDs := make(map[string]struct{})

	for i, ev := range ctx.events {
		if ev.Type != "tool.execution_start" && ev.Type != "tool.execution_complete" {
			continue
		}

		toolCallID := dataString(ev.Data, "toolCallId")
		if toolCallID == "" {
			continue
		}

		span := spansByToolCallID[toolCallID]
		if span == nil {
			span = &sessionToolSpan{
				ToolCallID: toolCallID,
				order:      i,
			}
			spansByToolCallID[toolCallID] = span
		}
		if i < span.order {
			span.order = i
		}
		if span.ParentToolCallID == "" {
			span.ParentToolCallID = dataString(ev.Data, "parentToolCallId")
		}
		if span.ToolName == "" {
			span.ToolName = dataString(ev.Data, "toolName")
			if span.ToolName == "" {
				span.ToolName = ctx.toolNames[toolCallID]
			}
		}
		if span.InteractionID == "" {
			span.InteractionID = resolveHistoryInteractionID(ctx, ev)
		}

		switch ev.Type {
		case "tool.execution_start":
			if span.StartTime == nil {
				ts := ev.Timestamp
				span.StartTime = &ts
				span.StartEventID = ev.ID
			}
			if ev.ID != "" {
				consumedEventIDs[ev.ID] = struct{}{}
			}
		case "tool.execution_complete":
			if span.EndTime == nil {
				ts := ev.Timestamp
				span.EndTime = &ts
				span.EndEventID = ev.ID
			}
			if span.Model == "" {
				span.Model = dataString(ev.Data, "model")
			}
			if span.Success == nil {
				span.Success = dataBoolPtr(ev.Data, "success")
			}
			if ev.ID != "" {
				consumedEventIDs[ev.ID] = struct{}{}
			}
		}
	}

	depthCache := make(map[string]int)
	spans := make([]*sessionToolSpan, 0, len(spansByToolCallID))
	for toolCallID, span := range spansByToolCallID {
		span.Depth = toolSpanDepth(toolCallID, spansByToolCallID, depthCache)
		switch {
		case span.StartTime != nil && span.EndTime != nil:
			span.State = "Complete"
		case span.StartTime != nil:
			span.State = "Open"
		default:
			span.State = "Orphan End"
		}
		spans = append(spans, span)
	}

	sort.SliceStable(spans, func(i, j int) bool {
		if !spans[i].effectiveTime().Equal(spans[j].effectiveTime()) {
			return spans[i].effectiveTime().Before(spans[j].effectiveTime())
		}
		return spans[i].order < spans[j].order
	})
	return spans, consumedEventIDs
}

func toolSpanDepth(toolCallID string, spans map[string]*sessionToolSpan, cache map[string]int) int {
	if depth, ok := cache[toolCallID]; ok {
		return depth
	}

	span := spans[toolCallID]
	if span == nil || span.ParentToolCallID == "" {
		cache[toolCallID] = 0
		return 0
	}

	parent, ok := spans[span.ParentToolCallID]
	if !ok || parent == nil || span.ParentToolCallID == toolCallID {
		cache[toolCallID] = 0
		return 0
	}

	depth := toolSpanDepth(span.ParentToolCallID, spans, cache) + 1
	if depth > 8 {
		depth = 8
	}
	cache[toolCallID] = depth
	return depth
}

func formatHistoryTransition(previous string, next string) string {
	switch {
	case previous != "" && next != "":
		return fmt.Sprintf("%s -> %s", previous, next)
	case next != "":
		return next
	default:
		return previous
	}
}

func describeSessionContextChanged(ev *sessionEvent) (string, string, []string) {
	repository := dataString(ev.Data, "repository")
	branch := dataString(ev.Data, "branch")
	cwd := dataString(ev.Data, "cwd")
	gitRoot := dataString(ev.Data, "gitRoot")
	headCommit := dataString(ev.Data, "headCommit")
	baseCommit := dataString(ev.Data, "baseCommit")

	detail := ""
	switch {
	case repository != "" && branch != "":
		detail = fmt.Sprintf("%s @ %s", repository, branch)
	case repository != "":
		detail = repository
	case branch != "":
		detail = branch
	default:
		detail = cwd
	}

	var extraLines []string
	if cwd != "" && cwd != detail {
		extraLines = append(extraLines, fmt.Sprintf("CWD: %s", cwd))
	}
	if gitRoot != "" && gitRoot != cwd {
		extraLines = append(extraLines, fmt.Sprintf("Git Root: %s", gitRoot))
	}
	if headCommit != "" {
		extraLines = append(extraLines, fmt.Sprintf("HEAD: %s", shortID(headCommit)))
	}
	if baseCommit != "" && baseCommit != headCommit {
		extraLines = append(extraLines, fmt.Sprintf("Base: %s", shortID(baseCommit)))
	}
	return "Context Changed", detail, extraLines
}

func describeSessionCompactionComplete(ev *sessionEvent) (string, string, []string) {
	var detailParts []string
	if checkpointNumber, ok := dataFloat(ev.Data, "checkpointNumber"); ok {
		detailParts = append(detailParts, fmt.Sprintf("Checkpoint #%.0f", checkpointNumber))
	}
	if success := dataBoolPtr(ev.Data, "success"); success != nil {
		detailParts = append(detailParts, fmt.Sprintf("Success: %v", *success))
	}
	detail := strings.Join(detailParts, ", ")

	var extraLines []string
	preCompactionTokens, hasPreCompactionTokens := dataFloat(ev.Data, "preCompactionTokens")
	preCompactionMessages, hasPreCompactionMessages := dataFloat(ev.Data, "preCompactionMessagesLength")
	switch {
	case hasPreCompactionTokens && hasPreCompactionMessages:
		extraLines = append(extraLines, fmt.Sprintf("Before: %.0f tokens, %.0f messages", preCompactionTokens, preCompactionMessages))
	case hasPreCompactionTokens:
		extraLines = append(extraLines, fmt.Sprintf("Before: %.0f tokens", preCompactionTokens))
	case hasPreCompactionMessages:
		extraLines = append(extraLines, fmt.Sprintf("Before: %.0f messages", preCompactionMessages))
	}
	if tokensUsed := dataMap(ev.Data, "compactionTokensUsed"); tokensUsed != nil {
		var tokenParts []string
		if input, ok := dataFloat(tokensUsed, "input"); ok {
			tokenParts = append(tokenParts, fmt.Sprintf("In %.0f", input))
		}
		if output, ok := dataFloat(tokensUsed, "output"); ok {
			tokenParts = append(tokenParts, fmt.Sprintf("Out %.0f", output))
		}
		if cachedInput, ok := dataFloat(tokensUsed, "cachedInput"); ok {
			tokenParts = append(tokenParts, fmt.Sprintf("Cached %.0f", cachedInput))
		}
		if len(tokenParts) > 0 {
			extraLines = append(extraLines, "Compaction Tokens: "+strings.Join(tokenParts, ", "))
		}
	}
	if checkpointPath := dataString(ev.Data, "checkpointPath"); checkpointPath != "" {
		extraLines = append(extraLines, fmt.Sprintf("Checkpoint Path: %s", checkpointPath))
	}
	if requestID := dataString(ev.Data, "requestId"); requestID != "" {
		extraLines = append(extraLines, fmt.Sprintf("Request ID: %s", shortID(requestID)))
	}
	if summary := dataString(ev.Data, "summaryContent"); summary != "" {
		extraLines = append(extraLines, "Summary: "+truncateRunes(normalizeInlineText(summary), 120))
	}
	return "Compaction Complete", detail, extraLines
}

func describeSessionModeChanged(ev *sessionEvent) (string, string, []string) {
	return "Mode Changed", formatHistoryTransition(dataString(ev.Data, "previousMode"), dataString(ev.Data, "newMode")), nil
}

func describeSessionWorkspaceFileChanged(ev *sessionEvent) (string, string, []string) {
	operation := dataString(ev.Data, "operation")
	path := dataString(ev.Data, "path")
	switch {
	case operation != "" && path != "":
		return "Workspace File Changed", fmt.Sprintf("%s: %s", operation, path), nil
	case path != "":
		return "Workspace File Changed", path, nil
	default:
		return "Workspace File Changed", operation, nil
	}
}

func describeToolUserRequested(ev *sessionEvent) (string, string, []string) {
	toolName := dataString(ev.Data, "toolName")
	if toolName == "" {
		toolName = "<unknown>"
	}
	command := normalizeInlineText(nestedDataString(ev.Data, "arguments", "command"))
	description := normalizeInlineText(nestedDataString(ev.Data, "arguments", "description"))
	detail := toolName
	switch {
	case command != "":
		detail = fmt.Sprintf("%s: %s", toolName, truncateRunes(command, 120))
	case description != "":
		detail = fmt.Sprintf("%s: %s", toolName, truncateRunes(description, 120))
	}
	var extraLines []string
	if description != "" && command != "" && description != command {
		extraLines = append(extraLines, "Description: "+truncateRunes(description, 120))
	}
	return "Tool Requested", detail, extraLines
}

func describeSessionInfo(ev *sessionEvent) (string, string, []string) {
	infoType := dataString(ev.Data, "infoType")
	message := normalizeInlineText(dataString(ev.Data, "message"))
	switch {
	case infoType != "" && message != "":
		return "Session Info", fmt.Sprintf("%s: %s", infoType, truncateRunes(message, 120)), nil
	case message != "":
		return "Session Info", truncateRunes(message, 120), nil
	default:
		return "Session Info", infoType, nil
	}
}

func describeSessionModelChange(ev *sessionEvent) (string, string, []string) {
	detail := formatHistoryTransition(dataString(ev.Data, "previousModel"), dataString(ev.Data, "newModel"))
	reasoningEffort := dataString(ev.Data, "reasoningEffort")
	previousReasoningEffort := dataString(ev.Data, "previousReasoningEffort")
	var extraLines []string
	switch {
	case previousReasoningEffort != "" && reasoningEffort != "" && previousReasoningEffort != reasoningEffort:
		extraLines = append(extraLines, fmt.Sprintf("Reasoning: %s", formatHistoryTransition(previousReasoningEffort, reasoningEffort)))
	case reasoningEffort != "":
		extraLines = append(extraLines, fmt.Sprintf("Reasoning: %s", reasoningEffort))
	}
	return "Model Changed", detail, extraLines
}

func describeHistoryEvent(ctx *historyRenderContext, ev *sessionEvent) (string, string, []string) {
	switch ev.Type {
	case "user.message":
		return "User", eventText(ev.Data), nil
	case "assistant.message":
		return "Assistant", eventText(ev.Data), nil
	case "tool.execution_start":
		toolName := dataString(ev.Data, "toolName")
		if toolName == "" {
			toolName = "<unknown>"
		}
		return "Tool Start", toolName, nil
	case "tool.execution_complete":
		toolName := dataString(ev.Data, "toolName")
		if toolName == "" {
			toolName = ctx.toolNames[dataString(ev.Data, "toolCallId")]
		}
		if toolName == "" {
			toolName = "<unknown>"
		}
		detail := toolName
		if model := dataString(ev.Data, "model"); model != "" {
			detail = fmt.Sprintf("%s [%s]", detail, model)
		}
		detail = fmt.Sprintf("%s (Success: %v)", detail, dataBool(ev.Data, "success"))
		return "Tool End", detail, nil
	case "session.start":
		cwd := nestedDataString(ev.Data, "context", "cwd")
		if cwd == "" {
			return "Session Start", "", nil
		}
		return "Session Start", fmt.Sprintf("CWD: %s", cwd), nil
	case "session.resume":
		return "Session Resume", "", nil
	case "session.context_changed":
		return describeSessionContextChanged(ev)
	case "session.compaction_start":
		return "Compaction Start", "", nil
	case "session.compaction_complete":
		return describeSessionCompactionComplete(ev)
	case "session.mode_changed":
		return describeSessionModeChanged(ev)
	case "session.workspace_file_changed":
		return describeSessionWorkspaceFileChanged(ev)
	case "tool.user_requested":
		return describeToolUserRequested(ev)
	case "session.info":
		return describeSessionInfo(ev)
	case "session.model_change":
		return describeSessionModelChange(ev)
	case "assistant.turn_start":
		if turn, ok := ctx.turnStartByEventID[ev.ID]; ok {
			detail := fmt.Sprintf("Turn #%d, Segment %d, Turn ID %s", turn.TurnNumber, turn.SegmentNumber, turn.TurnID)
			if turn.InteractionID != "" {
				detail = fmt.Sprintf("%s, Interaction %s", detail, shortID(turn.InteractionID))
			}
			return "Assistant Turn Start", detail, nil
		}
		return "Assistant Turn Start", "", nil
	case "assistant.turn_end":
		if turn, ok := ctx.turnEndByEventID[ev.ID]; ok {
			return "Assistant Turn End", fmt.Sprintf("Turn #%d, Duration: %s", turn.TurnNumber, turn.durationString(ctx.lastEventTime)), nil
		}
		return "Assistant Turn End", "", nil
	case "session.shutdown":
		total, _ := ev.Data["totalPremiumRequests"].(float64)
		var extraLines []string
		if metrics, ok := ev.Data["modelMetrics"].(map[string]any); ok {
			models := make([]string, 0, len(metrics))
			for model := range metrics {
				models = append(models, model)
			}
			sort.Strings(models)

			for _, model := range models {
				mv, ok := metrics[model].(map[string]any)
				if !ok {
					continue
				}
				if reqs, ok := mv["requests"].(map[string]any); ok {
					count, _ := reqs["count"].(float64)
					cost, _ := reqs["cost"].(float64)
					extraLines = append(extraLines, fmt.Sprintf("Model %s: Requests %.0f, Cost %.0f", model, count, cost))
				}
				if usage, ok := mv["usage"].(map[string]any); ok {
					in, _ := usage["inputTokens"].(float64)
					out, _ := usage["outputTokens"].(float64)
					extraLines = append(extraLines, fmt.Sprintf("Tokens: In %.0f, Out %.0f", in, out))
				}
			}
		}
		return "Session Shutdown", fmt.Sprintf("Total Premium Requests: %.0f", total), extraLines
	case "skill.invoked":
		name := dataString(ev.Data, "name")
		if name == "" {
			name = dataString(ev.Data, "skillName")
		}
		return "Skill Invoked", name, nil
	case "subagent.started":
		name := dataString(ev.Data, "agentDisplayName")
		if name == "" {
			name = dataString(ev.Data, "agentName")
		}
		return "Subagent Started", name, nil
	case "subagent.completed":
		name := dataString(ev.Data, "agentDisplayName")
		if name == "" {
			name = dataString(ev.Data, "agentName")
		}
		return "Subagent Completed", name, nil
	case "system.notification":
		kind := nestedDataString(ev.Data, "kind", "type")
		if kind == "" {
			kind = dataString(ev.Data, "type")
		}
		if kind == "" {
			kind = "notification"
		}
		return "System Notification", kind, nil
	case "session.plan_changed":
		return "Plan Changed", "", nil
	case "abort":
		return "Abort", "", nil
	default:
		detail := ev.Type
		if ev.ID != "" {
			detail = fmt.Sprintf("%s (%s)", ev.Type, ev.ID)
		}
		return "Event", detail, nil
	}
}

func formatHistoryEventText(depth int, label string, detail string) string {
	indent := strings.Repeat("  ", depth)
	if detail == "" {
		return indent + label
	}
	return indent + fmt.Sprintf("%-*s %s", historyEventLabelWidth, label, detail)
}

func formatHistoryEventLabel(depth int, label string) string {
	return strings.Repeat("  ", depth) + label
}

func formatHistoryExtraLine(depth int, detail string) string {
	return strings.Repeat("  ", depth) + strings.Repeat(" ", historyEventLabelWidth+1) + detail
}

func buildToolStartEventIndex(events []*sessionEvent) map[string]*sessionEvent {
	toolStarts := make(map[string]*sessionEvent)
	for _, ev := range events {
		if ev.Type != "tool.execution_start" {
			continue
		}
		toolCallID := dataString(ev.Data, "toolCallId")
		if toolCallID != "" {
			toolStarts[toolCallID] = ev
		}
	}
	return toolStarts
}

func resolveHistoryInteractionID(ctx *historyRenderContext, ev *sessionEvent) string {
	if ev == nil || ev.ID == "" {
		return ""
	}
	if interactionID, ok := ctx.interactionCache[ev.ID]; ok {
		return interactionID
	}

	interactionID := dataString(ev.Data, "interactionId")
	if interactionID == "" {
		if turn, ok := ctx.turnStartByEventID[ev.ID]; ok {
			interactionID = turn.InteractionID
		}
	}
	if interactionID == "" {
		if turn, ok := ctx.turnEndByEventID[ev.ID]; ok {
			interactionID = turn.InteractionID
		}
	}
	if interactionID == "" {
		toolCallID := dataString(ev.Data, "toolCallId")
		if toolCallID != "" {
			if start := ctx.toolStartByCallID[toolCallID]; start != nil && start.ID != ev.ID {
				interactionID = resolveHistoryInteractionID(ctx, start)
			}
		}
	}
	if interactionID == "" {
		parentToolCallID := dataString(ev.Data, "parentToolCallId")
		if parentToolCallID != "" {
			if parentStart := ctx.toolStartByCallID[parentToolCallID]; parentStart != nil && parentStart.ID != ev.ID {
				interactionID = resolveHistoryInteractionID(ctx, parentStart)
			}
		}
	}
	if interactionID == "" && ev.ParentID != "" {
		if parent, ok := ctx.eventMap[ev.ParentID]; ok {
			interactionID = resolveHistoryInteractionID(ctx, parent)
		}
	}

	ctx.interactionCache[ev.ID] = interactionID
	return interactionID
}

func buildToolNameIndex(events []*sessionEvent) map[string]string {
	toolNames := make(map[string]string)
	for _, ev := range events {
		if ev.Type != "tool.execution_start" {
			continue
		}
		toolCallID := dataString(ev.Data, "toolCallId")
		toolName := dataString(ev.Data, "toolName")
		if toolCallID != "" && toolName != "" {
			toolNames[toolCallID] = toolName
		}
	}
	return toolNames
}

func sortedStringsFromSet(values map[string]struct{}) []string {
	items := make([]string, 0, len(values))
	for value := range values {
		items = append(items, value)
	}
	sort.Strings(items)
	return items
}

func sortedNamedCounts(counts map[string]int) []sessionNamedCount {
	items := make([]sessionNamedCount, 0, len(counts))
	for name, count := range counts {
		items = append(items, sessionNamedCount{Name: name, Count: count})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count != items[j].Count {
			return items[i].Count > items[j].Count
		}
		return natural.Less(strings.ToLower(items[i].Name), strings.ToLower(items[j].Name))
	})
	return items
}

func sortSessionEventTypeCounts(counts map[string]int) []sessionEventTypeCount {
	items := make([]sessionEventTypeCount, 0, len(counts))
	for eventType, rows := range counts {
		items = append(items, sessionEventTypeCount{EventType: eventType, Rows: rows})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Rows != items[j].Rows {
			return items[i].Rows > items[j].Rows
		}
		return natural.Less(strings.ToLower(items[i].EventType), strings.ToLower(items[j].EventType))
	})
	return items
}

func sortSessionInteractionHubs(hubs map[string]*interactionHubAccumulator) []sessionInteractionHub {
	items := make([]sessionInteractionHub, 0, len(hubs))
	for interactionID, hub := range hubs {
		items = append(items, sessionInteractionHub{
			InteractionID: interactionID,
			MatchedEvents: hub.matchedEvents,
			ToolCalls:     len(hub.toolCalls),
			EventTypes:    sortedStringsFromSet(hub.eventTypes),
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].MatchedEvents != items[j].MatchedEvents {
			return items[i].MatchedEvents > items[j].MatchedEvents
		}
		return items[i].InteractionID < items[j].InteractionID
	})
	return items
}

func sortSessionNestedToolParents(parents map[string]*nestedToolParentAccumulator) []sessionNestedToolParent {
	items := make([]sessionNestedToolParent, 0, len(parents))
	for parentToolCallID, parent := range parents {
		childToolCallIDs := sortedStringsFromSet(parent.ChildToolCalls)
		items = append(items, sessionNestedToolParent{
			ParentToolCallID: parentToolCallID,
			ParentToolName:   parent.ParentToolName,
			InteractionID:    parent.InteractionID,
			ChildToolCalls:   len(childToolCallIDs),
			ChildTools:       sortedNamedCounts(parent.ChildTools),
			ChildEventTypes:  sortedStringsFromSet(parent.ChildEventTypes),
			ChildToolCallIDs: childToolCallIDs,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ChildToolCalls != items[j].ChildToolCalls {
			return items[i].ChildToolCalls > items[j].ChildToolCalls
		}
		return items[i].ParentToolCallID < items[j].ParentToolCallID
	})
	return items
}

func buildSessionGraphSummary(sessionID string, events []*sessionEvent) (*sessionGraphSummary, error) {
	if len(events) == 0 {
		return nil, fmt.Errorf("no parsable events found for session %s", sessionID)
	}

	ctx := buildHistoryRenderContext(events)
	interactionHubs := make(map[string]*interactionHubAccumulator)
	toolCalls := make(map[string]*toolCallVertexAccumulator)
	missingParentTypeCounts := make(map[string]int)

	for _, ev := range ctx.events {
		if interactionID := dataString(ev.Data, "interactionId"); interactionID != "" {
			hub := interactionHubs[interactionID]
			if hub == nil {
				hub = &interactionHubAccumulator{
					toolCalls:  make(map[string]struct{}),
					eventTypes: make(map[string]struct{}),
				}
				interactionHubs[interactionID] = hub
			}
			hub.matchedEvents++
			hub.eventTypes[ev.Type] = struct{}{}
			if toolCallID := dataString(ev.Data, "toolCallId"); toolCallID != "" {
				hub.toolCalls[toolCallID] = struct{}{}
			}
		}

		toolCallID := dataString(ev.Data, "toolCallId")
		if toolCallID == "" {
			continue
		}
		vertex := toolCalls[toolCallID]
		if vertex == nil {
			vertex = &toolCallVertexAccumulator{
				EventTypes: make(map[string]struct{}),
			}
			toolCalls[toolCallID] = vertex
		}
		if vertex.ParentToolCallID == "" {
			vertex.ParentToolCallID = dataString(ev.Data, "parentToolCallId")
		}
		if vertex.ToolName == "" {
			vertex.ToolName = dataString(ev.Data, "toolName")
			if vertex.ToolName == "" {
				vertex.ToolName = ctx.toolNames[toolCallID]
			}
		}
		if vertex.InteractionID == "" {
			vertex.InteractionID = resolveHistoryInteractionID(ctx, ev)
		}
		vertex.EventTypes[ev.Type] = struct{}{}
	}

	summary := &sessionGraphSummary{
		SessionID:           sessionID,
		EventVertices:       len(ctx.events),
		InteractionVertices: len(interactionHubs),
		ToolCallVertices:    len(toolCalls),
	}

	for _, ev := range ctx.events {
		if ev.ParentID != "" {
			summary.RowsWithParentID++
			if _, ok := ctx.eventMap[ev.ParentID]; ok {
				summary.EventParentEdges++
			} else {
				summary.MissingParentEventRows++
				missingParentTypeCounts[ev.Type]++
			}
		}
		if dataString(ev.Data, "interactionId") != "" {
			summary.EventInteractionEdges++
		}
		if dataString(ev.Data, "toolCallId") != "" {
			summary.EventToolCallEdges++
		}
		if parentToolCallID := dataString(ev.Data, "parentToolCallId"); parentToolCallID != "" {
			summary.RowsWithParentToolCallID++
			if _, ok := toolCalls[parentToolCallID]; !ok {
				summary.MissingParentToolCallRows++
			}
		}
	}

	nestedParents := make(map[string]*nestedToolParentAccumulator)
	for childToolCallID, child := range toolCalls {
		if child.ParentToolCallID == "" {
			continue
		}
		parent, ok := toolCalls[child.ParentToolCallID]
		if !ok {
			continue
		}
		summary.ToolCallParentEdges++

		acc := nestedParents[child.ParentToolCallID]
		if acc == nil {
			parentToolName := parent.ToolName
			if parentToolName == "" {
				parentToolName = ctx.toolNames[child.ParentToolCallID]
			}
			acc = &nestedToolParentAccumulator{
				ParentToolName:  parentToolName,
				ChildToolCalls:  make(map[string]struct{}),
				ChildTools:      make(map[string]int),
				ChildEventTypes: make(map[string]struct{}),
			}
			if acc.ParentToolName == "" {
				acc.ParentToolName = "<unknown>"
			}
			nestedParents[child.ParentToolCallID] = acc
		}
		if acc.InteractionID == "" {
			if child.InteractionID != "" {
				acc.InteractionID = child.InteractionID
			} else {
				acc.InteractionID = parent.InteractionID
			}
		}

		acc.ChildToolCalls[childToolCallID] = struct{}{}
		childToolName := child.ToolName
		if childToolName == "" {
			childToolName = "<unknown>"
		}
		acc.ChildTools[childToolName]++
		for eventType := range child.EventTypes {
			acc.ChildEventTypes[eventType] = struct{}{}
		}
	}

	summary.MissingParentTypes = sortSessionEventTypeCounts(missingParentTypeCounts)
	summary.InteractionHubs = sortSessionInteractionHubs(interactionHubs)
	summary.NestedToolParents = sortSessionNestedToolParents(nestedParents)
	return summary, nil
}

func buildTurnWindows(events []*sessionEvent) []*sessionTurnWindow {
	if len(events) == 0 {
		return nil
	}

	eventByID := make(map[string]*sessionEvent, len(events))
	openTurnsByID := make(map[string][]*sessionTurnWindow)
	segmentNumber := 0
	turns := make([]*sessionTurnWindow, 0)

	for _, ev := range events {
		if ev.ID != "" {
			eventByID[ev.ID] = ev
		}

		switch ev.Type {
		case "session.start", "session.resume":
			segmentNumber++
			openTurnsByID = make(map[string][]*sessionTurnWindow)
		case "assistant.turn_start":
			if segmentNumber == 0 {
				segmentNumber = 1
			}
			turn := &sessionTurnWindow{
				TurnNumber:    len(turns) + 1,
				SegmentNumber: segmentNumber,
				TurnID:        dataString(ev.Data, "turnId"),
				InteractionID: dataString(ev.Data, "interactionId"),
				ParentEventID: ev.ParentID,
				StartTime:     ev.Timestamp,
				ModelCalls:    make(map[string]int),
				ToolCalls:     make(map[string]int),
				startEventID:  ev.ID,
			}
			turns = append(turns, turn)
			openTurnsByID[turn.TurnID] = append(openTurnsByID[turn.TurnID], turn)
		case "assistant.turn_end":
			turnID := dataString(ev.Data, "turnId")
			queue := openTurnsByID[turnID]
			if len(queue) == 0 {
				continue
			}
			turn := queue[0]
			turn.EndTime = &ev.Timestamp
			turn.endEventID = ev.ID
			if len(queue) == 1 {
				delete(openTurnsByID, turnID)
			} else {
				openTurnsByID[turnID] = queue[1:]
			}
		}
	}

	lastEventTime := events[len(events)-1].Timestamp
	for _, turn := range turns {
		if parent := eventByID[turn.ParentEventID]; parent != nil && parent.Type == "user.message" {
			turn.ParentUserEventID = parent.ID
			turn.UserMessage = eventText(parent.Data)
		}

		windowEnd := turn.effectiveEnd(lastEventTime)
		for _, ev := range events {
			if ev.Timestamp.Before(turn.StartTime) || ev.Timestamp.After(windowEnd) {
				continue
			}
			if ev.Type != "session.shutdown" && ev.Timestamp.After(turn.lastActivityTime) {
				turn.lastActivityTime = ev.Timestamp
			}
			switch ev.Type {
			case "assistant.message":
				if text := eventText(ev.Data); text != "" {
					turn.AssistantMessages = append(turn.AssistantMessages, text)
				}
			case "tool.execution_start":
				if toolName := dataString(ev.Data, "toolName"); toolName != "" {
					turn.ToolCalls[toolName]++
				}
			case "tool.execution_complete":
				if model := dataString(ev.Data, "model"); model != "" {
					turn.ModelCalls[model]++
				}
			case "skill.invoked":
				turn.SkillEvents++
			case "subagent.started", "subagent.completed":
				turn.SubagentEvents++
			case "session.plan_changed":
				turn.PlanChangeEvents++
			case "abort":
				turn.AbortEvents++
			}
		}

		if len(turn.AssistantMessages) > 0 {
			turn.Summary = turn.AssistantMessages[0]
		} else {
			turn.Summary = turn.UserMessage
		}

		switch {
		case turn.EndTime != nil:
			turn.State = "Complete"
		case turn.AbortEvents > 0:
			turn.State = "Aborted"
		default:
			turn.State = "Open"
		}
	}

	return turns
}

func eventDepth(id string, eventMap map[string]*sessionEvent, cache map[string]int) int {
	if depth, ok := cache[id]; ok {
		return depth
	}

	depth := 0
	current := eventMap[id]
	for current != nil && current.ParentID != "" && depth < 8 {
		depth++
		parent, ok := eventMap[current.ParentID]
		if !ok {
			break
		}
		current = parent
	}

	cache[id] = depth
	return depth
}

func showHistoryOld(sessionID string, format string) {
	showHistoryNew(sessionID, format)
}

func showHistoryNew(sessionID string, format string) {
	if format == "yaml" {
		rawEvents, err := loadSessionRawEvents(sessionID)
		if err != nil {
			log.Printf("%v", err)
			return
		}
		printYAML(rawEvents)
		return
	}

	events, err := loadSessionEvents(sessionID)
	if err != nil {
		log.Printf("%v", err)
		return
	}

	rows, err := buildHistoryRows(events)
	if err != nil {
		log.Printf("No parsable events found for session %s", sessionID)
		return
	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "Time\tDelta\tEvent")
	lastInteractionID := ""
	for _, row := range rows {
		if row.InteractionID != "" && row.InteractionID != lastInteractionID {
			fmt.Fprintf(writer, "\t\t%s\n", formatHistoryEventText(0, "Interaction", row.InteractionID))
			lastInteractionID = row.InteractionID
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\n", row.Time, row.Delta, formatHistoryEventText(row.Depth, row.Label, row.Detail))
		for _, extraLine := range row.ExtraLines {
			fmt.Fprintf(writer, "\t\t%s\n", formatHistoryExtraLine(row.Depth, extraLine))
		}
	}
	if err := writer.Flush(); err != nil {
		log.Printf("Error writing history output: %v", err)
	}
}

func formatHistoryTurnDetail(row historySpanProjectionRow) string {
	detail := fmt.Sprintf("#%d, Segment %d, Turn ID %s", row.TurnNumber, row.SegmentNumber, row.TurnID)
	if row.TurnState != "" {
		detail = fmt.Sprintf("%s, %s", detail, row.TurnState)
	}
	return detail
}

func historyTurnRowDepth(row historySpanProjectionRow) int {
	depth := row.Depth + 1
	if depth < 2 {
		return 2
	}
	return depth
}

func historyTurnHeaderDepth() int {
	return 1
}

func historySpanDisplayDepth(row historySpanProjectionRow, groupBy string) int {
	if groupBy != historyGroupByTurn {
		return row.Depth
	}
	switch row.Label {
	case "User":
		return 1
	case "Assistant Turn Start", "Assistant Turn End":
		return historyTurnHeaderDepth()
	default:
		if row.TurnNumber != 0 {
			return historyTurnRowDepth(row)
		}
		return row.Depth
	}
}

func historyTurnKey(row historySpanProjectionRow) string {
	return fmt.Sprintf("%d/%d/%s", row.TurnNumber, row.SegmentNumber, row.TurnID)
}

func showHistorySpans(sessionID string, format string, groupBy string) {
	events, err := loadSessionEvents(sessionID)
	if err != nil {
		log.Printf("%v", err)
		return
	}

	rows, err := buildHistorySpanProjectionRows(events)
	if err != nil {
		log.Printf("No parsable events found for session %s", sessionID)
		return
	}

	if format == "yaml" {
		printYAML(rows)
		return
	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if groupBy == historyGroupByTurn {
		fmt.Fprintln(writer, "Time\tSpan\tStructure\tDetail")
	} else {
		fmt.Fprintln(writer, "Time\tSpan\tEvent")
	}
	lastInteractionID := ""
	lastUserEventID := ""
	lastTurnKey := ""
	for _, row := range rows {
		if row.InteractionID != "" && row.InteractionID != lastInteractionID {
			if groupBy == historyGroupByTurn {
				fmt.Fprintf(writer, "\t\t%s\t%s\n", formatHistoryEventLabel(0, "Interaction"), row.InteractionID)
			} else {
				fmt.Fprintf(writer, "\t\t%s\n", formatHistoryEventText(0, "Interaction", row.InteractionID))
			}
			lastInteractionID = row.InteractionID
			lastUserEventID = ""
			lastTurnKey = ""
		}
		if groupBy == historyGroupByTurn && row.UserEventID != "" && row.UserEventID != lastUserEventID && row.Label != "User" {
			fmt.Fprintf(writer, "\t\t%s\t%s\n", formatHistoryEventLabel(1, "User"), row.UserText)
			lastUserEventID = row.UserEventID
			lastTurnKey = ""
		}
		if groupBy == historyGroupByTurn && row.TurnNumber != 0 {
			turnKey := historyTurnKey(row)
			if row.Label == "Assistant Turn Start" {
				if turnKey != lastTurnKey {
					fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", row.Timestamp.Local().Format("15:04:05.000"), row.TurnDuration, formatHistoryEventLabel(historySpanDisplayDepth(row, groupBy), "Turn"), formatHistoryTurnDetail(row))
					lastTurnKey = turnKey
				}
				continue
			}
			if row.Label == "Assistant Turn End" {
				continue
			}
		}
		displayDepth := historySpanDisplayDepth(row, groupBy)
		if groupBy == historyGroupByTurn {
			fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", row.Timestamp.Local().Format("15:04:05.000"), row.Span, formatHistoryEventLabel(displayDepth, row.Label), row.Detail)
			for _, extraLine := range row.ExtraLines {
				fmt.Fprintf(writer, "\t\t\t%s\n", extraLine)
			}
		} else {
			fmt.Fprintf(writer, "%s\t%s\t%s\n", row.Timestamp.Local().Format("15:04:05.000"), row.Span, formatHistoryEventText(displayDepth, row.Label, row.Detail))
			for _, extraLine := range row.ExtraLines {
				fmt.Fprintf(writer, "\t\t%s\n", formatHistoryExtraLine(displayDepth, extraLine))
			}
		}
		if groupBy == historyGroupByTurn && row.Label == "User" {
			lastUserEventID = row.UserEventID
			lastTurnKey = ""
		}
	}
	if err := writer.Flush(); err != nil {
		log.Printf("Error writing history spans output: %v", err)
	}
}

func showGraph(ctx context.Context, client *copilot.Client, sessionID string, format string) {
	_ = ctx
	_ = client

	events, err := loadSessionEvents(sessionID)
	if err != nil {
		log.Printf("%v", err)
		return
	}

	summary, err := buildSessionGraphSummary(sessionID, events)
	if err != nil {
		log.Printf("%v", err)
		return
	}

	if format == "yaml" {
		printYAML(summary)
		return
	}

	fmt.Printf("--- Event Graph for Session: %s ---\n", sessionID)
	summaryTable := createTable([]string{"Metric", "Value"}, []int{1}, false, false)
	summaryTable.Append([]string{"Event vertices", strconv.Itoa(summary.EventVertices)})
	summaryTable.Append([]string{"Interaction vertices", strconv.Itoa(summary.InteractionVertices)})
	summaryTable.Append([]string{"Tool call vertices", strconv.Itoa(summary.ToolCallVertices)})
	summaryTable.Append([]string{"Event parent edges", strconv.Itoa(summary.EventParentEdges)})
	summaryTable.Append([]string{"Interaction edges", strconv.Itoa(summary.EventInteractionEdges)})
	summaryTable.Append([]string{"Tool call edges", strconv.Itoa(summary.EventToolCallEdges)})
	summaryTable.Append([]string{"Tool call parent edges", strconv.Itoa(summary.ToolCallParentEdges)})
	summaryTable.Append([]string{"Rows with parent ID", strconv.Itoa(summary.RowsWithParentID)})
	summaryTable.Append([]string{"Missing parent event rows", strconv.Itoa(summary.MissingParentEventRows)})
	summaryTable.Append([]string{"Rows with parent tool call ID", strconv.Itoa(summary.RowsWithParentToolCallID)})
	summaryTable.Append([]string{"Missing parent tool call rows", strconv.Itoa(summary.MissingParentToolCallRows)})
	summaryTable.Render()

	if len(summary.MissingParentTypes) > 0 {
		fmt.Println("\nMissing Parent Event Types:")
		table := createTable([]string{"Event Type", "Rows"}, []int{1}, false, false)
		for _, item := range summary.MissingParentTypes {
			table.Append([]string{item.EventType, strconv.Itoa(item.Rows)})
		}
		table.Render()
	}

	if len(summary.InteractionHubs) > 0 {
		fmt.Println("\nInteraction Hubs:")
		table := createTable([]string{"Interaction", "Events", "Tool Calls", "Event Types"}, []int{1, 2}, false, false)
		for i, hub := range summary.InteractionHubs {
			if i >= 10 {
				break
			}
			table.Append([]string{
				hub.InteractionID,
				strconv.Itoa(hub.MatchedEvents),
				strconv.Itoa(hub.ToolCalls),
				strings.Join(hub.EventTypes, ", "),
			})
		}
		table.Render()
		if len(summary.InteractionHubs) > 10 {
			fmt.Printf("Showing top 10 of %d interaction hubs.\n", len(summary.InteractionHubs))
		}
	}

	if len(summary.NestedToolParents) > 0 {
		fmt.Println("\nNested Tool Parents:")
		table := createTable([]string{"Parent Call", "Parent Tool", "Child Calls", "Child Tools", "Child Event Types", "Interaction"}, []int{2}, false, false)
		for i, parent := range summary.NestedToolParents {
			if i >= 10 {
				break
			}
			interactionID := parent.InteractionID
			if interactionID == "" {
				interactionID = "-"
			}
			table.Append([]string{
				parent.ParentToolCallID,
				parent.ParentToolName,
				strconv.Itoa(parent.ChildToolCalls),
				formatNamedCounts(parent.ChildTools),
				strings.Join(parent.ChildEventTypes, ", "),
				interactionID,
			})
		}
		table.Render()
		if len(summary.NestedToolParents) > 10 {
			fmt.Printf("Showing top 10 of %d nested tool parent groups.\n", len(summary.NestedToolParents))
		}
	}

	fmt.Println("\nNotes:")
	fmt.Println("- parentId and parentToolCallId describe different relationships; both are shown separately.")
	fmt.Println("- Interaction hubs count only direct interactionId edges from the event payload.")
	fmt.Println("- Nested tool parents are resolved from parentToolCallId when the parent tool call also exists in the same local log.")
}

func showTurnsV2(sessionID string, format string) {
	events, err := loadSessionEvents(sessionID)
	if err != nil {
		log.Printf("%v", err)
		return
	}
	if len(events) == 0 {
		log.Printf("No parsable events found for session %s", sessionID)
		return
	}

	turns := buildTurnWindows(events)
	if format == "yaml" {
		printYAML(turns)
		return
	}

	fmt.Printf("--- Turn Usage for Session: %s ---\n", sessionID)
	header := []string{"Turn #", "Segment", "Turn ID", "Start Time", "Duration", "State", "Model Calls", "Tools", "Summary"}
	table := createTable(header, []int{0, 1}, false, false)
	lastEventTime := events[len(events)-1].Timestamp
	segments := make(map[int]struct{})

	for _, turn := range turns {
		segments[turn.SegmentNumber] = struct{}{}
		summary := turn.Summary
		if summary == "" {
			summary = "-"
		} else {
			summary = truncateRunes(summary, 60)
		}

		table.Append([]string{
			strconv.Itoa(turn.TurnNumber),
			strconv.Itoa(turn.SegmentNumber),
			turn.TurnID,
			turn.StartTime.Local().Format("15:04:05"),
			turn.durationString(lastEventTime),
			turn.State,
			formatCountSummary(turn.ModelCalls),
			formatCountSummary(turn.ToolCalls),
			summary,
		})
	}
	table.Render()

	fmt.Println("\nNotes:")
	fmt.Println("- 'Turn #' is chronological within the session; raw 'Turn ID' can repeat.")
	if len(segments) > 1 {
		fmt.Println("- 'Segment' increments on session.start or session.resume.")
	}
	fmt.Println("- 'State' is derived from local turn_end and abort events, so active sessions can show Open turns.")
}

func newTurnsCmd(client *copilot.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "turns [sessionID]",
		Short: "Show turn-by-turn usage statistics for a session",
		Args:  cobra.MaximumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			sessionID, err := resolveSessionID(cmd.Context(), client, args)
			if err != nil {
				log.Printf("%v", err)
				return
			}
			showTurns(sessionID, outputFormat)
		},
	}
}

func showTurns(sessionID string, format string) {
	showTurnsV2(sessionID, format)
}

func newStatsCmd() *cobra.Command {
	var showAllHistory bool
	var showAPICosts bool
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show aggregate usage statistics from local session history",
		Run: func(cmd *cobra.Command, args []string) {
			showStats(outputFormat, showAllHistory, showAPICosts)
		},
	}
	cmd.Flags().BoolVarP(&showAllHistory, "all", "a", false, "Show statistics for all time (default: current month UTC)")
	cmd.Flags().BoolVar(&showAPICosts, "api-costs", false, "Estimate equivalent API costs from token usage")
	return cmd
}

func showStats(format string, showAllHistory bool, showAPICosts bool) {
	home, _ := os.UserHomeDir()
	stateDir := filepath.Join(home, ".copilot", "session-state")
	entries, _ := os.ReadDir(stateDir)

	stats := make(map[string]*statsModelStat)
	var totalPremiumRequests float64

	now := time.Now().UTC()
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		eventsPath := filepath.Join(stateDir, entry.Name(), "events.jsonl")
		if _, err := os.Stat(eventsPath); err != nil {
			continue
		}
		hasShutdown, err := sessionHasShutdown(eventsPath)
		if err != nil {
			log.Printf("Error reading %s: %v", eventsPath, err)
			continue
		}
		if err := visitJSONLObjects(eventsPath, func(ev map[string]any) error {
			if !showAllHistory {
				timestampStr, _ := ev["timestamp"].(string)
				ts, err := time.Parse(time.RFC3339, timestampStr)
				if err == nil && ts.Before(startOfMonth) {
					return nil
				}
			}

			data, _ := ev["data"].(map[string]any)
			if data == nil {
				return nil
			}

			if ev["type"] == "session.shutdown" {
				if total, ok := data["totalPremiumRequests"].(float64); ok {
					totalPremiumRequests += total
				}
				if metrics, ok := data["modelMetrics"].(map[string]any); ok {
					for model, m := range metrics {
						if mv, ok := m.(map[string]any); ok {
							if _, ok := stats[model]; !ok {
								stats[model] = &statsModelStat{}
							}
							s := stats[model]
							if reqs, ok := mv["requests"].(map[string]any); ok {
								count, _ := reqs["count"].(float64)
								cost, _ := reqs["cost"].(float64)
								s.Requests += int64(count)
								s.Cost += cost
							}
							if usage, ok := mv["usage"].(map[string]any); ok {
								in, _ := usage["inputTokens"].(float64)
								cacheRead, _ := usage["cacheReadTokens"].(float64)
								cacheWrite, _ := usage["cacheWriteTokens"].(float64)
								out, _ := usage["outputTokens"].(float64)
								s.Input += int64(in)
								s.CacheRead += int64(cacheRead)
								s.CacheWrite += int64(cacheWrite)
								s.Output += int64(out)
							}
						}
					}
				}
			} else if !hasShutdown && ev["type"] == "tool.execution_complete" {
				model, _ := data["model"].(string)
				if model != "" {
					if _, ok := stats[model]; !ok {
						stats[model] = &statsModelStat{}
					}
					stats[model].Requests++
				}
			}
			return nil
		}); err != nil {
			log.Printf("Error processing %s: %v", eventsPath, err)
			continue
		}
	}

	var totalEstimatedAPICostUSD float64
	var pricedModels []string
	var partiallyPricedModels []string
	var modelsWithoutAPIPricing []string
	var modelsWithoutTokenUsage []string
	hasCacheReadTokens := false
	hasCacheWriteTokens := false

	var models []string
	for m := range stats {
		models = append(models, m)
	}
	sort.Strings(models)

	for _, model := range models {
		s := stats[model]
		s.EstimatedOverageCostUSD = s.Cost * 0.04
		if s.CacheRead > 0 {
			hasCacheReadTokens = true
		}
		if s.CacheWrite > 0 {
			hasCacheWriteTokens = true
		}
		if !showAPICosts {
			continue
		}
		switch {
		case s.Input == 0 && s.CacheRead == 0 && s.CacheWrite == 0 && s.Output == 0:
			modelsWithoutTokenUsage = append(modelsWithoutTokenUsage, model)
		default:
			s.EstimatedAPICost = estimateAPICost(model, s)
			if s.EstimatedAPICost == nil {
				modelsWithoutAPIPricing = append(modelsWithoutAPIPricing, model)
				continue
			}
			totalEstimatedAPICostUSD += s.EstimatedAPICost.TotalUSD
			if s.EstimatedAPICost.IsComplete {
				pricedModels = append(pricedModels, model)
			} else {
				partiallyPricedModels = append(partiallyPricedModels, model)
			}
		}
	}

	if format == "yaml" {
		payload := map[string]any{
			"totalPremiumRequests":    totalPremiumRequests,
			"estimatedOverageCostUsd": totalPremiumRequests * 0.04,
			"modelStats":              stats,
			"isCurrentMonthOnly":      !showAllHistory,
		}
		if showAPICosts {
			payload["estimatedApiCostUsd"] = totalEstimatedAPICostUSD
			payload["priceCatalogVersion"] = apiPricingCatalogVersion
			payload["pricedModels"] = pricedModels
			payload["partiallyPricedModels"] = partiallyPricedModels
			payload["modelsWithoutApiPricing"] = modelsWithoutAPIPricing
			payload["modelsWithoutTokenUsage"] = modelsWithoutTokenUsage
			payload["apiPricingSources"] = []string{
				"https://developers.openai.com/api/docs/pricing",
				"https://platform.claude.com/docs/en/about-claude/pricing",
			}
			payload["apiPricingAssumptions"] = []string{
				"Estimates use hardcoded public API prices keyed by model ID.",
				"Model availability is plan-dependent; local shutdown metrics can still contain model IDs that are not currently visible in `copilot-show models`.",
				"OpenAI pricing uses the standard short-context tier; long-context, regional, and batch adjustments are not modeled.",
				"Anthropic cache reads use published cache-hit prices; cache writes are not priced because duration is not persisted in session logs.",
				"Active session tails without session.shutdown contribute request counts but not token-based costs.",
			}
			payload["modelCatalogSource"] = "https://docs.github.com/en/copilot/reference/ai-models/supported-models#model-multipliers"
		}
		printYAML(payload)
		return
	}

	title := "Total Premium Requests (Current Month UTC): %s\n\n"
	if showAllHistory {
		title = "Total Premium Requests (All Local History): %s\n\n"
	}
	fmt.Printf(title, formatFloatCompact(totalPremiumRequests))

	if len(stats) == 0 {
		fmt.Println("No detailed model statistics found for the selected period.")
		return
	}

	totalCostUSD := float64(totalPremiumRequests) * 0.04

	header := []string{"Model", "Requests", "Premium Requests (Cost)", "Input Tokens"}
	if showAPICosts && hasCacheReadTokens {
		header = append(header, "Cache Read Tokens")
	}
	if showAPICosts && hasCacheWriteTokens {
		header = append(header, "Cache Write Tokens")
	}
	header = append(header, "Output Tokens", "Est. Overage Cost")
	if showAPICosts {
		header = append(header, "Est. API Cost")
	}
	var rightAlignedCols []int
	for i := 1; i < len(header); i++ {
		rightAlignedCols = append(rightAlignedCols, i)
	}
	table := createTable(header, rightAlignedCols, false, false)

	for _, m := range models {
		s := stats[m]
		overageEst := formatUSD(s.EstimatedOverageCostUSD)
		if s.Cost == 0 {
			overageEst = "-"
		}
		row := []string{
			m,
			strconv.FormatInt(s.Requests, 10),
			formatFloatCompact(s.Cost),
			strconv.FormatInt(s.Input, 10),
		}
		if showAPICosts && hasCacheReadTokens {
			row = append(row, strconv.FormatInt(s.CacheRead, 10))
		}
		if showAPICosts && hasCacheWriteTokens {
			row = append(row, strconv.FormatInt(s.CacheWrite, 10))
		}
		row = append(row, strconv.FormatInt(s.Output, 10), overageEst)
		if showAPICosts {
			apiCost := "-"
			if s.EstimatedAPICost != nil {
				apiCost = formatUSD(s.EstimatedAPICost.TotalUSD)
				if !s.EstimatedAPICost.IsComplete {
					apiCost = ">= " + apiCost
				}
			}
			row = append(row, apiCost)
		}
		table.Append(row)
	}
	table.Render()
	if !showAllHistory {
		fmt.Printf("\nEstimated Total Overage Cost (if quota is exhausted): %s USD\n", formatUSD(totalCostUSD))
	} else {
		fmt.Printf("\nEstimated Total Overage Cost (across all history): %s USD\n", formatUSD(totalCostUSD))
	}
	if showAPICosts {
		label := "Estimated Total API Cost (priced closed segments): %s USD\n"
		if len(partiallyPricedModels) > 0 || len(modelsWithoutAPIPricing) > 0 {
			label = "Estimated Total API Cost (lower bound from priced closed segments): %s USD\n"
		}
		fmt.Printf(label, formatUSD(totalEstimatedAPICostUSD))
	}
	fmt.Println("Notes:")
	fmt.Println("- Overage cost uses $0.04 USD per premium request.")
	fmt.Println("- `Premium Requests (Cost)` can be fractional because model multipliers are preserved from session shutdown metrics.")
	if showAPICosts {
		fmt.Println("- API cost uses hardcoded public token prices from OpenAI and Anthropic docs.")
		fmt.Println("- Model availability is plan-dependent; local shutdown metrics can still contain model IDs that are not currently visible in `copilot-show models`.")
		fmt.Println("- `Cache Read Tokens` are billed at cache-hit prices when the selected model has a verified cached-input rate.")
		if hasCacheWriteTokens {
			fmt.Println("- `Cache Write Tokens` are shown separately. If a model lacks a verified write price, its API estimate becomes a lower bound.")
		} else {
			fmt.Println("- `Cache Write Tokens` are currently zero in the selected local history, so write pricing did not affect this estimate.")
		}
		fmt.Println("- OpenAI estimates use the standard short-context tier. Long-context, regional, fast-mode, batch, and tool-call surcharges are not modeled.")
		fmt.Println("- Active session tails without `session.shutdown` can contribute request counts, but not token-based API cost estimates.")
		if len(modelsWithoutAPIPricing) > 0 {
			fmt.Printf("- Models without hardcoded API pricing: %s\n", strings.Join(modelsWithoutAPIPricing, ", "))
		}
		if len(modelsWithoutTokenUsage) > 0 {
			fmt.Printf("- Models with request counts but no shutdown token usage yet: %s\n", strings.Join(modelsWithoutTokenUsage, ", "))
		}
	}
}

func getTerminalWidth() int {
	size, err := ts.GetSize()
	if err != nil || size.Col() <= 0 {
		return 80 // Default fallback
	}
	return size.Col()
}
