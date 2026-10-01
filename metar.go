package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// AWC JSON visibility is in statute miles, and may include a lower/upper
// bound (e.g. "6+" or "M1/4"). Keep those bounds visible to the photographer.
func metarVisibility(v any) (*float64, string) {
	if v == nil {
		return nil, ""
	}
	s := strings.TrimSpace(fmt.Sprint(v))
	qualifier := ""
	if strings.HasSuffix(s, "+") || strings.HasPrefix(s, "P") {
		qualifier = "atLeast"
		s = strings.TrimPrefix(strings.TrimSuffix(s, "+"), "P")
	}
	if strings.HasPrefix(s, "M") {
		qualifier = "below"
		s = strings.TrimPrefix(s, "M")
	}
	n, err := strconv.ParseFloat(s, 64)
	if strings.Contains(s, "/") {
		parts := strings.Split(s, "/")
		if len(parts) != 2 {
			return nil, ""
		}
		numerator, e1 := strconv.ParseFloat(parts[0], 64)
		denominator, e2 := strconv.ParseFloat(parts[1], 64)
		if e1 != nil || e2 != nil || denominator <= 0 {
			return nil, ""
		}
		n = numerator / denominator
		err = nil
	}
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 1000 {
		return nil, ""
	}
	metres := n * 1609.344
	return &metres, qualifier
}
func metarFog(weather string) bool {
	for _, token := range strings.Fields(strings.ToUpper(weather)) {
		if token == "FG" || token == "FZFG" || token == "MIFG" || token == "BCFG" || token == "PRFG" || token == "VCFG" {
			return true
		}
	}
	return false
}
