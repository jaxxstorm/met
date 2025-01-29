# Met

Met is a small CLI tool that will periodically scrape a metrics compatible endpoint and return the values interactively via [Bubbletea](https://github.com/charmbracelet/bubbletea)

Simply point it at an endpoint, and you'll get a nice periodically refreshed output.

Counter metrics will accumulate over time, whereas Gauge metrics will show the last returned value.

![Met](assets/met.gif)