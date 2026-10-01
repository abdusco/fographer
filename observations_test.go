package main

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeObservations(t *testing.T) {
	fixture := `{"features":[{"geometry":{"coordinates":[13.41,52.52,34]},"properties":{"data_000":{"shortname":"wigosLocalIdentifierCharacter","value":" 123 "},"data_001":{"shortname":"stationOrSiteName","value":" Berlin "},"data_002":{"shortname":"year","value":2026},"data_003":{"shortname":"month","value":10},"data_004":{"shortname":"day","value":1},"data_005":{"shortname":"hour","value":5},"data_006":{"shortname":"minute","value":0},"data_007":{"shortname":"horizontalVisibility","value":700,"unit":"m"},"data_008":{"shortname":"presentWeather","value":"FOG OR ICE FOG, SKY VISIBLE"},"data_009":{"shortname":"presentWeather","value":"RAIN"}}}]} `
	tests := []struct {
		name, body string
		count      int
		wantError  bool
	}{
		{"decoded by shortname", fixture, 1, false},
		{"empty", `{"features":[]}`, 0, false},
		{"invalid JSON", `not json`, 0, true},
		{"null visibility", strings.Replace(fixture, `"value":700`, `"value":null`, 1), 1, false},
		{"bad timestamp", strings.Replace(fixture, `"value":2026`, `"value":null`, 1), 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obs, err := decodeObservations(strings.NewReader(tt.body))
			if tt.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Len(t, obs, tt.count)
			if len(obs) > 0 {
				assert.Equal(t, "Berlin", obs[0].Name)
				assert.True(t, obs[0].Fog)
				assert.Equal(t, time.Date(2026, 10, 1, 5, 0, 0, 0, time.UTC).Unix(), obs[0].Time)
				if tt.name == "null visibility" {
					assert.Nil(t, obs[0].Visibility)
				} else {
					require.NotNil(t, obs[0].Visibility)
					assert.Equal(t, 700., *obs[0].Visibility)
				}
			}
		})
	}
}
func TestReportedFog(t *testing.T) {
	tests := []struct {
		weather string
		want    bool
	}{
		{"FOG OR ICE FOG, SKY VISIBLE", true}, {"FREEZING FOG", true}, {"FOG IN PATCHES", true},
		{"NO SIGNIFICANT PHENOMENON TO REPORT, PRESENT AND PAST WEATHER OMITTED", false},
		{"MIST", false}, {"RAIN", false}, {"FOG HAS DISSIPATED", false}, {"NO FOG", false},
	}
	for _, tt := range tests {
		t.Run(tt.weather, func(t *testing.T) { assert.Equal(t, tt.want, reportedFog(tt.weather)) })
	}
}
func TestMergeObservations(t *testing.T) {
	vis := 700.
	newerVis := 2000.
	tests := []struct {
		name                 string
		incoming             Observation
		visibility           float64
		visTime, weatherTime int64
		weather              string
	}{
		{"missing new visibility", Observation{ID: "a", Time: 200, Weather: "RAIN", WeatherTime: 200}, 700, 100, 200, "RAIN"},
		{"older report", Observation{ID: "a", Time: 50, Visibility: &newerVis, VisibilityTime: 50, Weather: "RAIN", WeatherTime: 50}, 700, 100, 100, "FOG"},
		{"new visibility missing weather", Observation{ID: "a", Time: 200, Visibility: &newerVis, VisibilityTime: 200}, 2000, 200, 100, "FOG"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old := Observation{ID: "a", Time: 100, Visibility: &vis, VisibilityTime: 100, Weather: "FOG", WeatherTime: 100, Fog: true}
			got := mergeObservations([]Observation{old}, []Observation{tt.incoming})
			require.Len(t, got, 1)
			require.NotNil(t, got[0].Visibility)
			assert.Equal(t, tt.visibility, *got[0].Visibility)
			assert.Equal(t, tt.visTime, got[0].VisibilityTime)
			assert.Equal(t, tt.weatherTime, got[0].WeatherTime)
			assert.Equal(t, tt.weather, got[0].Weather)
		})
	}
}
func TestFreshObservations(t *testing.T) {
	now := time.Date(2026, 10, 1, 6, 0, 0, 0, time.UTC)
	vis := 500.
	tests := []struct {
		name      string
		age       int64
		wantCount int
		stale     bool
	}{
		{"fresh", 60, 1, false}, {"ninety minutes", 5400, 1, false}, {"stale", 5401, 1, true}, {"three hours", 10800, 1, true}, {"expired", 10801, 0, false}, {"future clock error", -301, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			when := now.Unix() - tt.age
			got := freshObservations([]Observation{{ID: "a", Time: when, Visibility: &vis, VisibilityTime: when, WeatherTime: when, Weather: "FOG", Fog: true}}, now)
			require.Len(t, got, tt.wantCount)
			if len(got) > 0 {
				assert.Equal(t, tt.stale, got[0].Stale)
			}
		})
	}
	got := freshObservations([]Observation{{ID: "a", Visibility: &vis, VisibilityTime: now.Unix() - 10801, Weather: "RAIN", WeatherTime: now.Unix() - 60}}, now)
	require.Len(t, got, 1)
	assert.Nil(t, got[0].Visibility)
	assert.Equal(t, "RAIN", got[0].Weather)
}
func TestReportFiles(t *testing.T) {
	now := time.Date(2026, 10, 1, 6, 0, 0, 0, time.UTC)
	body := `<a href="Z__C_EDZW_20261001050000_bda01%2Csynop.geojson.gz">report</a><a href="Z__C_EDZW_20261001020000_bda01%2Csynop.geojson.gz">old</a><a href="../evil.geojson.gz">bad</a><a href="Z__C_EDZW_20261001070000_bda01%2Csynop.geojson.gz">future</a>`
	tests := []struct {
		name string
		seen map[string]bool
		want []string
	}{
		{"recent only", map[string]bool{}, []string{"Z__C_EDZW_20261001050000_bda01,synop.geojson.gz"}},
		{"skip processed", map[string]bool{"Z__C_EDZW_20261001050000_bda01,synop.geojson.gz": true}, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { assert.Equal(t, tt.want, reportFiles(body, now, tt.seen)) })
	}
}
