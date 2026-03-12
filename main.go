package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
	"github.com/goccy/go-yaml"
	"github.com/olekukonko/tablewriter"
	"github.com/olekukonko/tablewriter/tw"
	"github.com/olekukonko/ts"
	"github.com/spf13/cobra"
)

const version = "0.1.0"

var (
	outputFormat string
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

	rootCmd.AddCommand(newQuotaCmd(client))
	rootCmd.AddCommand(newModelsCmd(client))
	rootCmd.AddCommand(newToolsCmd(client))

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

func configureTable(table *tablewriter.Table, header []string, rightAlignedCols []int) {
	table.Configure(func(cfg *tablewriter.Config) {
		cfg.MaxWidth = getTerminalWidth()
		cfg.Row.Formatting.AutoWrap = tw.WrapNormal
		cfg.Row.Formatting.AutoFormat = tw.Off
		cfg.Header.Formatting.AutoFormat = tw.Off
		cfg.Header.Alignment.Global = tw.AlignLeft
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

	table := tablewriter.NewWriter(os.Stdout)
	header := []string{"Metric", "Entitlement", "Used", "Overage", "Usage %"}
	configureTable(table, header, []int{1, 2, 3, 4})

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
	fmt.Println("\nPlan Reference (Approximate Monthly Entitlement):")
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
	fmt.Println("Note: Quotas reset on the 1st of each month at 00:00 UTC.")
	fmt.Println("Note: 'Overage' shows the overage amount and whether it is permitted.")
	fmt.Println("Note: Each interaction's cost depends on the model's multiplier (e.g., Claude 4.6 Opus is 3x).")
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

	table := tablewriter.NewWriter(os.Stdout)
	header := []string{"ID", "Name", "Multiplier", "Context", "Output", "Prompt", "Vision", "Reasoning", "Efforts", "State"}
	configureTable(table, header, []int{2, 3, 4, 5})

	for _, m := range models.Models {
		multiplier := "-"
		if m.Billing != nil {
			multiplier = strconv.FormatFloat(m.Billing.Multiplier, 'f', -1, 64)
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

		table.Append([]string{
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
		})
	}
	table.Render()
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

	table := tablewriter.NewWriter(os.Stdout)
	header := []string{"Name", "Description", "Namespaced Name"}
	configureTable(table, header, nil)

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

		table := tablewriter.NewWriter(os.Stdout)
		header := []string{"Name", "Display Name", "Description"}
		configureTable(table, header, nil)

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
			configureTable(table, header, nil)
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
			configureTable(table, header, nil)
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
	table := tablewriter.NewWriter(os.Stdout)
	configureTable(table, []string{"Property", "Value"}, nil)
	table.Append([]string{"Version", status.Version})
	table.Append([]string{"Protocol Version", fmt.Sprintf("%d", status.ProtocolVersion)})
	table.Render()

	fmt.Println("\n--- Auth Status ---")
	tableAuth := tablewriter.NewWriter(os.Stdout)
	configureTable(tableAuth, []string{"Property", "Value"}, nil)
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

	table := tablewriter.NewWriter(os.Stdout)
	header := []string{"ID", "CWD", "Start Time", "Modified Time", "Status", "PIDs"}
	configureTable(table, header, nil)

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
	// SDK workaround: we need a way to call session.getMessages without a Session object
	// since we only have sessionID. client.RPC is ServerRpc which doesn't have SessionRpc.
	// We'll try to read local events.jsonl as a fallback/primary source since we analyzed it.

	home, _ := os.UserHomeDir()
	eventsPath := filepath.Join(home, ".copilot", "session-state", sessionID, "events.jsonl")

	if _, err := os.Stat(eventsPath); err == nil {
		f, err := os.Open(eventsPath)
		if err == nil {
			defer f.Close()
			scanner := bufio.NewScanner(f)
			var events []any
			for scanner.Scan() {
				var ev any
				if err := json.Unmarshal(scanner.Bytes(), &ev); err == nil {
					events = append(events, ev)
				}
			}

			if format == "yaml" {
				printYAML(events)
				return
			}

			// Simple display for events
			for _, ev := range events {
				m, ok := ev.(map[string]any)
				if !ok {
					continue
				}
				evType, _ := m["type"].(string)
				timestamp, _ := m["timestamp"].(string)
				data, _ := m["data"].(map[string]any)

				switch evType {
				case "user.message":
					content, _ := data["content"].(string)
					fmt.Printf("[%s] User: %s\n", timestamp, content)
				case "agent.message":
					content, _ := data["content"].(string)
					fmt.Printf("[%s] Agent: %s\n", timestamp, content)
				case "session.start":
					context, _ := data["context"].(map[string]any)
					cwd, _ := context["cwd"].(string)
					fmt.Printf("[%s] Session Start (CWD: %s)\n", timestamp, cwd)
				default:
					fmt.Printf("[%s] Event: %s\n", timestamp, evType)
				}
			}
			return
		}
	}

	log.Printf("No local events found for session %s and SDK method is currently unavailable", sessionID)
}

func getTerminalWidth() int {
	size, err := ts.GetSize()
	if err != nil || size.Col() <= 0 {
		return 80 // Default fallback
	}
	return size.Col()
}
