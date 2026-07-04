package render

import (
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/olekukonko/tablewriter/renderer"
	"github.com/olekukonko/tablewriter/tw"
	"golang.org/x/term"
)

const HistoryEventLabelWidth = 20

var (
	tableWidthOverride int
	tableFoldEnabled   = true
)

func CreateTable(header []string, rightAlignedCols []int, hierarchicalMerge bool, rowLine bool, mode string) *tablewriter.Table {
	return newTable(os.Stdout, header, rightAlignedCols, hierarchicalMerge, rowLine, mode, defaultTableMaxWidth(mode))
}

func SetTableWidthOverride(width int) {
	tableWidthOverride = width
}

// SetTableFoldEnabled switches between folded tables (word wrap, no terminal squeeze
// unless width is explicitly configured) and the legacy break-to-fit layout.
func SetTableFoldEnabled(enabled bool) {
	tableFoldEnabled = enabled
}

func TableFoldEnabled() bool {
	return tableFoldEnabled
}

func TableMaxWidth(mode string) int {
	return defaultTableMaxWidth(mode)
}

func newTable(w io.Writer, header []string, rightAlignedCols []int, hierarchicalMerge bool, rowLine bool, mode string, maxWidth int) *tablewriter.Table {
	var opts []tablewriter.Option

	if mode == "markdown" {
		opts = append(opts, tablewriter.WithRenderer(renderer.NewMarkdown()))
	} else if mode == "ascii" {
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

	if maxWidth > 0 {
		opts = append(opts, tablewriter.WithWidths(tw.CellWidth{Global: maxWidth}))
	}
	if shouldUseCompactPadding(mode, len(header), maxWidth) {
		opts = append(opts, tablewriter.WithPadding(tw.PaddingNone))
	}

	table := tablewriter.NewTable(w, opts...)

	wrapMode := tw.WrapBreak
	if tableFoldEnabled {
		wrapMode = tw.WrapNormal
	}

	table.Configure(func(cfg *tablewriter.Config) {
		cfg.Row.Formatting.AutoWrap = wrapMode
		cfg.Header.Formatting.AutoWrap = wrapMode
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

func defaultTableMaxWidth(mode string) int {
	if tableWidthOverride < 0 {
		return 0
	}
	if tableWidthOverride > 0 {
		return calculateTableMaxWidth(mode, tableWidthOverride)
	}
	if width, ok := tableWidthFromEnv(); ok {
		if width <= 0 {
			return 0
		}
		return calculateTableMaxWidth(mode, width)
	}
	if tableFoldEnabled {
		return 0
	}
	if width, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
		return calculateTableMaxWidth(mode, width)
	}
	return 0
}

func calculateTableMaxWidth(mode string, cols int) int {
	if mode == "markdown" || cols <= 0 {
		return 0
	}
	if cols <= 2 {
		return cols
	}
	// tablewriter's global width excludes the outer left/right borders from the
	// rendered width, so reserve those two columns here to make the visible
	// table width match the requested terminal width.
	return cols - 2
}

func shouldUseCompactPadding(mode string, columnCount int, maxWidth int) bool {
	if mode == "markdown" || columnCount <= 0 || maxWidth <= 0 {
		return false
	}
	return maxWidth <= columnCount*5
}

func tableWidthFromEnv() (int, bool) {
	for _, name := range []string{"COLUMNS", "COLUMN"} {
		raw := strings.TrimSpace(os.Getenv(name))
		if raw == "" {
			continue
		}
		width, err := strconv.Atoi(raw)
		if err != nil {
			continue
		}
		return width, true
	}
	return 0, false
}

func FormatFloatCompact(v float64) string {
	if math.Abs(v-math.Round(v)) < 1e-9 {
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func FormatUSD(v float64) string {
	return fmt.Sprintf("$%.2f", v)
}
