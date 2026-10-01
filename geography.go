package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"

	"github.com/peterstace/simplefeatures/geom"
)

//go:embed datafiles/germany.geojson
var germanyJSON []byte

//go:embed datafiles/turkey.geojson
var turkeyJSON []byte

type Country struct {
	Code              string          `json:"code"`
	Name              string          `json:"name"`
	Timezone          string          `json:"timezone"`
	TimeLabel         string          `json:"timeLabel"`
	Model             string          `json:"model"`
	ModelName         string          `json:"modelName"`
	Resolution        string          `json:"resolution"`
	ObservationSource string          `json:"observationSource"`
	Spacing           float64         `json:"spacingDegrees"`
	Bounds            [4]float64      `json:"bounds"`
	Center            [2]float64      `json:"center"`
	Boundary          json.RawMessage `json:"boundary"`
}

var countries = []Country{
	{Code: "DE", Name: "Germany", Timezone: "Europe/Berlin", TimeLabel: "Berlin time", Model: "icon_d2", ModelName: "DWD ICON D2", Resolution: "2 km", ObservationSource: "DWD station observations", Spacing: .3, Bounds: [4]float64{5.7, 47.1, 15.3, 55.2}, Center: [2]float64{10.4, 51.1}, Boundary: germanyJSON},
	{Code: "TR", Name: "Turkey", Timezone: "Europe/Istanbul", TimeLabel: "Istanbul time", Model: "icon_eu", ModelName: "DWD ICON EU", Resolution: "7 km", ObservationSource: "Airport METAR observations via NOAA Aviation Weather Center", Spacing: .5, Bounds: [4]float64{25.5, 35.5, 45, 42.5}, Center: [2]float64{35, 39}, Boundary: turkeyJSON},
}

func countryByCode(code string) (Country, bool) {
	for _, country := range countries {
		if country.Code == code {
			return country, true
		}
	}
	return Country{}, false
}

type Cell struct {
	Coordinate
	Geometry json.RawMessage `json:"geometry"`
}
type Geography struct {
	boundary geom.Geometry
	cells    []Cell
	bounds   [4]float64
}

func newGeography() (*Geography, error) {
	return geographyFor(countries[0])
}

func geographyFor(country Country) (*Geography, error) {
	boundary, err := geom.UnmarshalGeoJSON(country.Boundary)
	if err != nil {
		return nil, err
	}
	g := &Geography{boundary: boundary, bounds: country.Bounds}
	step := country.Spacing
	for y := country.Bounds[1]; y < country.Bounds[3]; y += step {
		for x := country.Bounds[0]; x < country.Bounds[2]; x += step {
			box, err := geom.UnmarshalWKT(fmt.Sprintf("POLYGON((%f %f,%f %f,%f %f,%f %f,%f %f))", x, y, x+step, y, x+step, y+step, x, y+step, x, y))
			if err != nil {
				return nil, err
			}
			clipped, err := geom.Intersection(boundary, box)
			if err != nil {
				return nil, err
			}
			if clipped.IsEmpty() || clipped.Area() == 0 {
				continue
			}
			point, err := geom.UnmarshalWKT(fmt.Sprintf("POINT(%f %f)", x+step/2, y+step/2))
			if err != nil {
				return nil, err
			}
			inside, err := geom.Contains(clipped, point)
			if err != nil {
				return nil, err
			}
			lat, lon := y+step/2, x+step/2
			if !inside {
				c, ok := clipped.PointOnSurface().Coordinates()
				if !ok {
					return nil, fmt.Errorf("grid cell has no sample point")
				}
				lat, lon = c.Y, c.X
			}
			b, err := clipped.MarshalJSON()
			if err != nil {
				return nil, err
			}
			g.cells = append(g.cells, Cell{Coordinate{lat, lon}, b})
		}
	}
	return g, nil
}
func (g *Geography) contains(lat, lon float64) bool {
	if math.IsNaN(lat) || math.IsNaN(lon) || math.IsInf(lat, 0) || math.IsInf(lon, 0) || lat < g.bounds[1] || lat > g.bounds[3] || lon < g.bounds[0] || lon > g.bounds[2] {
		return false
	}
	p, err := geom.UnmarshalWKT(fmt.Sprintf("POINT(%.8f %.8f)", lon, lat))
	if err != nil {
		return false
	}
	ok, err := geom.Covers(g.boundary, p)
	return err == nil && ok
}
