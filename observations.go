package main

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Observation struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	Latitude            float64  `json:"latitude"`
	Longitude           float64  `json:"longitude"`
	Time                int64    `json:"time"`
	Visibility          *float64 `json:"visibility"`
	VisibilityQualifier string   `json:"visibilityQualifier,omitempty"`
	VisibilityTime      int64    `json:"visibilityTime"`
	Weather             string   `json:"weather"`
	WeatherTime         int64    `json:"weatherTime"`
	Fog                 bool     `json:"fog"`
	Stale               bool     `json:"stale"`
}
type reportField struct {
	Short string `json:"shortname"`
	Value any    `json:"value"`
	Unit  string `json:"unit"`
}

func reportedFog(weather string) bool {
	s := strings.ToUpper(strings.TrimSpace(weather))
	return (strings.HasPrefix(s, "FOG") || strings.HasPrefix(s, "ICE FOG") || strings.HasPrefix(s, "FREEZING FOG")) && !strings.Contains(s, "DISSIPATED") && !strings.Contains(s, "DISAPPEARED")
}
func decodeObservations(r io.Reader) ([]Observation, error) {
	var collection struct {
		Features []struct {
			Geometry struct {
				Coordinates []float64 `json:"coordinates"`
			} `json:"geometry"`
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"features"`
	}
	if err := json.NewDecoder(io.LimitReader(r, 96<<20)).Decode(&collection); err != nil {
		return nil, err
	}
	result := []Observation{}
	for _, f := range collection.Features {
		if len(f.Geometry.Coordinates) < 2 {
			continue
		}
		fields := map[string]reportField{}
		keys := []string{}
		for key := range f.Properties {
			if strings.HasPrefix(key, "data_") {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			var v reportField
			if json.Unmarshal(f.Properties[key], &v) == nil && v.Short != "" && v.Value != nil {
				if _, ok := fields[v.Short]; !ok {
					fields[v.Short] = v
				}
			}
		}
		str := func(k string) string {
			v := fields[k].Value
			if v == nil {
				return ""
			}
			return strings.TrimSpace(fmt.Sprint(v))
		}
		num := func(k string) int { n, _ := strconv.Atoi(str(k)); return n }
		if num("year") < 2000 || num("month") < 1 || num("month") > 12 || num("day") < 1 || num("day") > 31 || num("hour") > 23 || num("minute") > 59 {
			continue
		}
		t := time.Date(num("year"), time.Month(num("month")), num("day"), num("hour"), num("minute"), 0, 0, time.UTC).Unix()
		id := strings.Join([]string{str("wigosIdentifierSeries"), str("wigosIssuerOfIdentifier"), str("wigosIssueNumber"), str("wigosLocalIdentifierCharacter")}, "-")
		if str("wigosLocalIdentifierCharacter") == "" {
			id = str("blockNumber") + "-" + str("stationNumber")
		}
		if id == "-" {
			id = fmt.Sprintf("%.5f,%.5f", f.Geometry.Coordinates[0], f.Geometry.Coordinates[1])
		}
		o := Observation{ID: id, Name: str("stationOrSiteName"), Latitude: f.Geometry.Coordinates[1], Longitude: f.Geometry.Coordinates[0], Time: t, Weather: str("presentWeather")}
		if v, ok := fields["horizontalVisibility"]; ok && v.Unit == "m" {
			n, ok := v.Value.(float64)
			if ok && n >= 0 {
				o.Visibility = &n
				o.VisibilityTime = t
			}
		}
		if o.Weather != "" {
			o.WeatherTime = t
			o.Fog = reportedFog(o.Weather)
		}
		if o.Visibility != nil || o.Weather != "" {
			result = append(result, o)
		}
	}
	return result, nil
}
func mergeObservations(previous, incoming []Observation) []Observation {
	byID := map[string]Observation{}
	for _, o := range previous {
		byID[o.ID] = o
	}
	for _, o := range incoming {
		old, ok := byID[o.ID]
		if !ok {
			byID[o.ID] = o
			continue
		}
		if o.Visibility != nil && o.VisibilityTime >= old.VisibilityTime {
			old.Visibility = o.Visibility
			old.VisibilityQualifier = o.VisibilityQualifier
			old.VisibilityTime = o.VisibilityTime
		}
		if o.Weather != "" && o.WeatherTime >= old.WeatherTime {
			old.Weather = o.Weather
			old.WeatherTime = o.WeatherTime
			old.Fog = o.Fog
		}
		if o.Time >= old.Time {
			old.Name = o.Name
			old.Latitude = o.Latitude
			old.Longitude = o.Longitude
			old.Time = o.Time
		}
		byID[o.ID] = old
	}
	result := make([]Observation, 0, len(byID))
	for _, o := range byID {
		result = append(result, o)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
func freshObservations(all []Observation, now time.Time) []Observation {
	result := []Observation{}
	for _, o := range all {
		if now.Unix()-o.VisibilityTime > 10800 {
			o.Visibility = nil
			o.VisibilityTime = 0
		}
		if now.Unix()-o.WeatherTime > 10800 {
			o.Weather = ""
			o.WeatherTime = 0
			o.Fog = false
		}
		o.Time = max(o.VisibilityTime, o.WeatherTime)
		if o.Time == 0 || o.Time > now.Add(5*time.Minute).Unix() {
			continue
		}
		o.Stale = now.Unix()-o.Time > 5400
		result = append(result, o)
	}
	return result
}

var reportLink = regexp.MustCompile(`href="([^"/]+\.geojson\.gz)"`)
var reportStamp = regexp.MustCompile(`EDZW_(\d{14})_`)

func reportFiles(body string, now time.Time, seen map[string]bool) []string {
	result := []string{}
	for _, m := range reportLink.FindAllStringSubmatch(body, -1) {
		name, err := url.PathUnescape(m[1])
		if err != nil || seen[name] {
			continue
		}
		stamp := reportStamp.FindStringSubmatch(name)
		if len(stamp) != 2 {
			continue
		}
		t, err := time.Parse("20060102150405", stamp[1])
		if err != nil || t.Before(now.Add(-3*time.Hour)) || t.After(now.Add(5*time.Minute)) {
			continue
		}
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}
func (s *Server) refreshObservations(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.config.ObservationsURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Fographer/1.0")
	resp, err := s.provider.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("DWD listing returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return err
	}
	files := reportFiles(string(body), time.Now(), s.seenReports)
	var old []Observation
	if entry, ok := s.cache.lookup("observations:germany"); ok {
		_ = json.Unmarshal(entry.Data, &old)
	}
	success := false
	for _, name := range files {
		endpoint := strings.TrimRight(s.config.ObservationsURL, "/") + "/" + url.PathEscape(name)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", "Fographer/1.0")
		response, err := s.provider.client.Do(req)
		if err != nil {
			s.logger.Warn("station report unavailable", "error", err)
			continue
		}
		if response.StatusCode != 200 {
			response.Body.Close()
			continue
		}
		z, err := gzip.NewReader(response.Body)
		if err != nil {
			response.Body.Close()
			continue
		}
		obs, err := decodeObservations(z)
		z.Close()
		response.Body.Close()
		if err != nil {
			s.logger.Warn("invalid station report", "error", err)
			continue
		}
		old = mergeObservations(old, obs)
		s.seenReports[name] = true
		success = true
	}
	if !success && len(files) > 0 {
		return fmt.Errorf("no DWD reports could be read")
	}
	if success {
		return s.cache.put("observations:germany", freshObservations(old, time.Now()))
	}
	// An empty listing is not evidence of a successful weather update.
	if _, ok := s.cache.lookup("observations:germany"); !ok {
		return fmt.Errorf("DWD has no recent station reports")
	}
	for name := range s.seenReports {
		stamp := reportStamp.FindStringSubmatch(name)
		if len(stamp) == 2 {
			t, _ := time.Parse("20060102150405", stamp[1])
			if t.Before(time.Now().Add(-3 * time.Hour)) {
				delete(s.seenReports, name)
			}
		}
	}
	return nil
}
