/* MapLibre is deliberately kept outside Alpine's reactive object. */
(() => {
  let map, selectedMarker, requestID = 0, searchID = 0, pointAbort, searchAbort, pollTimer;
  const emptyCollection = () => ({type: 'FeatureCollection', features: []});
  const readStorage = (key, fallback) => { try { return JSON.parse(localStorage.getItem(key)) ?? fallback; } catch { return fallback; } };
  const berlin = {timeZone: 'Europe/Berlin'};
  const floorHour = () => Math.floor(Date.now() / 3600000) * 3600;
  document.addEventListener('alpine:init', () => {
    Alpine.data('fographer', () => ({
      online: navigator.onLine, about: false, panelExpanded: false, toast: '',
      query: '', results: [], searching: false, searchError: '',
      selected: null, point: null, station: null, loadingSpot: false, spotError: '',
      overview: null, observations: null, overviewMessage: 'Finding the next quiet morning…', mapError: '',
      mode: 'now', timeIndex: 0, times: [], showModel: true, showStations: true,
      favorites: [],
      async init() {
        const stored = readStorage('fographer.spots', []);
        this.favorites = Array.isArray(stored) ? stored.filter(s => s && typeof s.name === 'string' && Number.isFinite(s.lat) && Number.isFinite(s.lon) && typeof s.id === 'string') : [];
        window.addEventListener('online', () => { this.online = true; this.refresh(); if (this.selected) this.fetchPoint(); });
        window.addEventListener('offline', () => { this.online = false; });
        this.times = Array.from({length: 48}, (_, i) => floorHour() + i * 3600);
        try {
          const config = await this.api('/api/config');
          if ('serviceWorker' in navigator) {
            if (config.debug) {
              const registrations = await navigator.serviceWorker.getRegistrations();
              await Promise.all(registrations.filter(r => r.scope === location.origin + '/').map(r => r.unregister()));
              const keys = await caches.keys();
              await Promise.all(keys.filter(k => k.startsWith('fographer-')).map(k => caches.delete(k)));
            } else navigator.serviceWorker.register('/sw.js').catch(() => this.notify('Offline caching is unavailable in this browser.'));
          }
          map = new maplibregl.Map({container: 'map', style: this.mapStyle(config), center: [10.4, 51.1], zoom: window.innerWidth < 700 ? 4.7 : 5.25, maxZoom: 16, minZoom: 3, attributionControl: false});
          const navigation = new maplibregl.NavigationControl({showCompass: true, visualizePitch: true});
          map.addControl(navigation, 'top-right');
          const resetAngle = map.getContainer().querySelector('.maplibregl-ctrl-compass');
          resetAngle.title = 'Reset angle to north-up';
          resetAngle.setAttribute('aria-label', 'Reset angle to north-up');
          map.addControl(new maplibregl.AttributionControl({compact: false, customAttribution: 'Weather: <a href="https://open-meteo.com">Open-Meteo</a> · DWD'}), 'bottom-right');
          map.on('error', event => { if (event.error) this.mapError = this.online ? 'Some map tiles could not load. Weather details and saved spots remain available.' : 'Offline: the basemap needs a connection. Your saved weather is still here.'; });
          map.on('load', () => {
            document.getElementById('map').dataset.ready = 'true';
            map.addSource('boundary', {type: 'geojson', data: {type: 'Feature', geometry: config.boundary, properties: {}}});
            map.addLayer({id: 'boundary', type: 'line', source: 'boundary', paint: {'line-color': '#9aab83', 'line-width': 1.2, 'line-opacity': .5}});
            map.addSource('fog', {type: 'geojson', data: emptyCollection()});
            map.addLayer({id: 'fog', type: 'fill', source: 'fog', paint: {'fill-color': ['match', ['get', 'fog'], 'fog', '#b7abd4', 'favorable', '#b6cf91', '#59635b'], 'fill-opacity': ['match', ['get', 'fog'], 'fog', .45, 'favorable', .24, 0]}});
            map.addSource('stations', {type: 'geojson', data: emptyCollection()});
            map.addLayer({id: 'stations', type: 'circle', source: 'stations', paint: {'circle-radius': ['case', ['get', 'fog'], 5.5, ['get', 'lowVisibility'], 5, 2.8], 'circle-color': ['case', ['get', 'fog'], '#efc393', ['get', 'lowVisibility'], '#d0a472', '#8eaa96'], 'circle-stroke-color': '#16251b', 'circle-stroke-width': 1.5, 'circle-opacity': ['case', ['get', 'stale'], .45, .9]}});
            map.on('click', e => {
              const hit = map.queryRenderedFeatures(e.point, {layers: ['stations']})[0];
              this.station = hit ? this.observations?.data.find(o => o.id === hit.properties.id) ?? null : null;
              this.selectSpot(e.lngLat.lat, e.lngLat.lng, this.station?.name || 'Untitled viewpoint', false);
            });
            map.on('mouseenter', 'stations', () => map.getCanvas().style.cursor = 'pointer');
            map.on('mouseleave', 'stations', () => map.getCanvas().style.cursor = '');
            this.renderMap();
          });
        } catch (err) { this.mapError = err.message.includes('WebGL') || err.message.includes('requestedAttributes') ? 'This browser cannot display the interactive map. Try enabling graphics acceleration. Place search and forecasts remain available.' : err.message; }
        await this.refresh();
        const previous = readStorage('fographer.lastSpot', null);
        if (previous && Number.isFinite(previous.lat) && Number.isFinite(previous.lon)) this.selectSpot(previous.lat, previous.lon, String(previous.name || 'Saved viewpoint'));
        pollTimer = setInterval(() => this.refresh(), 60000);
        window.addEventListener('pagehide', () => clearInterval(pollTimer));
      },
      mapStyle(config) {
        const layer = (id, type, sourceLayer, paint, extra = {}) => ({id, type, source: 'osm', 'source-layer': sourceLayer, paint, ...extra});
        return {version: 8, glyphs: location.origin + '/fonts/{fontstack}/{range}.pbf', sources: {osm: {type: 'vector', tiles: [config.tileURL], maxzoom: 14, attribution: '© <a href="https://www.openstreetmap.org/copyright">OpenStreetMap contributors</a>'}}, layers: [
          {id: 'background', type: 'background', paint: {'background-color': '#242e27'}},
          layer('ocean', 'fill', 'ocean', {'fill-color': '#152629'}),
          layer('land', 'fill', 'land', {'fill-color': ['match', ['get', 'kind'], ['forest', 'wood'], '#20392a', ['residential', 'industrial', 'commercial'], '#2e3530', ['grass', 'grassland', 'meadow'], '#293d2b', '#2b352b'], 'fill-opacity': .75}),
          layer('water', 'fill', 'water_polygons', {'fill-color': '#183238'}),
          layer('rivers', 'line', 'water_lines', {'line-color': '#325354', 'line-width': ['interpolate', ['linear'], ['zoom'], 7, .4, 14, 1.5]}),
          layer('roads', 'line', 'streets', {'line-color': ['match', ['get', 'kind'], ['motorway', 'trunk'], '#697056', '#455344'], 'line-width': ['interpolate', ['linear'], ['zoom'], 5, .3, 12, 1.1, 16, 2.5], 'line-opacity': .6}),
          layer('buildings', 'fill', 'buildings', {'fill-color': '#414d3f', 'fill-opacity': .6}),
          layer('borders', 'line', 'boundaries', {'line-color': '#67785a', 'line-width': .7, 'line-dasharray': [3, 3], 'line-opacity': .4}),
          layer('places', 'symbol', 'place_labels', {'text-color': '#c9d7bd', 'text-halo-color': '#1c2b21', 'text-halo-width': 1.5}, {layout: {'text-field': ['coalesce', ['get', 'name_en'], ['get', 'name']], 'text-font': ['Open Sans Semibold'], 'text-size': ['match', ['get', 'kind'], 'city', 16, 'town', 14, 13], 'text-padding': 12}}),
        ]};
      },
      async api(path, signal) {
        const timeout = new AbortController();
        const timer = setTimeout(() => timeout.abort(), 38000);
        const combined = signal ? AbortSignal.any([signal, timeout.signal]) : timeout.signal;
        try {
          const response = await fetch(path, {signal: combined, headers: navigator.onLine ? {} : {'X-Fographer-Offline': '1'}});
          let body; try { body = await response.json(); } catch { throw new Error('The server returned an unreadable response.'); }
          if (!response.ok) throw new Error(body.error || 'Weather is temporarily unavailable.');
          if (!navigator.onLine && body.fetchedAt) { body.offline = true; body.stale = true; }
          return body;
        } catch (err) {
          if (signal?.aborted) throw err;
          if (!navigator.onLine) throw new Error('Offline: this location has no saved data yet.');
          if (err.name === 'AbortError' || err.name === 'TimeoutError') throw new Error('The weather request timed out. Please try again.');
          throw err;
        } finally { clearTimeout(timer); }
      },
      async refresh() {
        const results = await Promise.allSettled([this.api('/api/overview'), this.api('/api/observations')]);
        if (results[0].status === 'fulfilled') {
          this.overview = results[0].value;
          const selected = this.selectedEpoch();
          const available = this.overview.data.times.filter(t => t >= floorHour());
          if (available.length) { this.times = available; const i = this.times.indexOf(selected); this.timeIndex = Math.max(0, i); }
          this.overviewMessage = this.freshness(this.overview);
        } else { this.overviewMessage = this.overview ? this.freshness(this.overview) : results[0].reason.message; }
        if (results[1].status === 'fulfilled') this.observations = results[1].value;
        else if (!this.observations) this.notify('Station observations are currently unavailable.');
        this.renderMap();
      },
      renderMap() {
        if (!map?.getSource('fog')) return;
        const index = this.overview?.data.times.indexOf(this.selectedEpoch()) ?? -1;
        const features = this.showModel && index >= 0 ? this.overview.data.features.map(f => ({type: 'Feature', geometry: f.geometry, properties: {fog: f.properties.states[index] || 'unknown'}})) : [];
        map.getSource('fog').setData({type: 'FeatureCollection', features: JSON.parse(JSON.stringify(features))});
        const data = this.mode === 'now' && this.showStations ? (this.observations?.data || []).map(o => {
          const latest = Math.max(o.visibilityTime || 0, o.weatherTime || 0);
          if (Date.now() / 1000 - latest > 10800) return null;
          const weatherFresh = Date.now() / 1000 - (o.weatherTime || 0) <= 10800;
          const visFresh = Date.now() / 1000 - (o.visibilityTime || 0) <= 10800;
          if ((!visFresh || o.visibility == null) && (!weatherFresh || !o.fog)) return null;
          return {type: 'Feature', geometry: {type: 'Point', coordinates: [o.longitude, o.latitude]}, properties: {id: o.id, fog: weatherFresh && o.fog, lowVisibility: visFresh && o.visibility != null && o.visibility < 1000, stale: Date.now() / 1000 - latest > 5400}};
        }).filter(Boolean) : [];
        map.getSource('stations').setData({type: 'FeatureCollection', features: data});
      },
      async search() {
        searchAbort?.abort(); const id = ++searchID; this.searchError = ''; this.results = [];
        if (this.query.trim().length < 2) { this.searching = false; return; }
        searchAbort = new AbortController(); this.searching = true; this.panelExpanded = true;
        try { const result = await this.api('/api/search?q=' + encodeURIComponent(this.query.trim()), searchAbort.signal); if (id === searchID) { this.results = result.data; if (!result.data.length) this.searchError = 'No German places found. Try another name.'; } }
        catch (err) { if (id === searchID) this.searchError = err.message; }
        finally { if (id === searchID) this.searching = false; }
      },
      async selectSpot(lat, lon, name, fly = true) {
        this.selected = {lat, lon, name}; this.point = null; this.spotError = ''; this.panelExpanded = true;
        if (fly) this.station = null;
        try { localStorage.setItem('fographer.lastSpot', JSON.stringify(this.selected)); } catch { /* Storage can be disabled. */ }
        selectedMarker?.remove();
        if (map) {
          selectedMarker = new maplibregl.Marker({color: '#d1e5a5', scale: .75}).setLngLat([lon, lat]).addTo(map);
          if (fly) map.flyTo({center: [lon, lat], zoom: 9, essential: false});
        }
        await this.fetchPoint();
      },
      async fetchPoint() {
        if (!this.selected) return;
        pointAbort?.abort(); pointAbort = new AbortController(); const id = ++requestID;
        this.loadingSpot = true; this.spotError = '';
        try {
          const data = await this.api(`/api/forecast?lat=${this.selected.lat.toFixed(4)}&lon=${this.selected.lon.toFixed(4)}`, pointAbort.signal);
          if (id !== requestID) return;
          this.point = data;
          if (!this.overview || !this.overview.data.times.some(t => t >= floorHour())) {
            const future = data.data.hours.map(h => h.time).filter(t => t >= floorHour());
            this.times = future.length ? future : data.data.hours.map(h => h.time);
            this.timeIndex = 0;
            if (!future.length) { this.mode = 'forecast'; this.notify('Showing an older saved forecast. Check its date before planning.'); }
          }
        } catch (err) { if (id === requestID) this.spotError = err.message; }
        finally { if (id === requestID) this.loadingSpot = false; }
      },
      clearSpot() { ++requestID; pointAbort?.abort(); this.selected = null; this.point = null; this.station = null; this.loadingSpot = false; selectedMarker?.remove(); try { localStorage.removeItem('fographer.lastSpot'); } catch {} },
      resetMap() { map?.fitBounds([[5.7, 47.1], [15.3, 55.2]], {padding: 40, duration: 700}); },
      locate() {
        if (!navigator.geolocation) { this.notify('Geolocation is unavailable. Search for a place instead.'); return; }
        navigator.geolocation.getCurrentPosition(p => this.selectSpot(p.coords.latitude, p.coords.longitude, 'My location'), () => this.notify('Location could not be read. Search for a place instead.'), {timeout: 10000});
      },
      selectedEpoch() { return this.mode === 'now' ? floorHour() : this.times[this.timeIndex]; },
      chooseTime(epoch) { if (!epoch) return; let i = this.times.indexOf(epoch); if (i < 0 && this.point) { this.times = this.point.data.hours.map(h => h.time); i = this.times.indexOf(epoch); } if (i >= 0) { this.timeIndex = i; this.mode = 'forecast'; this.renderMap(); } },
      jumpSunrise(epoch) { this.chooseTime(Math.floor(epoch / 3600) * 3600); },
      currentHour() { return this.point?.data.hours.find(h => h.time === this.selectedEpoch()); },
      nearbyHours() { const hours = this.point?.data.hours || []; let i = hours.findIndex(h => h.time >= this.selectedEpoch()); if (i < 0) i = Math.max(0, hours.length - 4); return hours.slice(i, i + 4); },
      nextSunrises() { return (this.point?.data.sun || []).filter(s => s.rise > Date.now() / 1000 && s.rise <= (this.point?.data.hours.at(-1)?.time || 0)).slice(0, 2); },
      clock(epoch) { return epoch ? new Intl.DateTimeFormat('en-GB', {...berlin, hour: '2-digit', minute: '2-digit'}).format(epoch * 1000) : '—'; },
      dateTime(epoch) { return epoch ? new Intl.DateTimeFormat('en-GB', {...berlin, day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit', timeZoneName: 'short'}).format(epoch * 1000) : '—'; },
      dayLabel(epoch) { if (!epoch) return '—'; const format = d => new Intl.DateTimeFormat('en-CA', {...berlin, year: 'numeric', month: '2-digit', day: '2-digit'}).format(d); const day = format(epoch * 1000); if (day === format(Date.now())) return 'Today'; if (day === format(Date.now() + 86400000)) return 'Tomorrow'; return new Intl.DateTimeFormat('en-GB', {...berlin, weekday: 'short', day: 'numeric', month: 'short'}).format(epoch * 1000); },
      selectedTimeLabel() { const epoch = this.selectedEpoch(); return (this.mode === 'now' ? 'Now · ' : '') + this.dayLabel(epoch) + ', ' + this.clock(epoch); },
      fogLabel(state) { return {fog: 'Predicted fog', favorable: 'Favorable conditions', clear: 'No fog signal', unknown: 'Insufficient data'}[state] || 'No forecast at this hour'; },
      metric(value, unit, digits = 0) { return value == null ? '—' : Number(value).toFixed(digits) + unit; },
      visibility(value) { return value == null ? '—' : value < 1000 ? Math.round(value) + ' m' : (value / 1000).toFixed(1) + ' km'; },
      freshness(data) { if (!data) return ''; const age = Math.max(0, Math.floor((Date.now() / 1000 - data.fetchedAt) / 60)); const text = age < 1 ? 'just now' : age < 60 ? age + ' min ago' : Math.floor(age / 60) + ' h ago'; return (data.offline ? 'Offline copy · ' : data.stale ? 'Stale data · ' : '') + 'Retrieved ' + text + (data.warning ? '. ' + data.warning : ''); },
      isSaved() { return !!this.selected && this.favorites.some(s => Math.abs(s.lat - this.selected.lat) < .0001 && Math.abs(s.lon - this.selected.lon) < .0001); },
      persistSpots() { try { localStorage.setItem('fographer.spots', JSON.stringify(this.favorites)); if (this.selected) localStorage.setItem('fographer.lastSpot', JSON.stringify(this.selected)); } catch { this.notify('This browser could not save your spots.'); } },
      toggleSave() {
        if (!this.selected) return;
        if (this.isSaved()) { this.favorites = this.favorites.filter(s => Math.abs(s.lat - this.selected.lat) >= .0001 || Math.abs(s.lon - this.selected.lon) >= .0001); this.notify('Spot removed.'); }
        else { const name = this.selected.name === 'Untitled viewpoint' ? prompt('Name this viewpoint', 'Quiet morning') : this.selected.name; if (!name?.trim()) return; this.selected.name = name.trim().slice(0, 100); this.favorites.push({...this.selected, id: crypto.randomUUID()}); this.notify('A place to return to. Spot saved.'); }
        this.persistSpots();
      },
      renameSpot(spot) { const name = prompt('Rename this viewpoint', spot.name); if (!name?.trim()) return; spot.name = name.trim().slice(0, 100); if (this.selected && Math.abs(this.selected.lat - spot.lat) < .0001 && Math.abs(this.selected.lon - spot.lon) < .0001) this.selected.name = spot.name; this.persistSpots(); },
      removeSpot(id) { this.favorites = this.favorites.filter(s => s.id !== id); this.persistSpots(); },
      notify(message) { this.toast = message; setTimeout(() => { if (this.toast === message) this.toast = ''; }, 5500); },
    }));
  });
})();
