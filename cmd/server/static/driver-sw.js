// Service worker da tela do motorista: guarda a página e os arquivos dela para abrir
// sem sinal. Sempre tenta a rede primeiro; a API nunca passa pelo cache.
const CACHE='driver-v1';
const SHELL=['/driver','/static/app.css','/static/driver.css','/static/app.js','/static/driver.js','/static/logo-totalfood.png'];
self.addEventListener('install',e=>{e.waitUntil(caches.open(CACHE).then(c=>c.addAll(SHELL)).catch(()=>{}));self.skipWaiting()});
self.addEventListener('activate',e=>{e.waitUntil(caches.keys().then(keys=>Promise.all(keys.filter(k=>k!==CACHE).map(k=>caches.delete(k)))).then(()=>self.clients.claim()))});
self.addEventListener('fetch',e=>{
  const url=new URL(e.request.url);
  if(e.request.method!=='GET'||url.origin!==location.origin)return;
  if(url.pathname!=='/driver'&&!url.pathname.startsWith('/static/'))return;
  e.respondWith(fetch(e.request).then(r=>{
    if(r.ok&&!r.redirected){const copy=r.clone();caches.open(CACHE).then(c=>c.put(url.pathname,copy))}
    return r
  }).catch(()=>caches.match(url.pathname).then(r=>r||Response.error())));
});
