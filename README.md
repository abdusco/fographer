# Fographer

A fog map for photographers across Europe, including Turkey. Discover ground and valley fog, inspect the next 48 hours at a viewpoint, and save places worth returning to.

Go + Echo v5, Alpine.js, and MapLibre GL JS. Browser libraries and label fonts are vendored. There is no frontend build step, Node requirement, map account, or API key.

## Run

Requires Go 1.25 or newer and an internet connection for weather and map tiles.

```sh
go run .
```

Open **http://localhost:8080**. The Europe overview warms up in the background, usually within a few minutes. You can select a spot while they load. Observation ingestion also runs independently. Provider failures are shown in the interface and server logs.

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

- Click the map, search anywhere in Europe, or choose a suggested viewpoint. Forecast sources are selected automatically; there is no country picker.
- Switch between Now and Forecast. The map uses UTC until you select a viewpoint; then the timeline uses that location’s timezone, regardless of your device timezone.
- Use the compass button beside the zoom controls to reset bearing, tilt, and roll to a flat, north-up view.
- Inspect visibility, wind, humidity, precipitation, and sunrise/sunset. Sunrise shortcuts select the containing forecast hour.
- Tap the star to save a spot. Use the pencil to rename it and × to delete it. Choosing a favorite moves the map to its location. Existing German and Turkish favorites remain supported. Favorites stay in this browser and do not sync between devices.
- Station markers appear only in Now. Their measurements describe the station, not the entire surrounding area.
- On phones, use the bottom-sheet handle to expand or collapse location details.

## Weather interpretation

The shaded map is always a **model estimate or forecast**, including Now. It uses DWD models through Open-Meteo. The overview is clipped to European land boundaries; selected viewpoints request their own local model forecast.

| Country | Forecast model | Native model resolution | Overview grid | Local timezone | Observations |
| --- | --- | --- | --- | --- | --- |
| Germany viewpoints | ICON D2 | Approximately 2 km | Adaptive | Europe/Berlin | DWD stations + airport METARs |
| Turkey viewpoints | ICON EU | Approximately 7 km | Adaptive | Europe/Istanbul | Airport METARs via NOAA AWC |
| Other European viewpoints | ICON Seamless | Approximately 2–11 km, by model coverage | Adaptive | Automatic from coordinates | Airport METARs via NOAA AWC |

The background continent overview samples every 2°. At zoom level 5 and above, the visible map requests a finer grid, starting at 0.25° and increasing spacing until there are at most 64 samples. Grid bounds are snapped for cache reuse. This spacing does not change point forecasts or imply that fog fills an entire cell.

Coverage includes European land from Iceland to western Russia (60° E), north to Svalbard (82° N), plus Turkey, Cyprus, and the Caucasus. Overseas territories outside this region are excluded. Natural Earth boundaries are approximate; coastal viewpoints may occasionally fall outside the land mask. ICON Seamless chooses regional or global forecasts automatically. Visibility is unavailable in ICON Global; it appears as —, while other available inputs still inform the fog rules.

**Predicted fog:** weather code 45 or 48, or visibility below 1 km with humidity ≥95% and precipitation below 0.2 mm/hour.

**Favorable conditions:** humidity ≥95%, temperature–dew-point spread ≤2°C, wind ≤10 km/h, and precipitation below 0.2 mm/hour. This is an explanatory heuristic, not a calibrated probability. Low clouds or poor visibility alone do not establish fog.

Missing values appear as unknown or —. Predictions cannot resolve every forest edge, river bend, or sheltered valley. There is no cloud-inversion or above-the-clouds forecasting in this version.

German observations are read from DWD's compressed SYNOP GeoJSON reports. Europe uses NOAA's bulk METAR CSV cache, filtered by European land boundaries. The bulk feed avoids the query API's 400-report cap. Airport coverage is sparse between airports and does not confirm conditions in a distant valley. METAR visibility is converted from statute miles to metres; bounds such as `6+` or `M1/4` remain visible as ≥ or <.

Relevant fields retain their own measurement times when a newer report omits them. Markers fade after 90 minutes; measurements older than three hours are removed. Reported fog and measured low visibility have separate labels.

The continent overview refreshes every three hours; viewport overlays cache for three hours, point forecasts for one hour, and place search for 24 hours. Observations poll every ten minutes. Germany and Turkey’s existing point cache keys are retained. Fresh overviews survive a server restart without an unnecessary refetch. Refreshes share a per-location request budget, limited to 400 location calls/minute and 9,000/day. The budget persists across restarts. This app defaults to personal, noncommercial use of Open-Meteo's public API.

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
| `GET /api/config` | Tile URL and Europe coverage metadata, boundaries, models |
| `GET /api/health` | Process health and Europe feed readiness |
| `GET /api/overview` | Europe GeoJSON cells, hourly timestamps, and fog states |
| `GET /api/overview?bbox=13,52,14,53` | Adaptive viewport overlay; bbox is west,south,east,north |
| `GET /api/observations` | Fresh European observations and field timestamps |
| `GET /api/forecast?lat=39.93&lon=32.86` | Automatic point forecast, local timezone, and solar times |
| `GET /api/search?q=Ankara` | Place search restricted to European coverage |

The app no longer sends a country parameter. Point forecasts detect Germany and Turkey from coordinates, choosing their existing model and cache; other European points use ICON Seamless with automatic timezone selection. Coordinates must be finite and within the bundled European land mask. A legacy `country=DE`, `TR`, or `EU` parameter can restrict requests explicitly. Legacy country overview snapshots are retained but only the European overview is warmed in the background. Unsupported area codes and bad input return 400; unavailable data without a cached fallback returns 503 with a `Retry-After` header. Unknown API endpoints return 404. Offline responses preserve original retrieval timestamps.

## Verify

```sh
go test -race ./...
go vet ./...
```

Tests use Testify table cases and fixture HTTP providers. They cover fog thresholds, missing fields, partial responses, timeouts, rate limiting, atomic snapshots, shared cache refreshes, stale fallback, station decoding/merging, both country boundaries, model/timezone selection, country search filtering, METAR visibility and fog parsing, country cache isolation, routes, and daylight-saving transitions.

## Vendored assets and attribution

| Asset | Version / source | License |
| --- | --- | --- |
| Alpine.js | 3.17.4, unpkg CDN distribution | MIT; `web/vendor/alpine-LICENSE.md` |
| MapLibre GL JS | 5.21.0, unpkg browser distribution | BSD-3-Clause; `web/vendor/maplibre-LICENSE.txt` |
| Open Sans Semibold glyphs | MapLibre demo font files, Latin ranges 0–1023 | SIL OFL; `web/fonts/OFL.txt` |
| Germany boundary | Natural Earth 1:50m admin-0, `nvkelso/natural-earth-vector` GeoJSON | Public domain |
| Turkey boundary | Natural Earth 1:10m admin-0, `nvkelso/natural-earth-vector` GeoJSON | Public domain |
| Europe boundary | Union of Natural Earth 1:50m admin-0 European countries, Turkey, Cyprus, and the Caucasus, clipped to the coverage bounds | Public domain |

MapLibre 5 is deliberately used for its single-file browser distribution. Glyphs are local; the style needs no sprites. Some non-Latin place-name glyphs outside the bundled ranges may be unavailable.

- Map data © [OpenStreetMap contributors](https://www.openstreetmap.org/copyright); [vector tile usage policy](https://operations.osmfoundation.org/policies/vector/).
- Forecasts © DWD via [Open-Meteo](https://open-meteo.com/en/docs/dwd-api), CC BY 4.0; fog classifications are app-derived modifications.
- Observations © [DWD Open Data](https://opendata.dwd.de/weather/weather_reports/synoptic/germany/geojson/).
- European airport observations via [NOAA Aviation Weather Center](https://aviationweather.gov/data/api/).
- Country boundaries © [Natural Earth](https://www.naturalearthdata.com/about/terms-of-use/).

PWA icons are committed PNG files. Regenerate them, if needed, with `go run tools/icons.go`. When changing cached frontend assets, increment the service worker version in `web/sw.js`.
