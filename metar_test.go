package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMETARVisibility(t *testing.T) {
	tests := []struct {
		name      string
		value     any
		metres    float64
		qualifier string
		missing   bool
	}{
		{"numeric miles", 4.97, 4.97 * 1609.344, "", false}, {"lower bound", "6+", 6 * 1609.344, "atLeast", false},
		{"less than quarter mile", "M1/4", .25 * 1609.344, "below", false}, {"fraction", "1/2", .5 * 1609.344, "", false},
		{"missing", nil, 0, "", true}, {"unreadable", "unknown", 0, "", true}, {"negative", -1., 0, "", true}, {"zero denominator", "1/0", 0, "", true}, {"NaN", "NaN", 0, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, qualifier := metarVisibility(tt.value)
			if tt.missing {
				assert.Nil(t, v)
				return
			}
			require.NotNil(t, v)
			assert.InDelta(t, tt.metres, *v, .001)
			assert.Equal(t, tt.qualifier, qualifier)
		})
	}
}
func TestMETARFog(t *testing.T) {
	tests := []struct {
		weather string
		want    bool
	}{{"FG", true}, {"FZFG", true}, {"-RA BCFG", true}, {"VCFG", true}, {"MIFG", true}, {"BR", false}, {"-TSRA", false}, {"", false}, {"NOTFG", false}}
	for _, tt := range tests {
		t.Run(tt.weather, func(t *testing.T) { assert.Equal(t, tt.want, metarFog(tt.weather)) })
	}
}
func TestDecodeMETARs(t *testing.T) {
	fixture := `[{"icaoId":"LTAC","name":"Ankara","lat":40.12,"lon":32.99,"obsTime":200,"visib":"6+","wxString":""},{"icaoId":"LTAC","name":"Ankara","lat":40.12,"lon":32.99,"obsTime":100,"visib":0.2,"wxString":"FG"},{"icaoId":"LTFM","name":"Istanbul","lat":41.28,"lon":28.74,"obsTime":200,"visib":0.2,"wxString":"FZFG"},{"icaoId":"LGAV","obsTime":200,"visib":6}]`
	tests := []struct {
		name, body string
		count      int
		wantError  bool
	}{{"latest Turkish airports", fixture, 2, false}, {"empty", `[]`, 0, false}, {"malformed", `invalid`, 0, true}, {"no timestamp", `[{"icaoId":"LTAC","visib":6}]`, 0, false}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obs, err := decodeMETARs(strings.NewReader(tt.body))
			if tt.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Len(t, obs, tt.count)
			if tt.count == 2 {
				assert.Equal(t, "LTAC", obs[0].ID)
				assert.Equal(t, int64(200), obs[0].VisibilityTime)
				assert.False(t, obs[0].Fog)
				assert.Equal(t, "atLeast", obs[0].VisibilityQualifier)
				assert.True(t, obs[1].Fog)
			}
		})
	}
}
func TestTurkeyObservationRefresh(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		wantError bool
	}{{"success", 200, false}, {"empty provider", 204, false}, {"rate limited", 429, true}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "35.5,25.5,42.5,45", r.URL.Query().Get("bbox"))
				assert.Equal(t, "3", r.URL.Query().Get("hours"))
				assert.NotEmpty(t, r.UserAgent())
				w.WriteHeader(tt.status)
				if tt.status == 200 {
					_, _ = fmt.Fprintf(w, `[{"icaoId":"LTAC","name":"Ankara","lat":40.12,"lon":32.99,"obsTime":%d,"visib":0.4,"wxString":"FG"}]`, time.Now().Unix())
				}
			}))
			defer upstream.Close()
			cache, err := newCache(t.TempDir())
			require.NoError(t, err)
			s := Server{config: Config{MetarURL: upstream.URL}, cache: cache, provider: &Provider{client: upstream.Client()}}
			err = s.refreshTurkeyObservations(context.Background())
			if tt.wantError {
				require.Error(t, err)
				_, ok := cache.lookup("observations:TR")
				assert.False(t, ok)
			} else {
				require.NoError(t, err)
				_, ok := cache.lookup("observations:TR")
				assert.True(t, ok)
			}
		})
	}
}
