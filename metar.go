package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
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
func decodeMETARs(r io.Reader) ([]Observation, error) {
	var reports []struct {
		ID         string  `json:"icaoId"`
		Name       string  `json:"name"`
		Lat        float64 `json:"lat"`
		Lon        float64 `json:"lon"`
		Time       int64   `json:"obsTime"`
		Visibility any     `json:"visib"`
		Weather    string  `json:"wxString"`
	}
	if err := json.NewDecoder(io.LimitReader(r, 5<<20)).Decode(&reports); err != nil {
		return nil, err
	}
	result := []Observation{}
	for _, r := range reports {
		if !strings.HasPrefix(r.ID, "LT") || r.Time <= 0 {
			continue
		}
		visibility, qualifier := metarVisibility(r.Visibility)
		weather := strings.TrimSpace(r.Weather)
		if weather == "" {
			weather = "No significant weather reported"
		}
		o := Observation{ID: r.ID, Name: r.Name, Latitude: r.Lat, Longitude: r.Lon, Time: r.Time, Visibility: visibility, VisibilityQualifier: qualifier, Weather: weather, WeatherTime: r.Time, Fog: metarFog(r.Weather)}
		if visibility != nil {
			o.VisibilityTime = r.Time
		}
		result = append(result, o)
	}
	return mergeObservations(nil, result), nil
}
func (s *Server) refreshTurkeyObservations(ctx context.Context) error {
	q := url.Values{"bbox": {"35.5,25.5,42.5,45"}, "format": {"json"}, "hours": {"3"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.config.MetarURL+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Fographer/1.0 (Turkey photography weather map)")
	response, err := s.provider.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	var incoming []Observation
	if response.StatusCode == http.StatusOK {
		incoming, err = decodeMETARs(response.Body)
		if err != nil {
			return err
		}
	} else if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("airport observations returned HTTP %d", response.StatusCode)
	}
	var old []Observation
	if entry, ok := s.cache.lookup("observations:TR"); ok {
		_ = json.Unmarshal(entry.Data, &old)
	}
	return s.cache.put("observations:TR", freshObservations(mergeObservations(old, incoming), time.Now()))
}
