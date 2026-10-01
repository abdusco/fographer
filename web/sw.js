const VERSION = 'fographer-v6';
const SHELL = VERSION + '-shell';
const WEATHER = VERSION + '-weather';
const ASSETS = ['/', '/index.html', '/style.css', '/app.js', '/icon.svg', '/manifest.webmanifest', '/assets/icon-192.png', '/assets/icon-512.png', '/vendor/alpine.js', '/vendor/maplibre-gl.js', '/vendor/maplibre-gl.css', ...['0-255', '256-511', '512-767', '768-1023'].map(r => '/fonts/Open%20Sans%20Semibold/' + r + '.pbf')];
self.addEventListener('install', event => { event.waitUntil(Promise.all([caches.open(SHELL).then(cache => cache.addAll(ASSETS)), caches.open(WEATHER).then(cache => cache.add('/api/config'))]).then(() => self.skipWaiting())); });
self.addEventListener('activate', event => { event.waitUntil(caches.keys().then(keys => Promise.all(keys.filter(k => k.startsWith('fographer-') && ![SHELL, WEATHER].includes(k)).map(k => caches.delete(k)))).then(() => self.clients.claim())); });
async function weather(request) {
  const cache = await caches.open(WEATHER);
  if (request.headers.get('X-Fographer-Offline') === '1' || self.navigator.onLine === false) {
    const saved = await cache.match(request);
    return saved ? savedResponse(saved, true) : new Response(JSON.stringify({error: 'Offline: no saved weather for this location yet.'}), {status: 503, headers: {'Content-Type': 'application/json'}});
  }
  try {
    const response = await fetch(request);
    if (response.ok) {
      await cache.put(request, response.clone());
      const keys = await cache.keys();
      for (const key of keys.slice(0, Math.max(0, keys.length - 40))) await cache.delete(key);
      return response;
    }
    const saved = await cache.match(request);
    return saved ? savedResponse(saved, false) : response;
  } catch {
    const saved = await cache.match(request);
    return saved ? savedResponse(saved, true) : new Response(JSON.stringify({error: 'Offline: no saved weather for this location yet.'}), {status: 503, headers: {'Content-Type': 'application/json'}});
  }
}
async function savedResponse(response, offline) {
  const body = await response.json();
  if (body.fetchedAt) { body.stale = true; body.offline = offline; body.warning = offline ? 'Showing previously loaded data.' : 'Server unavailable; showing previously loaded data.'; }
  return new Response(JSON.stringify(body), {headers: {'Content-Type': 'application/json'}});
}
self.addEventListener('fetch', event => {
  const url = new URL(event.request.url);
  if (event.request.method !== 'GET' || url.origin !== self.location.origin) return;
  // Basemap tiles (including a same-origin replacement provider) must use
  // normal HTTP caching, never this offline cache.
  if (url.pathname.startsWith('/api/') && url.pathname !== '/api/health') { event.respondWith(weather(event.request)); return; }
  if (ASSETS.includes(url.pathname) || url.pathname.startsWith('/fonts/')) event.respondWith(caches.open(SHELL).then(async cache => (await cache.match(event.request)) || fetch(event.request)));
});
