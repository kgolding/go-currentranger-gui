package main

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// CurrentRanger scientific notation pattern: e.g. "1234E-6"
var crPattern = regexp.MustCompile(`^(-?\d+\.?\d*)E([+-]?\d+)\s*$`)

// parseCurrentRangerLine parses a CurrentRanger serial line, returning the
// current in amps and whether parsing succeeded.
func parseCurrentRangerLine(line string) (float64, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return 0, false
	}
	if m := crPattern.FindStringSubmatch(line); m != nil {
		mantissa, err1 := strconv.ParseFloat(m[1], 64)
		exponent, err2 := strconv.Atoi(m[2])
		if err1 == nil && err2 == nil {
			return mantissa * math.Pow(10, float64(exponent)), true
		}
	}
	if v, err := strconv.ParseFloat(line, 64); err == nil {
		// The device's plain USB-logging lines (toggled by the 'u' command,
		// e.g. "-403.05") report current in microamps, not amps — confirmed
		// against a live device: raw values were exact multiples of ~403,
		// consistent with the ADC's µA-scale quantization step.
		return v * 1e-6, true
	}
	return 0, false
}

// formatCurrent renders a human-readable, auto-ranged current string.
func formatCurrent(amps float64) string {
	a := math.Abs(amps)
	switch {
	case a >= 1.0:
		return fmt.Sprintf("%.3f A", amps)
	case a >= 1e-3:
		return fmt.Sprintf("%.3f mA", amps*1e3)
	case a >= 1e-6:
		return fmt.Sprintf("%.3f uA", amps*1e6)
	default:
		return fmt.Sprintf("%.1f nA", amps*1e9)
	}
}

// formatMilliamps renders a value already in amps as a bare mA number,
// used for Y axis tick labels (mirrors smart_axis_formatter).
func formatMilliamps(amps float64) string {
	return fmt.Sprintf("%.2f", amps*1e3)
}
