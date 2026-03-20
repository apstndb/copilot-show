package render

import (
	"fmt"
	"math"
	"os"
	"strconv"

	"github.com/olekukonko/tablewriter"
	"github.com/olekukonko/tablewriter/renderer"
	"github.com/olekukonko/tablewriter/tw"
)

const HistoryEventLabelWidth = 20

func CreateTable(header []string, rightAlignedCols []int, hierarchicalMerge bool, rowLine bool, mode string) *tablewriter.Table {
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

func FormatFloatCompact(v float64) string {
	if math.Abs(v-math.Round(v)) < 1e-9 {
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func FormatUSD(v float64) string {
	return fmt.Sprintf("$%.2f", v)
}
