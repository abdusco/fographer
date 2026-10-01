package main

import (
	"testing"

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
