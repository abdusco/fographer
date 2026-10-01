# Fographer

A fog map for photographers across Europe, including Turkey. Explore current model estimates, 48-hour forecasts, station observations, and sunrise times. Save viewpoints in your browser.

## Run

Requires Go 1.27 or newer.

```sh
go run .
```

Open http://localhost:8080. The Europe overview loads in the background; you can select a viewpoint immediately.

```sh
PORT=3000 go run .          # Listen on all interfaces
DEBUG=1 go run .            # Read frontend assets from web/
go build -o fographer .     # Standalone binary with embedded assets
```

`ADDR` overrides `PORT`. `DATA_DIR` defaults to `data/` and stores weather snapshots. See [.env.example](.env.example) for all settings; it is not loaded automatically.

## Use

Click the map or search for a place. Forecast models and local timezones are selected automatically. Zoom in for a finer fog overlay, use the timeline to explore forecasts, and tap ☆ to save a spot. The compass button resets the map angle.

“Now” shading is a model estimate. Station markers show separate measurements, available only in Now. Airport coverage is sparse between stations.

- **Predicted fog:** model weather code 45/48, or visibility below 1 km with humidity ≥95% and rain below 0.2 mm/h.
- **Favorable conditions:** humidity ≥95%, temperature within 2°C of dew point, wind ≤10 km/h, and rain below 0.2 mm/h.
- Missing values appear as —. The rules are heuristics, not fog probabilities.

The map covers European land, including Iceland, Turkey, Cyprus, the Caucasus, and western Russia up to 60° E. Overview cells are sampled areas, not model resolution or a guarantee of fog throughout the cell.

Outside debug mode, the PWA caches the app and previously loaded forecasts for offline use. Basemap tiles need a connection. Hosting requires HTTPS for offline support and geolocation.

## Docker

```sh
docker build -t fographer .
docker run --rm -p 8080:8080 -v fographer-data:/data fographer
```

To run a published image behind Caddy:

```sh
docker compose pull
docker compose up -d
```

Compose binds to `127.0.0.1:8080` and keeps weather snapshots in a named volume. Set `FOGRAPHER_VERSION=v1.0.0` to pin a release or `PORT=3000` to change the host port. Point Caddy's `reverse_proxy` at that port.

## Release

Push a version tag to run checks, publish AMD64/ARM64 images to `ghcr.io/abdusco/fographer`, and create a GitHub release with generated notes:

```sh
git tag v1.0.0
git push origin v1.0.0
```

Images use the exact tag (`:v1.0.0`). Stable tags also update `:latest`; tags containing a hyphen, such as `v1.0.0-rc.1`, create prereleases without updating `:latest`. Publishing uses the built-in `GITHUB_TOKEN`. For anonymous pulls, set the GHCR package visibility to public after its first release.

## Check

```sh
go test -race ./...
go vet ./...
```

## Sources

Map data © [OpenStreetMap contributors](https://www.openstreetmap.org/copyright). Forecasts © DWD via [Open-Meteo](https://open-meteo.com/en/docs/dwd-api), CC BY 4.0; fog classifications are derived. Observations come from [DWD](https://opendata.dwd.de/weather/weather_reports/synoptic/germany/geojson/) and [NOAA Aviation Weather Center](https://aviationweather.gov/data/api/). Boundaries are public-domain [Natural Earth](https://www.naturalearthdata.com/about/terms-of-use/) data.

Vendored library and font licenses are in `web/vendor/` and `web/fonts/`.
