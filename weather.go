package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Hour struct {
	Time          int64    `json:"time"`
	Temperature   *float64 `json:"temperature"`
	Humidity      *float64 `json:"humidity"`
	DewPoint      *float64 `json:"dewPoint"`
	Visibility    *float64 `json:"visibility"`
	Wind          *float64 `json:"wind"`
	Precipitation *float64 `json:"precipitation"`
	Code          *float64 `json:"code"`
	LowCloud      *float64 `json:"lowCloud"`
	Fog           string   `json:"fog"`
}

func classify(h Hour) string {
	if h.Code != nil && (*h.Code == 45 || *h.Code == 48) {
		return "fog"
	}
	if h.Humidity == nil || h.Precipitation == nil {
		return "unknown"
	}
	if h.Visibility != nil && *h.Visibility < 1000 && *h.Humidity >= 95 && *h.Precipitation < .2 {
		return "fog"
	}
	if h.Temperature == nil || h.DewPoint == nil || h.Wind == nil {
		return "unknown"
	}
	if *h.Humidity >= 95 && *h.Temperature-*h.DewPoint <= 2 && *h.Wind <= 10 && *h.Precipitation < .2 {
		return "favorable"
	}
	return "clear"
}

type Sun struct {
	Day  int64 `json:"day"`
	Rise int64 `json:"rise"`
	Set  int64 `json:"set"`
}
type Forecast struct {
	Timezone  string  `json:"timezone,omitempty"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Hours     []Hour  `json:"hours"`
	Sun       []Sun   `json:"sun"`
}
type modelResponse struct {
	Timezone  string  `json:"timezone"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Hourly    struct {
		Time          []int64    `json:"time"`
		Temperature   []*float64 `json:"temperature_2m"`
		Humidity      []*float64 `json:"relative_humidity_2m"`
		DewPoint      []*float64 `json:"dew_point_2m"`
		Visibility    []*float64 `json:"visibility"`
		Wind          []*float64 `json:"wind_speed_10m"`
		Precipitation []*float64 `json:"precipitation"`
		Code          []*float64 `json:"weather_code"`
		LowCloud      []*float64 `json:"cloud_cover_low"`
	} `json:"hourly"`
	Daily struct {
		Time []int64 `json:"time"`
		Rise []int64 `json:"sunrise"`
		Set  []int64 `json:"sunset"`
	} `json:"daily"`
}

func value(a []*float64, i int) *float64 {
	if i >= len(a) || a[i] == nil || math.IsNaN(*a[i]) || math.IsInf(*a[i], 0) {
		return nil
	}
	return a[i]
}
func (r modelResponse) forecast() (Forecast, error) {
	f := Forecast{Timezone: r.Timezone, Latitude: r.Latitude, Longitude: r.Longitude, Hours: []Hour{}, Sun: []Sun{}}
	if len(r.Hourly.Time) == 0 {
		return f, errors.New("provider returned no forecast hours")
	}
	for i, t := range r.Hourly.Time {
		if i > 0 && t <= r.Hourly.Time[i-1] {
			return f, errors.New("provider timestamps are not increasing")
		}
		h := Hour{Time: t, Temperature: value(r.Hourly.Temperature, i), Humidity: value(r.Hourly.Humidity, i), DewPoint: value(r.Hourly.DewPoint, i), Visibility: value(r.Hourly.Visibility, i), Wind: value(r.Hourly.Wind, i), Precipitation: value(r.Hourly.Precipitation, i), Code: value(r.Hourly.Code, i), LowCloud: value(r.Hourly.LowCloud, i)}
		h.Fog = classify(h)
		f.Hours = append(f.Hours, h)
	}
	for i, t := range r.Daily.Time {
		if i < len(r.Daily.Rise) && i < len(r.Daily.Set) {
			f.Sun = append(f.Sun, Sun{t, r.Daily.Rise[i], r.Daily.Set[i]})
		}
	}
	return f, nil
}

// Counts locations, not just HTTP requests. Allow at most 400 location calls
// per minute and 9,000 per UTC day, leaving headroom under the free API limits.
type Budget struct {
	mu      sync.Mutex
	next    time.Time
	day     string
	used    int
	persist func(string, int)
}

func (b *Budget) wait(ctx context.Context, n int) error {
	b.mu.Lock()
	now := time.Now()
	day := now.UTC().Format("2006-01-02")
	if day != b.day {
		b.day = day
		b.used = 0
	}
	if b.used+n > 9000 {
		b.mu.Unlock()
		return errors.New("daily weather request budget reached")
	}
	start := b.next
	if start.Before(now) {
		start = now
	}
	b.next = start.Add(time.Duration(n) * 150 * time.Millisecond)
	b.used += n
	if b.persist != nil {
		b.persist(b.day, b.used)
	}
	b.mu.Unlock()
	timer := time.NewTimer(time.Until(start))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type Provider struct {
	client      *http.Client
	forecastURL string
	searchURL   string
	budget      *Budget
}

func (p *Provider) get(ctx context.Context, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Fographer/1.0 (personal fog photography app)")
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("weather provider returned HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 20<<20)).Decode(out)
}

type Coordinate struct {
	Lat float64
	Lon float64
}

func (p *Provider) forecasts(ctx context.Context, coords []Coordinate, country Country, sun bool) ([]Forecast, error) {
	if len(coords) == 0 {
		return nil, errors.New("no forecast locations")
	}
	if err := p.budget.wait(ctx, len(coords)); err != nil {
		return nil, err
	}
	lats, lons := []string{}, []string{}
	for _, c := range coords {
		lats = append(lats, strconv.FormatFloat(c.Lat, 'f', 4, 64))
		lons = append(lons, strconv.FormatFloat(c.Lon, 'f', 4, 64))
	}
	q := url.Values{"latitude": {strings.Join(lats, ",")}, "longitude": {strings.Join(lons, ",")}, "hourly": {"temperature_2m,relative_humidity_2m,dew_point_2m,visibility,wind_speed_10m,precipitation,weather_code,cloud_cover_low"}, "models": {country.Model}, "forecast_hours": {"48"}, "timezone": {country.Timezone}, "timeformat": {"unixtime"}}
	if sun {
		q.Set("daily", "sunrise,sunset")
		q.Set("forecast_days", "3")
	}
	var raw json.RawMessage
	if err := p.get(ctx, p.forecastURL+"?"+q.Encode(), &raw); err != nil {
		return nil, err
	}
	var models []modelResponse
	if len(raw) > 0 && raw[0] == '[' {
		if err := json.Unmarshal(raw, &models); err != nil {
			return nil, err
		}
	} else {
		var m modelResponse
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		models = append(models, m)
	}
	if len(models) != len(coords) {
		return nil, errors.New("provider returned incomplete location batch")
	}
	result := make([]Forecast, 0, len(models))
	for _, m := range models {
		f, err := m.forecast()
		if err != nil {
			return nil, err
		}
		result = append(result, f)
	}
	return result, nil
}

type Place struct {
	Name      string  `json:"name"`
	Region    string  `json:"region"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

func (p *Provider) search(ctx context.Context, query string) ([]Place, error) {
	if err := p.budget.wait(ctx, 1); err != nil {
		return nil, err
	}
	q := url.Values{"name": {query}, "count": {"20"}, "language": {"en"}, "format": {"json"}}
	var r struct {
		Results []struct {
			Name        string  `json:"name"`
			Region      string  `json:"admin1"`
			CountryName string  `json:"country"`
			Latitude    float64 `json:"latitude"`
			Longitude   float64 `json:"longitude"`
		} `json:"results"`
	}
	if err := p.get(ctx, p.searchURL+"?"+q.Encode(), &r); err != nil {
		return nil, err
	}
	places := []Place{}
	for _, v := range r.Results {
		if v.CountryName != "" {
			v.Region = strings.Trim(strings.Join([]string{v.Region, v.CountryName}, " · "), " ·")
		}
		places = append(places, Place{v.Name, v.Region, v.Latitude, v.Longitude})
	}
	return places, nil
}
