package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEuropeCoverage(t *testing.T) {
	cfg := configuration()
	cfg.DataDir = t.TempDir()
	s, err := newServer(cfg)
	require.NoError(t, err)
	assert.Less(t, len(s.geographies["EU"].cells)*8, 8500)
	tests := []struct {
		name     string
		lat, lon float64
		code     string
	}{
		{"Berlin", 52.52, 13.41, "DE"}, {"Istanbul", 41.01, 28.98, "TR"}, {"Paris", 48.85, 2.35, "EU"},
		{"London", 51.5, -.12, "EU"}, {"Madrid", 40.42, -3.7, "EU"}, {"Reykjavik", 64.15, -21.94, "EU"},
		{"Rome", 41.9, 12.5, "EU"}, {"Helsinki", 60.17, 24.94, "EU"}, {"Kyiv", 50.45, 30.52, "EU"},
		{"Moscow", 55.75, 37.62, "EU"}, {"Lake Bled", 46.36, 14.09, "EU"}, {"Loch Ness", 57.28, -4.48, "EU"},
		{"Cairo", 30.04, 31.24, ""}, {"New York", 40.71, -74, ""}, {"Atlantic", 48, -20, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			country, inside := s.locationCountry(tt.lat, tt.lon)
			assert.Equal(t, tt.code != "", inside)
			assert.Equal(t, tt.code, country.Code)
		})
	}
}

func TestViewportGrid(t *testing.T) {
	tests := []struct {
		name, raw string
		valid     bool
	}{
		{"Berlin", "13,52,14,53", true}, {"Europe", "-25,34,60,82", true}, {"edge", "-30,30,0,60", true},
		{"missing bound", "1,2,3", false}, {"inverted", "10,50,5,45", false}, {"NaN", "NaN,50,10,51", false}, {"outside", "-80,20,-70,40", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := viewportCountry(tt.raw)
			if !tt.valid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.LessOrEqual(t, int((c.Bounds[2]-c.Bounds[0])/c.Spacing*(c.Bounds[3]-c.Bounds[1])/c.Spacing), 64)
		})
	}
	a, err := viewportCountry("13.01,52.01,13.99,52.99")
	require.NoError(t, err)
	b, err := viewportCountry("13.02,52.02,13.98,52.98")
	require.NoError(t, err)
	assert.Equal(t, a.Bounds, b.Bounds)
}

func TestAutomaticPointRoutes(t *testing.T) {
	cfg := configuration()
	cfg.DataDir = t.TempDir()
	cfg.Debug = false
	s, err := newServer(cfg)
	require.NoError(t, err)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "icon_seamless", r.URL.Query().Get("models"))
		assert.Equal(t, "auto", r.URL.Query().Get("timezone"))
		_, _ = w.Write([]byte(`{"timezone":"Europe/Paris","hourly":{"time":[100],"weather_code":[45]}}`))
	}))
	defer upstream.Close()
	s.provider.forecastURL = upstream.URL
	require.NoError(t, s.cache.put("point:icon_d2:52.5200,13.4100", Forecast{Timezone: "Europe/Berlin", Hours: []Hour{{Time: 100, Fog: "fog"}}}))
	require.NoError(t, s.cache.put("point:icon_eu:41.0100,28.9800", Forecast{Timezone: "Europe/Istanbul", Hours: []Hour{{Time: 100, Fog: "fog"}}}))
	tests := []struct{ name, path, source, zone string }{
		{"Germany", "/api/forecast?lat=52.52&lon=13.41", "ICON D2", "Europe/Berlin"},
		{"Turkey", "/api/forecast?lat=41.01&lon=28.98", "ICON EU", "Europe/Istanbul"},
		{"France", "/api/forecast?lat=48.85&lon=2.35", "ICON Seamless", "Europe/Paris"},
		{"Query does not override location", "/api/forecast?country=TR&lat=52.52&lon=13.41", "ICON D2", "Europe/Berlin"},
	}
	app := s.routes()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			app.ServeHTTP(w, httptest.NewRequest("GET", tt.path, nil))
			require.Equal(t, 200, w.Code)
			assert.Contains(t, w.Body.String(), tt.source)
			assert.Contains(t, w.Body.String(), tt.zone)
		})
	}
}

func TestEuropeRoutes(t *testing.T) {
	cfg := configuration()
	cfg.DataDir = t.TempDir()
	cfg.Debug = false
	s, err := newServer(cfg)
	require.NoError(t, err)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.URL.Query().Get("countryCode"))
		_, _ = w.Write([]byte(`{"results":[{"name":"Paris","admin1":"Île-de-France","country":"France","latitude":48.85,"longitude":2.35},{"name":"Berlin","country":"Germany","latitude":52.52,"longitude":13.41},{"name":"New York","country":"United States","latitude":40.71,"longitude":-74}]}`))
	}))
	defer upstream.Close()
	s.provider.searchURL = upstream.URL
	when := time.Now().Unix()
	vis := 700.
	require.NoError(t, s.cache.put("overview:europe", Overview{Type: "FeatureCollection", Times: []int64{when}, Features: []Feature{}, Spacing: 2}))
	require.NoError(t, s.cache.put("observations:airports", []Observation{{ID: "LFPG", Name: "Paris airport", Latitude: 49, Longitude: 2.5, Visibility: &vis, VisibilityTime: when}, {ID: "KJFK", Name: "New York", Latitude: 40.71, Longitude: -74, Visibility: &vis, VisibilityTime: when}}))
	require.NoError(t, s.cache.put("observations:germany", []Observation{{ID: "berlin", Name: "Berlin station", Latitude: 52.52, Longitude: 13.41, Visibility: &vis, VisibilityTime: when}}))
	app := s.routes()
	tests := []struct{ name, path, contains string }{
		{"config", "/api/config", `"coverage"`},
		{"health", "/api/health?country=invalid", `"overviewReady":true`},
		{"overview", "/api/overview?country=TR", `"spacingDegrees":2`},
		{"observations", "/api/observations?country=DE", "Paris airport"},
		{"search", "/api/search?country=invalid&q=place", "Paris"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			app.ServeHTTP(w, httptest.NewRequest("GET", tt.path, nil))
			require.Equal(t, 200, w.Code)
			assert.Contains(t, w.Body.String(), tt.contains)
			if tt.name == "config" {
				assert.NotContains(t, w.Body.String(), `"countries"`)
			}
			if tt.name == "observations" {
				assert.Contains(t, w.Body.String(), "Berlin station")
				assert.NotContains(t, w.Body.String(), "New York")
			}
			if tt.name == "search" {
				assert.Contains(t, w.Body.String(), "Berlin")
				assert.Contains(t, w.Body.String(), "France")
				assert.NotContains(t, w.Body.String(), "New York")
			}
		})
	}
}

func TestEuropeMETARCache(t *testing.T) {
	when := time.Now().UTC().Format(time.RFC3339Nano)
	fixture := fmt.Sprintf("station_id,observation_time,latitude,longitude,visibility_statute_mi,wx_string\nLFPG,%s,49,2.5,6+,\nEGLL,%s,51.48,-.45,M1/4,FG\nKJFK,%s,40,-74,10,\n", when, when, when)
	tests := []struct {
		name, body string
		count      int
		bad        bool
	}{{"European airports", fixture, 2, false}, {"missing columns", "station_id\nLFPG\n", 0, true}, {"broken row", "station_id,observation_time,latitude,longitude,visibility_statute_mi,wx_string\nLFPG\n", 0, true}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obs, err := decodeEuropeMETARs(strings.NewReader(tt.body), func(lat, lon float64) bool { return lon > -25 && lon < 60 && lat > 34 && lat < 82 })
			if tt.bad {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Len(t, obs, tt.count)
			assert.Equal(t, "EGLL", obs[0].ID)
			assert.Equal(t, "below", obs[0].VisibilityQualifier)
			assert.True(t, obs[0].Fog)
			assert.Equal(t, "atLeast", obs[1].VisibilityQualifier)
		})
	}
}

func TestViewportForecast(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := len(strings.Split(r.URL.Query().Get("latitude"), ","))
		models := []map[string]any{}
		for i := 0; i < n; i++ {
			models = append(models, map[string]any{"hourly": map[string]any{"time": []int{100, 200}, "weather_code": []int{45, 0}}})
		}
		_ = json.NewEncoder(w).Encode(models)
	}))
	defer upstream.Close()
	s := Server{provider: &Provider{client: upstream.Client(), forecastURL: upstream.URL, budget: &Budget{}}}
	c, err := viewportCountry("2,48,3,49")
	require.NoError(t, err)
	result, err := s.viewportOverview(context.Background(), c)
	require.NoError(t, err)
	require.NotEmpty(t, result.Features)
	assert.Equal(t, []int64{100, 200}, result.Times)
	assert.LessOrEqual(t, len(result.Features), 64)
	c, err = viewportCountry("-20,45,-19,46")
	require.NoError(t, err)
	result, err = s.viewportOverview(context.Background(), c)
	require.NoError(t, err)
	assert.Empty(t, result.Features)
}
