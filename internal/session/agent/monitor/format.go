package monitor

import "fmt"

// FormatTokens returns a human-readable token count (e.g., "6k", "1.2M").
func FormatTokens(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 10000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	if n < 1000000 {
		return fmt.Sprintf("%dk", n/1000)
	}
	if n < 10000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
	return fmt.Sprintf("%dM", n/1000000)
}

// FormatCost returns a human-readable cost (e.g., "$0.12", "$1.23").
func FormatCost(usd float64) string {
	return fmt.Sprintf("$%.2f", usd)
}
