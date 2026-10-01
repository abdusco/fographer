//go:build ignore

// Regenerate committed PWA icons with: go run tools/icons.go
package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

func main() {
	for _, size := range []int{192, 512} {
		img := image.NewRGBA(image.Rect(0, 0, size, size))
		bg := color.RGBA{24, 33, 31, 255}
		ink := color.RGBA{209, 229, 165, 255}
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				img.SetRGBA(x, y, bg)
			}
		}
		for _, line := range [][3]float64{{17, 47, 25}, {14, 41, 33}, {23, 50, 41}} {
			for y := 0; y < size; y++ {
				for x := 0; x < size; x++ {
					px, py := float64(x)*64/float64(size), float64(y)*64/float64(size)
					nearest := math.Max(line[0], math.Min(line[1], px))
					if math.Hypot(px-nearest, py-line[2]) <= 1.75 {
						img.SetRGBA(x, y, ink)
					}
				}
			}
		}
		name := "web/assets/icon-192.png"
		if size == 512 {
			name = "web/assets/icon-512.png"
		}
		f, err := os.Create(name)
		if err != nil {
			panic(err)
		}
		if err := png.Encode(f, img); err != nil {
			panic(err)
		}
		if err := f.Close(); err != nil {
			panic(err)
		}
	}
}
