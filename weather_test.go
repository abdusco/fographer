package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name                                                     string
		humidity, temperature, dew, wind, visibility, rain, code float64
		missing                                                  string
		want                                                     string
	}{
		{"fog code", 40, 20, 5, 20, 10000, 1, 45, "", "fog"},
		{"freezing fog code", 40, -5, -10, 20, 10000, 1, 48, "", "fog"},
		{"humid low visibility", 95, 5, 3, 10, 999, .19, 3, "", "fog"},
		{"favorable boundary", 95, 5, 3, 10, 1000, .19, 3, "", "favorable"},
		{"rain is not fog", 95, 5, 3, 10, 999, .2, 61, "", "clear"},
		{"wind above limit", 95, 5, 3, 10.01, 5000, 0, 3, "", "clear"},
		{"dew point spread", 95, 5.01, 3, 0, 5000, 0, 3, "", "clear"},
		{"humidity below threshold", 94.9, 5, 3, 0, 999, 0, 3, "", "clear"},
		{"dry low visibility", 50, 20, 5, 0, 100, 0, 3, "", "clear"},
		{"missing humidity", 95, 5, 3, 0, 500, 0, 3, "humidity", "unknown"},
		{"missing rain", 95, 5, 3, 0, 500, 0, 3, "rain", "unknown"},
		{"missing wind", 95, 5, 3, 0, 5000, 0, 3, "wind", "unknown"},
		{"missing visibility can be favorable", 95, 5, 3, 0, 5000, 0, 3, "visibility", "favorable"},
		{"explicit code with missing inputs", 95, 5, 3, 0, 5000, 0, 45, "humidity", "fog"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := Hour{Humidity: &tt.humidity, Temperature: &tt.temperature, DewPoint: &tt.dew, Wind: &tt.wind, Visibility: &tt.visibility, Precipitation: &tt.rain, Code: &tt.code}
			switch tt.missing {
			case "humidity":
				h.Humidity = nil
			case "rain":
				h.Precipitation = nil
			case "wind":
				h.Wind = nil
			case "visibility":
				h.Visibility = nil
			}
			assert.Equal(t, tt.want, classify(h))
		})
	}
}
func TestModelResponse(t *testing.T) {
	tests := []struct {
		name, body string
		wantError  bool
		wantFog    string
	}{
		{"partial arrays", `{"latitude":52.52,"longitude":13.41,"hourly":{"time":[100,3700],"weather_code":[45,null]}}`, false, "fog"},
		{"no hours", `{"hourly":{"time":[]}}`, true, ""},
		{"unordered hours", `{"hourly":{"time":[100,99]}}`, true, ""},
		{"duplicate hours", `{"hourly":{"time":[100,100]}}`, true, ""},
		{"all missing values", `{"hourly":{"time":[100]}}`, false, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r modelResponse
			require.NoError(t, json.Unmarshal([]byte(tt.body), &r))
			f, err := r.forecast()
			if tt.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantFog, f.Hours[0].Fog)
			if len(f.Hours) > 1 {
				assert.Nil(t, f.Hours[1].Code)
				assert.Equal(t, "unknown", f.Hours[1].Fog)
			}
		})
	}
}
func TestProviderForecast(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		coords    []Coordinate
		wantError bool
	}{
		{"single", 200, `{"hourly":{"time":[100],"weather_code":[45]}}`, []Coordinate{{52.52, 13.41}}, false},
		{"batch", 200, `[{"hourly":{"time":[100]}},{"hourly":{"time":[100]}}]`, []Coordinate{{52.52, 13.41}, {48.05, 8.2}}, false},
		{"partial batch", 200, `[{"hourly":{"time":[100]}}]`, []Coordinate{{52.52, 13.41}, {48.05, 8.2}}, true},
		{"rate limited", 429, `{}`, []Coordinate{{52.52, 13.41}}, true},
		{"malformed", 200, `broken`, []Coordinate{{52.52, 13.41}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "icon_d2", r.URL.Query().Get("models"))
				assert.Equal(t, "48", r.URL.Query().Get("forecast_hours"))
				assert.Equal(t, "unixtime", r.URL.Query().Get("timeformat"))
				assert.Equal(t, "sunrise,sunset", r.URL.Query().Get("daily"))
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer upstream.Close()
			p := Provider{client: upstream.Client(), forecastURL: upstream.URL, budget: &Budget{}}
			f, err := p.forecasts(context.Background(), tt.coords, countries[0], true)
			if tt.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Len(t, f, len(tt.coords))
			}
		})
	}
}
func TestProviderTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer upstream.Close()
	p := Provider{client: &http.Client{Timeout: 20 * time.Millisecond}, forecastURL: upstream.URL, budget: &Budget{}}
	_, err := p.forecasts(context.Background(), []Coordinate{{52.52, 13.41}}, countries[0], false)
	require.Error(t, err)
}
func TestBudget(t *testing.T) {
	tests := []struct {
		name      string
		used      int
		day       string
		n         int
		wantError bool
	}{
		{"within limit", 8999, time.Now().UTC().Format("2006-01-02"), 1, false},
		{"limit exceeded", 9000, time.Now().UTC().Format("2006-01-02"), 1, true},
		{"new day", 9000, "2000-01-01", 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := Budget{used: tt.used, day: tt.day}
			err := b.wait(context.Background(), tt.n)
			if tt.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b := Budget{next: time.Now().Add(time.Hour)}
	require.ErrorIs(t, b.wait(ctx, 1), context.Canceled)
}
func TestDSTHours(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	require.NoError(t, err)
	tests := []struct {
		name  string
		start time.Time
		want  []string
	}{
		{"spring", time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC), []string{"01:00 CET", "03:00 CEST", "04:00 CEST"}},
		{"autumn", time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC), []string{"02:00 CEST", "02:00 CET", "03:00 CET"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := modelResponse{}
			for i := range 3 {
				r.Hourly.Time = append(r.Hourly.Time, tt.start.Add(time.Duration(i)*time.Hour).Unix())
			}
			f, err := r.forecast()
			require.NoError(t, err)
			for i, h := range f.Hours {
				assert.Equal(t, tt.want[i], time.Unix(h.Time, 0).In(berlin).Format("15:04 MST"))
			}
		})
	}
}
