package modeldocs

import (
	"sort"
	"strings"
	"unicode"

	"github.com/github/copilot-sdk/go/rpc"
)

const CatalogVersion = "github-docs-tables-2026-03-20"

const SourceNote = "This catalog is a hardcoded snapshot derived from github/docs data/tables/copilot. Treat it as documentation-oriented reference data, not runtime source-of-truth."

type Sources struct {
	ReleaseStatus      string `json:"releaseStatus" yaml:"releaseStatus"`
	SupportedClients   string `json:"supportedClients" yaml:"supportedClients"`
	SupportedPlans     string `json:"supportedPlans" yaml:"supportedPlans"`
	ModelComparison    string `json:"modelComparison" yaml:"modelComparison"`
	DeprecationHistory string `json:"deprecationHistory" yaml:"deprecationHistory"`
}

var CatalogSources = Sources{
	ReleaseStatus:      "https://raw.githubusercontent.com/github/docs/main/data/tables/copilot/model-release-status.yml",
	SupportedClients:   "https://raw.githubusercontent.com/github/docs/main/data/tables/copilot/model-supported-clients.yml",
	SupportedPlans:     "https://raw.githubusercontent.com/github/docs/main/data/tables/copilot/model-supported-plans.yml",
	ModelComparison:    "https://raw.githubusercontent.com/github/docs/main/data/tables/copilot/model-comparison.yml",
	DeprecationHistory: "https://raw.githubusercontent.com/github/docs/main/data/tables/copilot/model-deprecation-history.yml",
}

type ClientAvailability struct {
	Dotcom    bool `json:"dotcom" yaml:"dotcom"`
	CLI       bool `json:"cli" yaml:"cli"`
	VSCode    bool `json:"vscode" yaml:"vscode"`
	VS        bool `json:"vs" yaml:"vs"`
	Eclipse   bool `json:"eclipse" yaml:"eclipse"`
	Xcode     bool `json:"xcode" yaml:"xcode"`
	JetBrains bool `json:"jetbrains" yaml:"jetbrains"`
}

func (c ClientAvailability) SupportedClientNames() []string {
	var names []string
	if c.Dotcom {
		names = append(names, "GitHub.com")
	}
	if c.CLI {
		names = append(names, "Copilot CLI")
	}
	if c.VSCode {
		names = append(names, "VS Code")
	}
	if c.VS {
		names = append(names, "Visual Studio")
	}
	if c.Eclipse {
		names = append(names, "Eclipse")
	}
	if c.Xcode {
		names = append(names, "Xcode")
	}
	if c.JetBrains {
		names = append(names, "JetBrains IDEs")
	}
	return names
}

type PlanAvailability struct {
	Free       bool `json:"free" yaml:"free"`
	Student    bool `json:"student" yaml:"student"`
	Pro        bool `json:"pro" yaml:"pro"`
	ProPlus    bool `json:"proPlus" yaml:"proPlus"`
	Business   bool `json:"business" yaml:"business"`
	Enterprise bool `json:"enterprise" yaml:"enterprise"`
}

func (p PlanAvailability) SupportedPlanNames() []string {
	var names []string
	if p.Free {
		names = append(names, "Free")
	}
	if p.Student {
		names = append(names, "Student")
	}
	if p.Pro {
		names = append(names, "Pro")
	}
	if p.ProPlus {
		names = append(names, "Pro+")
	}
	if p.Business {
		names = append(names, "Business")
	}
	if p.Enterprise {
		names = append(names, "Enterprise")
	}
	return names
}

type Comparison struct {
	TaskArea       string `json:"taskArea,omitempty" yaml:"taskArea,omitempty"`
	ExcelsAt       string `json:"excelsAt,omitempty" yaml:"excelsAt,omitempty"`
	FurtherReading string `json:"furtherReading,omitempty" yaml:"furtherReading,omitempty"`
}

type RetiredModel struct {
	Name                 string `json:"name" yaml:"name"`
	RetirementDate       string `json:"retirementDate" yaml:"retirementDate"`
	SuggestedAlternative string `json:"suggestedAlternative" yaml:"suggestedAlternative"`
}

type DocsModel struct {
	Name          string             `json:"name" yaml:"name"`
	ReleaseStatus string             `json:"releaseStatus" yaml:"releaseStatus"`
	Clients       ClientAvailability `json:"clients" yaml:"clients"`
	Plans         PlanAvailability   `json:"plans" yaml:"plans"`
	Comparison    *Comparison        `json:"comparison,omitempty" yaml:"comparison,omitempty"`
}

type LiveMatch struct {
	ID                string   `json:"id" yaml:"id"`
	Name              string   `json:"name" yaml:"name"`
	PolicyState       string   `json:"policyState,omitempty" yaml:"policyState,omitempty"`
	BillingMultiplier *float64 `json:"billingMultiplier,omitempty" yaml:"billingMultiplier,omitempty"`
}

type JoinedModel struct {
	Name          string             `json:"name" yaml:"name"`
	ReleaseStatus string             `json:"releaseStatus" yaml:"releaseStatus"`
	Clients       ClientAvailability `json:"clients" yaml:"clients"`
	Plans         PlanAvailability   `json:"plans" yaml:"plans"`
	Comparison    *Comparison        `json:"comparison,omitempty" yaml:"comparison,omitempty"`
	VisibleNow    bool               `json:"visibleNow" yaml:"visibleNow"`
	LiveModels    []LiveMatch        `json:"liveModels,omitempty" yaml:"liveModels,omitempty"`
}

type Snapshot struct {
	CatalogVersion        string         `json:"catalogVersion" yaml:"catalogVersion"`
	SourceNote            string         `json:"sourceNote" yaml:"sourceNote"`
	Sources               Sources        `json:"sources" yaml:"sources"`
	Models                []JoinedModel  `json:"models" yaml:"models"`
	RetiredModels         []RetiredModel `json:"retiredModels" yaml:"retiredModels"`
	LiveModelsWithoutDocs []LiveMatch    `json:"liveModelsWithoutDocs,omitempty" yaml:"liveModelsWithoutDocs,omitempty"`
}

func NormalizeModelNameKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, ".0", "")
	s = strings.ReplaceAll(s, "preview", "")
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func BuildSnapshot(live []rpc.Model) Snapshot {
	liveByKey := make(map[string][]LiveMatch)
	for _, model := range live {
		match := LiveMatch{
			ID:   model.ID,
			Name: model.Name,
		}
		if model.Policy != nil {
			match.PolicyState = model.Policy.State
		}
		if model.Billing != nil {
			multiplier := model.Billing.Multiplier
			match.BillingMultiplier = &multiplier
		}
		for _, key := range liveModelKeys(model) {
			liveByKey[key] = appendUniqueLiveMatch(liveByKey[key], match)
		}
	}

	joined := make([]JoinedModel, 0, len(docsModels))
	docKeys := make(map[string]struct{}, len(docsModels))
	for _, model := range docsModels {
		key := NormalizeModelNameKey(model.Name)
		docKeys[key] = struct{}{}
		liveMatches := cloneLiveMatches(liveByKey[key])
		joined = append(joined, JoinedModel{
			Name:          model.Name,
			ReleaseStatus: model.ReleaseStatus,
			Clients:       model.Clients,
			Plans:         model.Plans,
			Comparison:    model.Comparison,
			VisibleNow:    len(liveMatches) > 0,
			LiveModels:    liveMatches,
		})
	}

	liveWithoutDocs := make([]LiveMatch, 0)
	for _, model := range live {
		hasDocs := false
		for _, key := range liveModelKeys(model) {
			if _, ok := docKeys[key]; ok {
				hasDocs = true
				break
			}
		}
		if hasDocs {
			continue
		}

		match := LiveMatch{
			ID:   model.ID,
			Name: model.Name,
		}
		if model.Policy != nil {
			match.PolicyState = model.Policy.State
		}
		if model.Billing != nil {
			multiplier := model.Billing.Multiplier
			match.BillingMultiplier = &multiplier
		}
		liveWithoutDocs = appendUniqueLiveMatch(liveWithoutDocs, match)
	}

	sort.Slice(liveWithoutDocs, func(i, j int) bool {
		if liveWithoutDocs[i].Name == liveWithoutDocs[j].Name {
			return liveWithoutDocs[i].ID < liveWithoutDocs[j].ID
		}
		return liveWithoutDocs[i].Name < liveWithoutDocs[j].Name
	})

	return Snapshot{
		CatalogVersion:        CatalogVersion,
		SourceNote:            SourceNote,
		Sources:               CatalogSources,
		Models:                joined,
		RetiredModels:         cloneRetiredModels(retiredModels),
		LiveModelsWithoutDocs: liveWithoutDocs,
	}
}

func liveModelKeys(model rpc.Model) []string {
	keys := make([]string, 0, 2)
	nameKey := NormalizeModelNameKey(model.Name)
	if nameKey != "" {
		keys = append(keys, nameKey)
	}
	idKey := NormalizeModelNameKey(model.ID)
	if idKey != "" && idKey != nameKey {
		keys = append(keys, idKey)
	}
	return keys
}

func appendUniqueLiveMatch(matches []LiveMatch, match LiveMatch) []LiveMatch {
	for _, existing := range matches {
		if existing.ID == match.ID {
			return matches
		}
	}
	return append(matches, match)
}

func cloneLiveMatches(matches []LiveMatch) []LiveMatch {
	if len(matches) == 0 {
		return nil
	}
	cloned := make([]LiveMatch, len(matches))
	copy(cloned, matches)
	sort.Slice(cloned, func(i, j int) bool {
		if cloned[i].Name == cloned[j].Name {
			return cloned[i].ID < cloned[j].ID
		}
		return cloned[i].Name < cloned[j].Name
	})
	return cloned
}

func cloneRetiredModels(models []RetiredModel) []RetiredModel {
	cloned := make([]RetiredModel, len(models))
	copy(cloned, models)
	return cloned
}

func clients(dotcom, cli, vscode, vs, eclipse, xcode, jetbrains bool) ClientAvailability {
	return ClientAvailability{
		Dotcom:    dotcom,
		CLI:       cli,
		VSCode:    vscode,
		VS:        vs,
		Eclipse:   eclipse,
		Xcode:     xcode,
		JetBrains: jetbrains,
	}
}

func plans(free, student, pro, proPlus, business, enterprise bool) PlanAvailability {
	return PlanAvailability{
		Free:       free,
		Student:    student,
		Pro:        pro,
		ProPlus:    proPlus,
		Business:   business,
		Enterprise: enterprise,
	}
}

func comparison(taskArea, excelsAt, furtherReading string) *Comparison {
	if taskArea == "" && excelsAt == "" && furtherReading == "" {
		return nil
	}
	return &Comparison{
		TaskArea:       taskArea,
		ExcelsAt:       excelsAt,
		FurtherReading: furtherReading,
	}
}

var docsModels = []DocsModel{
	{Name: "Claude Haiku 4.5", ReleaseStatus: "GA", Clients: clients(true, true, true, true, true, true, true), Plans: plans(true, true, true, true, true, true), Comparison: comparison("Fast help with simple or repetitive tasks", "Fast, reliable answers to lightweight coding questions", "https://assets.anthropic.com/m/99128ddd009bdcb/Claude-Haiku-4-5-System-Card.pdf")},
	{Name: "Claude Opus 4.5", ReleaseStatus: "GA", Clients: clients(true, true, true, true, true, true, true), Plans: plans(false, false, true, true, true, true), Comparison: comparison("Deep reasoning and debugging", "Complex problem-solving challenges, sophisticated reasoning", "https://assets.anthropic.com/m/64823ba7485345a7/Claude-Opus-4-5-System-Card.pdf")},
	{Name: "Claude Opus 4.6", ReleaseStatus: "GA", Clients: clients(true, true, true, true, true, true, true), Plans: plans(false, false, true, true, true, true), Comparison: comparison("Deep reasoning and debugging", "Complex problem-solving challenges, sophisticated reasoning", "https://www-cdn.anthropic.com/14e4fb01875d2a69f646fa5e574dea2b1c0ff7b5.pdf")},
	{Name: "Claude Opus 4.6 (fast mode) (preview)", ReleaseStatus: "Public preview", Clients: clients(false, true, true, false, false, false, false), Plans: plans(false, false, false, true, false, true), Comparison: comparison("Deep reasoning and debugging", "Complex problem-solving challenges, sophisticated reasoning", "Not available")},
	{Name: "Claude Sonnet 4", ReleaseStatus: "GA", Clients: clients(true, true, true, true, true, true, true), Plans: plans(false, false, true, true, true, true), Comparison: comparison("Deep reasoning and debugging", "Performance and practicality, perfectly balanced for coding workflows", "https://www-cdn.anthropic.com/6be99a52cb68eb70eb9572b4cafad13df32ed995.pdf")},
	{Name: "Claude Sonnet 4.5", ReleaseStatus: "GA", Clients: clients(true, true, true, true, true, true, true), Plans: plans(false, false, true, true, true, true), Comparison: comparison("General-purpose coding and agent tasks", "Complex problem-solving challenges, sophisticated reasoning", "https://assets.anthropic.com/m/12f214efcc2f457a/original/Claude-Sonnet-4-5-System-Card.pdf")},
	{Name: "Claude Sonnet 4.6", ReleaseStatus: "GA", Clients: clients(true, true, true, true, false, false, false), Plans: plans(false, false, true, true, true, true), Comparison: comparison("General-purpose coding and agent tasks", "Complex problem-solving challenges, sophisticated reasoning", "https://www-cdn.anthropic.com/78073f739564e986ff3e28522761a7a0b4484f84.pdf")},
	{Name: "Gemini 2.5 Pro", ReleaseStatus: "GA", Clients: clients(true, false, true, true, true, true, true), Plans: plans(false, true, true, true, true, true), Comparison: comparison("Deep reasoning and debugging", "Complex code generation, debugging, and research workflows", "https://storage.googleapis.com/model-cards/documents/gemini-2.5-pro.pdf")},
	{Name: "Gemini 3 Flash", ReleaseStatus: "Public preview", Clients: clients(true, false, true, true, true, true, true), Plans: plans(false, true, true, true, true, true), Comparison: comparison("Fast help with simple or repetitive tasks", "Fast, reliable answers to lightweight coding questions", "https://storage.googleapis.com/deepmind-media/Model-Cards/Gemini-3-Flash-Model-Card.pdf")},
	{Name: "Gemini 3 Pro", ReleaseStatus: "Public preview", Clients: clients(true, true, true, true, true, true, true), Plans: plans(false, true, true, true, true, true), Comparison: comparison("Deep reasoning and debugging", "Complex code generation, debugging, and research workflows", "https://storage.googleapis.com/deepmind-media/Model-Cards/Gemini-3-Pro-Model-Card.pdf")},
	{Name: "Gemini 3.1 Pro", ReleaseStatus: "Public preview", Clients: clients(true, false, true, true, false, false, false), Plans: plans(false, true, true, true, true, true), Comparison: comparison("Deep reasoning and debugging", "Effective and efficient edit-then-test loops with high tool precision", "Not available")},
	{Name: "Goldeneye", ReleaseStatus: "Public preview", Clients: clients(false, false, true, false, false, false, false), Plans: plans(true, false, false, false, false, false), Comparison: nil},
	{Name: "GPT-4.1", ReleaseStatus: "GA", Clients: clients(true, true, true, true, true, true, true), Plans: plans(true, true, true, true, true, true), Comparison: comparison("General-purpose coding and writing", "Fast, accurate code completions and explanations", "https://openai.com/index/gpt-4-1/")},
	{Name: "GPT-5 mini", ReleaseStatus: "GA", Clients: clients(true, true, true, true, true, true, true), Plans: plans(true, true, true, true, true, true), Comparison: comparison("General-purpose coding and writing", "Fast, accurate code completions and explanations", "https://cdn.openai.com/gpt-5-system-card.pdf")},
	{Name: "GPT-5.1", ReleaseStatus: "GA", Clients: clients(true, true, true, true, true, true, true), Plans: plans(false, true, true, true, true, true), Comparison: comparison("Deep reasoning and debugging", "Multi-step problem solving and architecture-level code analysis", "https://cdn.openai.com/pdf/4173ec8d-1229-47db-96de-06d87147e07e/5_1_system_card.pdf")},
	{Name: "GPT-5.1-Codex", ReleaseStatus: "GA", Clients: clients(false, true, true, false, true, true, true), Plans: plans(false, true, true, true, true, true), Comparison: comparison("Deep reasoning and debugging", "Multi-step problem solving and architecture-level code analysis", "Not available")},
	{Name: "GPT-5.1-Codex-Max", ReleaseStatus: "GA", Clients: clients(true, true, true, true, true, true, true), Plans: plans(false, true, true, true, true, true), Comparison: comparison("Agentic software development", "Agentic tasks", "https://cdn.openai.com/pdf/2a7d98b1-57e5-4147-8d0e-683894d782ae/5p1_codex_max_card_03.pdf")},
	{Name: "GPT-5.1-Codex-Mini", ReleaseStatus: "Public preview", Clients: clients(false, true, true, false, true, true, true), Plans: plans(false, true, true, true, true, true), Comparison: comparison("Deep reasoning and debugging", "Multi-step problem solving and architecture-level code analysis", "Not available")},
	{Name: "GPT-5.2", ReleaseStatus: "GA", Clients: clients(true, true, true, true, true, true, true), Plans: plans(false, true, true, true, true, true), Comparison: comparison("Deep reasoning and debugging", "Multi-step problem solving and architecture-level code analysis", "https://cdn.openai.com/pdf/3a4153c8-c748-4b71-8e31-aecbde944f8d/oai_5_2_system-card.pdf")},
	{Name: "GPT-5.2-Codex", ReleaseStatus: "GA", Clients: clients(true, true, true, true, true, true, true), Plans: plans(false, true, true, true, true, true), Comparison: comparison("Agentic software development", "Agentic tasks", "https://cdn.openai.com/pdf/ac7c37ae-7f4c-4442-b741-2eabdeaf77e0/oai_5_2_Codex.pdf")},
	{Name: "GPT-5.3-Codex", ReleaseStatus: "GA", Clients: clients(true, true, true, true, true, true, true), Plans: plans(false, true, true, true, true, true), Comparison: comparison("Agentic software development", "Agentic tasks", "https://deploymentsafety.openai.com/gpt-5-3-codex")},
	{Name: "GPT-5.4", ReleaseStatus: "GA", Clients: clients(true, true, true, true, true, true, true), Plans: plans(false, false, true, true, true, true), Comparison: comparison("Deep reasoning and debugging", "Multi-step problem solving and architecture-level code analysis", "https://deploymentsafety.openai.com/gpt-5-4-thinking/introduction")},
	{Name: "GPT-5.4 mini", ReleaseStatus: "GA", Clients: clients(true, true, true, true, true, true, true), Plans: plans(false, false, true, true, true, true), Comparison: comparison("Agentic software development", "Codebase exploration and is especially effective when using grep-style tools", "Not available")},
	{Name: "Grok Code Fast 1", ReleaseStatus: "GA", Clients: clients(true, false, true, true, true, true, true), Plans: plans(true, true, true, true, true, true), Comparison: comparison("General-purpose coding and writing", "Fast, accurate code completions and explanations", "https://data.x.ai/2025-08-20-grok-4-model-card.pdf")},
	{Name: "Raptor mini", ReleaseStatus: "Public preview", Clients: clients(false, false, true, false, false, false, false), Plans: plans(true, true, true, true, false, false), Comparison: comparison("General-purpose coding and writing", "Fast, accurate code completions and explanations", "Coming soon")},
}

var retiredModels = []RetiredModel{
	{Name: "Claude Opus 4.1", RetirementDate: "2026-02-17", SuggestedAlternative: "Claude Opus 4.6"},
	{Name: "GPT-5", RetirementDate: "2026-02-17", SuggestedAlternative: "GPT-5.2"},
	{Name: "GPT-5-Codex", RetirementDate: "2026-02-17", SuggestedAlternative: "GPT-5.2-Codex"},
	{Name: "Claude Sonnet 3.5", RetirementDate: "2025-11-06", SuggestedAlternative: "Claude Haiku 4.5"},
	{Name: "Claude Opus 4", RetirementDate: "2025-10-23", SuggestedAlternative: "Claude Opus 4.6"},
	{Name: "Claude Sonnet 3.7", RetirementDate: "2025-10-23", SuggestedAlternative: "Claude Sonnet 4.6"},
	{Name: "Claude Sonnet 3.7 Thinking", RetirementDate: "2025-10-23", SuggestedAlternative: "Claude Sonnet 4.6"},
	{Name: "Gemini 2.0 Flash", RetirementDate: "2025-10-23", SuggestedAlternative: "Gemini 2.5 Pro"},
	{Name: "o1-mini", RetirementDate: "2025-10-23", SuggestedAlternative: "GPT-5 mini"},
	{Name: "o3", RetirementDate: "2025-10-23", SuggestedAlternative: "GPT-5.2"},
	{Name: "o3-mini", RetirementDate: "2025-10-23", SuggestedAlternative: "GPT-5 mini"},
	{Name: "o4-mini", RetirementDate: "2025-10-23", SuggestedAlternative: "GPT-5 mini"},
}
