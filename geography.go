package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"github.com/peterstace/simplefeatures/geom"
	"math"
)

//go:embed datafiles/germany.geojson
var germanyJSON []byte

type Cell struct {
	Coordinate
	Geometry json.RawMessage `json:"geometry"`
}
type Geography struct {
	boundary geom.Geometry
	cells    []Cell
}

func newGeography() (*Geography, error) {
	boundary, err := geom.UnmarshalGeoJSON(germanyJSON)
	if err != nil {
		return nil, err
	}
	g := &Geography{boundary: boundary}
	for y := 47.1; y < 55.2; y += .3 {
		for x := 5.7; x < 15.3; x += .3 {
			box, err := geom.UnmarshalWKT(fmt.Sprintf("POLYGON((%f %f,%f %f,%f %f,%f %f,%f %f))", x, y, x+.3, y, x+.3, y+.3, x, y+.3, x, y))
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
			point, err := geom.UnmarshalWKT(fmt.Sprintf("POINT(%f %f)", x+.15, y+.15))
			if err != nil {
				return nil, err
			}
			inside, err := geom.Contains(clipped, point)
			if err != nil {
				return nil, err
			}
			lat, lon := y+.15, x+.15
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
	if math.IsNaN(lat) || math.IsNaN(lon) || math.IsInf(lat, 0) || math.IsInf(lon, 0) || lat < 47 || lat > 56 || lon < 5 || lon > 16 {
		return false
	}
	p, err := geom.UnmarshalWKT(fmt.Sprintf("POINT(%.8f %.8f)", lon, lat))
	if err != nil {
		return false
	}
	ok, err := geom.Covers(g.boundary, p)
	return err == nil && ok
}
