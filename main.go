package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

//go:embed web
var webFiles embed.FS

type Config struct {
	Addr, DataDir, TileURL, ForecastURL, SearchURL, ObservationsURL string
	Debug                                                           bool
}

func env(key, fallback string) string {
	if s := os.Getenv(key); s != "" {
		return s
	}
	return fallback
}
func configuration() Config {
	addr := "127.0.0.1:8080"
	if port := os.Getenv("PORT"); port != "" {
		addr = net.JoinHostPort("0.0.0.0", port)
	}
	return Config{Addr: env("ADDR", addr), Debug: os.Getenv("DEBUG") == "1", DataDir: env("DATA_DIR", "data"), TileURL: env("TILE_URL", "https://vector.openstreetmap.org/shortbread_v1/{z}/{x}/{y}.mvt"), ForecastURL: env("FORECAST_URL", "https://api.open-meteo.com/v1/dwd-icon"), SearchURL: env("SEARCH_URL", "https://geocoding-api.open-meteo.com/v1/search"), ObservationsURL: env("OBSERVATIONS_URL", "https://opendata.dwd.de/weather/weather_reports/synoptic/germany/geojson/")}
}

type Server struct {
	config      Config
	cache       *Cache
	geography   *Geography
	provider    *Provider
	logger      *slog.Logger
	seenReports map[string]bool
}

func newServer(cfg Config) (*Server, error) {
	cache, err := newCache(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	g, err := newGeography()
	if err != nil {
		return nil, err
	}
	budget := &Budget{}
	var saved struct {
		Day  string
		Used int
	}
	if b, err := os.ReadFile(filepath.Join(cfg.DataDir, "budget.snapshot")); err == nil {
		_ = json.Unmarshal(b, &saved)
		budget.day = saved.Day
		budget.used = saved.Used
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	budget.persist = func(day string, used int) {
		b, _ := json.Marshal(struct {
			Day  string
			Used int
		}{day, used})
		if err := atomicWrite(filepath.Join(cfg.DataDir, "budget.snapshot"), b); err != nil {
			logger.Warn("request budget snapshot failed", "error", err)
		}
	}
	return &Server{config: cfg, cache: cache, geography: g, provider: &Provider{client: &http.Client{Timeout: 25 * time.Second}, forecastURL: cfg.ForecastURL, searchURL: cfg.SearchURL, budget: budget}, logger: logger, seenReports: map[string]bool{}}, nil
}

type Feature struct {
	Type       string          `json:"type"`
	Geometry   json.RawMessage `json:"geometry"`
	Properties any             `json:"properties"`
}
type Overview struct {
	Type     string    `json:"type"`
	Times    []int64   `json:"times"`
	Features []Feature `json:"features"`
	Spacing  float64   `json:"spacingDegrees"`
}

func (s *Server) refreshOverview(ctx context.Context) error {
	all := []Forecast{}
	for start := 0; start < len(s.geography.cells); start += 40 {
		end := min(start+40, len(s.geography.cells))
		coords := []Coordinate{}
		for _, c := range s.geography.cells[start:end] {
			coords = append(coords, c.Coordinate)
		}
		forecasts, err := s.provider.forecasts(ctx, coords, false)
		if err != nil {
			return err
		}
		all = append(all, forecasts...)
	}
	if len(all) == 0 {
		return errors.New("no overview grid cells")
	}
	result := Overview{Type: "FeatureCollection", Times: []int64{}, Features: []Feature{}, Spacing: .3}
	for _, h := range all[0].Hours {
		result.Times = append(result.Times, h.Time)
	}
	for i, f := range all {
		byTime := map[int64]string{}
		for _, h := range f.Hours {
			byTime[h.Time] = h.Fog
		}
		states := []string{}
		for _, t := range result.Times {
			state, ok := byTime[t]
			if !ok {
				state = "unknown"
			}
			states = append(states, state)
		}
		result.Features = append(result.Features, Feature{Type: "Feature", Geometry: s.geography.cells[i].Geometry, Properties: struct {
			ID     int      `json:"id"`
			States []string `json:"states"`
		}{i, states}})
	}
	return s.cache.put("overview", result)
}
func (s *Server) workers(ctx context.Context) {
	go func() {
		for {
			if err := s.refreshOverview(ctx); err != nil && ctx.Err() == nil {
				s.logger.Warn("forecast overview refresh failed", "error", err)
			}
			delay := 3 * time.Hour
			if _, ok := s.cache.lookup("overview"); !ok {
				delay = 5 * time.Minute
			}
			t := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
		}
	}()
	go func() {
		for {
			if err := s.refreshObservations(ctx); err != nil && ctx.Err() == nil {
				s.logger.Warn("station refresh failed", "error", err)
			}
			t := time.NewTimer(10 * time.Minute)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
		}
	}()
}

var units = map[string]string{"temperature": "°C", "dewPoint": "°C", "humidity": "%", "visibility": "m", "wind": "km/h", "precipitation": "mm/hour", "lowCloud": "%", "time": "Unix seconds"}

func unavailable(c *echo.Context, message string) error {
	c.Response().Header().Set("Retry-After", "30")
	return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": message})
}
func (s *Server) routes() *echo.Echo {
	e := echo.New()
	e.Use(middleware.Recover(), middleware.Gzip())
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			c.Response().Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
			c.Response().Header().Set("X-Content-Type-Options", "nosniff")
			if s.config.Debug || strings.HasPrefix(c.Request().URL.Path, "/api/") {
				c.Response().Header().Set("Cache-Control", "no-store")
			} else if c.Request().URL.Path == "/sw.js" {
				c.Response().Header().Set("Cache-Control", "no-cache")
			}
			return next(c)
		}
	})
	e.GET("/api/config", func(c *echo.Context) error {
		return c.JSON(200, map[string]any{"tileURL": s.config.TileURL, "timezone": "Europe/Berlin", "boundary": json.RawMessage(germanyJSON), "debug": s.config.Debug})
	})
	e.GET("/api/health", func(c *echo.Context) error {
		_, overview := s.cache.lookup("overview")
		_, obs := s.cache.lookup("observations")
		return c.JSON(200, map[string]any{"status": "ok", "overviewReady": overview, "observationsReady": obs, "gridCells": len(s.geography.cells)})
	})
	e.GET("/api/overview", func(c *echo.Context) error {
		entry, ok := s.cache.lookup("overview")
		if !ok {
			return unavailable(c, "The Germany forecast is warming up. Try again shortly; you can already select a spot.")
		}
		return c.JSON(200, Envelope{Entry: entry, Source: "DWD ICON D2 via Open-Meteo · 0.3° sampled overview", Stale: time.Since(time.Unix(entry.FetchedAt, 0)) > 3*time.Hour, Units: units})
	})
	e.GET("/api/observations", func(c *echo.Context) error {
		entry, ok := s.cache.lookup("observations")
		if !ok {
			return unavailable(c, "Station observations are not available yet.")
		}
		var all []Observation
		if err := json.Unmarshal(entry.Data, &all); err != nil {
			return err
		}
		fresh := freshObservations(all, time.Now())
		filtered := []Observation{}
		for _, o := range fresh {
			if s.geography.contains(o.Latitude, o.Longitude) {
				filtered = append(filtered, o)
			}
		}
		b, err := json.Marshal(filtered)
		if err != nil {
			return err
		}
		entry.Data = b
		return c.JSON(200, Envelope{Entry: entry, Source: "DWD station observations", Stale: time.Since(time.Unix(entry.FetchedAt, 0)) > 20*time.Minute, Units: units})
	})
	e.GET("/api/forecast", func(c *echo.Context) error {
		lat, errLat := strconv.ParseFloat(c.QueryParam("lat"), 64)
		lon, errLon := strconv.ParseFloat(c.QueryParam("lon"), 64)
		if errLat != nil || errLon != nil || !s.geography.contains(lat, lon) {
			return c.JSON(400, map[string]string{"error": "Select a location within Germany."})
		}
		key := fmt.Sprintf("point:%.4f,%.4f", lat, lon)
		ctx, cancel := context.WithTimeout(c.Request().Context(), 35*time.Second)
		defer cancel()
		entry, stale, err := s.cache.load(ctx, key, time.Hour, func(ctx context.Context) (any, error) {
			f, err := s.provider.forecasts(ctx, []Coordinate{{lat, lon}}, true)
			if err != nil {
				return nil, err
			}
			return f[0], nil
		})
		if err != nil && !stale {
			s.logger.Warn("point forecast unavailable", "error", err)
			return unavailable(c, "Forecast unavailable. Please try again shortly.")
		}
		warning := ""
		if err != nil {
			warning = "Provider unavailable; showing the last saved forecast."
		}
		return c.JSON(200, Envelope{Entry: entry, Source: "DWD ICON D2 via Open-Meteo · model forecast", Stale: stale, Warning: warning, Units: units})
	})
	e.GET("/api/search", func(c *echo.Context) error {
		query := strings.TrimSpace(c.QueryParam("q"))
		if len([]rune(query)) < 2 || len(query) > 120 {
			return c.JSON(400, map[string]string{"error": "Enter between 2 and 120 characters."})
		}
		ctx, cancel := context.WithTimeout(c.Request().Context(), 30*time.Second)
		defer cancel()
		entry, stale, err := s.cache.load(ctx, "search:"+strings.ToLower(query), 24*time.Hour, func(ctx context.Context) (any, error) { return s.provider.search(ctx, query) })
		if err != nil && !stale {
			return unavailable(c, "Place search unavailable. Select a spot on the map instead.")
		}
		return c.JSON(200, Envelope{Entry: entry, Source: "Open-Meteo geocoding", Stale: stale})
	})
	e.Any("/api/*", func(c *echo.Context) error { return c.JSON(404, map[string]string{"error": "Unknown API endpoint."}) })
	assets, err := fs.Sub(webFiles, "web")
	if err != nil {
		panic(err)
	}
	if s.config.Debug {
		assets = os.DirFS("web")
		// Retire an existing production worker so its app-shell cache cannot
		// hide changes made to files on disk during development.
		e.GET("/sw.js", func(c *echo.Context) error {
			return c.Blob(http.StatusOK, "application/javascript", []byte(`self.addEventListener('install', event => event.waitUntil(self.skipWaiting())); self.addEventListener('activate', event => event.waitUntil(caches.keys().then(keys => Promise.all(keys.filter(key => key.startsWith('fographer-')).map(key => caches.delete(key)))).then(() => self.clients.claim()).then(() => self.registration.unregister())));`))
		})
	}
	e.GET("/*", echo.WrapHandler(http.FileServer(http.FS(assets))))
	return e
}
func main() {
	s, err := newServer(configuration())
	if err != nil {
		slog.Error("startup failed", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	s.workers(ctx)
	server := &http.Server{Addr: s.config.Addr, Handler: s.routes(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 45 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	s.logger.Info("Fographer ready", "address", s.config.Addr, "grid_cells", len(s.geography.cells))
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		s.logger.Error("server failed", "error", err)
		os.Exit(1)
	}
}
