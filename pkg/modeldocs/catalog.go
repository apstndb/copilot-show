package modeldocs

import (
	"context"
	"sort"
	"strings"
	"unicode"

	"github.com/github/copilot-sdk/go/rpc"
)

const SourceNote = "This catalog uses an embedded snapshot refreshed from github/docs at a recorded commit. Use scripts/update-modeldocs-snapshot.sh to refresh it, or --latest to attempt a fresh github/docs fetch. Treat it as documentation-oriented reference data, not runtime source-of-truth."

type SnapshotOptions struct {
	PreferLatest bool
}

type Sources struct {
	ReleaseStatus       string `json:"releaseStatus" yaml:"releaseStatus"`
	SupportedClients    string `json:"supportedClients" yaml:"supportedClients"`
	SupportedPlans      string `json:"supportedPlans" yaml:"supportedPlans"`
	ModelComparison     string `json:"modelComparison" yaml:"modelComparison"`
	DeprecationHistory  string `json:"deprecationHistory" yaml:"deprecationHistory"`
	EmbeddedRepo        string `json:"embeddedRepo,omitempty" yaml:"embeddedRepo,omitempty"`
	EmbeddedRef         string `json:"embeddedRef,omitempty" yaml:"embeddedRef,omitempty"`
	EmbeddedCommit      string `json:"embeddedCommit,omitempty" yaml:"embeddedCommit,omitempty"`
	EmbeddedSnapshotDir string `json:"embeddedSnapshotDir,omitempty" yaml:"embeddedSnapshotDir,omitempty"`
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
	LoadedFrom            string         `json:"loadedFrom" yaml:"loadedFrom"`
	LoadWarnings          []string       `json:"loadWarnings,omitempty" yaml:"loadWarnings,omitempty"`
	Models                []JoinedModel  `json:"models" yaml:"models"`
	RetiredModels         []RetiredModel `json:"retiredModels" yaml:"retiredModels"`
	LiveModelsWithoutDocs []LiveMatch    `json:"liveModelsWithoutDocs,omitempty" yaml:"liveModelsWithoutDocs,omitempty"`
}

type docsCatalog struct {
	CatalogVersion string
	Sources        Sources
	LoadedFrom     string
	LoadWarnings   []string
	Models         []DocsModel
	RetiredModels  []RetiredModel
}

func BuildSnapshot(live []rpc.Model) Snapshot {
	snapshot, err := buildSnapshotWithFetcher(context.Background(), live, SnapshotOptions{}, defaultFetchLatestFile)
	if err != nil {
		return Snapshot{
			CatalogVersion: "unavailable",
			SourceNote:     SourceNote,
			Sources:        baseCatalogSources(),
			LoadedFrom:     string(loadModeEmbedded),
			LoadWarnings:   []string{err.Error()},
		}
	}
	return snapshot
}

func BuildSnapshotWithOptions(ctx context.Context, live []rpc.Model, options SnapshotOptions) (Snapshot, error) {
	return buildSnapshotWithFetcher(ctx, live, options, defaultFetchLatestFile)
}

func buildSnapshotWithFetcher(ctx context.Context, live []rpc.Model, options SnapshotOptions, fetcher latestFileFetcher) (Snapshot, error) {
	catalog, err := loadCatalog(ctx, options, fetcher)
	if err != nil {
		return Snapshot{}, err
	}
	return buildSnapshotFromCatalog(live, catalog), nil
}

func buildSnapshotFromCatalog(live []rpc.Model, catalog docsCatalog) Snapshot {
	liveByKey := make(map[string][]LiveMatch)
	for _, model := range live {
		match := LiveMatch{ID: model.ID, Name: model.Name}
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

	joined := make([]JoinedModel, 0, len(catalog.Models))
	docKeys := make(map[string]struct{}, len(catalog.Models))
	for _, model := range catalog.Models {
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

		match := LiveMatch{ID: model.ID, Name: model.Name}
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
		CatalogVersion:        catalog.CatalogVersion,
		SourceNote:            SourceNote,
		Sources:               catalog.Sources,
		LoadedFrom:            catalog.LoadedFrom,
		LoadWarnings:          cloneStrings(catalog.LoadWarnings),
		Models:                joined,
		RetiredModels:         cloneRetiredModels(catalog.RetiredModels),
		LiveModelsWithoutDocs: liveWithoutDocs,
	}
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

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}
