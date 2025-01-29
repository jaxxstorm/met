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
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

var Version = "dev"

type CLI struct {
	Endpoint string        `help:"The Prometheus-compatible metrics endpoint to poll" short:"x" env:"MET_ENDPOINT"`
	Interval time.Duration `help:"How often to poll the endpoint" default:"2s" short:"s" env:"MET_INTERVAL"`
	Version  bool          `help:"Print version information" short:"v"`
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
	key       string
	name      string
	labels    string
	isCounter bool
	prevVal   float64
	accumVal  float64
	gaugeVal  float64
}

type model struct {
	endpoint string
	interval time.Duration

	initialized  bool
	metricsList  []metricData
	metricsIndex map[string]int
	err          error
	quit         bool
}

type tickMsg time.Time
type metricsMsg struct {
	families map[string]*dto.MetricFamily
	err      error
}

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
		m = updateMetrics(m, msg.families)
		if !m.initialized {
			sort.Slice(m.metricsList, func(i, j int) bool {
				if m.metricsList[i].name == m.metricsList[j].name {
					return m.metricsList[i].labels < m.metricsList[j].labels
				}
				return m.metricsList[i].name < m.metricsList[j].name
			})
			m.initialized = true
		}
		return m, tickCmd(m.interval)
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			m.quit = true
			return m, tea.Quit
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
	s := fmt.Sprintf("Prometheus metrics from %s (refresh every %s)\n", m.endpoint, m.interval)
	s += "Counters are accumulated; other metrics show the last raw value.\n\n"
	if len(m.metricsList) == 0 {
		s += "No metrics found or still fetching...\n"
	} else {
		for _, md := range m.metricsList {
			if md.isCounter {
				s += fmt.Sprintf("%s{%s} = %.2f\n", md.name, md.labels, md.accumVal)
			} else {
				s += fmt.Sprintf("%s{%s} = %.2f\n", md.name, md.labels, md.gaugeVal)
			}
		}
	}
	s += "\nPress q or Ctrl+C to quit.\n"
	return s
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
			var val float64
			switch mf.GetType() {
			case dto.MetricType_COUNTER:
				val = pm.GetCounter().GetValue()
			case dto.MetricType_GAUGE:
				val = pm.GetGauge().GetValue()
			case dto.MetricType_UNTYPED:
				val = pm.GetUntyped().GetValue()
			case dto.MetricType_SUMMARY:
				val = pm.GetSummary().GetSampleSum()
			case dto.MetricType_HISTOGRAM:
				val = pm.GetHistogram().GetSampleSum()
			}
			if md.isCounter {
				diff := val - md.prevVal
				if diff < 0 {
					md.accumVal += val
				} else {
					md.accumVal += diff
				}
				md.prevVal = val
			} else {
				md.gaugeVal = val
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
		kong.Description("A top-like viewer for Prometheus metrics"),
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
