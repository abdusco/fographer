package main

import (
	"compress/gzip"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// NOAA recommends its bulk cache for large regions; a bounding-box query
// truncates at 400 reports and would silently miss European airports.
func decodeEuropeMETARs(r io.Reader, inside func(float64, float64) bool) ([]Observation, error) {
	reader := csv.NewReader(io.LimitReader(r, 16<<20))
	header, err := reader.Read()
	if err != nil {
		return nil, err
	}
	columns := map[string]int{}
	for i, name := range header {
		columns[name] = i
	}
	for _, name := range []string{"station_id", "observation_time", "latitude", "longitude", "visibility_statute_mi", "wx_string"} {
		if _, ok := columns[name]; !ok {
			return nil, fmt.Errorf("airport cache missing %s", name)
		}
	}
	observations := []Observation{}
	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		lat, e1 := strconv.ParseFloat(row[columns["latitude"]], 64)
		lon, e2 := strconv.ParseFloat(row[columns["longitude"]], 64)
		when, e3 := time.Parse(time.RFC3339Nano, row[columns["observation_time"]])
		id := row[columns["station_id"]]
		if e1 != nil || e2 != nil || e3 != nil || id == "" || !inside(lat, lon) {
			continue
		}
		vis, qualifier := metarVisibility(row[columns["visibility_statute_mi"]])
		weather := strings.TrimSpace(row[columns["wx_string"]])
		fog := metarFog(weather)
		if weather == "" {
			weather = "No significant weather reported"
		}
		o := Observation{ID: id, Name: id + " airport", Latitude: lat, Longitude: lon, Time: when.Unix(), Weather: weather, WeatherTime: when.Unix(), Fog: fog, Visibility: vis, VisibilityQualifier: qualifier}
		if vis != nil {
			o.VisibilityTime = when.Unix()
		}
		observations = append(observations, o)
	}
	return mergeObservations(nil, observations), nil
}

func (s *Server) refreshEuropeObservations(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.config.MetarCacheURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Fographer/1.0 (Europe photography weather map)")
	response, err := s.provider.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("airport cache returned HTTP %d", response.StatusCode)
	}
	zipped, err := gzip.NewReader(response.Body)
	if err != nil {
		return err
	}
	defer zipped.Close()
	incoming, err := decodeEuropeMETARs(zipped, func(lat, lon float64) bool { _, ok := s.locationCountry(lat, lon); return ok })
	if err != nil {
		return err
	}
	var old []Observation
	if entry, ok := s.cache.lookup("observations:EU"); ok {
		_ = json.Unmarshal(entry.Data, &old)
	}
	return s.cache.put("observations:EU", freshObservations(mergeObservations(old, incoming), time.Now()))
}
