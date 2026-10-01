package main

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Use a stable grid and cap each viewport at 64 samples. Panning inside the
// same grid extent reuses the cache rather than consuming new location calls.
func viewportCountry(raw string) (Country, error) {
	country := countries[2]
	parts := strings.Split(raw, ",")
	if len(parts) != 4 {
		return Country{}, fmt.Errorf("Use bbox=west,south,east,north")
	}
	bounds := [4]float64{}
	for i, part := range parts {
		n, err := strconv.ParseFloat(part, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return Country{}, fmt.Errorf("Invalid map bounds")
		}
		bounds[i] = n
	}
	if bounds[0] >= bounds[2] || bounds[1] >= bounds[3] {
		return Country{}, fmt.Errorf("Invalid map bounds")
	}
	bounds[0] = max(bounds[0], country.Bounds[0])
	bounds[1] = max(bounds[1], country.Bounds[1])
	bounds[2] = min(bounds[2], country.Bounds[2])
	bounds[3] = min(bounds[3], country.Bounds[3])
	if bounds[0] >= bounds[2] || bounds[1] >= bounds[3] {
		return Country{}, fmt.Errorf("Move the map within Europe")
	}
	for step := .25; step <= 32; step *= 2 {
		snapped := [4]float64{math.Floor(bounds[0]/step) * step, math.Floor(bounds[1]/step) * step, math.Ceil(bounds[2]/step) * step, math.Ceil(bounds[3]/step) * step}
		count := math.Round((snapped[2]-snapped[0])/step) * math.Round((snapped[3]-snapped[1])/step)
		if count <= 64 {
			country.Bounds = snapped
			country.Spacing = step
			return country, nil
		}
	}
	return Country{}, fmt.Errorf("Map bounds are too large")
}

func (s *Server) viewportOverview(ctx context.Context, country Country) (Overview, error) {
	geography, err := geographyFor(country)
	if err != nil {
		return Overview{}, err
	}
	result := Overview{Type: "FeatureCollection", Times: []int64{}, Features: []Feature{}, Spacing: country.Spacing}
	// An ocean-only viewport is a successful, empty overlay.
	if len(geography.cells) == 0 {
		return result, nil
	}
	for start := 0; start < len(geography.cells); start += 32 {
		end := min(start+32, len(geography.cells))
		coords := []Coordinate{}
		for _, cell := range geography.cells[start:end] {
			coords = append(coords, cell.Coordinate)
		}
		forecasts, err := s.provider.forecasts(ctx, coords, country, false)
		if err != nil {
			return Overview{}, err
		}
		if len(result.Times) == 0 {
			for _, hour := range forecasts[0].Hours {
				result.Times = append(result.Times, hour.Time)
			}
		}
		for i, forecast := range forecasts {
			byTime := map[int64]string{}
			for _, hour := range forecast.Hours {
				byTime[hour.Time] = hour.Fog
			}
			states := []string{}
			for _, epoch := range result.Times {
				state := byTime[epoch]
				if state == "" {
					state = "unknown"
				}
				states = append(states, state)
			}
			result.Features = append(result.Features, Feature{Type: "Feature", Geometry: geography.cells[start+i].Geometry, Properties: struct {
				ID     int      `json:"id"`
				States []string `json:"states"`
			}{start + i, states}})
		}
	}
	return result, nil
}
