package cost

import (
	"fmt"
	"math"
)

func FormatUSDPerMonthFull(usd float64) string {
	// Full detail for Details pane.
	return fmt.Sprintf("$%.2f/mo", usd)
}

func FormatUSDPerMonthTable(usd float64) string {
	// Compact for table: whole dollars.
	return fmt.Sprintf("$%.0f/mo", usd)
}

func FormatUSDPerMonthCompact(usd float64) string {
	// Compact for navigator totals.
	abs := math.Abs(usd)
	switch {
	case abs >= 1_000_000_000:
		return fmt.Sprintf("$%.2fb/mo", usd/1_000_000_000)
	case abs >= 1_000_000:
		return fmt.Sprintf("$%.2fm/mo", usd/1_000_000)
	case abs >= 1_000:
		return fmt.Sprintf("$%.2fk/mo", usd/1_000)
	default:
		return fmt.Sprintf("$%.0f/mo", usd)
	}
}
