package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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

const version = "0.1.5"

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
		Version: version,
		Run: func(cmd *cobra.Command, args []string) {
			_ = cmd.Help()
		},
	}

	rootCmd.PersistentFlags().StringVarP(&outputFormat, "format", "f", "table", "Output format (table, yaml)")
	rootCmd.PersistentFlags().StringVar(&tableMode, "table-mode", "default", "Table mode (default, ascii, markdown)")
	rootCmd.PersistentFlags().StringVar(&uiVersion, "ui-version", "v2", "UI version for A/B testing (v1, v2)")

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

	if uiVersion == "v2" {
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
	} else {
		// v1 (Legacy) simple table style
		if tableMode == "markdown" {
			opts = append(opts, tablewriter.WithRenderer(renderer.NewMarkdown()))
		} else if tableMode == "ascii" {
			// tablewriter v1.1.3 doesn't have a simple ascii renderer, but we can use default
		}
	}

	table := tablewriter.NewTable(os.Stdout, opts...)

	table.Configure(func(cfg *tablewriter.Config) {
		cfg.Row.Formatting.AutoWrap = tw.WrapNormal
		cfg.Row.Formatting.AutoFormat = tw.Off
		cfg.Header.Formatting.AutoFormat = tw.Off
		cfg.Header.Alignment.Global = tw.AlignLeft

		if uiVersion == "v2" && hierarchicalMerge {
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

func newHistoryCmd(client *copilot.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "history [sessionID]",
		Short: "Show conversation history for a session",
		Args:  cobra.MaximumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			var sessionID string
			if len(args) > 0 {
				sessionID = args[0]
			} else {
				// 1. Check Foreground
				if fg, _ := client.GetForegroundSessionID(cmd.Context()); fg != nil {
					sessionID = *fg
				} else {
					// 2. Check Last
					if last, _ := client.GetLastSessionID(cmd.Context()); last != nil {
						sessionID = *last
					}
				}

				if sessionID == "" {
					log.Printf("No session ID provided and no foreground/last session found")
					return
				}
			}
			showHistory(cmd.Context(), client, sessionID, outputFormat)
		},
	}
}

func showHistory(ctx context.Context, client *copilot.Client, sessionID string, format string) {
	home, _ := os.UserHomeDir()
	eventsPath := filepath.Join(home, ".copilot", "session-state", sessionID, "events.jsonl")

	if _, err := os.Stat(eventsPath); err != nil {
		log.Printf("No local events found for session %s", sessionID)
		return
	}

	f, err := os.Open(eventsPath)
	if err != nil {
		log.Printf("Error opening events file: %v", err)
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var events []map[string]any
	eventMap := make(map[string]map[string]any)

	for scanner.Scan() {
		var ev map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &ev); err == nil {
			events = append(events, ev)
			if id, ok := ev["id"].(string); ok {
				eventMap[id] = ev
			}
		}
	}

	if format == "yaml" {
		printYAML(events)
		return
	}

	// Build a simple tree for indentation and latency
	// We'll also calculate latency from parent or previous event
	var prevTs time.Time

	for i, ev := range events {
		evType, _ := ev["type"].(string)
		id, _ := ev["id"].(string)
		parentId, _ := ev["parentId"].(string)
		timestampStr, _ := ev["timestamp"].(string)
		data, _ := ev["data"].(map[string]any)

		ts, _ := time.Parse(time.RFC3339, timestampStr)

		// Calculate latency from parent if possible, else from previous event
		var latencyStr string
		if i > 0 {
			latency := ts.Sub(prevTs)
			latencyStr = fmt.Sprintf("+%v", latency.Round(time.Millisecond))
		}
		if parentId != "" {
			if parent, ok := eventMap[parentId]; ok {
				ptsStr, _ := parent["timestamp"].(string)
				pts, err := time.Parse(time.RFC3339, ptsStr)
				if err == nil {
					pLatency := ts.Sub(pts)
					latencyStr = fmt.Sprintf(" (p+%v)", pLatency.Round(time.Millisecond))
				}
			}
		}
		prevTs = ts

		// Determine indentation (very simplified: root or not)
		indent := ""
		if parentId != "" {
			indent = "  "
		}

		displayTs := ts.Local().Format("15:04:05.000")
		prefix := fmt.Sprintf("[%s]%s %s", displayTs, latencyStr, indent)

		switch evType {
		case "user.message":
			content, _ := data["content"].(string)
			if content == "" {
				content, _ = data["transformedContent"].(string)
			}
			fmt.Printf("%s User: %s\n", prefix, strings.ReplaceAll(content, "\n", " "))
		case "assistant.message":
			content, _ := data["content"].(string)
			fmt.Printf("%s Assistant: %s\n", prefix, strings.ReplaceAll(content, "\n", " "))
		case "tool.execution_start":
			toolName, _ := data["toolName"].(string)
			fmt.Printf("%s Tool Start: %s\n", prefix, toolName)
		case "tool.execution_complete":
			toolName, _ := data["toolName"].(string)
			model, _ := data["model"].(string)
			success, _ := data["success"].(bool)
			modelStr := ""
			if model != "" {
				modelStr = fmt.Sprintf(" [%s]", model)
			}
			fmt.Printf("%s Tool End: %s%s (Success: %v)\n", prefix, toolName, modelStr, success)
		case "session.start":
			context, _ := data["context"].(map[string]any)
			cwd, _ := context["cwd"].(string)
			fmt.Printf("%s Session Start (CWD: %s)\n", prefix, cwd)
		case "assistant.turn_start":
			fmt.Printf("%s Assistant Turn Start\n", prefix)
		case "assistant.turn_end":
			fmt.Printf("%s Assistant Turn End\n", prefix)
		case "session.shutdown":
			total, _ := data["totalPremiumRequests"].(float64)
			fmt.Printf("%s Session Shutdown (Total Premium Requests: %.0f)\n", prefix, total)
			if metrics, ok := data["modelMetrics"].(map[string]any); ok {
				for model, m := range metrics {
					if mv, ok := m.(map[string]any); ok {
						if reqs, ok := mv["requests"].(map[string]any); ok {
							count, _ := reqs["count"].(float64)
							cost, _ := reqs["cost"].(float64)
							fmt.Printf("%s   Model: %s (Requests: %.0f, Cost: %.0f)\n", indent, model, count, cost)
						}
						if usage, ok := mv["usage"].(map[string]any); ok {
							in, _ := usage["inputTokens"].(float64)
							out, _ := usage["outputTokens"].(float64)
							fmt.Printf("%s     Tokens: In: %.0f, Out: %.0f\n", indent, in, out)
						}
					}
				}
			}
		case "abort":
			fmt.Printf("%s Abort\n", prefix)
		default:
			fmt.Printf("%s Event: %s (%s)\n", prefix, evType, id)
		}
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
	normalize := func(s string) string {
		s = strings.ReplaceAll(strings.ReplaceAll(strings.ToLower(s), " ", ""), "-", "")
		return strings.TrimSuffix(s, "preview")
	}

	modelsList, err := client.RPC.Models.List(ctx)
	if err == nil {
		for _, m := range modelsList.Models {
			if m.Billing != nil {
				multiplierMap[normalize(m.Name)] = m.Billing.Multiplier
				multiplierMap[normalize(m.ID)] = m.Billing.Multiplier
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
			if m, ok := multiplierMap[normalize(item.Model)]; ok {
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
		if uiVersion == "v2" {
			if usageItems[i].SKU != usageItems[j].SKU {
				return natural.Less(strings.ToLower(usageItems[i].SKU), strings.ToLower(usageItems[j].SKU))
			}
			return natural.Less(strings.ToLower(usageItems[i].Model), strings.ToLower(usageItems[j].Model))
		}
		// v1 (Legacy) simple lexicographical sort
		if usageItems[i].SKU != usageItems[j].SKU {
			return usageItems[i].SKU < usageItems[j].SKU
		}
		return usageItems[i].Model < usageItems[j].Model
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
	if uiVersion == "v1" {
		header = []string{"Period", "SKU", "Model", "Used (req.)", "Billed (req.)", "Amount (USD)"}
	}
	if last == 0 {
		header = header[1:] // Remove Period column if only one response
	}
	table := createTable(header, []int{len(header) - 4, len(header) - 3, len(header) - 2, len(header) - 1}, uiVersion == "v2" && last > 0, uiVersion == "v2" && last > 0)

	if uiVersion == "v2" {
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
	} else {
		// v1 (Legacy) simple flat table
		for _, item := range usageItems {
			row := []string{
				item.Period,
				item.SKU,
				item.Model,
				strconv.FormatFloat(item.GrossQuantity, 'f', -1, 64),
				strconv.FormatFloat(item.NetQuantity, 'f', -1, 64),
				fmt.Sprintf("$%.2f", item.NetAmount),
			}
			if last == 0 {
				row = row[1:]
			}
			table.Append(row)
		}
	}
	table.Render()
	fmt.Println("\nNotes:")
	if uiVersion == "v2" {
		fmt.Println("- 'Multiplier' is the request consumption rate per interaction for the model.")
	}
	fmt.Println("- 'Used (req.)' is the total premium requests consumed.")
	fmt.Println("- 'Billed (req.)' is the overage amount you are billed for.")
	fmt.Println("- 'Amount (USD)' is the total billed cost in USD.")
	fmt.Println("- 'req.' stands for 'requests'.")
}

func newTurnsCmd(client *copilot.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "turns [sessionID]",
		Short: "Show turn-by-turn usage statistics for a session",
		Args:  cobra.MaximumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			var sessionID string
			if len(args) > 0 {
				sessionID = args[0]
			} else {
				if fg, _ := client.GetForegroundSessionID(cmd.Context()); fg != nil {
					sessionID = *fg
				} else if last, _ := client.GetLastSessionID(cmd.Context()); last != nil {
					sessionID = *last
				}
				if sessionID == "" {
					log.Printf("No session ID provided and no foreground/last session found")
					return
				}
			}
			showTurns(sessionID, outputFormat)
		},
	}
}

func showTurns(sessionID string, format string) {
	home, _ := os.UserHomeDir()
	eventsPath := filepath.Join(home, ".copilot", "session-state", sessionID, "events.jsonl")

	if _, err := os.Stat(eventsPath); err != nil {
		log.Printf("No local events found for session %s", sessionID)
		return
	}

	f, err := os.Open(eventsPath)
	if err != nil {
		log.Printf("Error opening events file: %v", err)
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	type turnInfo struct {
		TurnID    string            `json:"turnId" yaml:"turnId"`
		StartTime time.Time         `json:"startTime" yaml:"startTime"`
		EndTime   *time.Time        `json:"endTime,omitempty" yaml:"endTime,omitempty"`
		Models    map[string]int    `json:"models" yaml:"models"` // model -> requests
		Messages  []string          `json:"messages" yaml:"messages"`
	}

	var turns []*turnInfo
	turnMap := make(map[string]*turnInfo) // interactionId -> turn
	idToEvent := make(map[string]map[string]any)

	for scanner.Scan() {
		var ev map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			continue
		}
		id, _ := ev["id"].(string)
		evType, _ := ev["type"].(string)
		data, _ := ev["data"].(map[string]any)
		ts, _ := time.Parse(time.RFC3339, ev["timestamp"].(string))
		idToEvent[id] = ev

		switch evType {
		case "assistant.turn_start":
			tID, _ := data["turnId"].(string)
			iID, _ := data["interactionId"].(string)
			turn := &turnInfo{
				TurnID:    tID,
				StartTime: ts,
				Models:    make(map[string]int),
			}
			turns = append(turns, turn)
			if iID != "" {
				turnMap[iID] = turn
			}
		case "assistant.turn_end":
			tID, _ := data["turnId"].(string)
			// turn_end might not have interactionId, but parent/turn context can be used
			// In our simplified model, the latest turn with matching ID might be it
			for i := len(turns) - 1; i >= 0; i-- {
				if turns[i].TurnID == tID && turns[i].EndTime == nil {
					turns[i].EndTime = &ts
					break
				}
			}
		case "tool.execution_complete":
			model, _ := data["model"].(string)
			iID, _ := data["interactionId"].(string)
			if model != "" && iID != "" {
				if t, ok := turnMap[iID]; ok {
					t.Models[model]++
				}
			}
		case "user.message":
			content, _ := data["content"].(string)
			if content == "" {
				content, _ = data["transformedContent"].(string)
			}
			// User message starts a sequence. We don't have interactionId yet, 
			// but we can associate it with the next turn.
			// (Simplified: just keep track of recent message)
		case "assistant.message":
			// Interaction ID is often available here too
			iID, _ := data["interactionId"].(string)
			if iID != "" {
				if t, ok := turnMap[iID]; ok {
					content, _ := data["content"].(string)
					if content != "" {
						t.Messages = append(t.Messages, content)
					}
				}
			}
		case "session.shutdown":
			// shutdown metrics are session-wide
		}
	}

	if format == "yaml" {
		printYAML(turns)
		return
	}

	fmt.Printf("--- Turn Usage for Session: %s ---\n", sessionID)
	header := []string{"Turn", "Start Time", "Duration", "Model Calls", "Summary"}
	table := createTable(header, nil, false, false)

	for _, t := range turns {
		duration := "-"
		if t.EndTime != nil {
			duration = t.EndTime.Sub(t.StartTime).Round(time.Millisecond).String()
		}
		
		var models []string
		for m, count := range t.Models {
			models = append(models, fmt.Sprintf("%s (%d)", m, count))
		}
		sort.Strings(models)
		modelStr := strings.Join(models, ", ")
		if modelStr == "" {
			modelStr = "-"
		}

		summary := "-"
		if len(t.Messages) > 0 {
			summary = t.Messages[0]
			if len(summary) > 40 {
				summary = summary[:40] + "..."
			}
		}

		table.Append([]string{
			t.TurnID,
			t.StartTime.Local().Format("15:04:05"),
			duration,
			modelStr,
			summary,
		})
	}
	table.Render()
}

func newStatsCmd() *cobra.Command {
	var showAllHistory bool
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show aggregate usage statistics from local session history",
		Run: func(cmd *cobra.Command, args []string) {
			showStats(outputFormat, showAllHistory)
		},
	}
	cmd.Flags().BoolVarP(&showAllHistory, "all", "a", false, "Show statistics for all time (default: current month UTC)")
	return cmd
}

func showStats(format string, showAllHistory bool) {
	home, _ := os.UserHomeDir()
	stateDir := filepath.Join(home, ".copilot", "session-state")
	entries, _ := os.ReadDir(stateDir)

	type modelStat struct {
		Requests int64 `json:"requests" yaml:"requests"`
		Cost     int64 `json:"cost" yaml:"cost"`
		Input    int64 `json:"inputTokens" yaml:"inputTokens"`
		Output   int64 `json:"outputTokens" yaml:"outputTokens"`
	}
	stats := make(map[string]*modelStat)
	var totalPremiumRequests int64

	now := time.Now().UTC()
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		eventsPath := filepath.Join(stateDir, entry.Name(), "events.jsonl")
		f, err := os.Open(eventsPath)
		if err != nil {
			continue
		}

		var sessionEvents []map[string]any
		scanner := bufio.NewScanner(f)
		hasShutdown := false
		for scanner.Scan() {
			var ev map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &ev); err == nil {
				sessionEvents = append(sessionEvents, ev)
				if ev["type"] == "session.shutdown" {
					hasShutdown = true
				}
			}
		}
		f.Close()

		for _, ev := range sessionEvents {
			if !showAllHistory {
				timestampStr, _ := ev["timestamp"].(string)
				ts, err := time.Parse(time.RFC3339, timestampStr)
				if err == nil && ts.Before(startOfMonth) {
					continue
				}
			}

			data, _ := ev["data"].(map[string]any)
			if data == nil {
				continue
			}

			if ev["type"] == "session.shutdown" {
				if total, ok := data["totalPremiumRequests"].(float64); ok {
					totalPremiumRequests += int64(total)
				}
				if metrics, ok := data["modelMetrics"].(map[string]any); ok {
					for model, m := range metrics {
						if mv, ok := m.(map[string]any); ok {
							if _, ok := stats[model]; !ok {
								stats[model] = &modelStat{}
							}
							s := stats[model]
							if reqs, ok := mv["requests"].(map[string]any); ok {
								count, _ := reqs["count"].(float64)
								cost, _ := reqs["cost"].(float64)
								s.Requests += int64(count)
								s.Cost += int64(cost)
							}
							if usage, ok := mv["usage"].(map[string]any); ok {
								in, _ := usage["inputTokens"].(float64)
								out, _ := usage["outputTokens"].(float64)
								s.Input += int64(in)
								s.Output += int64(out)
							}
						}
					}
				}
			} else if !hasShutdown && ev["type"] == "tool.execution_complete" {
				// Ongoing session: estimate from individual tool completions
				model, _ := data["model"].(string)
				if model != "" {
					if _, ok := stats[model]; !ok {
						stats[model] = &modelStat{}
					}
					s := stats[model]
					s.Requests++
					// Since we don't know the exact cost logic here (it's server side), 
					// we just count it as 1 request.
					// Note: This is an estimation for active sessions.
				}
			}
		}
	}

	if format == "yaml" {
		printYAML(map[string]any{
			"totalPremiumRequests": totalPremiumRequests,
			"modelStats":           stats,
			"isCurrentMonthOnly":   !showAllHistory,
		})
		return
	}

	title := "Total Premium Requests (Current Month UTC): %d\n\n"
	if showAllHistory {
		title = "Total Premium Requests (All Local History): %d\n\n"
	}
	fmt.Printf(title, totalPremiumRequests)

	if len(stats) == 0 {
		fmt.Println("No detailed model statistics found for the selected period.")
		return
	}

	totalCostUSD := float64(totalPremiumRequests) * 0.04

	header := []string{"Model", "Requests", "Premium Requests (Cost)", "Input Tokens", "Output Tokens", "Est. Overage Cost"}
	table := createTable(header, []int{1, 2, 3, 4, 5}, false, false)

	var models []string
	for m := range stats {
		models = append(models, m)
	}
	sort.Strings(models)

	for _, m := range models {
		s := stats[m]
		overageEst := fmt.Sprintf("$%.2f", float64(s.Cost)*0.04)
		if s.Cost == 0 {
			overageEst = "-"
		}
		table.Append([]string{
			m,
			strconv.FormatInt(s.Requests, 10),
			strconv.FormatInt(s.Cost, 10),
			strconv.FormatInt(s.Input, 10),
			strconv.FormatInt(s.Output, 10),
			overageEst,
		})
	}
	table.Render()
	if !showAllHistory {
		fmt.Printf("\nEstimated Total Overage Cost (if quota is exhausted): $%.2f USD\n", totalCostUSD)
	} else {
		fmt.Printf("\nEstimated Total Overage Cost (across all history): $%.2f USD\n", totalCostUSD)
	}
	fmt.Println("Note: Cost estimation is based on $0.04 USD per premium request for overage usage.")
}

func getTerminalWidth() int {
	size, err := ts.GetSize()
	if err != nil || size.Col() <= 0 {
		return 80 // Default fallback
	}
	return size.Col()
}
