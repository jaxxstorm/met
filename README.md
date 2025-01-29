# Met

Met is a small CLI tool that will periodically scrape a metrics compatible endpoint and return the values interactively via [Bubbletea](https://github.com/charmbracelet/bubbletea)

Simply point it at an endpoint, and you'll get a nice periodically refreshed output.

Counter metrics will accumulate over time, whereas Gauge metrics will show the last returned value.

```
met --endpoint http://100.100.100.100/metrics
Prometheus metrics from http://100.100.100.100/metrics (refresh every 2s)
Counters are accumulated, other metrics show last scraped value.

tailscaled_advertised_routes{} = 0.00
tailscaled_approved_routes{} = 0.00
tailscaled_inbound_bytes_total{path="derp"} = 1223988.00
tailscaled_inbound_bytes_total{path="direct_ipv4"} = 10566884.00
tailscaled_inbound_bytes_total{path="direct_ipv6"} = 10565710.00
tailscaled_inbound_packets_total{path="derp"} = 1308.00
tailscaled_inbound_packets_total{path="direct_ipv4"} = 10848.00
tailscaled_inbound_packets_total{path="direct_ipv6"} = 26236.00
tailscaled_outbound_bytes_total{path="derp"} = 674904.00
tailscaled_outbound_bytes_total{path="direct_ipv4"} = 4119164.00
tailscaled_outbound_bytes_total{path="direct_ipv6"} = 4104124.00
tailscaled_outbound_dropped_packets_total{reason="error"} = 157.00
tailscaled_outbound_packets_total{path="derp"} = 1932.00
tailscaled_outbound_packets_total{path="direct_ipv4"} = 8203.00
tailscaled_outbound_packets_total{path="direct_ipv6"} = 8189.00

Press q or Ctrl+C to quit.
```