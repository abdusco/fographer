# Fographer

A fog map for photographers in Germany. Discover ground and valley fog, inspect the next 48 hours at a viewpoint, and save places worth returning to.

Go + Echo v5, Alpine.js, and MapLibre GL JS. Browser libraries and label fonts are vendored. There is no frontend build step, Node requirement, map account, or API key.

## Run

Requires Go 1.25 or newer and an internet connection for weather and map tiles.

```sh
go run .
```

Open **http://localhost:8080**. The Germany overview warms up in the background, usually within a few minutes. You can select a spot while it loads. Station ingestion also runs independently. Provider failures are shown in the interface and server logs.

Set `PORT` to choose a port and listen on all interfaces:

```sh
PORT=3000 go run .
```

An explicit `ADDR` overrides `PORT`, for example `ADDR=127.0.0.1:3000 go run .`. With neither set, the app listens on `127.0.0.1:8080`. The Docker image sets `PORT=8080`; override it with `-e PORT=3000 -p 3000:3000`.

For a standalone binary:

```sh
go build -o fographer .
./fographer
```

Assets are embedded in the binary. Runtime snapshots live in `data/` and survive restarts. Configuration comes from environment variables listed in `.env.example`; that file is documentation, not automatically loaded.

During development, run from the project directory with `DEBUG=1` to serve frontend assets directly from `web/`:

```sh
DEBUG=1 PORT=3000 go run .
```

Edit CSS, JavaScript, or HTML and refresh the browser; no rebuild is needed for asset changes. Debug mode disables asset caching and retires the app's service worker. Weather caching still works on the backend. With `DEBUG` unset or any value other than `1`, assets are served from the binary and PWA caching is enabled.

## Use

- Click the map, search a German place, or choose one of the suggested regions.
- Switch between Now and Forecast. The timeline displays Berlin time, regardless of your device timezone.
- Use the compass button beside the zoom controls to reset bearing, tilt, and roll to a flat, north-up view.
- Inspect visibility, wind, humidity, precipitation, and sunrise/sunset. Sunrise shortcuts select the containing forecast hour.
- Tap the star to save a spot. Use the pencil to rename it and × to delete it. Favorites stay in this browser and do not sync between devices.
- Station markers appear only in Now. Their measurements describe the station, not the entire surrounding area.
- On phones, use the bottom-sheet handle to expand or collapse location details.

## Weather interpretation

The shaded map is always a **model estimate or forecast**, including Now. It uses DWD ICON D2 through Open-Meteo. The Germany overview samples a 0.3° grid and clips each displayed cell to the Germany boundary. Selected viewpoints request their own local model forecast, with approximately 2 km native model resolution.

**Predicted fog:** weather code 45 or 48, or visibility below 1 km with humidity ≥95% and precipitation below 0.2 mm/hour.

**Favorable conditions:** humidity ≥95%, temperature–dew-point spread ≤2°C, wind ≤10 km/h, and precipitation below 0.2 mm/hour. This is an explanatory heuristic, not a calibrated probability. Low clouds or poor visibility alone do not establish fog.

Missing values appear as unknown or —. Predictions cannot resolve every forest edge, river bend, or sheltered valley. There is no cloud-inversion or above-the-clouds forecasting in this version.

DWD observations are read from compressed SYNOP GeoJSON reports. Relevant fields retain their own measurement times when a newer report omits them. Markers fade after 90 minutes; measurements older than three hours are removed. Reported fog and measured low visibility have separate labels.

The overview refreshes every three hours, point forecasts cache for one hour, station reports poll every ten minutes, and place search caches for 24 hours. Refreshes share a per-location request budget, limited to 400 location calls/minute and 9,000/day. The budget persists across restarts. This app defaults to personal, noncommercial use of Open-Meteo's public API.

## Install and offline use

The PWA works on localhost for development. A hosted installation needs **HTTPS** for service workers and geolocation. Use a reverse proxy for TLS; the Go server serves HTTP.

After the first successful visit, the service worker caches the app shell and up to 40 successful API responses. Saved spots and previously loaded weather remain available offline. The last selected spot is reopened automatically. Cached weather displays its age and an offline/stale label; an uncached spot displays an unavailable message.

Basemap tiles are never added to the service-worker cache or downloaded for offline use. The public OSM servers allow interactive viewing, require attribution and normal browser caching, and provide no availability guarantee. The local map style targets Shortbread v1; a replacement tile source must use that schema or come with a matching style.

## Docker

```sh
docker build -t fographer .
docker run --rm -p 8080:8080 -v fographer-data:/data fographer
```

The container runs as a non-root user. Bind-mounting a host data directory requires granting UID 10001 write access. Actual hosting and authentication are not included; the default local listener binds to loopback.

## API

All endpoints are read-only. Weather responses have `data`, `source`, `fetchedAt` (Unix seconds), `stale`, and units; cached fallbacks can also include `warning`.

| Endpoint | Purpose |
| --- | --- |
| `GET /api/config` | Tile URL, Berlin timezone, Germany boundary |
| `GET /api/health` | Process health and feed readiness |
| `GET /api/overview` | GeoJSON cells, hourly timestamps, and fog states |
| `GET /api/observations` | Latest relevant station measurements and field timestamps |
| `GET /api/forecast?lat=52.52&lon=13.41` | Hourly point forecast and solar times |
| `GET /api/search?q=Berlin` | German place search |

Coordinates must be finite and within the bundled Germany polygon. Bad input returns 400; unavailable data without a cached fallback returns 503 with a `Retry-After` header. Unknown API endpoints return 404. Offline responses preserve original retrieval timestamps.

## Verify

```sh
go test -race ./...
go vet ./...
```

Tests use Testify table cases and fixture HTTP providers. They cover fog thresholds, missing fields, partial responses, timeouts, rate limiting, atomic snapshots, shared cache refreshes, stale fallback, station decoding/merging, geography, routes, and daylight-saving transitions.

## Vendored assets and attribution

| Asset | Version / source | License |
| --- | --- | --- |
| Alpine.js | 3.17.4, unpkg CDN distribution | MIT; `web/vendor/alpine-LICENSE.md` |
| MapLibre GL JS | 5.21.0, unpkg browser distribution | BSD-3-Clause; `web/vendor/maplibre-LICENSE.txt` |
| Open Sans Semibold glyphs | MapLibre demo font files, Latin ranges 0–1023 | SIL OFL; `web/fonts/OFL.txt` |
| Germany boundary | Natural Earth 1:50m admin-0, `nvkelso/natural-earth-vector` GeoJSON | Public domain |

MapLibre 5 is deliberately used for its single-file browser distribution. Glyphs are local; the style needs no sprites. Some non-Latin place-name glyphs outside the bundled ranges may be unavailable.

- Map data © [OpenStreetMap contributors](https://www.openstreetmap.org/copyright); [vector tile usage policy](https://operations.osmfoundation.org/policies/vector/).
- Forecasts © DWD via [Open-Meteo](https://open-meteo.com/en/docs/dwd-api), CC BY 4.0; fog classifications are app-derived modifications.
- Observations © [DWD Open Data](https://opendata.dwd.de/weather/weather_reports/synoptic/germany/geojson/).
- Germany boundary © [Natural Earth](https://www.naturalearthdata.com/about/terms-of-use/).

PWA icons are committed PNG files. Regenerate them, if needed, with `go run tools/icons.go`. When changing cached frontend assets, increment the service worker version in `web/sw.js`.
