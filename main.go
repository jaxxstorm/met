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

type CLI struct {
	Endpoint string        `help:"The Prometheus-compatible metrics endpoint to poll" short:"x" env:"MET_ENDPOINT"`
	Interval time.Duration `help:"How often to poll the endpoint" default:"2s" short:"s" env:"MET_INTERVAL"`
	Version  bool          `help:"Print version information" short:"v"`
	Include  []string      `help:"Include only metrics that match the given glob" short:"i"`
	Exclude  []string      `help:"Exclude metrics that match the given glob" short:"e"`
	Labels   []string      `help:"Show only metrics with the given labels" short:"l"`
}

func (c *CLI) AfterApply() error {
	if c.Version {
		return nil
	}
	if c.Endpoint == "" {
		return errors.New("must specify an endpoint to scrape - eg: --endpoint http://localhost:9090/metrics")
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

type model struct {
	endpoint     string
	interval     time.Duration
	initialized  bool
	metricsList  []metricData
	metricsIndex map[string]int
	err          error
	quit         bool
	selected     int
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
		updated := updateMetrics(m, msg.families)
		if !updated.initialized {
			sort.Slice(updated.metricsList, func(i, j int) bool {
				if updated.metricsList[i].name == updated.metricsList[j].name {
					return updated.metricsList[i].labels < updated.metricsList[j].labels
				}
				return updated.metricsList[i].name < updated.metricsList[j].name
			})
			updated.initialized = true
		}
		return updated, tickCmd(m.interval)

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
		return fmt.Sprintf("Error fetching metrics: %v\n\nPress q or Ctrl+C to quit.\n", m.err)
	}
	listView := m.renderList()
	graphView := m.renderGraph()

	var b strings.Builder
	b.WriteString(listView)
	b.WriteString("\n")
	b.WriteString(graphView)
	b.WriteString("\n\nPress q or Ctrl+C to quit. Use ↑/↓ or j/k to select.\n")
	return b.String()
}

// renderList: uses tablewriter for columns: [Key | Value | Inc Diff | Total Diff].
func (m model) renderList() string {
	if len(m.metricsList) == 0 {
		return fmt.Sprintf("Prometheus metrics from %s (every %s)\nNo metrics found or still fetching...\n",
			m.endpoint, m.interval)
	}

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

		// Value (lastScrapedVal for counters, gaugeVal for gauges)
		var valStr string
		if md.isCounter {
			valStr = fmt.Sprintf("%.2f", md.lastScrapedVal)
		} else {
			valStr = fmt.Sprintf("%.2f", md.gaugeVal)
		}

		// Inc Diff
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

		// Total Diff (accumVal) if counter, else --
		var totalDiffStr string
		if md.isCounter {
			totalDiffStr = fmt.Sprintf("%.2f", md.accumVal)
		} else {
			totalDiffStr = "--"
		}

		keyStr := fmt.Sprintf("%s %s", cursor, md.key)
		table.Append([]string{keyStr, valStr, incDiffStr, totalDiffStr})
	}
	table.Render()

	sb.WriteString(tableString.String())
	return sb.String()
}

func (m model) renderGraph() string {
	if len(m.metricsList) == 0 || m.selected < 0 || m.selected >= len(m.metricsList) {
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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

// updateMetrics: counters track both an incremental diff (lastDelta) and a total diff (accumVal).
func updateMetrics(m model, families map[string]*dto.MetricFamily) model {
	if m.metricsIndex == nil {
		m.metricsIndex = make(map[string]int)
	}
	seen := make(map[string]struct{})

	for name, mf := range families {
		for _, pm := range mf.Metric {
			lblStr, lblKey := renderLabels(pm.Label)
			key := name + "{" + lblKey + "}"

			idx, found := m.metricsIndex[key]
			if !found {
				md := metricData{
					key:       key,
					name:      name,
					labels:    lblStr,
					isCounter: mf.GetType() == dto.MetricType_COUNTER,
				}
				m.metricsList = append(m.metricsList, md)
				idx = len(m.metricsList) - 1
				m.metricsIndex[key] = idx
			}
			md := m.metricsList[idx]

			var raw float64
			switch mf.GetType() {
			case dto.MetricType_COUNTER:
				raw = pm.GetCounter().GetValue()
			case dto.MetricType_GAUGE:
				raw = pm.GetGauge().GetValue()
			case dto.MetricType_UNTYPED:
				raw = pm.GetUntyped().GetValue()
			case dto.MetricType_SUMMARY:
				raw = pm.GetSummary().GetSampleSum()
			case dto.MetricType_HISTOGRAM:
				raw = pm.GetHistogram().GetSampleSum()
			}

			if md.isCounter {
				diff := raw - md.prevVal
				if diff < 0 {
					// Reset or decreased
					md.accumVal += raw
					md.lastDelta = raw
				} else if diff > 0 {
					md.accumVal += diff
					md.lastDelta = diff
				}
				// If diff == 0, keep lastDelta from previous so we see
				// highlight until next increment or reset
				md.prevVal = raw
				md.lastScrapedVal = raw
			} else {
				md.gaugeVal = raw
				md.lastDelta = 0
				md.lastScrapedVal = raw
			}

			var current float64
			if md.isCounter {
				current = md.accumVal
			} else {
				current = md.gaugeVal
			}
			md.history = append(md.history, current)
			if len(md.history) > maxHistory {
				md.history = md.history[len(md.history)-maxHistory:]
			}
			m.metricsList[idx] = md
			seen[key] = struct{}{}
		}
	}

	// Remove stale metrics
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
	switch kctx.Command() {
	default:
		runProgram(cli)
	}
}

func runProgram(cli CLI) {
	initialModel := model{
		endpoint: cli.Endpoint,
		interval: cli.Interval,
	}
	p := tea.NewProgram(initialModel)
	_, err := p.Run()
	if err != nil {
		log.Fatal(err)
	}
}
