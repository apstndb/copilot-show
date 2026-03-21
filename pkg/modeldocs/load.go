package modeldocs

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
)

//go:embed snapshot/*.yml snapshot/source.json
var embeddedSnapshotFS embed.FS

const (
	embeddedSnapshotDir  = "snapshot"
	embeddedSourceFile   = "snapshot/source.json"
	embeddedSnapshotPath = "pkg/modeldocs/snapshot"
)

type loadMode string

const (
	loadModeEmbedded         loadMode = "embedded"
	loadModeLatest           loadMode = "latest"
	loadModeEmbeddedFallback loadMode = "embedded-fallback"
)

type latestFileFetcher func(ctx context.Context, url string) ([]byte, error)

type embeddedSourceMetadata struct {
	Repo         string `json:"repo" yaml:"repo"`
	Ref          string `json:"ref" yaml:"ref"`
	Commit       string `json:"commit" yaml:"commit"`
	SnapshotPath string `json:"snapshotPath" yaml:"snapshotPath"`
}

type releaseStatusRow struct {
	Name          string `yaml:"name"`
	ReleaseStatus string `yaml:"release_status"`
}

type supportedClientsRow struct {
	Name      string `yaml:"name"`
	Dotcom    bool   `yaml:"dotcom"`
	CLI       bool   `yaml:"cli"`
	VSCode    bool   `yaml:"vscode"`
	VS        bool   `yaml:"vs"`
	Eclipse   bool   `yaml:"eclipse"`
	Xcode     bool   `yaml:"xcode"`
	JetBrains bool   `yaml:"jetbrains"`
}

type supportedPlansRow struct {
	Name       string `yaml:"name"`
	Free       bool   `yaml:"free"`
	Student    bool   `yaml:"student"`
	Pro        bool   `yaml:"pro"`
	ProPlus    bool   `yaml:"pro_plus"`
	Business   bool   `yaml:"business"`
	Enterprise bool   `yaml:"enterprise"`
}

type modelComparisonRow struct {
	Name           string `yaml:"name"`
	TaskArea       string `yaml:"task_area"`
	ExcelsAt       string `yaml:"excels_at"`
	FurtherReading string `yaml:"further_reading"`
}

type deprecationHistoryRow struct {
	Name                 string `yaml:"name"`
	RetirementDate       string `yaml:"retirement_date"`
	SuggestedAlternative string `yaml:"suggested_alternative"`
}

func baseCatalogSources() Sources {
	return Sources{
		ReleaseStatus:      "https://raw.githubusercontent.com/github/docs/main/data/tables/copilot/model-release-status.yml",
		SupportedClients:   "https://raw.githubusercontent.com/github/docs/main/data/tables/copilot/model-supported-clients.yml",
		SupportedPlans:     "https://raw.githubusercontent.com/github/docs/main/data/tables/copilot/model-supported-plans.yml",
		ModelComparison:    "https://raw.githubusercontent.com/github/docs/main/data/tables/copilot/model-comparison.yml",
		DeprecationHistory: "https://raw.githubusercontent.com/github/docs/main/data/tables/copilot/model-deprecation-history.yml",
	}
}

func loadCatalog(ctx context.Context, options SnapshotOptions, fetcher latestFileFetcher) (docsCatalog, error) {
	embeddedCatalog, err := loadCatalogFromFS(embeddedSnapshotFS)
	if err != nil {
		return docsCatalog{}, fmt.Errorf("load embedded modeldocs snapshot: %w", err)
	}
	if !options.PreferLatest {
		return embeddedCatalog, nil
	}

	latestCatalog, err := loadLatestCatalog(ctx, fetcher, embeddedCatalog.Sources)
	if err != nil {
		embeddedCatalog.LoadedFrom = string(loadModeEmbeddedFallback)
		embeddedCatalog.LoadWarnings = append(embeddedCatalog.LoadWarnings, fmt.Sprintf("Unable to load latest github/docs copilot tables: %v. Falling back to embedded snapshot %s.", err, embeddedCatalog.CatalogVersion))
		return embeddedCatalog, nil
	}
	return latestCatalog, nil
}

func loadCatalogFromFS(fsys fs.FS) (docsCatalog, error) {
	metadata, err := loadEmbeddedSourceMetadata(fsys)
	if err != nil {
		return docsCatalog{}, err
	}

	sources := baseCatalogSources()
	sources.EmbeddedRepo = metadata.Repo
	sources.EmbeddedRef = metadata.Ref
	sources.EmbeddedCommit = metadata.Commit
	sources.EmbeddedSnapshotDir = metadata.SnapshotPath

	releaseStatusData, err := fs.ReadFile(fsys, embeddedSnapshotDir+"/model-release-status.yml")
	if err != nil {
		return docsCatalog{}, err
	}
	supportedClientsData, err := fs.ReadFile(fsys, embeddedSnapshotDir+"/model-supported-clients.yml")
	if err != nil {
		return docsCatalog{}, err
	}
	supportedPlansData, err := fs.ReadFile(fsys, embeddedSnapshotDir+"/model-supported-plans.yml")
	if err != nil {
		return docsCatalog{}, err
	}
	modelComparisonData, err := fs.ReadFile(fsys, embeddedSnapshotDir+"/model-comparison.yml")
	if err != nil {
		return docsCatalog{}, err
	}
	deprecationData, err := fs.ReadFile(fsys, embeddedSnapshotDir+"/model-deprecation-history.yml")
	if err != nil {
		return docsCatalog{}, err
	}

	version := "github-docs-snapshot-" + shortSHA(metadata.Commit)
	return parseDocsCatalog(releaseStatusData, supportedClientsData, supportedPlansData, modelComparisonData, deprecationData, version, string(loadModeEmbedded), nil, sources)
}

func loadLatestCatalog(ctx context.Context, fetcher latestFileFetcher, sources Sources) (docsCatalog, error) {
	if fetcher == nil {
		fetcher = defaultFetchLatestFile
	}

	releaseStatusData, err := fetcher(ctx, sources.ReleaseStatus)
	if err != nil {
		return docsCatalog{}, err
	}
	supportedClientsData, err := fetcher(ctx, sources.SupportedClients)
	if err != nil {
		return docsCatalog{}, err
	}
	supportedPlansData, err := fetcher(ctx, sources.SupportedPlans)
	if err != nil {
		return docsCatalog{}, err
	}
	modelComparisonData, err := fetcher(ctx, sources.ModelComparison)
	if err != nil {
		return docsCatalog{}, err
	}
	deprecationData, err := fetcher(ctx, sources.DeprecationHistory)
	if err != nil {
		return docsCatalog{}, err
	}

	return parseDocsCatalog(releaseStatusData, supportedClientsData, supportedPlansData, modelComparisonData, deprecationData, "github-docs-latest", string(loadModeLatest), nil, sources)
}

func loadEmbeddedSourceMetadata(fsys fs.FS) (embeddedSourceMetadata, error) {
	data, err := fs.ReadFile(fsys, embeddedSourceFile)
	if err != nil {
		return embeddedSourceMetadata{}, fmt.Errorf("read embedded snapshot source metadata: %w", err)
	}
	var metadata embeddedSourceMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return embeddedSourceMetadata{}, fmt.Errorf("decode embedded snapshot source metadata: %w", err)
	}
	if metadata.Repo == "" || metadata.Ref == "" || metadata.Commit == "" {
		return embeddedSourceMetadata{}, fmt.Errorf("embedded snapshot source metadata is missing required fields")
	}
	return metadata, nil
}

func parseDocsCatalog(releaseStatusData, supportedClientsData, supportedPlansData, modelComparisonData, deprecationData []byte, version, loadedFrom string, warnings []string, sources Sources) (docsCatalog, error) {
	if err := validateRequiredKeys(releaseStatusData, "model-release-status.yml", []string{"name", "release_status"}); err != nil {
		return docsCatalog{}, err
	}
	if err := validateRequiredKeys(supportedClientsData, "model-supported-clients.yml", []string{"name", "dotcom", "cli", "vscode", "vs", "eclipse", "xcode", "jetbrains"}); err != nil {
		return docsCatalog{}, err
	}
	if err := validateRequiredKeys(supportedPlansData, "model-supported-plans.yml", []string{"name", "free", "student", "pro", "pro_plus", "business", "enterprise"}); err != nil {
		return docsCatalog{}, err
	}
	if err := validateRequiredKeys(modelComparisonData, "model-comparison.yml", []string{"name"}); err != nil {
		return docsCatalog{}, err
	}
	if err := validateRequiredKeys(deprecationData, "model-deprecation-history.yml", []string{"name", "retirement_date"}); err != nil {
		return docsCatalog{}, err
	}

	var releaseRows []releaseStatusRow
	if err := yaml.Unmarshal(releaseStatusData, &releaseRows); err != nil {
		return docsCatalog{}, fmt.Errorf("decode model-release-status.yml: %w", err)
	}
	if len(releaseRows) == 0 {
		return docsCatalog{}, fmt.Errorf("model-release-status.yml did not produce any rows")
	}

	var clientRows []supportedClientsRow
	if err := yaml.Unmarshal(supportedClientsData, &clientRows); err != nil {
		return docsCatalog{}, fmt.Errorf("decode model-supported-clients.yml: %w", err)
	}
	var planRows []supportedPlansRow
	if err := yaml.Unmarshal(supportedPlansData, &planRows); err != nil {
		return docsCatalog{}, fmt.Errorf("decode model-supported-plans.yml: %w", err)
	}
	var comparisonRows []modelComparisonRow
	if err := yaml.Unmarshal(modelComparisonData, &comparisonRows); err != nil {
		return docsCatalog{}, fmt.Errorf("decode model-comparison.yml: %w", err)
	}
	var deprecationRows []deprecationHistoryRow
	if err := yaml.Unmarshal(deprecationData, &deprecationRows); err != nil {
		return docsCatalog{}, fmt.Errorf("decode model-deprecation-history.yml: %w", err)
	}

	clientMap := make(map[string]supportedClientsRow, len(clientRows))
	for _, row := range clientRows {
		if strings.TrimSpace(row.Name) == "" {
			return docsCatalog{}, fmt.Errorf("model-supported-clients.yml contains an empty model name")
		}
		key := NormalizeModelNameKey(row.Name)
		if _, exists := clientMap[key]; exists {
			return docsCatalog{}, fmt.Errorf("model-supported-clients.yml contains duplicate model %q", row.Name)
		}
		clientMap[key] = row
	}

	planMap := make(map[string]supportedPlansRow, len(planRows))
	for _, row := range planRows {
		if strings.TrimSpace(row.Name) == "" {
			return docsCatalog{}, fmt.Errorf("model-supported-plans.yml contains an empty model name")
		}
		key := NormalizeModelNameKey(row.Name)
		if _, exists := planMap[key]; exists {
			return docsCatalog{}, fmt.Errorf("model-supported-plans.yml contains duplicate model %q", row.Name)
		}
		planMap[key] = row
	}

	comparisonMap := make(map[string]modelComparisonRow, len(comparisonRows))
	for _, row := range comparisonRows {
		if strings.TrimSpace(row.Name) == "" {
			return docsCatalog{}, fmt.Errorf("model-comparison.yml contains an empty model name")
		}
		key := NormalizeModelNameKey(row.Name)
		if _, exists := comparisonMap[key]; exists {
			return docsCatalog{}, fmt.Errorf("model-comparison.yml contains duplicate model %q", row.Name)
		}
		comparisonMap[key] = row
	}

	docsModels := make([]DocsModel, 0, len(releaseRows))
	for _, row := range releaseRows {
		if strings.TrimSpace(row.Name) == "" || strings.TrimSpace(row.ReleaseStatus) == "" {
			return docsCatalog{}, fmt.Errorf("model-release-status.yml is missing required fields for model %q", row.Name)
		}
		key := NormalizeModelNameKey(row.Name)
		clientRow, ok := clientMap[key]
		if !ok {
			return docsCatalog{}, fmt.Errorf("model-supported-clients.yml is missing model %q", row.Name)
		}
		planRow, ok := planMap[key]
		if !ok {
			return docsCatalog{}, fmt.Errorf("model-supported-plans.yml is missing model %q", row.Name)
		}
		comparisonRow, ok := comparisonMap[key]
		var comparison *Comparison
		if ok {
			comparison = &Comparison{
				TaskArea:       comparisonRow.TaskArea,
				ExcelsAt:       comparisonRow.ExcelsAt,
				FurtherReading: comparisonRow.FurtherReading,
			}
		}
		docsModels = append(docsModels, DocsModel{
			Name:          row.Name,
			ReleaseStatus: row.ReleaseStatus,
			Clients: ClientAvailability{
				Dotcom:    clientRow.Dotcom,
				CLI:       clientRow.CLI,
				VSCode:    clientRow.VSCode,
				VS:        clientRow.VS,
				Eclipse:   clientRow.Eclipse,
				Xcode:     clientRow.Xcode,
				JetBrains: clientRow.JetBrains,
			},
			Plans: PlanAvailability{
				Free:       planRow.Free,
				Student:    planRow.Student,
				Pro:        planRow.Pro,
				ProPlus:    planRow.ProPlus,
				Business:   planRow.Business,
				Enterprise: planRow.Enterprise,
			},
			Comparison: comparison,
		})
	}

	retiredModels := make([]RetiredModel, 0, len(deprecationRows))
	for _, row := range deprecationRows {
		if strings.TrimSpace(row.Name) == "" || strings.TrimSpace(row.RetirementDate) == "" {
			return docsCatalog{}, fmt.Errorf("model-deprecation-history.yml is missing required fields for model %q", row.Name)
		}
		retiredModels = append(retiredModels, RetiredModel{
			Name:                 row.Name,
			RetirementDate:       row.RetirementDate,
			SuggestedAlternative: row.SuggestedAlternative,
		})
	}
	if len(retiredModels) > 1 {
		sort.SliceStable(retiredModels, func(i, j int) bool {
			if retiredModels[i].RetirementDate == retiredModels[j].RetirementDate {
				return retiredModels[i].Name < retiredModels[j].Name
			}
			return retiredModels[i].RetirementDate > retiredModels[j].RetirementDate
		})
	}

	return docsCatalog{
		CatalogVersion: version,
		Sources:        sources,
		LoadedFrom:     loadedFrom,
		LoadWarnings:   cloneStrings(warnings),
		Models:         docsModels,
		RetiredModels:  retiredModels,
	}, nil
}

func defaultFetchLatestFile(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request for %s: %w", url, err)
	}

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: %s", url, resp.Status)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", url, err)
	}
	return data, nil
}

func shortSHA(commit string) string {
	commit = strings.TrimSpace(commit)
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

func validateRequiredKeys(data []byte, fileName string, requiredKeys []string) error {
	var rows []map[string]any
	if err := yaml.Unmarshal(data, &rows); err != nil {
		return fmt.Errorf("inspect %s for required keys: %w", fileName, err)
	}
	for i, row := range rows {
		for _, key := range requiredKeys {
			if _, ok := row[key]; !ok {
				return fmt.Errorf("%s row %d is missing required key %q", fileName, i+1, key)
			}
		}
	}
	return nil
}
