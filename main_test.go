package main

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigurationAddress(t *testing.T) {
	tests := []struct {
		name, port, addr, want string
	}{
		{"default", "", "", "127.0.0.1:8080"},
		{"port", "3000", "", "0.0.0.0:3000"},
		{"explicit address", "", "127.0.0.1:9000", "127.0.0.1:9000"},
		{"address overrides port", "3000", "127.0.0.1:9000", "127.0.0.1:9000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PORT", tt.port)
			t.Setenv("ADDR", tt.addr)
			assert.Equal(t, tt.want, configuration().Addr)
		})
	}
}

func TestConfigurationDebug(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"", false}, {"0", false}, {"true", false}, {"1", true},
	}
	for _, tt := range tests {
		t.Run("DEBUG="+tt.value, func(t *testing.T) {
			t.Setenv("DEBUG", tt.value)
			assert.Equal(t, tt.want, configuration().Debug)
		})
	}
}

func TestAssetMode(t *testing.T) {
	tests := []struct {
		name  string
		debug bool
	}{
		{"embedded", false}, {"disk", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(dir, "web"), 0700))
			t.Chdir(dir)
			require.NoError(t, os.WriteFile("web/style.css", []byte("/* first disk version */"), 0600))
			s := &Server{config: Config{Debug: tt.debug}}
			app := s.routes()
			first := httptest.NewRecorder()
			app.ServeHTTP(first, httptest.NewRequest("GET", "/style.css", nil))
			require.Equal(t, 200, first.Code)
			require.NoError(t, os.WriteFile("web/style.css", []byte("/* edited disk version */"), 0600))
			second := httptest.NewRecorder()
			app.ServeHTTP(second, httptest.NewRequest("GET", "/style.css", nil))
			require.Equal(t, 200, second.Code)
			if tt.debug {
				assert.Equal(t, "/* first disk version */", first.Body.String())
				assert.Equal(t, "/* edited disk version */", second.Body.String())
				assert.Equal(t, "no-store", second.Header().Get("Cache-Control"))
			} else {
				assert.Equal(t, first.Body.String(), second.Body.String())
				assert.NotContains(t, first.Body.String(), "disk version")
			}
			worker := httptest.NewRecorder()
			app.ServeHTTP(worker, httptest.NewRequest("GET", "/sw.js", nil))
			require.Equal(t, 200, worker.Code)
			if tt.debug {
				assert.Contains(t, worker.Body.String(), "self.registration.unregister()")
				assert.NotContains(t, worker.Body.String(), "addAll(ASSETS)")
			} else {
				assert.Contains(t, worker.Body.String(), "addAll(ASSETS)")
			}
		})
	}
}

func TestGeography(t *testing.T) {
	g, err := newGeography()
	require.NoError(t, err)
	require.NotEmpty(t, g.cells)
	assert.Less(t, len(g.cells), 800)
	tests := []struct {
		name     string
		lat, lon float64
		want     bool
	}{
		{"Berlin", 52.52, 13.41, true}, {"Black Forest", 48.05, 8.20, true}, {"Saxon Switzerland", 50.92, 14.07, true}, {"Bavarian Alps", 47.58, 11.07, true}, {"Paris", 48.85, 2.35, false}, {"Vienna", 48.21, 16.37, false}, {"NaN", math.NaN(), 13.41, false}, {"infinite", 52.52, math.Inf(1), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { assert.Equal(t, tt.want, g.contains(tt.lat, tt.lon)) })
	}
	for _, cell := range g.cells {
		require.True(t, g.contains(cell.Lat, cell.Lon), "sample must fall inside clipped cell")
	}
}
func TestRoutes(t *testing.T) {
	cfg := configuration()
	cfg.Debug = false
	cfg.DataDir = t.TempDir()
	s, err := newServer(cfg)
	require.NoError(t, err)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) }))
	defer upstream.Close()
	s.provider.forecastURL = upstream.URL
	s.provider.searchURL = upstream.URL
	now := time.Now().Unix()
	old := now - 7200
	visibility := 500.
	require.NoError(t, s.cache.put("observations:airports", []Observation{{ID: "berlin", Name: "Berlin", Latitude: 52.52, Longitude: 13.41, Time: old, Visibility: &visibility, VisibilityTime: old}}))
	require.NoError(t, s.cache.put("point:icon_d2:52.5200,13.4100", Forecast{Hours: []Hour{{Time: now, Fog: "fog"}}}))
	app := s.routes()
	tests := []struct {
		name, path string
		status     int
		contains   string
	}{
		{"health", "/api/health", 200, `"overviewReady":false`},
		{"config", "/api/config", 200, "shortbread_v1"},
		{"warming overview", "/api/overview", 503, "warming up"},
		{"observations:airports", "/api/observations", 200, `"stale":true`},
		{"cached point", "/api/forecast?lat=52.52&lon=13.41", 200, `"fog":"fog"`},
		{"invalid latitude", "/api/forecast?lat=invalid&lon=13", 400, "within Europe"},
		{"outside Europe", "/api/forecast?lat=30.04&lon=31.24", 400, "within Europe"},
		{"nonfinite", "/api/forecast?lat=NaN&lon=13.41", 400, "within Europe"},
		{"missing coords", "/api/forecast", 400, "within Europe"},
		{"uncached upstream failure", "/api/forecast?lat=48.05&lon=8.2", 503, "Forecast unavailable"},
		{"short search", "/api/search?q=a", 400, "120 characters"},
		{"search failure", "/api/search?q=Berlin", 503, "search unavailable"},
		{"unknown API", "/api/no-such-route", 404, "Unknown API"},
		{"home", "/", 200, "Follow the fog."},
		{"service worker", "/sw.js", 200, "fographer-v6"},
		{"map library", "/vendor/maplibre-gl.js", 200, "maplibregl"},
		{"PWA icon", "/assets/icon-192.png", 200, "PNG"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tt.path, nil)
			w := httptest.NewRecorder()
			app.ServeHTTP(w, r)
			assert.Equal(t, tt.status, w.Code)
			assert.Contains(t, w.Body.String(), tt.contains)
			if tt.status == 503 {
				assert.Equal(t, "30", w.Header().Get("Retry-After"))
			}
		})
	}
	t.Run("expired cached point fallback", func(t *testing.T) {
		s.cache.mu.Lock()
		e := s.cache.entries["point:icon_d2:52.5200,13.4100"]
		e.FetchedAt = old
		s.cache.entries["point:icon_d2:52.5200,13.4100"] = e
		s.cache.mu.Unlock()
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest("GET", "/api/forecast?lat=52.52&lon=13.41", nil))
		assert.Equal(t, 200, w.Code)
		var got Envelope
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
		assert.True(t, got.Stale)
		assert.NotEmpty(t, got.Warning)
	})
}
