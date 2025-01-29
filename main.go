package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/alecthomas/kong"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/guptarohit/asciigraph"
	"github.com/olekukonko/tablewriter"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

var Version = "dev"

// CLI flags
type CLI struct {
	Endpoint  string        `help:"Metrics endpoint to poll" short:"x" env:"MET_ENDPOINT"`
	Interval  time.Duration `help:"Poll interval" default:"2s" short:"s" env:"MET_INTERVAL"`
	Version   bool          `help:"Print version information" short:"v"`
	Include   []string      `help:"Include metrics whose name contains these substrings" short:"i"`
	Exclude   []string      `help:"Exclude metrics whose name contains these substrings" short:"e"`
	Labels    []string      `help:"Show only metrics with label=value (ANDed)" short:"l"`
	ShowGraph bool          `help:"Display an ASCII graph for the selected metric" default:"false"`
}

func (c *CLI) AfterApply() error {
	if c.Version {
		return nil
	}
	if c.Endpoint == "" {
		return errors.New("must specify an endpoint to scrape, e.g. --endpoint http://localhost:9090/metrics")
	}
	return nil
}

type metricData struct {
	key            string
	name           string
	labels         string
	isCounter      bool
	prevVal        float64
	accumVal       float64
	gaugeVal       float64
	history        []float64
	lastDelta      float64
	lastScrapedVal float64
}

type labelFilter struct {
	name  string
	value string
}

type model struct {
	endpoint     string
	interval     time.Duration
	initialized  bool
	metricsList  []metricData
	metricsIndex map[string]int
	err          error
	quit         bool
	selected     int

	includes     []string
	excludes     []string
	labelFilters []labelFilter
	showGraph    bool
}

type tickMsg time.Time
type metricsMsg struct {
	families map[string]*dto.MetricFamily
	err      error
}

const maxHistory = 30

func (m model) Init() tea.Cmd {
	return tea.Batch(
		fetchMetricsCmd(m.endpoint),
		tickCmd(m.interval),
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		return m, fetchMetricsCmd(m.endpoint)

	case metricsMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, tickCmd(m.interval)
		}
		newM := updateMetrics(m, msg.families)
		if !newM.initialized {
			sort.Slice(newM.metricsList, func(i, j int) bool {
				if newM.metricsList[i].name == newM.metricsList[j].name {
					return newM.metricsList[i].labels < newM.metricsList[j].labels
				}
				return newM.metricsList[i].name < newM.metricsList[j].name
			})
			newM.initialized = true
		}
		return newM, tickCmd(newM.interval)

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			m.quit = true
			return m, tea.Quit
		case "up", "k":
			if m.selected > 0 {
				m.selected--
			}
		case "down", "j":
			if m.selected < len(m.metricsList)-1 {
				m.selected++
			}
		}
	}
	return m, nil
}

func (m model) View() string {
	if m.quit {
		return ""
	}
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n\nPress q or Ctrl+C to quit.\n", m.err)
	}
	if len(m.metricsList) == 0 {
		return fmt.Sprintf("Prometheus metrics from %s (every %s)\nNo metrics matched filters or still fetching...\n\nPress q or Ctrl+C to quit.\n",
			m.endpoint, m.interval)
	}

	listView := m.renderList()
	var graphView string
	if m.showGraph {
		graphView = m.renderGraph()
	}

	var sb strings.Builder
	sb.WriteString(listView)
	if graphView != "" {
		sb.WriteString("\n")
		sb.WriteString(graphView)
	}
	sb.WriteString("\n\nPress q or Ctrl+C to quit. Use ↑/↓ or j/k to select.\n")
	return sb.String()
}

func (m model) renderList() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Prometheus metrics from %s (every %s)\n\n", m.endpoint, m.interval))

	tableString := &strings.Builder{}
	table := tablewriter.NewWriter(tableString)
	table.SetHeader([]string{"Key", "Value", "Inc Diff", "Total Diff"})
	table.SetAutoWrapText(false)
	table.SetBorder(true)
	table.SetRowSeparator("-")
	table.SetColumnSeparator("|")
	table.SetCenterSeparator("+")
	table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
	table.SetAlignment(tablewriter.ALIGN_LEFT)

	for i, md := range m.metricsList {
		cursor := " "
		if i == m.selected {
			cursor = ">"
		}

		valStr := fmt.Sprintf("%.2f", md.lastScrapedVal)
		if !md.isCounter {
			valStr = fmt.Sprintf("%.2f", md.gaugeVal)
		}

		var incDiffStr string
		if md.isCounter {
			if md.lastDelta > 0 {
				incDiffStr = fmt.Sprintf("\x1b[32m+%.2f\x1b[0m", md.lastDelta)
			} else if md.lastDelta < 0 {
				incDiffStr = fmt.Sprintf("%.2f", md.lastDelta)
			} else {
				incDiffStr = "0.00"
			}
		} else {
			incDiffStr = "--"
		}

		totalDiffStr := "--"
		if md.isCounter {
			totalDiffStr = fmt.Sprintf("%.2f", md.accumVal)
		}

		keyStr := fmt.Sprintf("%s %s", cursor, md.key)
		table.Append([]string{keyStr, valStr, incDiffStr, totalDiffStr})
	}
	table.Render()
	sb.WriteString(tableString.String())
	return sb.String()
}

func (m model) renderGraph() string {
	if m.selected < 0 || m.selected >= len(m.metricsList) {
		return ""
	}
	md := m.metricsList[m.selected]
	if len(md.history) == 0 {
		return "(no data)"
	}
	title := fmt.Sprintf("%s{%s}", md.name, md.labels)
	graph := asciigraph.Plot(
		md.history,
		asciigraph.Height(12),
		asciigraph.Caption(title),
		asciigraph.Width(70),
	)
	return graph
}

// ---------- Fetch & Tick Commands ----------

func fetchMetricsCmd(endpoint string) tea.Cmd {
	return func() tea.Msg {
		fams, err := scrapeMetrics(endpoint)
		return metricsMsg{families: fams, err: err}
	}
}

func tickCmd(interval time.Duration) tea.Cmd {
	return tea.Tick(interval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func scrapeMetrics(url string) (map[string]*dto.MetricFamily, error) {
	req, err := http.NewRequestWithContext(context.Background(), "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("got status %d from server", resp.StatusCode)
	}
	var parser expfmt.TextParser
	return parser.TextToMetricFamilies(resp.Body)
}

// ---------- Updating Logic ----------

func updateMetrics(m model, families map[string]*dto.MetricFamily) model {
	if m.metricsIndex == nil {
		m.metricsIndex = make(map[string]int)
	}

	seen := make(map[string]struct{})
	for name, mf := range families {
		for _, pm := range mf.Metric {
			lblStr, lblKey := renderLabels(pm.Label)
			key := name + "{" + lblKey + "}"

			if !m.passNameFilters(name) {
				continue
			}
			if !m.passLabelFilters(pm.Label) {
				continue
			}

			raw := getRawValue(mf, pm)
			idx, found := m.metricsIndex[key]
			if !found {
				// It's brand new. Initialize so the first incremental diff is 0.
				md := metricData{
					key:       key,
					name:      name,
					labels:    lblStr,
					isCounter: mf.GetType() == dto.MetricType_COUNTER,
				}
				if md.isCounter {
					md.prevVal = raw               // make first diff = 0
					md.lastScrapedVal = raw        // for the table's "Value"
					md.lastDelta = 0               // no increment yet
					// accumVal remains 0 until next scrape
				} else {
					md.gaugeVal = raw
				}

				m.metricsList = append(m.metricsList, md)
				idx = len(m.metricsList) - 1
				m.metricsIndex[key] = idx
			}

			md := m.metricsList[idx]
			if md.isCounter {
				diff := raw - md.prevVal
				if diff < 0 {
					// If the counter reset or decreased
					md.accumVal += raw
					md.lastDelta = raw
				} else if diff > 0 {
					md.accumVal += diff
					md.lastDelta = diff
				} else {
					// diff == 0 => no increment
				}
				md.prevVal = raw
				md.lastScrapedVal = raw
			} else {
				md.gaugeVal = raw
				md.lastDelta = 0
				md.lastScrapedVal = raw
			}

			curVal := md.gaugeVal
			if md.isCounter {
				curVal = md.accumVal
			}
			md.history = append(md.history, curVal)
			if len(md.history) > maxHistory {
				md.history = md.history[len(md.history)-maxHistory:]
			}

			m.metricsList[idx] = md
			seen[key] = struct{}{}
		}
	}

	newList := make([]metricData, 0, len(seen))
	newIndex := make(map[string]int, len(seen))
	for _, md := range m.metricsList {
		if _, ok := seen[md.key]; ok {
			newIndex[md.key] = len(newList)
			newList = append(newList, md)
		}
	}
	m.metricsList = newList
	m.metricsIndex = newIndex
	return m
}

// getRawValue extracts the numeric value from a Prometheus metric family for a single Metric.
func getRawValue(mf *dto.MetricFamily, pm *dto.Metric) float64 {
	switch mf.GetType() {
	case dto.MetricType_COUNTER:
		return pm.GetCounter().GetValue()
	case dto.MetricType_GAUGE:
		return pm.GetGauge().GetValue()
	case dto.MetricType_UNTYPED:
		return pm.GetUntyped().GetValue()
	case dto.MetricType_SUMMARY:
		return pm.GetSummary().GetSampleSum()
	case dto.MetricType_HISTOGRAM:
		return pm.GetHistogram().GetSampleSum()
	default:
		return 0
	}
}

// ---------- Filtering ----------

func (m model) passNameFilters(metricName string) bool {
	if len(m.includes) > 0 {
		matchedAny := false
		for _, inc := range m.includes {
			if strings.Contains(metricName, inc) {
				matchedAny = true
				break
			}
		}
		if !matchedAny {
			return false
		}
	}
	for _, exc := range m.excludes {
		if strings.Contains(metricName, exc) {
			return false
		}
	}
	return true
}

func (m model) passLabelFilters(lbls []*dto.LabelPair) bool {
	if len(m.labelFilters) == 0 {
		return true
	}
	labelMap := make(map[string]string, len(lbls))
	for _, lp := range lbls {
		labelMap[lp.GetName()] = lp.GetValue()
	}
	for _, lf := range m.labelFilters {
		val, ok := labelMap[lf.name]
		if !ok || val != lf.value {
			return false
		}
	}
	return true
}

func renderLabels(lbls []*dto.LabelPair) (string, string) {
	if len(lbls) == 0 {
		return "", ""
	}
	sort.Slice(lbls, func(i, j int) bool {
		return lbls[i].GetName() < lbls[j].GetName()
	})
	var displayParts, keyParts []string
	for _, lp := range lbls {
		displayParts = append(displayParts, fmt.Sprintf(`%s="%s"`, lp.GetName(), lp.GetValue()))
		keyParts = append(keyParts, fmt.Sprintf(`%s="%s"`, lp.GetName(), lp.GetValue()))
	}
	return strings.Join(displayParts, " "), strings.Join(keyParts, ",")
}

func main() {
	var cli CLI
	kctx := kong.Parse(&cli,
		kong.Name("met"),
		kong.Description("An interactive terminal-based viewer for Prometheus metrics"),
		kong.Vars{"version": Version},
	)

	if cli.Version {
		fmt.Printf("met %s\n", Version)
		return
	}

	var labelFilters []labelFilter
	for _, lf := range cli.Labels {
		parts := strings.SplitN(lf, "=", 2)
		if len(parts) != 2 {
			log.Fatalf("Bad --labels arg %q, want name=value", lf)
		}
		labelFilters = append(labelFilters, labelFilter{parts[0], parts[1]})
	}

	initialModel := model{
		endpoint:     cli.Endpoint,
		interval:     cli.Interval,
		includes:     cli.Include,
		excludes:     cli.Exclude,
		labelFilters: labelFilters,
		showGraph:    cli.ShowGraph,
	}

	switch kctx.Command() {
	default:
		p := tea.NewProgram(initialModel)
		if _, err := p.Run(); err != nil {
			log.Fatal(err)
		}
	}
}
