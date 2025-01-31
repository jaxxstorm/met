# Met

Met is a small CLI tool that will periodically scrape a metrics compatible endpoint and return the values interactively via [Bubbletea](https://github.com/charmbracelet/bubbletea)

Simply point it at an endpoint, and you'll get a nice periodically refreshed output.

Counter metrics will accumulate over time, whereas Gauge metrics will show the last returned value.

![Met](assets/met-features.gif)

## Including and Excluding Metrics

`met` has flags for controlling the metrics you'd like to display.

`--include` does a substring match on metric names and _includes_ them.
`--exclude` does a substring match on metric names and _excludes_ them.
`--labels` will examine metric labels and only show the ones with a string match.

### Examples

Given the following metrics

```
# TYPE tailscaled_advertised_routes gauge
# HELP tailscaled_advertised_routes Number of advertised network routes (e.g. by a subnet router)
tailscaled_advertised_routes 0
# TYPE tailscaled_approved_routes gauge
# HELP tailscaled_approved_routes Number of approved network routes (e.g. by a subnet router)
tailscaled_approved_routes 0
# TYPE tailscaled_inbound_bytes_total counter
# HELP tailscaled_inbound_bytes_total Counts the number of bytes received from other peers
tailscaled_inbound_bytes_total{path="derp"} 13972
tailscaled_inbound_bytes_total{path="direct_ipv4"} 13997076
tailscaled_inbound_bytes_total{path="direct_ipv6"} 74484000
# TYPE tailscaled_inbound_dropped_packets_total counter
# HELP tailscaled_inbound_dropped_packets_total Counts the number of dropped packets received by the node from other peers
# TYPE tailscaled_inbound_packets_total counter
# HELP tailscaled_inbound_packets_total Counts the number of packets received from other peers
tailscaled_inbound_packets_total{path="derp"} 101
tailscaled_inbound_packets_total{path="direct_ipv4"} 72229
tailscaled_inbound_packets_total{path="direct_ipv6"} 64962
# TYPE tailscaled_outbound_bytes_total counter
# HELP tailscaled_outbound_bytes_total Counts the number of bytes sent to other peers
tailscaled_outbound_bytes_total{path="derp"} 34988
tailscaled_outbound_bytes_total{path="direct_ipv4"} 9677128
tailscaled_outbound_bytes_total{path="direct_ipv6"} 10987440
# TYPE tailscaled_outbound_dropped_packets_total counter
# HELP tailscaled_outbound_dropped_packets_total Counts the number of packets dropped while being sent to other peers
tailscaled_outbound_dropped_packets_total{reason="error"} 0
# TYPE tailscaled_outbound_packets_total counter
# HELP tailscaled_outbound_packets_total Counts the number of packets sent to other peers
tailscaled_outbound_packets_total{path="derp"} 204
tailscaled_outbound_packets_total{path="direct_ipv4"} 69930
tailscaled_outbound_packets_total{path="direct_ipv6"} 22211
```

#### Include only specific metrics

```
met --endpoint http://100.100.100.100/metrics --include advertised
Prometheus metrics from http://100.100.100.100/metrics (every 2s)

+----------------------------------+-------+----------+------------+
| KEY                              | VALUE | INC DIFF | TOTAL DIFF |
+----------------------------------+-------+----------+------------+
| > tailscaled_advertised_routes{} | 0.00  | --       | --         |
+----------------------------------+-------+----------+------------+

Page 1-1 of 1 total metrics


Use ↑/↓ to move selection, PgUp/PgDn to scroll.
Press q or Ctrl+C to quit.
```

#### Exclude metrics

```
met --endpoint http://100.100.100.100/metrics --exclude inbound,outbound
Prometheus metrics from http://100.100.100.100/metrics (every 2s)

+----------------------------------+-------+----------+------------+
| KEY                              | VALUE | INC DIFF | TOTAL DIFF |
+----------------------------------+-------+----------+------------+
| > tailscaled_advertised_routes{} | 0.00  | --       | --         |
|   tailscaled_approved_routes{}   | 0.00  | --       | --         |
+----------------------------------+-------+----------+------------+

Page 1-2 of 2 total metrics


Use ↑/↓ to move selection, PgUp/PgDn to scroll.
Press q or Ctrl+C to quit.
```

#### Labels

```
met --endpoint http://100.100.100.100/metrics --labels path=derp
Prometheus metrics from http://100.100.100.100/metrics (every 2s)

+--------------------------------------------------+----------+-----------+------------+
| KEY                                              | VALUE    | INC DIFF  | TOTAL DIFF |
+--------------------------------------------------+----------+-----------+------------+
| > tailscaled_inbound_bytes_total{path="derp"}    | 13972.00 | +13972.00 | 34988.00   |
|   tailscaled_inbound_packets_total{path="derp"}  | 101.00   | +101.00   | 204.00     |
|   tailscaled_outbound_bytes_total{path="derp"}   | 34988.00 | +21016.00 | 34988.00   |
|   tailscaled_outbound_packets_total{path="derp"} | 204.00   | +103.00   | 204.00     |
+--------------------------------------------------+----------+-----------+------------+
```
