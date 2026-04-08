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
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/apstndb/copilot-show/pkg/analyze"
	"github.com/apstndb/copilot-show/pkg/modeldocs"
	"github.com/apstndb/copilot-show/pkg/render"
	"github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
	"github.com/goccy/go-yaml"
	"github.com/maruel/natural"
	"github.com/olekukonko/tablewriter"
	"github.com/olekukonko/ts"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	uiVersionOld = "old"
	uiVersionNew = "new"

	historyViewRaw   = "raw"
	historyViewSpans = "spans"

	historyGroupByNone = "none"
	historyGroupByTurn = "turn"
)

var (
	// version can be injected at build time with:
	//   go build -ldflags "-X main.version=v0.1.6"
	version      string
	outputFormat string
	tableMode    string
	uiVersion    string
)

func cliVersion() string {
	info, ok := debug.ReadBuildInfo()
	return resolveVersion(version, info, ok)
}

func resolveVersion(explicit string, info *debug.BuildInfo, ok bool) string {
	if explicit != "" {
		return explicit
	}
	if !ok || info == nil {
		return "(unknown)"
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	revision := buildSettingValue(info, "vcs.revision")
	if revision != "" {
		if len(revision) > 12 {
			revision = revision[:12]
		}
		if buildSettingValue(info, "vcs.modified") == "true" {
			return revision + "-dirty"
		}
		return revision
	}
	if info.Main.Version != "" {
		return info.Main.Version
	}
	return "(unknown)"
}

func buildSettingValue(info *debug.BuildInfo, key string) string {
	if info == nil {
		return ""
	}
	for _, setting := range info.Settings {
		if setting.Key == key {
			return setting.Value
		}
	}
	return ""
}

type hiddenCommandState struct {
	command *cobra.Command
	hidden  bool
}

type hiddenFlagState struct {
	flag   *pflag.Flag
	hidden bool
}

func withVisibleHiddenHelp(cmd *cobra.Command, enabled bool, fn func()) {
	if !enabled || cmd == nil {
		fn()
		return
	}

	root := cmd.Root()
	var commandStates []hiddenCommandState
	var flagStates []hiddenFlagState
	seenFlags := make(map[*pflag.Flag]struct{})
	collectFlagState := func(set *pflag.FlagSet) {
		if set == nil {
			return
		}
		set.VisitAll(func(flag *pflag.Flag) {
			if flag == nil {
				return
			}
			if _, ok := seenFlags[flag]; ok {
				return
			}
			seenFlags[flag] = struct{}{}
			flagStates = append(flagStates, hiddenFlagState{flag: flag, hidden: flag.Hidden})
			flag.Hidden = false
		})
	}

	var walk func(*cobra.Command)
	walk = func(current *cobra.Command) {
		commandStates = append(commandStates, hiddenCommandState{command: current, hidden: current.Hidden})
		current.Hidden = false
		collectFlagState(current.PersistentFlags())
		collectFlagState(current.Flags())
		collectFlagState(current.LocalFlags())
		collectFlagState(current.InheritedFlags())
		collectFlagState(current.NonInheritedFlags())
		for _, child := range current.Commands() {
			walk(child)
		}
	}
	walk(root)

	defer func() {
		for i := len(flagStates) - 1; i >= 0; i-- {
			flagStates[i].flag.Hidden = flagStates[i].hidden
		}
		for i := len(commandStates) - 1; i >= 0; i-- {
			commandStates[i].command.Hidden = commandStates[i].hidden
		}
	}()

	fn()
}

func configureShowHiddenHelp(root *cobra.Command, showHidden *bool) {
	defaultHelpFunc := root.HelpFunc()
	var wrap func(*cobra.Command)
	wrap = func(cmd *cobra.Command) {
		cmd.SetHelpFunc(func(current *cobra.Command, args []string) {
			withVisibleHiddenHelp(current, showHidden != nil && *showHidden, func() {
				defaultHelpFunc(current, args)
			})
		})
		for _, child := range cmd.Commands() {
			wrap(child)
		}
	}
	wrap(root)
}

func main() {
	// 1. Initialize Copilot CLI client
	client := copilot.NewClient(nil)
	ctx := context.Background()
	if err := client.Start(ctx); err != nil {
		log.Fatalf("Failed to start client: %v", err)
	}
	defer client.Stop()

	var showHiddenHelp bool
	rootCmd := &cobra.Command{
		Use:     "copilot-show",
		Short:   "A tool to inspect GitHub Copilot information",
		Version: cliVersion(),
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
	rootCmd.PersistentFlags().BoolVar(&showHiddenHelp, "show-hidden", false, "Include hidden commands and hidden flags in help output")
	rootCmd.PersistentFlags().StringVar(&uiVersion, "ui-version", uiVersionNew, "Hidden UI selector for temporary A/B testing (old, new)")
	if err := rootCmd.PersistentFlags().MarkHidden("ui-version"); err != nil {
		log.Fatalf("Failed to hide ui-version flag: %v", err)
	}

	rootCmd.AddCommand(newQuotaCmd(client))
	rootCmd.AddCommand(newModelsCmd(client))
	rootCmd.AddCommand(newModelDocsCmd(client))
	rootCmd.AddCommand(newToolsCmd(client))
	rootCmd.AddCommand(newStatsCmd())
	rootCmd.AddCommand(newUsageCmd(client))
	rootCmd.AddCommand(newTurnsCmd(client))

	hiddenCmds := []*cobra.Command{
		newAgentsCmd(client),
		newSkillsCmd(client),
		newExtensionsCmd(client),
		newPluginsCmd(client),
		newMcpCmd(client),
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
		newResumeBranchesCmd(client),
		newValidateEventsCmd(client),
	}
	for _, c := range hiddenCmds {
		c.Hidden = true
		rootCmd.AddCommand(c)
	}
	configureShowHiddenHelp(rootCmd, &showHiddenHelp)

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
	table := render.CreateTable(header, []int{1, 2, 3, 4}, false, false, tableMode)

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

	snapshot := modeldocs.BuildSnapshot(models.Models)
	for _, warning := range snapshot.LoadWarnings {
		fmt.Fprintf(os.Stderr, "Warning: %s\n", warning)
	}
	modelSnapshot := buildRuntimeModelsSnapshot(models.Models, snapshot)

	if format == "yaml" {
		printYAML(modelSnapshot)
		return
	}

	header := []string{"ID", "Name", "Multiplier", "Context", "Output", "Prompt", "Vision", "Reasoning", "Efforts", "State"}
	table := render.CreateTable(header, []int{2, 3, 4, 5}, false, false, tableMode)

	for _, model := range modelSnapshot.Models {
		table.Append([]string{
			model.ID,
			model.Name,
			model.MultiplierDisplay,
			fmt.Sprintf("%.0f", model.MaxContextWindowTokens),
			formatOptionalTokenLimit(model.MaxOutputTokens),
			formatOptionalTokenLimit(model.MaxPromptTokens),
			boolYesNo(model.SupportsVision),
			formatReasoningSupport(model.SupportsReasoning, model.DefaultReasoningEffort),
			formatReasoningEfforts(model.SupportedReasoningEfforts),
			formatOptionalText(model.State),
		})
	}
	table.Render()
	fmt.Println("\nNotes:")
	fmt.Println("- `Multiplier` prefers the docs-backed paid multiplier from github/docs for docs-known models.")
	fmt.Println("- Runtime-only models without a docs match fall back to the live Copilot SDK billing multiplier.")
	fmt.Printf("- Multiplier catalog version: %s (%s)\n", modelSnapshot.ModelCatalogVersion, modelSnapshot.ModelCatalogLoadedFrom)
	fmt.Println("- Free-plan multipliers are loaded too, but `models` currently displays the paid-plan column.")
	if len(modelSnapshot.ModelCatalogWarnings) > 0 {
		fmt.Println("- Warnings:")
		for _, warning := range modelSnapshot.ModelCatalogWarnings {
			fmt.Printf("  - %s\n", warning)
		}
	}
}

type runtimeModelsSnapshot struct {
	ModelCatalogSource     string             `json:"modelCatalogSource" yaml:"modelCatalogSource"`
	ModelCatalogVersion    string             `json:"modelCatalogVersion,omitempty" yaml:"modelCatalogVersion,omitempty"`
	ModelCatalogLoadedFrom string             `json:"modelCatalogLoadedFrom,omitempty" yaml:"modelCatalogLoadedFrom,omitempty"`
	ModelCatalogWarnings   []string           `json:"modelCatalogWarnings,omitempty" yaml:"modelCatalogWarnings,omitempty"`
	MultiplierPlan         string             `json:"multiplierPlan" yaml:"multiplierPlan"`
	Models                 []runtimeModelView `json:"models" yaml:"models"`
}

type runtimeModelView struct {
	ID                        string                        `json:"id" yaml:"id"`
	Name                      string                        `json:"name" yaml:"name"`
	MultiplierDisplay         string                        `json:"multiplierDisplay" yaml:"multiplierDisplay"`
	SelectedMultiplier        *float64                      `json:"selectedMultiplier,omitempty" yaml:"selectedMultiplier,omitempty"`
	MultiplierSource          string                        `json:"multiplierSource" yaml:"multiplierSource"`
	DocsMultipliers           *modeldocs.RequestMultipliers `json:"docsMultipliers,omitempty" yaml:"docsMultipliers,omitempty"`
	LiveMultiplier            *float64                      `json:"liveMultiplier,omitempty" yaml:"liveMultiplier,omitempty"`
	MaxContextWindowTokens    float64                       `json:"maxContextWindowTokens" yaml:"maxContextWindowTokens"`
	MaxOutputTokens           *float64                      `json:"maxOutputTokens,omitempty" yaml:"maxOutputTokens,omitempty"`
	MaxPromptTokens           *float64                      `json:"maxPromptTokens,omitempty" yaml:"maxPromptTokens,omitempty"`
	SupportsVision            bool                          `json:"supportsVision" yaml:"supportsVision"`
	SupportsReasoning         bool                          `json:"supportsReasoning" yaml:"supportsReasoning"`
	DefaultReasoningEffort    string                        `json:"defaultReasoningEffort,omitempty" yaml:"defaultReasoningEffort,omitempty"`
	SupportedReasoningEfforts []string                      `json:"supportedReasoningEfforts,omitempty" yaml:"supportedReasoningEfforts,omitempty"`
	State                     string                        `json:"state,omitempty" yaml:"state,omitempty"`
}

func buildRuntimeModelsSnapshot(models []rpc.Model, snapshot modeldocs.Snapshot) runtimeModelsSnapshot {
	docsMultipliers := buildDocsMultiplierMap(snapshot.Models)
	result := runtimeModelsSnapshot{
		ModelCatalogSource:     snapshot.Sources.ModelMultipliers,
		ModelCatalogVersion:    snapshot.CatalogVersion,
		ModelCatalogLoadedFrom: snapshot.LoadedFrom,
		ModelCatalogWarnings:   append([]string(nil), snapshot.LoadWarnings...),
		MultiplierPlan:         "paid",
		Models:                 make([]runtimeModelView, 0, len(models)),
	}
	for _, model := range models {
		docsMultiplier := lookupDocsMultipliers(model, docsMultipliers)
		liveMultiplier := liveBillingMultiplier(model)
		selectedMultiplier, multiplierSource := selectModelMultiplier(docsMultiplier, liveMultiplier)

		supportsVision := model.Capabilities.Supports.Vision != nil && *model.Capabilities.Supports.Vision
		supportsReasoning := model.Capabilities.Supports.ReasoningEffort != nil && *model.Capabilities.Supports.ReasoningEffort

		policyState := ""
		if model.Policy != nil {
			policyState = model.Policy.State
		}

		result.Models = append(result.Models, runtimeModelView{
			ID:                        model.ID,
			Name:                      model.Name,
			MultiplierDisplay:         formatMultiplierValue(selectedMultiplier),
			SelectedMultiplier:        cloneOptionalFloat(selectedMultiplier),
			MultiplierSource:          multiplierSource,
			DocsMultipliers:           cloneModeldocsRequestMultipliers(docsMultiplier),
			LiveMultiplier:            cloneOptionalFloat(liveMultiplier),
			MaxContextWindowTokens:    model.Capabilities.Limits.MaxContextWindowTokens,
			MaxOutputTokens:           cloneOptionalFloat(model.Capabilities.Limits.MaxOutputTokens),
			MaxPromptTokens:           cloneOptionalFloat(model.Capabilities.Limits.MaxPromptTokens),
			SupportsVision:            supportsVision,
			SupportsReasoning:         supportsReasoning,
			DefaultReasoningEffort:    derefString(model.DefaultReasoningEffort),
			SupportedReasoningEfforts: append([]string(nil), model.SupportedReasoningEfforts...),
			State:                     policyState,
		})
	}
	return result
}

func buildDocsMultiplierMap(models []modeldocs.JoinedModel) map[string]*modeldocs.RequestMultipliers {
	multipliers := make(map[string]*modeldocs.RequestMultipliers, len(models))
	for _, model := range models {
		key := modeldocs.NormalizeModelNameKey(model.Name)
		if key == "" || model.Multipliers == nil {
			continue
		}
		multipliers[key] = model.Multipliers
	}
	return multipliers
}

func lookupDocsMultipliers(model rpc.Model, multipliers map[string]*modeldocs.RequestMultipliers) *modeldocs.RequestMultipliers {
	nameKey := modeldocs.NormalizeModelNameKey(model.Name)
	if multipliers[nameKey] != nil {
		return multipliers[nameKey]
	}
	idKey := modeldocs.NormalizeModelNameKey(model.ID)
	if idKey != "" {
		return multipliers[idKey]
	}
	return nil
}

func liveBillingMultiplier(model rpc.Model) *float64 {
	if model.Billing == nil {
		return nil
	}
	multiplier := model.Billing.Multiplier
	return &multiplier
}

func selectModelMultiplier(docsMultiplier *modeldocs.RequestMultipliers, liveMultiplier *float64) (*float64, string) {
	if docsMultiplier != nil && docsMultiplier.Paid != nil {
		return docsMultiplier.Paid, "github/docs paid"
	}
	if liveMultiplier != nil {
		return liveMultiplier, "copilot-sdk live"
	}
	return nil, "unavailable"
}

func cloneModeldocsRequestMultipliers(multipliers *modeldocs.RequestMultipliers) *modeldocs.RequestMultipliers {
	if multipliers == nil {
		return nil
	}
	cloned := &modeldocs.RequestMultipliers{}
	if multipliers.Paid != nil {
		paid := *multipliers.Paid
		cloned.Paid = &paid
	}
	if multipliers.Free != nil {
		free := *multipliers.Free
		cloned.Free = &free
	}
	return cloned
}

func cloneOptionalFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func formatOptionalTokenLimit(value *float64) string {
	if value == nil {
		return "-"
	}
	return fmt.Sprintf("%.0f", *value)
}

func formatReasoningSupport(supported bool, defaultEffort string) string {
	if !supported {
		return "No"
	}
	if defaultEffort == "" {
		return "Yes"
	}
	return fmt.Sprintf("Yes (%s)", defaultEffort)
}

func formatReasoningEfforts(efforts []string) string {
	if len(efforts) == 0 {
		return "-"
	}
	return strings.Join(efforts, ", ")
}

func formatOptionalText(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func newModelDocsCmd(client *copilot.Client) *cobra.Command {
	var useLatest bool
	var showAll bool
	cmd := &cobra.Command{
		Use:   "model-docs",
		Short: "Show CLI-focused model metadata from github/docs and the live CLI list",
		Run: func(cmd *cobra.Command, args []string) {
			showModelDocs(cmd.Context(), client, outputFormat, useLatest, showAll)
		},
	}
	cmd.Flags().BoolVar(&useLatest, "latest", false, "Attempt to fetch the latest github/docs copilot tables before falling back to the embedded snapshot")
	cmd.Flags().BoolVar(&showAll, "all", false, "Include docs-backed metadata that is not specific to Copilot CLI")
	return cmd
}

func showModelDocs(ctx context.Context, client *copilot.Client, format string, useLatest bool, showAll bool) {
	models, err := client.RPC.Models.List(ctx)
	if err != nil {
		log.Printf("Error listing live models: %v", err)
		return
	}

	snapshot, err := modeldocs.BuildSnapshotWithOptions(ctx, models.Models, modeldocs.SnapshotOptions{PreferLatest: useLatest})
	if err != nil {
		log.Printf("Error loading model docs snapshot: %v", err)
		return
	}
	for _, warning := range snapshot.LoadWarnings {
		fmt.Fprintf(os.Stderr, "Warning: %s\n", warning)
	}
	cliModels := copilotCLIModelDocs(snapshot.Models)
	if format == "yaml" {
		if showAll {
			printYAML(snapshot)
		} else {
			printYAML(buildCLIFocusedModelDocsSnapshot(snapshot))
		}
		return
	}

	header := []string{"Name", "Provider", "Release", "Multiplier", "Visible Now", "Supported Plans"}
	displayModels := cliModels
	if showAll {
		header = []string{"Name", "Provider", "Release", "Multiplier", "Copilot CLI", "Visible Now", "Supported Plans", "Agent", "Ask", "Edit", "Task Area"}
		displayModels = snapshot.Models
	}
	table := render.CreateTable(header, nil, false, false, tableMode)
	for _, model := range displayModels {
		taskArea := "-"
		if model.Comparison != nil && model.Comparison.TaskArea != "" {
			taskArea = model.Comparison.TaskArea
		}

		planNames := model.Plans.SupportedPlanNames()
		supportedPlans := "-"
		if len(planNames) > 0 {
			supportedPlans = strings.Join(planNames, ", ")
		}

		if showAll {
			table.Append([]string{
				model.Name,
				model.Provider,
				model.ReleaseStatus,
				formatJoinedModelMultiplier(model),
				boolYesNo(model.Clients.CLI),
				boolYesNo(model.VisibleNow),
				supportedPlans,
				boolYesNo(model.Modes.Agent),
				boolYesNo(model.Modes.Ask),
				boolYesNo(model.Modes.Edit),
				taskArea,
			})
		} else {
			table.Append([]string{
				model.Name,
				model.Provider,
				model.ReleaseStatus,
				formatJoinedModelMultiplier(model),
				boolYesNo(model.VisibleNow),
				supportedPlans,
			})
		}
	}
	table.Render()

	if showAll && len(snapshot.RetiredModels) > 0 {
		fmt.Println("\nRetired Models:")
		retiredTable := render.CreateTable([]string{"Name", "Retired On", "Suggested Alternative"}, nil, false, false, tableMode)
		for _, model := range snapshot.RetiredModels {
			retiredTable.Append([]string{model.Name, model.RetirementDate, model.SuggestedAlternative})
		}
		retiredTable.Render()
	}

	if len(snapshot.LiveModelsWithoutDocs) > 0 {
		fmt.Println("\nLive Models Without Docs Snapshot Match:")
		liveTable := render.CreateTable([]string{"ID", "Name", "State", "Multiplier"}, nil, false, false, tableMode)
		for _, model := range snapshot.LiveModelsWithoutDocs {
			state := "-"
			if model.PolicyState != "" {
				state = model.PolicyState
			}
			liveTable.Append([]string{model.ID, model.Name, state, formatModelDocsLiveMultipliers([]modeldocs.LiveMatch{model})})
		}
		liveTable.Render()
	}

	fmt.Println("\nNotes:")
	fmt.Printf("- Catalog version: %s\n", snapshot.CatalogVersion)
	fmt.Printf("- Loaded from: %s\n", snapshot.LoadedFrom)
	fmt.Println("- This command uses an embedded snapshot refreshed from github/docs at a recorded commit; `scripts/update-modeldocs-snapshot.sh` refreshes it, and `--latest` attempts fresh github/docs data with embedded fallback on fetch or compatibility errors.")
	fmt.Println("- `Multiplier` prefers the docs-backed paid multiplier from github/docs and only falls back to live CLI billing when the docs snapshot has no multiplier for a visible row.")
	fmt.Println("- `Visible Now` comes from the local Copilot CLI server and can vary by plan, organization policy, rollout, and account state.")
	if showAll {
		fmt.Println("- `Copilot CLI` comes from the docs-supported client matrix.")
		fmt.Println("- `Provider`, `Agent`, `Ask`, `Edit`, and `Task Area` come from docs-backed model metadata.")
		fmt.Println("- Retired models are listed only with `--all`.")
		fmt.Println("- Free-plan multipliers are stored in YAML too, but this command currently displays the paid-plan multiplier column.")
		fmt.Println("- Use `-f yaml` for the full provider, mode, per-client, per-plan, and model-card metadata.")
	} else {
		fmt.Printf("- Showing %d docs-backed models that support Copilot CLI.\n", len(cliModels))
		fmt.Println("- Models without Copilot CLI support and retired-model history are hidden by default. Use `--all` to include them and expose broader docs-backed metadata.")
		fmt.Println("- Free-plan multipliers are stored in YAML too, but this command currently displays the paid-plan multiplier column.")
		fmt.Println("- Use `-f yaml --all` for the full provider, mode, per-client, per-plan, and model-card metadata.")
	}
	if len(snapshot.LoadWarnings) > 0 {
		fmt.Println("- Warnings:")
		for _, warning := range snapshot.LoadWarnings {
			fmt.Printf("  - %s\n", warning)
		}
	}
}

func copilotCLIModelDocs(models []modeldocs.JoinedModel) []modeldocs.JoinedModel {
	cliModels := make([]modeldocs.JoinedModel, 0, len(models))
	for _, model := range models {
		if !model.Clients.CLI {
			continue
		}
		cliModels = append(cliModels, model)
	}
	return cliModels
}

func formatModelDocsLiveMultipliers(matches []modeldocs.LiveMatch) string {
	if len(matches) == 0 {
		return "-"
	}

	seen := make(map[string]struct{}, len(matches))
	values := make([]string, 0, len(matches))
	for _, match := range matches {
		if match.BillingMultiplier == nil {
			continue
		}
		value := formatMultiplierValue(match.BillingMultiplier)
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	if len(values) == 0 {
		return "-"
	}
	return strings.Join(values, ", ")
}

func formatJoinedModelMultiplier(model modeldocs.JoinedModel) string {
	if model.Multipliers != nil && model.Multipliers.Paid != nil {
		return formatMultiplierValue(model.Multipliers.Paid)
	}
	return formatModelDocsLiveMultipliers(model.LiveModels)
}

func formatMultiplierValue(multiplier *float64) string {
	if multiplier == nil {
		return "-"
	}
	if *multiplier == 0 {
		return "Included (0)"
	}
	return strconv.FormatFloat(*multiplier, 'f', -1, 64)
}

type cliFocusedModelDocsSnapshot struct {
	CatalogVersion        string                `json:"catalogVersion" yaml:"catalogVersion"`
	SourceNote            string                `json:"sourceNote" yaml:"sourceNote"`
	Sources               modeldocs.Sources     `json:"sources" yaml:"sources"`
	LoadedFrom            string                `json:"loadedFrom" yaml:"loadedFrom"`
	LoadWarnings          []string              `json:"loadWarnings,omitempty" yaml:"loadWarnings,omitempty"`
	Models                []cliFocusedModelDocs `json:"models" yaml:"models"`
	LiveModelsWithoutDocs []modeldocs.LiveMatch `json:"liveModelsWithoutDocs,omitempty" yaml:"liveModelsWithoutDocs,omitempty"`
}

type cliFocusedModelDocs struct {
	Name          string                        `json:"name" yaml:"name"`
	Provider      string                        `json:"provider" yaml:"provider"`
	ReleaseStatus string                        `json:"releaseStatus" yaml:"releaseStatus"`
	VisibleNow    bool                          `json:"visibleNow" yaml:"visibleNow"`
	Plans         modeldocs.PlanAvailability    `json:"plans" yaml:"plans"`
	Multipliers   *modeldocs.RequestMultipliers `json:"multipliers,omitempty" yaml:"multipliers,omitempty"`
	LiveModels    []modeldocs.LiveMatch         `json:"liveModels,omitempty" yaml:"liveModels,omitempty"`
}

func buildCLIFocusedModelDocsSnapshot(snapshot modeldocs.Snapshot) cliFocusedModelDocsSnapshot {
	cliModels := copilotCLIModelDocs(snapshot.Models)
	models := make([]cliFocusedModelDocs, 0, len(cliModels))
	for _, model := range cliModels {
		models = append(models, cliFocusedModelDocs{
			Name:          model.Name,
			Provider:      model.Provider,
			ReleaseStatus: model.ReleaseStatus,
			VisibleNow:    model.VisibleNow,
			Plans:         model.Plans,
			Multipliers:   cloneModeldocsRequestMultipliers(model.Multipliers),
			LiveModels:    append([]modeldocs.LiveMatch(nil), model.LiveModels...),
		})
	}
	return cliFocusedModelDocsSnapshot{
		CatalogVersion:        snapshot.CatalogVersion,
		SourceNote:            snapshot.SourceNote,
		Sources:               snapshot.Sources,
		LoadedFrom:            snapshot.LoadedFrom,
		LoadWarnings:          append([]string(nil), snapshot.LoadWarnings...),
		Models:                models,
		LiveModelsWithoutDocs: append([]modeldocs.LiveMatch(nil), snapshot.LiveModelsWithoutDocs...),
	}
}

func boolYesNo(v bool) string {
	if v {
		return "Yes"
	}
	return "No"
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
	table := render.CreateTable(header, nil, false, false, tableMode)

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
		table := render.CreateTable(header, nil, false, false, tableMode)

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

func newSkillsCmd(client *copilot.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "skills",
		Short: "List available skills",
		Run: func(cmd *cobra.Command, args []string) {
			showSkills(cmd.Context(), client, outputFormat)
		},
	}
}

func showSkills(ctx context.Context, client *copilot.Client, format string) {
	err := withSession(ctx, client, func(session *copilot.Session) error {
		res, err := session.RPC.Skills.List(ctx)
		if err != nil {
			return err
		}

		if format == "yaml" {
			printYAML(res)
			return nil
		}

		header := []string{"Name", "Enabled", "Source", "Invocable", "Path", "Description"}
		table := render.CreateTable(header, nil, false, false, tableMode)

		for _, s := range res.Skills {
			path := ""
			if s.Path != nil {
				path = *s.Path
			}
			desc := strings.SplitN(s.Description, "\n", 2)[0]
			if len(desc) > 80 {
				desc = desc[:77] + "..."
			}
			table.Append([]string{
				s.Name,
				fmt.Sprintf("%v", s.Enabled),
				s.Source,
				fmt.Sprintf("%v", s.UserInvocable),
				path,
				desc,
			})
		}
		table.Render()
		return nil
	})
	if err != nil {
		log.Printf("Error in skills command: %v", err)
	}
}

func newExtensionsCmd(client *copilot.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "extensions",
		Short: "List available extensions",
		Run: func(cmd *cobra.Command, args []string) {
			showExtensions(cmd.Context(), client, outputFormat)
		},
	}
}

func showExtensions(ctx context.Context, client *copilot.Client, format string) {
	err := withSession(ctx, client, func(session *copilot.Session) error {
		res, err := session.RPC.Extensions.List(ctx)
		if err != nil {
			return err
		}

		if format == "yaml" {
			printYAML(res)
			return nil
		}

		header := []string{"ID", "Name", "Status", "Source", "PID"}
		table := render.CreateTable(header, nil, false, false, tableMode)

		for _, e := range res.Extensions {
			pid := ""
			if e.PID != nil {
				pid = strconv.FormatInt(*e.PID, 10)
			}
			table.Append([]string{
				e.ID,
				e.Name,
				string(e.Status),
				string(e.Source),
				pid,
			})
		}
		table.Render()
		return nil
	})
	if err != nil {
		log.Printf("Error in extensions command: %v", err)
	}
}

func newPluginsCmd(client *copilot.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "plugins",
		Short: "List installed plugins",
		Run: func(cmd *cobra.Command, args []string) {
			showPlugins(cmd.Context(), client, outputFormat)
		},
	}
}

func showPlugins(ctx context.Context, client *copilot.Client, format string) {
	err := withSession(ctx, client, func(session *copilot.Session) error {
		res, err := session.RPC.Plugins.List(ctx)
		if err != nil {
			return err
		}

		if format == "yaml" {
			printYAML(res)
			return nil
		}

		header := []string{"Name", "Enabled", "Marketplace", "Version"}
		table := render.CreateTable(header, nil, false, false, tableMode)

		for _, p := range res.Plugins {
			version := ""
			if p.Version != nil {
				version = *p.Version
			}
			table.Append([]string{
				p.Name,
				fmt.Sprintf("%v", p.Enabled),
				p.Marketplace,
				version,
			})
		}
		table.Render()
		return nil
	})
	if err != nil {
		log.Printf("Error in plugins command: %v", err)
	}
}

func newMcpCmd(client *copilot.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "List MCP servers",
		Run: func(cmd *cobra.Command, args []string) {
			showMcp(cmd.Context(), client, outputFormat)
		},
	}
}

func showMcp(ctx context.Context, client *copilot.Client, format string) {
	err := withSession(ctx, client, func(session *copilot.Session) error {
		res, err := session.RPC.Mcp.List(ctx)
		if err != nil {
			return err
		}

		if format == "yaml" {
			printYAML(res)
			return nil
		}

		header := []string{"Name", "Status", "Source", "Error"}
		table := render.CreateTable(header, nil, false, false, tableMode)

		for _, s := range res.Servers {
			source := ""
			if s.Source != nil {
				source = *s.Source
			}
			errMsg := ""
			if s.Error != nil {
				errMsg = *s.Error
			}
			table.Append([]string{
				s.Name,
				string(s.Status),
				source,
				errMsg,
			})
		}
		table.Render()
		return nil
	})
	if err != nil {
		log.Printf("Error in mcp command: %v", err)
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
			table := render.CreateTable(header, nil, false, false, tableMode)
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
			table := render.CreateTable(header, nil, false, false, tableMode)
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
	table := render.CreateTable([]string{"Property", "Value"}, nil, false, false, tableMode)
	table.Append([]string{"Version", status.Version})
	table.Append([]string{"Protocol Version", fmt.Sprintf("%d", status.ProtocolVersion)})
	table.Render()

	fmt.Println("\n--- Auth Status ---")
	tableAuth := render.CreateTable([]string{"Property", "Value"}, nil, false, false, tableMode)
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
	table := render.CreateTable(header, nil, false, false, tableMode)

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

func newResumeBranchesCmd(client *copilot.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "resume-branches [sessionID]",
		Short: "Trace inferred work branches that start from session.resume",
		Args:  cobra.MaximumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			sessionID, err := resolveSessionID(cmd.Context(), client, args)
			if err != nil {
				log.Printf("%v", err)
				return
			}
			showResumeBranches(cmd.Context(), client, sessionID, outputFormat)
		},
	}
}

func newValidateEventsCmd(client *copilot.Client) *cobra.Command {
	var sampleLimit int
	cmd := &cobra.Command{
		Use:   "validate-events [sessionID]",
		Short: "Validate local session events against copilot-sdk generated types",
		Args:  cobra.MaximumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if sampleLimit < 0 {
				log.Printf("invalid --samples %d: expected >= 0", sampleLimit)
				return
			}
			sessionID, err := resolveSessionID(cmd.Context(), client, args)
			if err != nil {
				log.Printf("%v", err)
				return
			}
			showValidateEvents(cmd.Context(), client, sessionID, outputFormat, sampleLimit)
		},
	}
	cmd.Flags().IntVar(&sampleLimit, "samples", 20, "Maximum non-OK validation samples to include")
	return cmd
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
				multiplierMap[analyze.NormalizeModelKey(m.Name)] = m.Billing.Multiplier
				multiplierMap[analyze.NormalizeModelKey(m.ID)] = m.Billing.Multiplier
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
			if m, ok := multiplierMap[analyze.NormalizeModelKey(item.Model)]; ok {
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
	table := render.CreateTable(header, []int{len(header) - 4, len(header) - 3, len(header) - 2, len(header) - 1}, last > 0, last > 0, tableMode)

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
	Ephemeral *bool
	Data      map[string]any
	RawData   any
}

func (e *sessionEvent) sessionEventType() copilot.SessionEventType {
	if e == nil {
		return ""
	}
	return copilot.SessionEventType(e.Type)
}

func rawSessionEventType(raw map[string]any) copilot.SessionEventType {
	if raw == nil {
		return ""
	}
	value, _ := raw["type"].(string)
	return copilot.SessionEventType(value)
}

func isKnownSDKSessionEventType(eventType copilot.SessionEventType) bool {
	_, ok := knownSDKSessionEventTypes[eventType]
	return ok
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

type sessionEventValidationIssueCount struct {
	Issue string `json:"issue" yaml:"issue"`
	Rows  int    `json:"rows" yaml:"rows"`
}

type sessionEventValidationSample struct {
	Row             int    `json:"row" yaml:"row"`
	ID              string `json:"id,omitempty" yaml:"id,omitempty"`
	Type            string `json:"type" yaml:"type"`
	SDKKnownType    bool   `json:"sdkKnownType" yaml:"sdkKnownType"`
	SDKCompatible   bool   `json:"sdkCompatible" yaml:"sdkCompatible"`
	LocalCompatible bool   `json:"localCompatible" yaml:"localCompatible"`
	Issue           string `json:"issue" yaml:"issue"`
	TimestampKind   string `json:"timestampKind,omitempty" yaml:"timestampKind,omitempty"`
	TimestampValue  string `json:"timestampValue,omitempty" yaml:"timestampValue,omitempty"`
	DataKind        string `json:"dataKind,omitempty" yaml:"dataKind,omitempty"`
	SDKError        string `json:"sdkError,omitempty" yaml:"sdkError,omitempty"`
}

type sessionResumeContinuitySample struct {
	Row                           int    `json:"row" yaml:"row"`
	ID                            string `json:"id,omitempty" yaml:"id,omitempty"`
	Issue                         string `json:"issue" yaml:"issue"`
	AlreadyInUse                  bool   `json:"alreadyInUse" yaml:"alreadyInUse"`
	ParentID                      string `json:"parentId,omitempty" yaml:"parentId,omitempty"`
	ParentType                    string `json:"parentType,omitempty" yaml:"parentType,omitempty"`
	EventCount                    int    `json:"eventCount" yaml:"eventCount"`
	RowsBeforeResume              int    `json:"rowsBeforeResume" yaml:"rowsBeforeResume"`
	EventCountMatches             bool   `json:"eventCountMatches" yaml:"eventCountMatches"`
	ParentMatchesPreviousShutdown bool   `json:"parentMatchesPreviousShutdown" yaml:"parentMatchesPreviousShutdown"`
	ParentMatchesPreviousEvent    bool   `json:"parentMatchesPreviousEvent" yaml:"parentMatchesPreviousEvent"`
	GapSeconds                    int64  `json:"gapSeconds,omitempty" yaml:"gapSeconds,omitempty"`
}

type sessionEventValidationSummary struct {
	SessionID                    string                             `json:"sessionId" yaml:"sessionId"`
	EventsPath                   string                             `json:"eventsPath" yaml:"eventsPath"`
	SampleLimit                  int                                `json:"sampleLimit" yaml:"sampleLimit"`
	TotalRows                    int                                `json:"totalRows" yaml:"totalRows"`
	SDKKnownTypeRows             int                                `json:"sdkKnownTypeRows" yaml:"sdkKnownTypeRows"`
	SDKUnknownTypeRows           int                                `json:"sdkUnknownTypeRows" yaml:"sdkUnknownTypeRows"`
	SDKCompatibleRows            int                                `json:"sdkCompatibleRows" yaml:"sdkCompatibleRows"`
	SDKIncompatibleRows          int                                `json:"sdkIncompatibleRows" yaml:"sdkIncompatibleRows"`
	LocalCompatibleRows          int                                `json:"localCompatibleRows" yaml:"localCompatibleRows"`
	LocalIncompatibleRows        int                                `json:"localIncompatibleRows" yaml:"localIncompatibleRows"`
	LocalOnlyFallbackRows        int                                `json:"localOnlyFallbackRows" yaml:"localOnlyFallbackRows"`
	UnknownTypeSDKCompatibleRows int                                `json:"unknownTypeSdkCompatibleRows" yaml:"unknownTypeSdkCompatibleRows"`
	ResumeRows                   int                                `json:"resumeRows" yaml:"resumeRows"`
	GracefulResumeRows           int                                `json:"gracefulResumeRows" yaml:"gracefulResumeRows"`
	ResumeWhileInUseRows         int                                `json:"resumeWhileInUseRows" yaml:"resumeWhileInUseRows"`
	ResumeFromLastEventRows      int                                `json:"resumeFromLastEventRows" yaml:"resumeFromLastEventRows"`
	SuspiciousResumeRows         int                                `json:"suspiciousResumeRows" yaml:"suspiciousResumeRows"`
	IssueCounts                  []sessionEventValidationIssueCount `json:"issueCounts,omitempty" yaml:"issueCounts,omitempty"`
	ResumeIssueCounts            []sessionEventValidationIssueCount `json:"resumeIssueCounts,omitempty" yaml:"resumeIssueCounts,omitempty"`
	Samples                      []sessionEventValidationSample     `json:"samples,omitempty" yaml:"samples,omitempty"`
	ResumeSamples                []sessionResumeContinuitySample    `json:"resumeSamples,omitempty" yaml:"resumeSamples,omitempty"`
}

type sessionResumeBranchReport struct {
	SessionID                    string                `json:"sessionId" yaml:"sessionId"`
	ResumeRows                   int                   `json:"resumeRows" yaml:"resumeRows"`
	GracefulResumeRows           int                   `json:"gracefulResumeRows" yaml:"gracefulResumeRows"`
	ResumeWhileInUseRows         int                   `json:"resumeWhileInUseRows" yaml:"resumeWhileInUseRows"`
	ResumeFromLastEventRows      int                   `json:"resumeFromLastEventRows" yaml:"resumeFromLastEventRows"`
	ResumeEventCountMismatchRows int                   `json:"resumeEventCountMismatchRows" yaml:"resumeEventCountMismatchRows"`
	ResumeParentMismatchRows     int                   `json:"resumeParentMismatchRows" yaml:"resumeParentMismatchRows"`
	Branches                     []sessionResumeBranch `json:"branches,omitempty" yaml:"branches,omitempty"`
}

type sessionResumeBranch struct {
	ResumeRow               int        `json:"resumeRow" yaml:"resumeRow"`
	ResumeID                string     `json:"resumeId,omitempty" yaml:"resumeId,omitempty"`
	ResumeTime              time.Time  `json:"resumeTime" yaml:"resumeTime"`
	Kind                    string     `json:"kind" yaml:"kind"`
	AlreadyInUse            bool       `json:"alreadyInUse" yaml:"alreadyInUse"`
	ParentID                string     `json:"parentId,omitempty" yaml:"parentId,omitempty"`
	ParentType              string     `json:"parentType,omitempty" yaml:"parentType,omitempty"`
	EventCount              int        `json:"eventCount" yaml:"eventCount"`
	RowsBeforeResume        int        `json:"rowsBeforeResume" yaml:"rowsBeforeResume"`
	EventCountDelta         int        `json:"eventCountDelta" yaml:"eventCountDelta"`
	GapSeconds              int64      `json:"gapSeconds,omitempty" yaml:"gapSeconds,omitempty"`
	NextResumeRow           int        `json:"nextResumeRow,omitempty" yaml:"nextResumeRow,omitempty"`
	NextShutdownRow         int        `json:"nextShutdownRow,omitempty" yaml:"nextShutdownRow,omitempty"`
	ActiveInteractionIDs    []string   `json:"activeInteractionIds,omitempty" yaml:"activeInteractionIds,omitempty"`
	SeedReason              string     `json:"seedReason,omitempty" yaml:"seedReason,omitempty"`
	BranchInteractionIDs    []string   `json:"branchInteractionIds,omitempty" yaml:"branchInteractionIds,omitempty"`
	CompetingInteractionIDs []string   `json:"competingInteractionIds,omitempty" yaml:"competingInteractionIds,omitempty"`
	FirstInteractionRow     int        `json:"firstInteractionRow,omitempty" yaml:"firstInteractionRow,omitempty"`
	LastInteractionRow      int        `json:"lastInteractionRow,omitempty" yaml:"lastInteractionRow,omitempty"`
	LastInteractionTime     *time.Time `json:"lastInteractionTime,omitempty" yaml:"lastInteractionTime,omitempty"`
	LastEventType           string     `json:"lastEventType,omitempty" yaml:"lastEventType,omitempty"`
	Duration                string     `json:"duration,omitempty" yaml:"duration,omitempty"`
	EventRows               int        `json:"eventRows,omitempty" yaml:"eventRows,omitempty"`
	UserMessages            int        `json:"userMessages,omitempty" yaml:"userMessages,omitempty"`
	AssistantMessages       int        `json:"assistantMessages,omitempty" yaml:"assistantMessages,omitempty"`
	Turns                   int        `json:"turns,omitempty" yaml:"turns,omitempty"`
	ToolCalls               int        `json:"toolCalls,omitempty" yaml:"toolCalls,omitempty"`
	Models                  []string   `json:"models,omitempty" yaml:"models,omitempty"`
	FirstUserEventID        string     `json:"firstUserEventId,omitempty" yaml:"firstUserEventId,omitempty"`
	FirstUserText           string     `json:"firstUserText,omitempty" yaml:"firstUserText,omitempty"`
	ContinuedPastNextResume bool       `json:"continuedPastNextResume,omitempty" yaml:"continuedPastNextResume,omitempty"`
	ReachedShutdown         bool       `json:"reachedShutdown,omitempty" yaml:"reachedShutdown,omitempty"`
	OpenAtLogEnd            bool       `json:"openAtLogEnd,omitempty" yaml:"openAtLogEnd,omitempty"`
	Confidence              string     `json:"confidence" yaml:"confidence"`
}

type sessionResumePoint struct {
	ResumeRow                     int
	ResumeEvent                   *sessionEvent
	Kind                          string
	AlreadyInUse                  bool
	ParentType                    string
	EventCount                    int
	RowsBeforeResume              int
	EventCountDelta               int
	GapSeconds                    int64
	ParentMatchesPreviousShutdown bool
	ParentMatchesPreviousEvent    bool
}

type sessionInteractionTrace struct {
	InteractionID     string
	FirstRow          int
	FirstTime         time.Time
	FirstType         string
	LastRow           int
	LastTime          time.Time
	LastType          string
	EventRows         int
	UserMessages      int
	AssistantMessages int
	Turns             int
	ToolCalls         int
	FirstUserRow      int
	FirstUserEventID  string
	FirstUserText     string
	HasOpenTurn       bool
	modelSet          map[string]struct{}
	Models            []string
}

type sessionEventValidationRowRef struct {
	Row       int
	ID        string
	Type      string
	Timestamp time.Time
}

// Mirrors the generated SessionEventType constants so validator output can tell
// whether a row uses a type known to the current copilot-sdk build.
var knownSDKSessionEventTypes = map[copilot.SessionEventType]struct{}{
	copilot.SessionEventTypeAbort:                         {},
	copilot.SessionEventTypeAssistantIntent:               {},
	copilot.SessionEventTypeAssistantMessage:              {},
	copilot.SessionEventTypeAssistantMessageDelta:         {},
	copilot.SessionEventTypeAssistantReasoning:            {},
	copilot.SessionEventTypeAssistantReasoningDelta:       {},
	copilot.SessionEventTypeAssistantStreamingDelta:       {},
	copilot.SessionEventTypeAssistantTurnEnd:              {},
	copilot.SessionEventTypeAssistantTurnStart:            {},
	copilot.SessionEventTypeAssistantUsage:                {},
	copilot.SessionEventTypeCapabilitiesChanged:           {},
	copilot.SessionEventTypeCommandCompleted:              {},
	copilot.SessionEventTypeCommandExecute:                {},
	copilot.SessionEventTypeCommandQueued:                 {},
	copilot.SessionEventTypeCommandsChanged:               {},
	copilot.SessionEventTypeElicitationCompleted:          {},
	copilot.SessionEventTypeElicitationRequested:          {},
	copilot.SessionEventTypeExitPlanModeCompleted:         {},
	copilot.SessionEventTypeExitPlanModeRequested:         {},
	copilot.SessionEventTypeExternalToolCompleted:         {},
	copilot.SessionEventTypeExternalToolRequested:         {},
	copilot.SessionEventTypeHookEnd:                       {},
	copilot.SessionEventTypeHookStart:                     {},
	copilot.SessionEventTypeMcpOauthCompleted:             {},
	copilot.SessionEventTypeMcpOauthRequired:              {},
	copilot.SessionEventTypePendingMessagesModified:       {},
	copilot.SessionEventTypePermissionCompleted:           {},
	copilot.SessionEventTypePermissionRequested:           {},
	copilot.SessionEventTypeSamplingCompleted:             {},
	copilot.SessionEventTypeSamplingRequested:             {},
	copilot.SessionEventTypeSessionBackgroundTasksChanged: {},
	copilot.SessionEventTypeSessionCompactionComplete:     {},
	copilot.SessionEventTypeSessionCompactionStart:        {},
	copilot.SessionEventTypeSessionContextChanged:         {},
	copilot.SessionEventTypeSessionCustomAgentsUpdated:    {},
	copilot.SessionEventTypeSessionError:                  {},
	copilot.SessionEventTypeSessionExtensionsLoaded:       {},
	copilot.SessionEventTypeSessionHandoff:                {},
	copilot.SessionEventTypeSessionIdle:                   {},
	copilot.SessionEventTypeSessionInfo:                   {},
	copilot.SessionEventTypeSessionMcpServerStatusChanged: {},
	copilot.SessionEventTypeSessionMcpServersLoaded:       {},
	copilot.SessionEventTypeSessionModeChanged:            {},
	copilot.SessionEventTypeSessionModelChange:            {},
	copilot.SessionEventTypeSessionPlanChanged:            {},
	copilot.SessionEventTypeSessionRemoteSteerableChanged: {},
	copilot.SessionEventTypeSessionResume:                 {},
	copilot.SessionEventTypeSessionShutdown:               {},
	copilot.SessionEventTypeSessionSkillsLoaded:           {},
	copilot.SessionEventTypeSessionSnapshotRewind:         {},
	copilot.SessionEventTypeSessionStart:                  {},
	copilot.SessionEventTypeSessionTaskComplete:           {},
	copilot.SessionEventTypeSessionTitleChanged:           {},
	copilot.SessionEventTypeSessionToolsUpdated:           {},
	copilot.SessionEventTypeSessionTruncation:             {},
	copilot.SessionEventTypeSessionUsageInfo:              {},
	copilot.SessionEventTypeSessionWarning:                {},
	copilot.SessionEventTypeSessionWorkspaceFileChanged:   {},
	copilot.SessionEventTypeSkillInvoked:                  {},
	copilot.SessionEventTypeSubagentCompleted:             {},
	copilot.SessionEventTypeSubagentDeselected:            {},
	copilot.SessionEventTypeSubagentFailed:                {},
	copilot.SessionEventTypeSubagentSelected:              {},
	copilot.SessionEventTypeSubagentStarted:               {},
	copilot.SessionEventTypeSystemMessage:                 {},
	copilot.SessionEventTypeSystemNotification:            {},
	copilot.SessionEventTypeToolExecutionComplete:         {},
	copilot.SessionEventTypeToolExecutionPartialResult:    {},
	copilot.SessionEventTypeToolExecutionProgress:         {},
	copilot.SessionEventTypeToolExecutionStart:            {},
	copilot.SessionEventTypeToolUserRequested:             {},
	copilot.SessionEventTypeUserInputCompleted:            {},
	copilot.SessionEventTypeUserInputRequested:            {},
	copilot.SessionEventTypeUserMessage:                   {},
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

func visitJSONLRows(path string, fn func(rowNo int, line []byte, raw map[string]any) error) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("error opening events file: %w", err)
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	rowNo := 0
	for {
		var ev map[string]any
		line, readErr := reader.ReadBytes('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return fmt.Errorf("error reading %s row %d: %w", path, rowNo+1, readErr)
		}
		if len(line) == 0 && errors.Is(readErr, io.EOF) {
			break
		}
		trimmed := strings.TrimSpace(string(line))
		if trimmed == "" {
			if errors.Is(readErr, io.EOF) {
				break
			}
			continue
		}
		if err := json.Unmarshal([]byte(trimmed), &ev); err != nil {
			return fmt.Errorf("error decoding %s row %d: %w", path, rowNo+1, err)
		}
		rowNo++
		if err := fn(rowNo, []byte(trimmed), ev); err != nil {
			if errors.Is(err, errStopJSONLIteration) {
				return nil
			}
			return err
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
	}

	return nil
}

func visitJSONLObjects(path string, fn func(map[string]any) error) error {
	return visitJSONLRows(path, func(_ int, _ []byte, raw map[string]any) error {
		return fn(raw)
	})
}

func sessionHasShutdown(eventsPath string) (bool, error) {
	hasShutdown := false
	err := visitJSONLObjects(eventsPath, func(ev map[string]any) error {
		if rawSessionEventType(ev) == copilot.SessionEventTypeSessionShutdown {
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

func boolPtrFromAny(value any) *bool {
	b, ok := value.(bool)
	if !ok {
		return nil
	}
	return &b
}

var (
	plausibleSessionTimestampMin = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	plausibleSessionTimestampMax = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
)

func isPlausibleSessionTimestamp(ts time.Time) bool {
	return !ts.Before(plausibleSessionTimestampMin) && ts.Before(plausibleSessionTimestampMax)
}

func parseUnixTimestampInteger(value int64) (time.Time, bool) {
	// Numeric timestamps appear in mixed sec/ms/us/ns forms. Prefer the first
	// interpretation that lands in a plausible Copilot CLI time range before
	// falling back to a magnitude-only guess for outliers.
	candidates := []time.Time{
		time.Unix(value, 0).UTC(),
		time.UnixMilli(value).UTC(),
		time.UnixMicro(value).UTC(),
		time.Unix(0, value).UTC(),
	}
	for _, ts := range candidates {
		if isPlausibleSessionTimestamp(ts) {
			return ts, true
		}
	}

	switch {
	case value >= 1e18 || value <= -1e18:
		return time.Unix(0, value).UTC(), true
	case value >= 1e15 || value <= -1e15:
		return time.Unix(0, value*int64(time.Microsecond)).UTC(), true
	case value >= 1e12 || value <= -1e12:
		return time.Unix(0, value*int64(time.Millisecond)).UTC(), true
	default:
		return time.Unix(value, 0).UTC(), true
	}
}

func parseUnixTimestampFloat(value float64) (time.Time, bool) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return time.Time{}, false
	}
	whole, frac := math.Modf(value)
	if frac != 0 {
		for _, scale := range []float64{1, 1e3, 1e6, 1e9} {
			scaled := value / scale
			scaledWhole, scaledFrac := math.Modf(scaled)
			ts := time.Unix(int64(scaledWhole), int64(scaledFrac*float64(time.Second))).UTC()
			if isPlausibleSessionTimestamp(ts) {
				return ts, true
			}
		}
		return time.Unix(int64(whole), int64(frac*float64(time.Second))).UTC(), true
	}
	return parseUnixTimestampInteger(int64(whole))
}

func parseSessionTimestamp(value any) (time.Time, bool) {
	switch v := value.(type) {
	case string:
		v = strings.TrimSpace(v)
		if v == "" {
			return time.Time{}, false
		}
		for _, layout := range []string{
			time.RFC3339Nano,
			time.RFC3339,
			"2006-01-02T15:04:05.999999999Z0700",
			"2006-01-02T15:04:05Z0700",
			"2006-01-02 15:04:05.999999999Z07:00",
			"2006-01-02 15:04:05Z07:00",
		} {
			if ts, err := time.Parse(layout, v); err == nil {
				return ts, true
			}
		}
		if unixValue, err := strconv.ParseInt(v, 10, 64); err == nil {
			return parseUnixTimestampInteger(unixValue)
		}
		if unixValue, err := strconv.ParseFloat(v, 64); err == nil {
			return parseUnixTimestampFloat(unixValue)
		}
	case float64:
		return parseUnixTimestampFloat(v)
	case int:
		return parseUnixTimestampInteger(int64(v))
	case int64:
		return parseUnixTimestampInteger(v)
	case json.Number:
		if unixValue, err := v.Int64(); err == nil {
			return parseUnixTimestampInteger(unixValue)
		}
		if unixValue, err := v.Float64(); err == nil {
			return parseUnixTimestampFloat(unixValue)
		}
	}
	return time.Time{}, false
}

func parseSessionEvent(raw map[string]any) *sessionEvent {
	ts, ok := parseSessionTimestamp(raw["timestamp"])
	if !ok {
		return nil
	}

	// Keep event parsing intentionally schema-light so local log analysis still
	// works when Copilot CLI starts emitting new event types before copilot-sdk
	// has caught up with a newer generated schema.
	rawData := raw["data"]
	data, _ := rawData.(map[string]any)
	id, _ := raw["id"].(string)
	// parentId is the event-level lineage edge. parentToolCallId stays inside Data
	// and forms a separate tool-span lineage used for nested task/subagent work.
	parentID, _ := raw["parentId"].(string)
	evType, _ := raw["type"].(string)

	return &sessionEvent{
		ID:        id,
		Type:      evType,
		ParentID:  parentID,
		Timestamp: ts,
		Ephemeral: boolPtrFromAny(raw["ephemeral"]),
		Data:      data,
		RawData:   rawData,
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

func jsonValueKind(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case bool:
		return "bool"
	case float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, json.Number:
		return "number"
	default:
		return fmt.Sprintf("%T", value)
	}
}

func sessionEventValidationIssue(knownType bool, sdkCompatible bool, localCompatible bool) string {
	switch {
	case sdkCompatible && localCompatible && knownType:
		return ""
	case sdkCompatible && localCompatible:
		return "unknown-type-sdk-compatible"
	case sdkCompatible && !localCompatible && knownType:
		return "known-type-local-parser-dropped"
	case sdkCompatible && !localCompatible:
		return "unknown-type-local-parser-dropped"
	case !sdkCompatible && localCompatible && knownType:
		return "known-type-local-only-fallback"
	case !sdkCompatible && localCompatible:
		return "unknown-type-local-only-fallback"
	case knownType:
		return "known-type-dropped"
	default:
		return "unknown-type-dropped"
	}
}

func sessionResumeContinuityIssue(eventCountMatches bool, parentMatchesPreviousShutdown bool, parentMatchesPreviousEvent bool, alreadyInUse bool) string {
	switch {
	case !eventCountMatches:
		return "resume-event-count-mismatch"
	case parentMatchesPreviousShutdown:
		return ""
	case parentMatchesPreviousEvent && alreadyInUse:
		return "resume-while-in-use"
	case parentMatchesPreviousEvent:
		return "resume-from-last-event"
	default:
		return "resume-parent-mismatch"
	}
}

func buildResumeContinuityPoints(events []*sessionEvent) []sessionResumePoint {
	if len(events) == 0 {
		return nil
	}

	eventMap := make(map[string]*sessionEvent, len(events))
	for _, ev := range events {
		if ev.ID != "" {
			eventMap[ev.ID] = ev
		}
	}

	points := make([]sessionResumePoint, 0)
	var previousShutdown *sessionEvent
	for i, ev := range events {
		if ev.sessionEventType() == copilot.SessionEventTypeSessionShutdown {
			previousShutdown = ev
			continue
		}
		if ev.sessionEventType() != copilot.SessionEventTypeSessionResume {
			continue
		}

		rowsBeforeResume := i
		eventCountValue, _ := scalarInt64(ev.Data["eventCount"])
		eventCount := int(eventCountValue)
		alreadyInUse := dataBool(ev.Data, "alreadyInUse")
		var previousEvent *sessionEvent
		if i > 0 {
			previousEvent = events[i-1]
		}

		parentType := ""
		if parent := eventMap[ev.ParentID]; parent != nil {
			parentType = parent.Type
		}
		parentMatchesPreviousShutdown := previousShutdown != nil && ev.ParentID != "" && ev.ParentID == previousShutdown.ID
		parentMatchesPreviousEvent := previousEvent != nil && ev.ParentID != "" && ev.ParentID == previousEvent.ID
		eventCountMatches := eventCount == rowsBeforeResume
		issue := sessionResumeContinuityIssue(eventCountMatches, parentMatchesPreviousShutdown, parentMatchesPreviousEvent, alreadyInUse)
		kind := "graceful"
		if issue != "" {
			kind = issue
		}

		var gapSeconds int64
		if previousEvent != nil {
			gapSeconds = int64(ev.Timestamp.Sub(previousEvent.Timestamp).Round(time.Second) / time.Second)
			if gapSeconds < 0 {
				gapSeconds = 0
			}
		}

		points = append(points, sessionResumePoint{
			ResumeRow:                     i + 1,
			ResumeEvent:                   ev,
			Kind:                          kind,
			AlreadyInUse:                  alreadyInUse,
			ParentType:                    parentType,
			EventCount:                    eventCount,
			RowsBeforeResume:              rowsBeforeResume,
			EventCountDelta:               eventCount - rowsBeforeResume,
			GapSeconds:                    gapSeconds,
			ParentMatchesPreviousShutdown: parentMatchesPreviousShutdown,
			ParentMatchesPreviousEvent:    parentMatchesPreviousEvent,
		})
	}

	return points
}

func activeInteractionIDsBeforeRow(events []*sessionEvent, row int) []string {
	if row <= 1 || len(events) == 0 {
		return nil
	}

	active := make(map[string]struct{})
	openTurnsByID := make(map[string][]string)
	limit := row - 1
	if limit > len(events) {
		limit = len(events)
	}
	for i := 0; i < limit; i++ {
		ev := events[i]
		switch ev.sessionEventType() {
		case copilot.SessionEventTypeSessionStart, copilot.SessionEventTypeSessionResume, copilot.SessionEventTypeSessionShutdown:
			openTurnsByID = make(map[string][]string)
		case copilot.SessionEventTypeAssistantTurnStart:
			turnID := dataString(ev.Data, "turnId")
			if turnID == "" {
				continue
			}
			openTurnsByID[turnID] = append(openTurnsByID[turnID], dataString(ev.Data, "interactionId"))
		case copilot.SessionEventTypeAssistantTurnEnd:
			turnID := dataString(ev.Data, "turnId")
			queue := openTurnsByID[turnID]
			if len(queue) == 0 {
				continue
			}
			if len(queue) == 1 {
				delete(openTurnsByID, turnID)
			} else {
				openTurnsByID[turnID] = queue[1:]
			}
		}
	}
	for _, queue := range openTurnsByID {
		for _, interactionID := range queue {
			if interactionID != "" {
				active[interactionID] = struct{}{}
			}
		}
	}
	return sortedStringsFromSet(active)
}

func nextEventRowOfType(events []*sessionEvent, startRow int, eventType copilot.SessionEventType) int {
	if startRow < 0 {
		startRow = 0
	}
	for i := startRow; i < len(events); i++ {
		if events[i].sessionEventType() == eventType {
			return i + 1
		}
	}
	return 0
}

func buildInteractionTraces(ctx *historyRenderContext, toolSpans []*sessionToolSpan) map[string]*sessionInteractionTrace {
	traces := make(map[string]*sessionInteractionTrace)
	for i, ev := range ctx.events {
		switch ev.sessionEventType() {
		case copilot.SessionEventTypeSessionStart, copilot.SessionEventTypeSessionResume, copilot.SessionEventTypeSessionShutdown:
			continue
		}
		interactionID := resolveHistoryInteractionID(ctx, ev)
		if interactionID == "" {
			continue
		}
		trace := traces[interactionID]
		if trace == nil {
			trace = &sessionInteractionTrace{
				InteractionID: interactionID,
				modelSet:      make(map[string]struct{}),
			}
			traces[interactionID] = trace
		}

		row := i + 1
		if trace.FirstRow == 0 || row < trace.FirstRow {
			trace.FirstRow = row
			trace.FirstTime = ev.Timestamp
			trace.FirstType = ev.Type
		}
		if row >= trace.LastRow {
			trace.LastRow = row
			trace.LastTime = ev.Timestamp
			trace.LastType = ev.Type
		}
		trace.EventRows++

		switch ev.sessionEventType() {
		case copilot.SessionEventTypeUserMessage:
			trace.UserMessages++
			if trace.FirstUserRow == 0 {
				trace.FirstUserRow = row
				trace.FirstUserEventID = ev.ID
				trace.FirstUserText = eventText(ev.Data)
			}
		case copilot.SessionEventTypeAssistantMessage:
			trace.AssistantMessages++
		}
	}

	for _, turn := range ctx.turns {
		if turn == nil || turn.InteractionID == "" {
			continue
		}
		trace := traces[turn.InteractionID]
		if trace == nil {
			trace = &sessionInteractionTrace{
				InteractionID: turn.InteractionID,
				modelSet:      make(map[string]struct{}),
			}
			traces[turn.InteractionID] = trace
		}
		trace.Turns++
		if turn.State == "Open" {
			trace.HasOpenTurn = true
		}
	}

	for _, span := range toolSpans {
		if span == nil || span.InteractionID == "" {
			continue
		}
		trace := traces[span.InteractionID]
		if trace == nil {
			trace = &sessionInteractionTrace{
				InteractionID: span.InteractionID,
				modelSet:      make(map[string]struct{}),
			}
			traces[span.InteractionID] = trace
		}
		trace.ToolCalls++
		if span.Model != "" {
			trace.modelSet[span.Model] = struct{}{}
		}
	}

	for _, trace := range traces {
		trace.Models = sortedStringsFromSet(trace.modelSet)
	}

	return traces
}

func sortedInteractionTraces(traces map[string]*sessionInteractionTrace) []*sessionInteractionTrace {
	if len(traces) == 0 {
		return nil
	}
	sorted := make([]*sessionInteractionTrace, 0, len(traces))
	for _, trace := range traces {
		if trace != nil {
			sorted = append(sorted, trace)
		}
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].FirstRow != sorted[j].FirstRow {
			return sorted[i].FirstRow < sorted[j].FirstRow
		}
		return sorted[i].InteractionID < sorted[j].InteractionID
	})
	return sorted
}

func sessionResumeBranchConfidence(kind string, seedReason string, activeCount int, competingCount int) string {
	if seedReason == "" || seedReason == "no-interaction" || seedReason == "continued-open-interaction" {
		return "low"
	}
	if kind == "resume-event-count-mismatch" || kind == "resume-parent-mismatch" {
		if activeCount == 0 && competingCount == 0 {
			return "medium"
		}
		return "low"
	}
	if activeCount == 0 && competingCount == 0 {
		return "high"
	}
	return "medium"
}

func buildSessionResumeBranchReport(sessionID string, events []*sessionEvent) (*sessionResumeBranchReport, error) {
	if len(events) == 0 {
		return nil, fmt.Errorf("no parsable events found")
	}

	ctx := buildHistoryRenderContext(events)
	toolSpans, _ := buildSessionToolSpans(ctx)
	traces := buildInteractionTraces(ctx, toolSpans)
	sortedTraces := sortedInteractionTraces(traces)
	points := buildResumeContinuityPoints(events)

	report := &sessionResumeBranchReport{
		SessionID:  sessionID,
		ResumeRows: len(points),
	}
	for _, point := range points {
		switch point.Kind {
		case "graceful":
			report.GracefulResumeRows++
		case "resume-while-in-use":
			report.ResumeWhileInUseRows++
		case "resume-from-last-event":
			report.ResumeFromLastEventRows++
		case "resume-event-count-mismatch":
			report.ResumeEventCountMismatchRows++
		case "resume-parent-mismatch":
			report.ResumeParentMismatchRows++
		}
	}

	for idx, point := range points {
		nextResumeRow := 0
		if idx+1 < len(points) {
			nextResumeRow = points[idx+1].ResumeRow
		}
		nextShutdownRow := nextEventRowOfType(events, point.ResumeRow, copilot.SessionEventTypeSessionShutdown)
		searchLimitRow := len(events) + 1
		if nextResumeRow > 0 && nextResumeRow < searchLimitRow {
			searchLimitRow = nextResumeRow
		}
		if nextShutdownRow > 0 && nextShutdownRow < searchLimitRow {
			searchLimitRow = nextShutdownRow
		}

		branch := sessionResumeBranch{
			ResumeRow:            point.ResumeRow,
			ResumeID:             point.ResumeEvent.ID,
			ResumeTime:           point.ResumeEvent.Timestamp,
			Kind:                 point.Kind,
			AlreadyInUse:         point.AlreadyInUse,
			ParentID:             point.ResumeEvent.ParentID,
			ParentType:           point.ParentType,
			EventCount:           point.EventCount,
			RowsBeforeResume:     point.RowsBeforeResume,
			EventCountDelta:      point.EventCountDelta,
			GapSeconds:           point.GapSeconds,
			NextResumeRow:        nextResumeRow,
			NextShutdownRow:      nextShutdownRow,
			ActiveInteractionIDs: activeInteractionIDsBeforeRow(events, point.ResumeRow),
			SeedReason:           "no-interaction",
			Confidence:           "low",
		}

		candidates := make([]*sessionInteractionTrace, 0)
		for _, trace := range sortedTraces {
			if trace.FirstRow <= point.ResumeRow {
				continue
			}
			if trace.FirstRow >= searchLimitRow {
				continue
			}
			candidates = append(candidates, trace)
		}

		chain := make([]*sessionInteractionTrace, 0)
		competing := make([]*sessionInteractionTrace, 0)
		switch {
		case len(candidates) > 0:
			branch.SeedReason = "first-new-interaction"
			chain = append(chain, candidates[0])
			currentLastRow := candidates[0].LastRow
			ambiguous := false
			for _, candidate := range candidates[1:] {
				if ambiguous || candidate.FirstRow <= currentLastRow {
					ambiguous = true
					competing = append(competing, candidate)
					continue
				}
				chain = append(chain, candidate)
				if candidate.LastRow > currentLastRow {
					currentLastRow = candidate.LastRow
				}
			}
		case len(branch.ActiveInteractionIDs) == 1:
			if trace := traces[branch.ActiveInteractionIDs[0]]; trace != nil {
				branch.SeedReason = "continued-open-interaction"
				chain = append(chain, trace)
			}
		}

		branchIDs := make([]string, 0, len(chain))
		competingIDs := make([]string, 0, len(competing))
		models := make(map[string]struct{})
		var firstUserTrace *sessionInteractionTrace
		var lastTrace *sessionInteractionTrace
		for _, trace := range chain {
			branchIDs = append(branchIDs, trace.InteractionID)
			branch.EventRows += trace.EventRows
			branch.UserMessages += trace.UserMessages
			branch.AssistantMessages += trace.AssistantMessages
			branch.Turns += trace.Turns
			branch.ToolCalls += trace.ToolCalls
			for _, model := range trace.Models {
				models[model] = struct{}{}
			}
			if branch.FirstInteractionRow == 0 || trace.FirstRow < branch.FirstInteractionRow {
				branch.FirstInteractionRow = trace.FirstRow
			}
			if trace.LastRow > branch.LastInteractionRow {
				branch.LastInteractionRow = trace.LastRow
				ts := trace.LastTime
				branch.LastInteractionTime = &ts
				branch.LastEventType = trace.LastType
				lastTrace = trace
			}
			if trace.FirstUserRow > 0 && (firstUserTrace == nil || trace.FirstUserRow < firstUserTrace.FirstUserRow) {
				firstUserTrace = trace
			}
		}
		for _, trace := range competing {
			competingIDs = append(competingIDs, trace.InteractionID)
		}
		branch.BranchInteractionIDs = branchIDs
		branch.CompetingInteractionIDs = competingIDs
		branch.Models = sortedStringsFromSet(models)
		if firstUserTrace != nil {
			branch.FirstUserEventID = firstUserTrace.FirstUserEventID
			branch.FirstUserText = firstUserTrace.FirstUserText
		}
		if lastTrace != nil && lastTrace.HasOpenTurn && branch.LastInteractionRow == len(events) {
			branch.OpenAtLogEnd = true
		}
		if branch.LastInteractionTime != nil {
			branch.Duration = branch.LastInteractionTime.Sub(point.ResumeEvent.Timestamp).Round(time.Millisecond).String()
		}
		if nextResumeRow > 0 && branch.LastInteractionRow >= nextResumeRow {
			branch.ContinuedPastNextResume = true
		}
		if nextShutdownRow > 0 && branch.LastInteractionRow >= nextShutdownRow {
			branch.ReachedShutdown = true
		}
		branch.Confidence = sessionResumeBranchConfidence(branch.Kind, branch.SeedReason, len(branch.ActiveInteractionIDs), len(branch.CompetingInteractionIDs))
		report.Branches = append(report.Branches, branch)
	}

	return report, nil
}

func validateSessionEvents(sessionID string, sampleLimit int) (*sessionEventValidationSummary, error) {
	eventsPath := sessionEventsPath(sessionID)
	if _, err := os.Stat(eventsPath); err != nil {
		return nil, fmt.Errorf("no local events found for session %s", sessionID)
	}
	return validateSessionEventsAtPath(sessionID, eventsPath, sampleLimit)
}

func validateSessionEventsAtPath(sessionID string, eventsPath string, sampleLimit int) (*sessionEventValidationSummary, error) {
	if sampleLimit < 0 {
		sampleLimit = 0
	}

	summary := &sessionEventValidationSummary{
		SessionID:   sessionID,
		EventsPath:  eventsPath,
		SampleLimit: sampleLimit,
	}
	issueCounts := make(map[string]int)
	resumeIssueCounts := make(map[string]int)
	priorEventTypesByID := make(map[string]string)
	var previousEvent *sessionEventValidationRowRef
	var previousShutdown *sessionEventValidationRowRef

	if err := visitJSONLRows(eventsPath, func(rowNo int, line []byte, raw map[string]any) error {
		rowsBeforeCurrent := summary.TotalRows
		summary.TotalRows++

		id, _ := raw["id"].(string)
		parentID, _ := raw["parentId"].(string)
		eventType := rawSessionEventType(raw)
		knownType := isKnownSDKSessionEventType(eventType)
		if knownType {
			summary.SDKKnownTypeRows++
		} else {
			summary.SDKUnknownTypeRows++
		}

		sdkCompatible := true
		sdkError := ""
		if _, err := copilot.UnmarshalSessionEvent(line); err != nil {
			sdkCompatible = false
			sdkError = err.Error()
			summary.SDKIncompatibleRows++
		} else {
			summary.SDKCompatibleRows++
		}

		localCompatible := parseSessionEvent(raw) != nil
		if localCompatible {
			summary.LocalCompatibleRows++
		} else {
			summary.LocalIncompatibleRows++
		}
		if localCompatible && !sdkCompatible {
			summary.LocalOnlyFallbackRows++
		}
		if !knownType && sdkCompatible {
			summary.UnknownTypeSDKCompatibleRows++
		}

		ts, _ := parseSessionTimestamp(raw["timestamp"])
		if eventType == copilot.SessionEventTypeSessionResume {
			summary.ResumeRows++

			resumeData, _ := raw["data"].(map[string]any)
			eventCount, eventCountOK := scalarInt64(resumeData["eventCount"])
			alreadyInUse := dataBool(resumeData, "alreadyInUse")
			eventCountMatches := eventCountOK && eventCount == int64(rowsBeforeCurrent)
			parentMatchesPreviousShutdown := previousShutdown != nil && parentID != "" && parentID == previousShutdown.ID
			parentMatchesPreviousEvent := previousEvent != nil && parentID != "" && parentID == previousEvent.ID
			resumeIssue := sessionResumeContinuityIssue(eventCountMatches, parentMatchesPreviousShutdown, parentMatchesPreviousEvent, alreadyInUse)
			switch resumeIssue {
			case "":
				summary.GracefulResumeRows++
			case "resume-while-in-use":
				summary.ResumeWhileInUseRows++
			case "resume-from-last-event":
				summary.ResumeFromLastEventRows++
			default:
				summary.SuspiciousResumeRows++
			}
			if resumeIssue != "" {
				resumeIssueCounts[resumeIssue]++
				if sampleLimit > 0 && len(summary.ResumeSamples) < sampleLimit {
					sample := sessionResumeContinuitySample{
						Row:                           rowNo,
						ID:                            id,
						Issue:                         resumeIssue,
						AlreadyInUse:                  alreadyInUse,
						ParentID:                      parentID,
						ParentType:                    priorEventTypesByID[parentID],
						EventCount:                    int(eventCount),
						RowsBeforeResume:              rowsBeforeCurrent,
						EventCountMatches:             eventCountMatches,
						ParentMatchesPreviousShutdown: parentMatchesPreviousShutdown,
						ParentMatchesPreviousEvent:    parentMatchesPreviousEvent,
					}
					if previousEvent != nil && !previousEvent.Timestamp.IsZero() && !ts.IsZero() {
						sample.GapSeconds = int64(ts.Sub(previousEvent.Timestamp).Seconds())
					}
					summary.ResumeSamples = append(summary.ResumeSamples, sample)
				}
			}
		}

		issue := sessionEventValidationIssue(knownType, sdkCompatible, localCompatible)
		if issue != "" {
			issueCounts[issue]++
			if sampleLimit > 0 && len(summary.Samples) < sampleLimit {
				timestampValue := scalarString(raw["timestamp"])
				if timestampValue != "" {
					timestampValue = truncateRunes(normalizeInlineText(timestampValue), 48)
				}
				summary.Samples = append(summary.Samples, sessionEventValidationSample{
					Row:             rowNo,
					ID:              id,
					Type:            string(eventType),
					SDKKnownType:    knownType,
					SDKCompatible:   sdkCompatible,
					LocalCompatible: localCompatible,
					Issue:           issue,
					TimestampKind:   jsonValueKind(raw["timestamp"]),
					TimestampValue:  timestampValue,
					DataKind:        jsonValueKind(raw["data"]),
					SDKError:        truncateRunes(normalizeInlineText(sdkError), 160),
				})
			}
		}

		current := &sessionEventValidationRowRef{
			Row:       rowNo,
			ID:        id,
			Type:      string(eventType),
			Timestamp: ts,
		}
		if id != "" {
			priorEventTypesByID[id] = current.Type
		}
		if eventType == copilot.SessionEventTypeSessionShutdown {
			previousShutdown = current
		}
		previousEvent = current
		return nil
	}); err != nil {
		return nil, err
	}

	summary.IssueCounts = sortedValidationIssueCounts(issueCounts)
	summary.ResumeIssueCounts = sortedValidationIssueCounts(resumeIssueCounts)

	return summary, nil
}

func dataString(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	value, _ := data[key].(string)
	return value
}

func dataScalarString(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	return scalarString(data[key])
}

func scalarString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case bool, float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%v", v)
	default:
		return ""
	}
}

func scalarInt64(value any) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int8:
		return int64(v), true
	case int16:
		return int64(v), true
	case int32:
		return int64(v), true
	case int64:
		return v, true
	case uint:
		return int64(v), true
	case uint8:
		return int64(v), true
	case uint16:
		return int64(v), true
	case uint32:
		return int64(v), true
	case uint64:
		if v > math.MaxInt64 {
			return 0, false
		}
		return int64(v), true
	case float32:
		if math.Trunc(float64(v)) != float64(v) {
			return 0, false
		}
		return int64(v), true
	case float64:
		if math.Trunc(v) != v {
			return 0, false
		}
		return int64(v), true
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i, true
		}
		if f, err := v.Float64(); err == nil && math.Trunc(f) == f {
			return int64(f), true
		}
	case string:
		if v == "" {
			return 0, false
		}
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return i, true
		}
		if f, err := strconv.ParseFloat(v, 64); err == nil && math.Trunc(f) == f {
			return int64(f), true
		}
	}
	return 0, false
}

func sortedValidationIssueCounts(counts map[string]int) []sessionEventValidationIssueCount {
	if len(counts) == 0 {
		return nil
	}
	issues := make([]string, 0, len(counts))
	for issue := range counts {
		issues = append(issues, issue)
	}
	sort.Strings(issues)
	sorted := make([]sessionEventValidationIssueCount, 0, len(issues))
	for _, issue := range issues {
		sorted = append(sorted, sessionEventValidationIssueCount{
			Issue: issue,
			Rows:  counts[issue],
		})
	}
	return sorted
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
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

func inlineScalarText(value string) string {
	return truncateRunes(normalizeInlineText(value), 120)
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
		if ev.sessionEventType() != copilot.SessionEventTypeUserMessage {
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
		eventType := ev.sessionEventType()
		if eventType != copilot.SessionEventTypeToolExecutionStart && eventType != copilot.SessionEventTypeToolExecutionComplete {
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

		switch eventType {
		case copilot.SessionEventTypeToolExecutionStart:
			if span.StartTime == nil {
				ts := ev.Timestamp
				span.StartTime = &ts
				span.StartEventID = ev.ID
			}
			if ev.ID != "" {
				consumedEventIDs[ev.ID] = struct{}{}
			}
		case copilot.SessionEventTypeToolExecutionComplete:
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

func describeCapabilitiesChanged(ev *sessionEvent) (string, string, []string) {
	if ui := dataMap(ev.Data, "ui"); ui != nil {
		if elicitation := dataBoolPtr(ui, "elicitation"); elicitation != nil {
			if *elicitation {
				return "Capabilities Changed", "UI elicitation enabled", nil
			}
			return "Capabilities Changed", "UI elicitation disabled", nil
		}
	}
	return "Capabilities Changed", "", nil
}

func describeSamplingEvent(label string, ev *sessionEvent) (string, string, []string) {
	serverName := dataString(ev.Data, "serverName")
	requestID := dataScalarString(ev.Data, "mcpRequestId")
	detail := ""
	switch {
	case serverName != "" && requestID != "":
		detail = fmt.Sprintf("%s (%s)", serverName, shortID(requestID))
	case serverName != "":
		detail = serverName
	case requestID != "":
		detail = "Request " + shortID(requestID)
	}

	var extraLines []string
	if model := dataString(ev.Data, "model"); model != "" {
		extraLines = append(extraLines, "Model: "+model)
	}
	if source := dataString(ev.Data, "initiator"); source != "" {
		extraLines = append(extraLines, "Initiator: "+source)
	}
	return label, detail, extraLines
}

func describeSessionCustomAgentsUpdated(ev *sessionEvent) (string, string, []string) {
	rawAgents, _ := ev.Data["agents"].([]any)
	names := make([]string, 0, len(rawAgents))
	for _, rawAgent := range rawAgents {
		agent, ok := rawAgent.(map[string]any)
		if !ok {
			continue
		}
		name := firstNonEmpty(
			dataString(agent, "displayName"),
			dataString(agent, "name"),
			dataString(agent, "id"),
		)
		if name != "" {
			names = append(names, name)
		}
	}

	detail := fmt.Sprintf("%d agents", len(rawAgents))
	if len(names) > 0 {
		detail = fmt.Sprintf("%s: %s", detail, truncateRunes(strings.Join(names, ", "), 120))
	}

	var extraLines []string
	if warnings, ok := ev.Data["warnings"].([]any); ok && len(warnings) > 0 {
		extraLines = append(extraLines, fmt.Sprintf("Warnings: %d", len(warnings)))
	}
	if errors, ok := ev.Data["errors"].([]any); ok && len(errors) > 0 {
		extraLines = append(extraLines, fmt.Sprintf("Errors: %d", len(errors)))
	}
	return "Custom Agents Updated", detail, extraLines
}

func describeSessionRemoteSteerableChanged(ev *sessionEvent) (string, string, []string) {
	if remoteSteerable := dataBoolPtr(ev.Data, "remoteSteerable"); remoteSteerable != nil {
		if *remoteSteerable {
			return "Remote Steering Changed", "Enabled", nil
		}
		return "Remote Steering Changed", "Disabled", nil
	}
	return "Remote Steering Changed", "", nil
}

func historyEventFamilyLabel(eventType string) string {
	switch {
	case strings.HasPrefix(eventType, "assistant."):
		return "Assistant Event"
	case strings.HasPrefix(eventType, "session."):
		return "Session Event"
	case strings.HasPrefix(eventType, "tool."):
		return "Tool Event"
	case strings.HasPrefix(eventType, "subagent."):
		return "Subagent Event"
	case strings.HasPrefix(eventType, "skill."):
		return "Skill Event"
	case strings.HasPrefix(eventType, "system."):
		return "System Event"
	case strings.HasPrefix(eventType, "permission."):
		return "Permission Event"
	case strings.HasPrefix(eventType, "external_tool."):
		return "External Tool Event"
	case strings.HasPrefix(eventType, "user_input."):
		return "User Input Event"
	case strings.HasPrefix(eventType, "elicitation."):
		return "Elicitation Event"
	case strings.HasPrefix(eventType, "command."):
		return "Command Event"
	case strings.HasPrefix(eventType, "mcp."):
		return "MCP Event"
	case strings.HasPrefix(eventType, "hook."):
		return "Hook Event"
	case strings.HasPrefix(eventType, "exit_plan_mode."):
		return "Plan Event"
	case strings.HasPrefix(eventType, "user."):
		return "User Event"
	default:
		return "Event"
	}
}

func rawDataInlineText(rawData any) string {
	return inlineScalarText(scalarString(rawData))
}

func bestEffortHistorySummary(ev *sessionEvent) string {
	if summary := inlineScalarText(eventText(ev.Data)); summary != "" {
		return summary
	}
	for _, candidate := range []string{
		dataString(ev.Data, "message"),
		dataString(ev.Data, "intent"),
		dataString(ev.Data, "question"),
		dataString(ev.Data, "summary"),
		dataString(ev.Data, "summaryContent"),
		nestedDataString(ev.Data, "arguments", "command"),
		nestedDataString(ev.Data, "arguments", "description"),
		nestedDataString(ev.Data, "error", "message"),
		nestedDataString(ev.Data, "permissionRequest", "path"),
		nestedDataString(ev.Data, "permissionRequest", "title"),
	} {
		if summary := inlineScalarText(candidate); summary != "" {
			return summary
		}
	}
	if transition := formatHistoryTransition(dataString(ev.Data, "previousMode"), dataString(ev.Data, "newMode")); transition != "" {
		return transition
	}
	if transition := formatHistoryTransition(dataString(ev.Data, "previousModel"), dataString(ev.Data, "newModel")); transition != "" {
		return transition
	}
	if path := dataString(ev.Data, "path"); path != "" {
		if operation := dataScalarString(ev.Data, "operation"); operation != "" {
			return fmt.Sprintf("%s: %s", operation, path)
		}
		return path
	}
	name := firstNonEmpty(
		dataString(ev.Data, "toolName"),
		dataString(ev.Data, "agentDisplayName"),
		dataString(ev.Data, "agentName"),
		dataString(ev.Data, "name"),
		dataString(ev.Data, "skillName"),
		dataString(ev.Data, "serverName"),
		dataString(ev.Data, "selectedModel"),
		dataString(ev.Data, "currentModel"),
	)
	if name != "" {
		if status := dataScalarString(ev.Data, "status"); status != "" && !strings.EqualFold(name, status) {
			return fmt.Sprintf("%s (%s)", name, status)
		}
		return name
	}
	if status := dataScalarString(ev.Data, "status"); status != "" {
		return status
	}
	if summary := rawDataInlineText(ev.RawData); summary != "" {
		return summary
	}
	if requestID := dataString(ev.Data, "requestId"); requestID != "" {
		return "Request " + shortID(requestID)
	}
	if toolCallID := dataString(ev.Data, "toolCallId"); toolCallID != "" {
		return "Tool Call " + shortID(toolCallID)
	}
	return ""
}

// Unknown future events should still render as meaningful rows instead of
// disappearing just because Copilot CLI shipped a new type before copilot-sdk
// (or this repository's explicit switch statements) learned about it.
func describeUnknownHistoryEvent(ev *sessionEvent) (string, string, []string) {
	label := historyEventFamilyLabel(ev.Type)
	detail := ev.Type
	if summary := bestEffortHistorySummary(ev); summary != "" {
		detail = fmt.Sprintf("%s: %s", ev.Type, summary)
	} else if ev.ID != "" {
		detail = fmt.Sprintf("%s (%s)", ev.Type, ev.ID)
	}

	extraLines := make([]string, 0, 8)
	seenExtraLines := make(map[string]struct{})
	appendExtraLine := func(line string) {
		if line == "" {
			return
		}
		if _, ok := seenExtraLines[line]; ok {
			return
		}
		seenExtraLines[line] = struct{}{}
		extraLines = append(extraLines, line)
	}

	if ev.Ephemeral != nil && *ev.Ephemeral {
		appendExtraLine("Ephemeral")
	}
	if requestID := dataString(ev.Data, "requestId"); requestID != "" {
		appendExtraLine("Request ID: " + shortID(requestID))
	}
	if toolCallID := dataString(ev.Data, "toolCallId"); toolCallID != "" {
		appendExtraLine("Tool Call ID: " + shortID(toolCallID))
	}
	if parentToolCallID := dataString(ev.Data, "parentToolCallId"); parentToolCallID != "" {
		appendExtraLine("Parent Tool Call ID: " + shortID(parentToolCallID))
	}
	if interactionID := dataString(ev.Data, "interactionId"); interactionID != "" {
		appendExtraLine("Interaction: " + shortID(interactionID))
	}
	if turnID := dataString(ev.Data, "turnId"); turnID != "" {
		appendExtraLine("Turn ID: " + turnID)
	}
	if model := dataString(ev.Data, "model"); model != "" && !strings.Contains(detail, model) {
		appendExtraLine("Model: " + model)
	}
	if status := dataScalarString(ev.Data, "status"); status != "" && !strings.Contains(detail, status) {
		appendExtraLine("Status: " + status)
	}
	if path := dataString(ev.Data, "path"); path != "" && !strings.Contains(detail, path) {
		appendExtraLine("Path: " + path)
	}
	if question := inlineScalarText(dataString(ev.Data, "question")); question != "" && !strings.Contains(detail, question) {
		appendExtraLine("Question: " + question)
	}
	if message := inlineScalarText(dataString(ev.Data, "message")); message != "" && !strings.Contains(detail, message) {
		appendExtraLine("Message: " + message)
	}
	if summary := inlineScalarText(dataString(ev.Data, "summary")); summary != "" && !strings.Contains(detail, summary) {
		appendExtraLine("Summary: " + summary)
	}
	if summary := inlineScalarText(dataString(ev.Data, "summaryContent")); summary != "" && !strings.Contains(detail, summary) {
		appendExtraLine("Summary: " + summary)
	}

	return label, detail, extraLines
}

func describeHistoryEvent(ctx *historyRenderContext, ev *sessionEvent) (string, string, []string) {
	switch ev.sessionEventType() {
	case copilot.SessionEventTypeUserMessage:
		return "User", eventText(ev.Data), nil
	case copilot.SessionEventTypeAssistantMessage:
		return "Assistant", eventText(ev.Data), nil
	case copilot.SessionEventTypeToolExecutionStart:
		toolName := dataString(ev.Data, "toolName")
		if toolName == "" {
			toolName = "<unknown>"
		}
		return "Tool Start", toolName, nil
	case copilot.SessionEventTypeToolExecutionComplete:
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
	case copilot.SessionEventTypeSessionStart:
		cwd := nestedDataString(ev.Data, "context", "cwd")
		if cwd == "" {
			return "Session Start", "", nil
		}
		return "Session Start", fmt.Sprintf("CWD: %s", cwd), nil
	case copilot.SessionEventTypeSessionResume:
		return "Session Resume", "", nil
	case copilot.SessionEventTypeSessionContextChanged:
		return describeSessionContextChanged(ev)
	case copilot.SessionEventTypeCapabilitiesChanged:
		return describeCapabilitiesChanged(ev)
	case copilot.SessionEventTypeSessionCompactionStart:
		return "Compaction Start", "", nil
	case copilot.SessionEventTypeSessionCompactionComplete:
		return describeSessionCompactionComplete(ev)
	case copilot.SessionEventTypeSessionModeChanged:
		return describeSessionModeChanged(ev)
	case copilot.SessionEventTypeSamplingRequested:
		return describeSamplingEvent("Sampling Requested", ev)
	case copilot.SessionEventTypeSamplingCompleted:
		return describeSamplingEvent("Sampling Completed", ev)
	case copilot.SessionEventTypeSessionCustomAgentsUpdated:
		return describeSessionCustomAgentsUpdated(ev)
	case copilot.SessionEventTypeSessionWorkspaceFileChanged:
		return describeSessionWorkspaceFileChanged(ev)
	case copilot.SessionEventTypeToolUserRequested:
		return describeToolUserRequested(ev)
	case copilot.SessionEventTypeSessionInfo:
		return describeSessionInfo(ev)
	case copilot.SessionEventTypeSessionModelChange:
		return describeSessionModelChange(ev)
	case copilot.SessionEventTypeSessionRemoteSteerableChanged:
		return describeSessionRemoteSteerableChanged(ev)
	case copilot.SessionEventTypeAssistantTurnStart:
		if turn, ok := ctx.turnStartByEventID[ev.ID]; ok {
			detail := fmt.Sprintf("Turn #%d, Segment %d, Turn ID %s", turn.TurnNumber, turn.SegmentNumber, turn.TurnID)
			if turn.InteractionID != "" {
				detail = fmt.Sprintf("%s, Interaction %s", detail, shortID(turn.InteractionID))
			}
			return "Assistant Turn Start", detail, nil
		}
		return "Assistant Turn Start", "", nil
	case copilot.SessionEventTypeAssistantTurnEnd:
		if turn, ok := ctx.turnEndByEventID[ev.ID]; ok {
			return "Assistant Turn End", fmt.Sprintf("Turn #%d, Duration: %s", turn.TurnNumber, turn.durationString(ctx.lastEventTime)), nil
		}
		return "Assistant Turn End", "", nil
	case copilot.SessionEventTypeSessionShutdown:
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
					// count tracks assistant-output volume by model, while cost is the
					// billed premium-request amount from shutdown. Nested subagent work
					// can leave count > 0 even when the billed cost for that model is 0.
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
	case copilot.SessionEventTypeSkillInvoked:
		name := dataString(ev.Data, "name")
		if name == "" {
			name = dataString(ev.Data, "skillName")
		}
		return "Skill Invoked", name, nil
	case copilot.SessionEventTypeSubagentStarted:
		name := dataString(ev.Data, "agentDisplayName")
		if name == "" {
			name = dataString(ev.Data, "agentName")
		}
		return "Subagent Started", name, nil
	case copilot.SessionEventTypeSubagentCompleted:
		name := dataString(ev.Data, "agentDisplayName")
		if name == "" {
			name = dataString(ev.Data, "agentName")
		}
		return "Subagent Completed", name, nil
	case copilot.SessionEventTypeSystemNotification:
		kind := nestedDataString(ev.Data, "kind", "type")
		if kind == "" {
			kind = dataString(ev.Data, "type")
		}
		if kind == "" {
			kind = "notification"
		}
		return "System Notification", kind, nil
	case copilot.SessionEventTypeSessionPlanChanged:
		return "Plan Changed", "", nil
	case copilot.SessionEventTypeAbort:
		return "Abort", "", nil
	default:
		return describeUnknownHistoryEvent(ev)
	}
}

func formatHistoryEventText(depth int, label string, detail string) string {
	indent := strings.Repeat("  ", depth)
	if detail == "" {
		return indent + label
	}
	return indent + fmt.Sprintf("%-*s %s", render.HistoryEventLabelWidth, label, detail)
}

func formatHistoryEventLabel(depth int, label string) string {
	return strings.Repeat("  ", depth) + label
}

func formatHistoryExtraLine(depth int, detail string) string {
	return strings.Repeat("  ", depth) + strings.Repeat(" ", render.HistoryEventLabelWidth+1) + detail
}

func buildToolStartEventIndex(events []*sessionEvent) map[string]*sessionEvent {
	toolStarts := make(map[string]*sessionEvent)
	for _, ev := range events {
		if ev.sessionEventType() != copilot.SessionEventTypeToolExecutionStart {
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
		if ev.sessionEventType() != copilot.SessionEventTypeToolExecutionStart {
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

func formatShortIDSummary(ids []string) string {
	if len(ids) == 0 {
		return "-"
	}
	if len(ids) == 1 {
		return shortID(ids[0])
	}
	return fmt.Sprintf("%s (+%d)", shortID(ids[0]), len(ids)-1)
}

func formatSignedInt(value int) string {
	if value > 0 {
		return fmt.Sprintf("+%d", value)
	}
	return strconv.Itoa(value)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func formatAPICostRate(value *float64) string {
	if value == nil {
		return "-"
	}
	return render.FormatFloatCompact(*value)
}

func formatUsageTokenKind(key string) string {
	base := strings.TrimSuffix(key, "Tokens")
	if base == "" {
		base = key
	}
	var b strings.Builder
	for i, r := range base {
		if i > 0 {
			prev := rune(base[i-1])
			if unicode.IsUpper(r) && (unicode.IsLower(prev) || unicode.IsDigit(prev)) {
				b.WriteByte(' ')
			} else if unicode.IsDigit(r) && unicode.IsLetter(prev) {
				b.WriteByte(' ')
			}
		}
		if i == 0 {
			r = unicode.ToUpper(r)
		}
		b.WriteRune(r)
	}
	if strings.HasSuffix(key, "Tokens") {
		b.WriteString(" Tokens")
	}
	return b.String()
}

func addShutdownUsageTokens(stat *analyze.ModelStat, usage map[string]any) {
	for key, raw := range usage {
		value, ok := raw.(float64)
		if !ok {
			continue
		}
		tokens := int64(value)
		switch key {
		case "inputTokens":
			stat.Input += tokens
		case "cacheReadTokens":
			stat.CacheRead += tokens
		case "cacheWriteTokens":
			stat.CacheWrite += tokens
		case "outputTokens":
			stat.Output += tokens
		default:
			stat.AddExtraUsage(key, tokens)
		}
	}
}

type statsAPICostDisplayLine struct {
	Kind     string
	Tokens   string
	Rate     string
	Subtotal string
}

func buildStatsAPICostDisplayLines(stat *analyze.ModelStat) []statsAPICostDisplayLine {
	if stat == nil {
		return nil
	}
	estimate := stat.EstimatedAPICost
	displayedInputTokens := stat.Input
	if estimate != nil {
		displayedInputTokens = estimate.UncachedInputTokens
	} else if stat.CacheRead > 0 && stat.Input >= stat.CacheRead {
		displayedInputTokens = stat.Input - stat.CacheRead
	}

	lines := []statsAPICostDisplayLine{{
		Kind:     "Input Tokens",
		Tokens:   strconv.FormatInt(displayedInputTokens, 10),
		Rate:     "-",
		Subtotal: "-",
	}}
	if estimate != nil {
		lines[0].Rate = render.FormatFloatCompact(estimate.InputUSDPerMTok)
		lines[0].Subtotal = render.FormatUSD(estimate.InputUSD)
	}

	if stat.CacheRead > 0 {
		line := statsAPICostDisplayLine{
			Kind:     "Cache Read Tokens",
			Tokens:   strconv.FormatInt(stat.CacheRead, 10),
			Rate:     "-",
			Subtotal: "-",
		}
		if estimate != nil {
			line.Rate = formatAPICostRate(estimate.CacheReadUSDPerMTok)
			if estimate.CacheReadUSDPerMTok != nil {
				line.Subtotal = render.FormatUSD(estimate.CacheReadUSD)
			}
		}
		lines = append(lines, line)
	}

	if stat.CacheWrite > 0 {
		line := statsAPICostDisplayLine{
			Kind:     "Cache Write Tokens",
			Tokens:   strconv.FormatInt(stat.CacheWrite, 10),
			Rate:     "-",
			Subtotal: "-",
		}
		if estimate != nil {
			line.Rate = formatAPICostRate(estimate.CacheWriteUSDPerMTok)
			if estimate.CacheWriteUSDPerMTok != nil {
				line.Subtotal = render.FormatUSD(estimate.CacheWriteUSD)
			}
		}
		lines = append(lines, line)
	}

	for _, key := range stat.SortedExtraUsageKeys() {
		lines = append(lines, statsAPICostDisplayLine{
			Kind:     formatUsageTokenKind(key),
			Tokens:   strconv.FormatInt(stat.ExtraUsageTokens[key], 10),
			Rate:     "-",
			Subtotal: "-",
		})
	}

	outputLine := statsAPICostDisplayLine{
		Kind:     "Output Tokens",
		Tokens:   strconv.FormatInt(stat.Output, 10),
		Rate:     "-",
		Subtotal: "-",
	}
	if estimate != nil {
		outputLine.Rate = render.FormatFloatCompact(estimate.OutputUSDPerMTok)
		outputLine.Subtotal = render.FormatUSD(estimate.OutputUSD)
		if containsString(estimate.MissingPriceComponents, "outputTokens") {
			outputLine.Subtotal = "-"
		}
	}
	lines = append(lines, outputLine)

	return lines
}

func joinStatsAPICostDisplayLines(lines []statsAPICostDisplayLine, selector func(statsAPICostDisplayLine) string) string {
	if len(lines) == 0 {
		return "-"
	}
	values := make([]string, 0, len(lines))
	for _, line := range lines {
		values = append(values, selector(line))
	}
	return strings.Join(values, "\n")
}

func formatStatsAPICostKinds(stat *analyze.ModelStat) string {
	return joinStatsAPICostDisplayLines(buildStatsAPICostDisplayLines(stat), func(line statsAPICostDisplayLine) string {
		return line.Kind
	})
}

func formatStatsAPICostTokenValues(stat *analyze.ModelStat) string {
	return joinStatsAPICostDisplayLines(buildStatsAPICostDisplayLines(stat), func(line statsAPICostDisplayLine) string {
		return line.Tokens
	})
}

func formatStatsAPICostRates(stat *analyze.ModelStat) string {
	return joinStatsAPICostDisplayLines(buildStatsAPICostDisplayLines(stat), func(line statsAPICostDisplayLine) string {
		return line.Rate
	})
}

func formatStatsAPICostSubtotals(stat *analyze.ModelStat) string {
	return joinStatsAPICostDisplayLines(buildStatsAPICostDisplayLines(stat), func(line statsAPICostDisplayLine) string {
		return line.Subtotal
	})
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

// Nested parents are grouped by parentToolCallId rather than parentId because
// the tool-span lineage stays useful even when the event DAG is incomplete for
// assistant.message rows and other nested control-plane activity.
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

		// toolCallId / parentToolCallId describe nested control-plane lineage and
		// are intentionally tracked separately from parentId event edges.
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

		// session.start/session.resume begin a new segment. turnId is only stable
		// within a segment, so open turn queues reset here instead of assuming
		// session-global turn numbering across resumes. This still applies when
		// an unclean recovery resumes from the last persisted event instead of a
		// preceding session.shutdown row.
		switch ev.sessionEventType() {
		case copilot.SessionEventTypeSessionStart, copilot.SessionEventTypeSessionResume:
			segmentNumber++
			openTurnsByID = make(map[string][]*sessionTurnWindow)
		case copilot.SessionEventTypeAssistantTurnStart:
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
		case copilot.SessionEventTypeAssistantTurnEnd:
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
		if parent := eventByID[turn.ParentEventID]; parent != nil && parent.sessionEventType() == copilot.SessionEventTypeUserMessage {
			turn.ParentUserEventID = parent.ID
			turn.UserMessage = eventText(parent.Data)
		}

		windowEnd := turn.effectiveEnd(lastEventTime)
		for _, ev := range events {
			if ev.Timestamp.Before(turn.StartTime) || ev.Timestamp.After(windowEnd) {
				continue
			}
			eventType := ev.sessionEventType()
			if eventType != copilot.SessionEventTypeSessionShutdown && ev.Timestamp.After(turn.lastActivityTime) {
				turn.lastActivityTime = ev.Timestamp
			}
			switch eventType {
			case copilot.SessionEventTypeAssistantMessage:
				if text := eventText(ev.Data); text != "" {
					turn.AssistantMessages = append(turn.AssistantMessages, text)
				}
			case copilot.SessionEventTypeToolExecutionStart:
				if toolName := dataString(ev.Data, "toolName"); toolName != "" {
					turn.ToolCalls[toolName]++
				}
			case copilot.SessionEventTypeToolExecutionComplete:
				if model := dataString(ev.Data, "model"); model != "" {
					turn.ModelCalls[model]++
				}
			case copilot.SessionEventTypeSkillInvoked:
				turn.SkillEvents++
			case copilot.SessionEventTypeSubagentStarted, copilot.SessionEventTypeSubagentCompleted:
				turn.SubagentEvents++
			case copilot.SessionEventTypeSessionPlanChanged:
				turn.PlanChangeEvents++
			case copilot.SessionEventTypeAbort:
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
	summaryTable := render.CreateTable([]string{"Metric", "Value"}, []int{1}, false, false, tableMode)
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
		table := render.CreateTable([]string{"Event Type", "Rows"}, []int{1}, false, false, tableMode)
		for _, item := range summary.MissingParentTypes {
			table.Append([]string{item.EventType, strconv.Itoa(item.Rows)})
		}
		table.Render()
	}

	if len(summary.InteractionHubs) > 0 {
		fmt.Println("\nInteraction Hubs:")
		table := render.CreateTable([]string{"Interaction", "Events", "Tool Calls", "Event Types"}, []int{1, 2}, false, false, tableMode)
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
		table := render.CreateTable([]string{"Parent Call", "Parent Tool", "Child Calls", "Child Tools", "Child Event Types", "Interaction"}, []int{2}, false, false, tableMode)
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

func showValidateEvents(ctx context.Context, client *copilot.Client, sessionID string, format string, sampleLimit int) {
	_ = ctx
	_ = client

	summary, err := validateSessionEvents(sessionID, sampleLimit)
	if err != nil {
		log.Printf("%v", err)
		return
	}

	if format == "yaml" {
		printYAML(summary)
		return
	}

	fmt.Printf("--- Session Event Validation for Session: %s ---\n", sessionID)
	summaryTable := render.CreateTable([]string{"Metric", "Value"}, []int{1}, false, false, tableMode)
	summaryTable.Append([]string{"Events path", summary.EventsPath})
	summaryTable.Append([]string{"Total rows", strconv.Itoa(summary.TotalRows)})
	summaryTable.Append([]string{"SDK-known type rows", strconv.Itoa(summary.SDKKnownTypeRows)})
	summaryTable.Append([]string{"SDK-unknown type rows", strconv.Itoa(summary.SDKUnknownTypeRows)})
	summaryTable.Append([]string{"SDK-compatible rows", strconv.Itoa(summary.SDKCompatibleRows)})
	summaryTable.Append([]string{"SDK-incompatible rows", strconv.Itoa(summary.SDKIncompatibleRows)})
	summaryTable.Append([]string{"Local-compatible rows", strconv.Itoa(summary.LocalCompatibleRows)})
	summaryTable.Append([]string{"Local-incompatible rows", strconv.Itoa(summary.LocalIncompatibleRows)})
	summaryTable.Append([]string{"Local-only fallback rows", strconv.Itoa(summary.LocalOnlyFallbackRows)})
	summaryTable.Append([]string{"Unknown type but SDK-compatible rows", strconv.Itoa(summary.UnknownTypeSDKCompatibleRows)})
	summaryTable.Append([]string{"Resume rows", strconv.Itoa(summary.ResumeRows)})
	summaryTable.Append([]string{"Graceful resume rows", strconv.Itoa(summary.GracefulResumeRows)})
	summaryTable.Append([]string{"Resume-while-in-use rows", strconv.Itoa(summary.ResumeWhileInUseRows)})
	summaryTable.Append([]string{"Resume-from-last-event rows", strconv.Itoa(summary.ResumeFromLastEventRows)})
	summaryTable.Append([]string{"Suspicious resume rows", strconv.Itoa(summary.SuspiciousResumeRows)})
	summaryTable.Render()

	if len(summary.IssueCounts) > 0 {
		fmt.Println("\nIssue Counts:")
		issueTable := render.CreateTable([]string{"Issue", "Rows"}, []int{1}, false, false, tableMode)
		for _, issue := range summary.IssueCounts {
			issueTable.Append([]string{issue.Issue, strconv.Itoa(issue.Rows)})
		}
		issueTable.Render()
	}

	if len(summary.ResumeIssueCounts) > 0 {
		fmt.Println("\nResume Continuity:")
		resumeIssueTable := render.CreateTable([]string{"Issue", "Rows"}, []int{1}, false, false, tableMode)
		for _, issue := range summary.ResumeIssueCounts {
			resumeIssueTable.Append([]string{issue.Issue, strconv.Itoa(issue.Rows)})
		}
		resumeIssueTable.Render()
	}

	if len(summary.Samples) > 0 {
		fmt.Println("\nSamples:")
		sampleTable := render.CreateTable([]string{"Row", "Type", "ID", "Issue", "SDK Known", "SDK", "Local", "Timestamp", "Data", "Error"}, []int{1, 3, 9}, false, false, tableMode)
		for _, sample := range summary.Samples {
			id := "-"
			if sample.ID != "" {
				id = shortID(sample.ID)
			}
			timestamp := sample.TimestampKind
			if sample.TimestampValue != "" {
				timestamp = fmt.Sprintf("%s (%s)", sample.TimestampKind, sample.TimestampValue)
			}
			sdkError := sample.SDKError
			if sdkError == "" {
				sdkError = "-"
			}
			sampleTable.Append([]string{
				strconv.Itoa(sample.Row),
				sample.Type,
				id,
				sample.Issue,
				strconv.FormatBool(sample.SDKKnownType),
				strconv.FormatBool(sample.SDKCompatible),
				strconv.FormatBool(sample.LocalCompatible),
				timestamp,
				sample.DataKind,
				sdkError,
			})
		}
		sampleTable.Render()
		if summary.SampleLimit > 0 && len(summary.Samples) >= summary.SampleLimit {
			fmt.Printf("Showing first %d non-OK rows.\n", summary.SampleLimit)
		}
	}

	if len(summary.ResumeSamples) > 0 {
		fmt.Println("\nResume Samples:")
		resumeSampleTable := render.CreateTable([]string{"Row", "ID", "Issue", "In Use", "Parent", "Parent Type", "Event Count", "Rows Before", "Count OK", "Prev Shutdown", "Prev Event", "Gap s"}, []int{1, 2, 5}, false, false, tableMode)
		for _, sample := range summary.ResumeSamples {
			id := "-"
			if sample.ID != "" {
				id = shortID(sample.ID)
			}
			parentID := "-"
			if sample.ParentID != "" {
				parentID = shortID(sample.ParentID)
			}
			parentType := sample.ParentType
			if parentType == "" {
				parentType = "-"
			}
			gapSeconds := "-"
			if sample.GapSeconds > 0 {
				gapSeconds = strconv.FormatInt(sample.GapSeconds, 10)
			}
			resumeSampleTable.Append([]string{
				strconv.Itoa(sample.Row),
				id,
				sample.Issue,
				strconv.FormatBool(sample.AlreadyInUse),
				parentID,
				parentType,
				strconv.Itoa(sample.EventCount),
				strconv.Itoa(sample.RowsBeforeResume),
				strconv.FormatBool(sample.EventCountMatches),
				strconv.FormatBool(sample.ParentMatchesPreviousShutdown),
				strconv.FormatBool(sample.ParentMatchesPreviousEvent),
				gapSeconds,
			})
		}
		resumeSampleTable.Render()
		if summary.SampleLimit > 0 && len(summary.ResumeSamples) >= summary.SampleLimit {
			fmt.Printf("Showing first %d non-graceful resume rows.\n", summary.SampleLimit)
		}
	}

	fmt.Println("\nNotes:")
	fmt.Println("- 'SDK known' is based on the current generated copilot.SessionEventType constants.")
	fmt.Println("- 'SDK compatible' means copilot.UnmarshalSessionEvent accepted the original JSONL row as-is.")
	fmt.Println("- 'Local compatible' means copilot-show's schema-light parser would still retain the event.")
	fmt.Println("- A graceful resume points parentId at the previous session.shutdown and keeps data.eventCount equal to the prior row count.")
	fmt.Println("- 'resume-while-in-use' means data.eventCount still matched, parentId pointed at the last persisted event, and data.alreadyInUse was true; this is consistent with another live process already holding the same session.")
	fmt.Println("- 'resume-from-last-event' means data.eventCount still matched and parentId pointed at the last persisted event without an alreadyInUse signal; this is a weaker heuristic that can indicate crash recovery or another unclean continuation.")
}

func showResumeBranches(ctx context.Context, client *copilot.Client, sessionID string, format string) {
	_ = ctx
	_ = client

	events, err := loadSessionEvents(sessionID)
	if err != nil {
		log.Printf("%v", err)
		return
	}

	report, err := buildSessionResumeBranchReport(sessionID, events)
	if err != nil {
		log.Printf("%v", err)
		return
	}

	if format == "yaml" {
		printYAML(report)
		return
	}

	fmt.Printf("--- Resume Branches for Session: %s ---\n", sessionID)
	summaryTable := render.CreateTable([]string{"Metric", "Value"}, []int{1}, false, false, tableMode)
	summaryTable.Append([]string{"Resume rows", strconv.Itoa(report.ResumeRows)})
	summaryTable.Append([]string{"Graceful resume rows", strconv.Itoa(report.GracefulResumeRows)})
	summaryTable.Append([]string{"Resume-while-in-use rows", strconv.Itoa(report.ResumeWhileInUseRows)})
	summaryTable.Append([]string{"Resume-from-last-event rows", strconv.Itoa(report.ResumeFromLastEventRows)})
	summaryTable.Append([]string{"Resume event-count mismatch rows", strconv.Itoa(report.ResumeEventCountMismatchRows)})
	summaryTable.Append([]string{"Resume parent mismatch rows", strconv.Itoa(report.ResumeParentMismatchRows)})
	summaryTable.Render()

	if len(report.Branches) == 0 {
		fmt.Println("\nNo session.resume rows found in the local log.")
		return
	}

	fmt.Println("\nInferred Branches:")
	table := render.CreateTable([]string{"Resume", "Kind", "Delta", "Active", "Branch", "Rows", "Duration", "Users", "Turns", "Tools", "Competing", "Conf"}, []int{0, 2, 3, 6, 7, 8, 9, 10}, false, false, tableMode)
	for _, branch := range report.Branches {
		rows := "-"
		if branch.FirstInteractionRow > 0 && branch.LastInteractionRow > 0 {
			rows = fmt.Sprintf("%d->%d", branch.FirstInteractionRow, branch.LastInteractionRow)
		}
		duration := branch.Duration
		if duration == "" {
			duration = "-"
		}
		table.Append([]string{
			strconv.Itoa(branch.ResumeRow),
			strings.TrimPrefix(branch.Kind, "resume-"),
			formatSignedInt(branch.EventCountDelta),
			strconv.Itoa(len(branch.ActiveInteractionIDs)),
			formatShortIDSummary(branch.BranchInteractionIDs),
			rows,
			duration,
			strconv.Itoa(branch.UserMessages),
			strconv.Itoa(branch.Turns),
			strconv.Itoa(branch.ToolCalls),
			strconv.Itoa(len(branch.CompetingInteractionIDs)),
			branch.Confidence,
		})
	}
	table.Render()

	fmt.Println("\nNotes:")
	fmt.Println("- Branches are inferred from interaction IDs first seen after each session.resume, then extended only while later new interactions stay non-overlapping.")
	fmt.Println("- When another new interaction begins before the current inferred chain closes, later ownership becomes ambiguous and moves to 'Competing'.")
	fmt.Println("- 'Delta' is data.eventCount - rows_before_resume. Stable +1 rows can be legacy behavior, so mismatch alone does not prove corruption.")
	fmt.Println("- Use `-f yaml` to inspect parent types, competing interaction IDs, last event types, models, and the first user message attached to each inferred branch.")
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
	table := render.CreateTable(header, []int{0, 1}, false, false, tableMode)
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
	var apiPricingOverridePath string
	var showAPIPricingTemplate bool
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show aggregate usage statistics from local session history",
		Run: func(cmd *cobra.Command, args []string) {
			if showAPIPricingTemplate {
				fmt.Print(analyze.APIPricingTemplate())
				return
			}
			if apiPricingOverridePath != "" {
				showAPICosts = true
			}
			showStats(outputFormat, showAllHistory, showAPICosts, apiPricingOverridePath)
		},
	}
	cmd.Flags().BoolVarP(&showAllHistory, "all", "a", false, "Show statistics for all time (default: current month UTC)")
	cmd.Flags().BoolVar(&showAPICosts, "api-costs", false, "Estimate equivalent API costs from token usage")
	cmd.Flags().StringVar(&apiPricingOverridePath, "api-pricing", "", "Overlay built-in API pricing with model prices from a local YAML file (implies --api-costs)")
	cmd.Flags().BoolVar(&showAPIPricingTemplate, "api-pricing-template", false, "Print a commented YAML template for --api-pricing and exit")
	return cmd
}

func showStats(format string, showAllHistory bool, showAPICosts bool, apiPricingOverridePath string) {
	home, _ := os.UserHomeDir()
	stateDir := filepath.Join(home, ".copilot", "session-state")
	entries, _ := os.ReadDir(stateDir)

	stats := make(map[string]*analyze.ModelStat)
	var totalPremiumRequests float64
	var apiPricingOverrides *analyze.APIPricingOverrides
	hasActiveAPIPricingOverrides := false
	if apiPricingOverridePath != "" {
		var err error
		apiPricingOverrides, err = analyze.LoadAPIPricingOverrides(apiPricingOverridePath)
		if err != nil {
			log.Printf("Error loading API pricing override: %v", err)
			return
		}
		hasActiveAPIPricingOverrides = apiPricingOverrides.HasActiveModels()
	}

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

			switch rawSessionEventType(ev) {
			case copilot.SessionEventTypeSessionShutdown:
				if total, ok := data["totalPremiumRequests"].(float64); ok {
					totalPremiumRequests += total
				}
				if metrics, ok := data["modelMetrics"].(map[string]any); ok {
					for model, m := range metrics {
						if mv, ok := m.(map[string]any); ok {
							if _, ok := stats[model]; !ok {
								stats[model] = &analyze.ModelStat{}
							}
							s := stats[model]
							if reqs, ok := mv["requests"].(map[string]any); ok {
								count, _ := reqs["count"].(float64)
								cost, _ := reqs["cost"].(float64)
								s.Requests += int64(count)
								s.Cost += cost
							}
							if usage, ok := mv["usage"].(map[string]any); ok {
								addShutdownUsageTokens(s, usage)
							}
						}
					}
				}
			case copilot.SessionEventTypeToolExecutionComplete:
				if hasShutdown {
					break
				}
				model, _ := data["model"].(string)
				if model != "" {
					if _, ok := stats[model]; !ok {
						stats[model] = &analyze.ModelStat{}
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
	hasExtraUsageTokens := false

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
		if len(s.SortedExtraUsageKeys()) > 0 {
			hasExtraUsageTokens = true
		}
		if !showAPICosts {
			continue
		}
		switch {
		case !s.HasTokenUsage():
			modelsWithoutTokenUsage = append(modelsWithoutTokenUsage, model)
		default:
			s.EstimatedAPICost = analyze.EstimateAPICostWithOverrides(model, s, apiPricingOverrides)
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
			apiPricingAssumption := "Estimates use built-in public API prices keyed by model ID."
			if hasActiveAPIPricingOverrides {
				apiPricingAssumption = fmt.Sprintf("Estimates use built-in public API prices keyed by model ID, then apply local YAML overrides from %s.", apiPricingOverrides.Path)
			}
			payload["estimatedApiCostUsd"] = totalEstimatedAPICostUSD
			payload["priceCatalogVersion"] = apiPricingOverrides.CatalogVersion()
			payload["pricedModels"] = pricedModels
			payload["partiallyPricedModels"] = partiallyPricedModels
			payload["modelsWithoutApiPricing"] = modelsWithoutAPIPricing
			payload["modelsWithoutTokenUsage"] = modelsWithoutTokenUsage
			payload["apiPricingSources"] = apiPricingOverrides.Sources()
			payload["apiPricingAssumptions"] = []string{
				apiPricingAssumption,
				"Model availability is plan-dependent; local shutdown metrics can still contain model IDs that are not currently visible in `copilot-show models`.",
				"Rows are still shown when a model lacks API pricing; those rows keep request and token counts, while API totals become lower bounds.",
				"Shutdown `inputTokens` are treated as total input that can already include `cacheReadTokens`; the estimate prices uncached input as `max(inputTokens - cacheReadTokens, 0)` and prices cache reads separately.",
				"OpenAI and Gemini pricing use standard short-context tiers; long-context, regional, storage, and batch adjustments are not modeled.",
				"Anthropic cache reads and Gemini context-cache reads use published cached-input rates; cache writes and storage are not priced because duration is not persisted in session logs.",
				"Active session tails without session.shutdown contribute request counts but not token-based costs.",
			}
			if hasActiveAPIPricingOverrides {
				payload["apiPricingOverridePath"] = apiPricingOverrides.Path
				payload["apiPricingOverrideModels"] = apiPricingOverrides.ModelIDs
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
	fmt.Printf(title, render.FormatFloatCompact(totalPremiumRequests))

	if len(stats) == 0 {
		fmt.Println("No detailed model statistics found for the selected period.")
		return
	}

	totalCostUSD := float64(totalPremiumRequests) * 0.04

	header := []string{"Model", "Req.", "PR"}
	rightAlignedCols := []int{1, 2}
	if showAPICosts && uiVersion == uiVersionNew {
		header = append(header, "Token Kind", "Tokens", "USD/Mtok", "Subtotal", "PR Cost", "API Cost")
		rightAlignedCols = append(rightAlignedCols, 4, 5, 6, 7, 8)
	} else {
		header = append(header, "Input Tokens")
		rightAlignedCols = append(rightAlignedCols, 3)
		if showAPICosts && hasCacheReadTokens {
			header = append(header, "Cache Read Tokens")
			rightAlignedCols = append(rightAlignedCols, len(header)-1)
		}
		if showAPICosts && hasCacheWriteTokens {
			header = append(header, "Cache Write Tokens")
			rightAlignedCols = append(rightAlignedCols, len(header)-1)
		}
		header = append(header, "Output Tokens", "PR Cost")
		rightAlignedCols = append(rightAlignedCols, len(header)-2, len(header)-1)
		if showAPICosts {
			header = append(header, "API Cost")
			rightAlignedCols = append(rightAlignedCols, len(header)-1)
		}
	}
	table := render.CreateTable(header, rightAlignedCols, false, false, tableMode)

	for _, m := range models {
		s := stats[m]
		overageEst := render.FormatUSD(s.EstimatedOverageCostUSD)
		if s.Cost == 0 {
			overageEst = "-"
		}
		row := []string{
			m,
			strconv.FormatInt(s.Requests, 10),
			render.FormatFloatCompact(s.Cost),
		}
		if showAPICosts {
			apiCost := "-"
			if s.EstimatedAPICost != nil {
				apiCost = render.FormatUSD(s.EstimatedAPICost.TotalUSD)
				if !s.EstimatedAPICost.IsComplete {
					apiCost = ">= " + apiCost
				}
			}
			if uiVersion == uiVersionNew {
				row = append(
					row,
					formatStatsAPICostKinds(s),
					formatStatsAPICostTokenValues(s),
					formatStatsAPICostRates(s),
					formatStatsAPICostSubtotals(s),
					overageEst,
					apiCost,
				)
			} else {
				row = append(row, strconv.FormatInt(s.Input, 10))
				if hasCacheReadTokens {
					row = append(row, strconv.FormatInt(s.CacheRead, 10))
				}
				if hasCacheWriteTokens {
					row = append(row, strconv.FormatInt(s.CacheWrite, 10))
				}
				row = append(row, strconv.FormatInt(s.Output, 10), overageEst)
				row = append(row, apiCost)
			}
		} else {
			row = append(row, strconv.FormatInt(s.Input, 10), strconv.FormatInt(s.Output, 10), overageEst)
		}
		table.Append(row)
	}
	table.Render()
	if !showAllHistory {
		fmt.Printf("\nEstimated Total Overage Cost (if quota is exhausted): %s USD\n", render.FormatUSD(totalCostUSD))
	} else {
		fmt.Printf("\nEstimated Total Overage Cost (across all history): %s USD\n", render.FormatUSD(totalCostUSD))
	}
	if showAPICosts {
		label := "Estimated Total API Cost (priced closed segments): %s USD\n"
		if len(partiallyPricedModels) > 0 || len(modelsWithoutAPIPricing) > 0 {
			label = "Estimated Total API Cost (lower bound from priced closed segments): %s USD\n"
		}
		fmt.Printf(label, render.FormatUSD(totalEstimatedAPICostUSD))
	}
	fmt.Println("Notes:")
	fmt.Println("- Overage cost uses $0.04 USD per premium request.")
	fmt.Println("- `PR` means premium requests and can be fractional because model multipliers are preserved from session shutdown metrics.")
	fmt.Println("- `PR Cost` is the hypothetical $0.04-per-PR overage charge; if your usage stays within the premium requests included in your plan, the actual overage billed can still be $0.")
	if showAPICosts {
		if !hasActiveAPIPricingOverrides {
			fmt.Println("- API cost uses built-in public token prices from OpenAI, Anthropic, and Google docs.")
		} else {
			fmt.Printf("- API cost uses built-in public token prices with local YAML overrides from %s.\n", apiPricingOverrides.Path)
		}
		fmt.Println("- Model availability is plan-dependent; local shutdown metrics can still contain model IDs that are not currently visible in `copilot-show models`.")
		fmt.Println("- Models without API pricing still appear in the table; their `API Cost` stays `-`, and the total becomes a lower bound.")
		if uiVersion == uiVersionNew {
			fmt.Println("- In the new table, `Input Tokens` shows only the uncached billed portion after subtracting `Cache Read Tokens`; raw shutdown `usage.inputTokens` still remains available in YAML output.")
		} else {
			fmt.Println("- `Input Tokens` are taken directly from shutdown `usage.inputTokens`. When `Cache Read Tokens` are present, the estimate treats them as a subset of input and prices only the uncached remainder at the full input-token rate.")
		}
		if uiVersion == uiVersionNew {
			fmt.Println("- The new table lays out `Token Kind`, `Tokens`, `USD/Mtok`, and `Subtotal` as aligned logical rows inside each model row.")
		}
		fmt.Println("- `Cache Read Tokens` use published cached-input or context-caching rates when the selected model has a verified read price.")
		if hasCacheWriteTokens {
			fmt.Println("- `Cache Write Tokens` are shown separately. If a model lacks a verified write or storage price, its API estimate becomes a lower bound.")
		} else {
			fmt.Println("- `Cache Write Tokens` are currently zero in the selected local history, so write pricing did not affect this estimate.")
		}
		if hasExtraUsageTokens {
			fmt.Println("- Additional shutdown `usage` keys are shown as extra token rows in the new table. They currently have no built-in API pricing, so affected model totals remain lower bounds until explicit support is added.")
		}
		fmt.Println("- OpenAI and Gemini estimates use standard short-context tiers. Long-context, regional, fast-mode, batch, and tool-call surcharges are not modeled.")
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
