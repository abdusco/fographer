package main

import (
	"context"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTurkeyGeography(t *testing.T) {
	g, err := geographyFor(countries[1])
	require.NoError(t, err)
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
func TestForecastModels(t *testing.T) {
	tests := []struct {
		index                 int
		name, model, timezone string
	}{
		{0, "Germany", "icon_d2", "Europe/Berlin"}, {1, "Turkey", "icon_eu", "Europe/Istanbul"}, {2, "Europe", "icon_seamless", "auto"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, tt.model, r.URL.Query().Get("models"))
				assert.Equal(t, tt.timezone, r.URL.Query().Get("timezone"))
				_, _ = w.Write([]byte(`{"hourly":{"time":[100],"weather_code":[45]}}`))
			}))
			defer upstream.Close()
			p := Provider{client: upstream.Client(), forecastURL: upstream.URL, budget: &Budget{}}
			forecasts, err := p.forecasts(context.Background(), []Coordinate{{39.93, 32.86}}, countries[tt.index], true)
			require.NoError(t, err)
			require.Len(t, forecasts, 1)
		})
	}
}
