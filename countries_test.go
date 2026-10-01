package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTurkeyGeography(t *testing.T) {
	g, err := geographyFor(countries[1])
	require.NoError(t, err)
	require.NotEmpty(t, g.cells)
	// Eight daily refreshes of both countries must leave room for point lookups.
	de, err := newGeography()
	require.NoError(t, err)
	assert.Less(t, (len(de.cells)+len(g.cells))*8, 8500)
	tests := []struct {
		name     string
		lat, lon float64
		want     bool
	}{
		{"Istanbul", 41.01, 28.98, true}, {"Ankara", 39.93, 32.86, true}, {"Van", 38.5, 43.4, true},
		{"Abant", 40.61, 31.28, true}, {"Kizilcahamam", 40.62, 32.97, true}, {"Uzungol", 40.619, 40.295, true},
		{"Berlin", 52.52, 13.41, false}, {"Athens", 37.98, 23.72, false}, {"Nicosia", 35.19, 33.36, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { assert.Equal(t, tt.want, g.contains(tt.lat, tt.lon)) })
	}
	for _, cell := range g.cells {
		require.True(t, g.contains(cell.Lat, cell.Lon), "Turkey samples must stay inside Turkey")
	}
}
func TestCountryProviders(t *testing.T) {
	tests := []struct{ code, model, timezone string }{
		{"DE", "icon_d2", "Europe/Berlin"}, {"TR", "icon_eu", "Europe/Istanbul"},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			country, ok := countryByCode(tt.code)
			require.True(t, ok)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/forecast" {
					assert.Equal(t, tt.model, r.URL.Query().Get("models"))
					assert.Equal(t, tt.timezone, r.URL.Query().Get("timezone"))
					_, _ = w.Write([]byte(`{"hourly":{"time":[100],"weather_code":[45]}}`))
				} else {
					assert.Equal(t, tt.code, r.URL.Query().Get("countryCode"))
					_, _ = w.Write([]byte(`{"results":[{"name":"Berlin","country_code":"DE"},{"name":"Ankara","country_code":"TR"},{"name":"Paris","country_code":"FR"}]}`))
				}
			}))
			defer upstream.Close()
			p := Provider{client: upstream.Client(), forecastURL: upstream.URL + "/forecast", searchURL: upstream.URL + "/search", budget: &Budget{}}
			forecasts, err := p.forecasts(context.Background(), []Coordinate{{39.93, 32.86}}, country, true)
			require.NoError(t, err)
			require.Len(t, forecasts, 1)
			places, err := p.search(context.Background(), "place", country)
			require.NoError(t, err)
			require.Len(t, places, 1)
			if tt.code == "TR" {
				assert.Equal(t, "Ankara", places[0].Name)
			} else {
				assert.Equal(t, "Berlin", places[0].Name)
			}
		})
	}
}
func TestCountryRoutes(t *testing.T) {
	cfg := configuration()
	cfg.Debug = false
	cfg.DataDir = t.TempDir()
	s, err := newServer(cfg)
	require.NoError(t, err)
	when := time.Now().Unix()
	vis := 700.
	require.NoError(t, s.cache.put("point:TR:39.9300,32.8600", Forecast{Hours: []Hour{{Time: when, Fog: "favorable"}}}))
	require.NoError(t, s.cache.put("observations:TR", []Observation{{ID: "LTAC", Name: "Ankara", Latitude: 39.93, Longitude: 32.86, Visibility: &vis, VisibilityTime: when}, {ID: "outside", Latitude: 52.52, Longitude: 13.41, Visibility: &vis, VisibilityTime: when}}))
	require.NoError(t, s.cache.put("overview:TR", Overview{Type: "FeatureCollection", Spacing: .5, Times: []int64{when}, Features: []Feature{}}))
	app := s.routes()
	tests := []struct {
		name, path string
		status     int
		contains   string
	}{
		{"Turkish point", "/api/forecast?country=TR&lat=39.93&lon=32.86", 200, "DWD ICON EU"},
		{"Turkish point rejected in Germany", "/api/forecast?country=DE&lat=39.93&lon=32.86", 400, "within Germany"},
		{"German point rejected in Turkey", "/api/forecast?country=TR&lat=52.52&lon=13.41", 400, "within Turkey"},
		{"Turkish overview", "/api/overview?country=TR", 200, `"spacingDegrees":0.5`},
		{"Turkish observations", "/api/observations?country=TR", 200, "NOAA Aviation Weather Center"},
		{"case-insensitive country", "/api/overview?country=tr", 200, "DWD ICON EU"},
		{"unsupported overview country", "/api/overview?country=FR", 400, "Unsupported coverage area"},
		{"unsupported search country", "/api/search?country=FR&q=Paris", 400, "Unsupported coverage area"},
		{"unsupported point country", "/api/forecast?country=FR&lat=48&lon=2", 400, "Unsupported coverage area"},
		{"unsupported observation country", "/api/observations?country=FR", 400, "Unsupported coverage area"},
		{"Turkey readiness", "/api/health?country=TR", 200, `"country":"TR"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			app.ServeHTTP(w, httptest.NewRequest("GET", tt.path, nil))
			require.Equal(t, tt.status, w.Code)
			assert.Contains(t, w.Body.String(), tt.contains)
			if tt.name == "Turkish observations" {
				assert.NotContains(t, w.Body.String(), `"id":"outside"`)
			}
		})
	}
}
