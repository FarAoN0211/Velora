// Reproductor web completo (Player) en Chromium con una API simulada que decide cuándo y en qué orden responde.
// Comprueba: controles y avisos como en TV, marcas, foco, entrada táctil y teclado, tamaños de pantalla, listas lentas que no retrasan el vídeo, saltos que responden desordenados, un cambio de audio superado por
// un salto y un cambio de título con la preparación anterior aún pendiente.
// Uso: npm run build && node pruebas/reproductor-e2e.mjs   (necesita el paquete playwright y su Chromium)
import assert from 'node:assert/strict';
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright';

const dist = process.env.VELORA_DIST ?? fileURLToPath(new URL('../dist', import.meta.url));
const servidor = http.createServer((req, res) => {
  const p = path.join(dist, decodeURIComponent(new URL(req.url, 'http://x').pathname));
  const f = fs.existsSync(p) && fs.statSync(p).isFile() ? p : path.join(dist, 'index.html');
  const tipos = { '.js': 'text/javascript', '.css': 'text/css', '.html': 'text/html', '.woff2': 'font/woff2', '.svg': 'image/svg+xml' };
  res.writeHead(200, { 'Content-Type': tipos[path.extname(f)] ?? 'application/octet-stream' });
  fs.createReadStream(f).pipe(res);
});
await new Promise((ok) => servidor.listen(0, '127.0.0.1', ok));
servidor.unref();
const base = process.env.VELORA_BASE_URL ?? `http://127.0.0.1:${servidor.address().port}`;

const item = (id, title) => ({
  id, library_id: 'L', title, year: 2020, has_poster: false, position_ms: 0, runtime_ms: 600000, played: false, added_at: '2026-01-01T00:00:00Z',
  has_backdrop: false, overview: null, tagline: null, official_rating: null, genres: [], extras: [],
  files: [{ id: 'F' + id, container: 'mkv', duration_ms: 600000, size_bytes: 1, version_label: null, missing: false, streams: [
    { index: 0, kind: 'video', codec: 'h264', language: null, title: null, default: true },
    { index: 1, kind: 'audio', codec: 'ac3', language: 'spa', title: null, default: true },
    { index: 2, kind: 'audio', codec: 'aac', language: 'eng', title: null, default: false },
  ] }],
});
const plan = (file, audio) => ({ method: 'direct_stream', audio_index: audio, convert_audio: false, reasons: null, summary: '', seekable: false,
  url: `/api/v1/stream/files/${file}/remux?audio=${audio}&convert=false`, duration_ms: 600000 });

// Cada prueba decide qué respuestas retener; las retenidas se sueltan con soltar(clave).
let correctos = 0;
async function escenario(nombre, prueba, opciones = {}) {
  if (process.env.VELORA_CASO && !nombre.includes(process.env.VELORA_CASO)) return;
  const navegador = await chromium.launch(process.env.CHROME_BIN ? { executablePath: process.env.CHROME_BIN, args: ['--no-sandbox'] } : {});
  const pagina = await navegador.newPage({ viewport: opciones.viewport ?? { width: 1280, height: 720 }, hasTouch: !!opciones.touch, ...(opciones.tv ? {userAgent: "Chrome VeloraTV"} : {}) });
  if (opciones.fullscreen) await pagina.addInitScript((mode) => {
    window.fullscreenCalls = [];
    let current = null;
    Object.defineProperty(document, 'fullscreenElement', { configurable: true, get: () => current });
    HTMLElement.prototype.requestFullscreen = function () {
      window.fullscreenCalls.push('enter');
      if (mode === 'first-tap' && window.fullscreenCalls.length === 1) return Promise.reject(new Error('gesture required'));
      current = this; document.dispatchEvent(new Event('fullscreenchange')); return Promise.resolve();
    };
    document.exitFullscreen = () => { window.fullscreenCalls.push('exit'); current = null; document.dispatchEvent(new Event('fullscreenchange')); return Promise.resolve(); };
    if (mode === 'apk') window.VeloraApp = { pantallaCompletaReproductor: active => window.fullscreenCalls.push(active ? 'app-enter' : 'app-exit') };
  }, opciones.fullscreen);
  if (opciones.sinUUID) await pagina.addInitScript(() => Object.defineProperty(globalThis.crypto, 'randomUUID', {value: undefined, configurable: true}));
  if (opciones.sinCrypto) await pagina.addInitScript(() => Object.defineProperty(globalThis, 'crypto', {value: undefined, configurable: true}));
  pagina.setDefaultTimeout(5000);
  const retenidas = new Map();
  const retener = new Set();
  const canceladas = [];
  const pedidas = [];
  pagina.on('requestfailed', (r) => canceladas.push(new URL(r.url()).pathname + new URL(r.url()).search));
  await pagina.route('**/api/v1/**', async (route) => {
    const u = new URL(route.request().url());
    const ruta = u.pathname + u.search;
    pedidas.push(ruta);
    let cuerpo = {};
    if (u.pathname === '/api/v1/system/info') cuerpo = { name: 'VELORA', version: 'x', language: 'es', setup_required: false };
    else if (u.pathname === '/api/v1/me') cuerpo = { user: { id: 'U', username: 'u', display_name: 'U', is_admin: !!opciones.admin, has_pin: false, can_play: true, can_download: false, can_delete: false } };
    else if (u.pathname === '/api/v1/libraries') cuerpo = {libraries: []};
    else if (u.pathname === '/api/v1/collections') cuerpo = {collections: opciones.colecciones ?? []};
    else if (u.pathname === '/api/v1/collections/123') cuerpo = {collection:{id:123,name:'Una saga',count:2,poster_id:'I1'},items:[{...item('I1','Primera película'),kind:'movie',has_poster:true,library_4k:true},{...item('I2','Segunda película'),kind:'movie',has_poster:true}]};
    else if (u.pathname === '/api/v1/admin/now-playing') cuerpo = { items: opciones.activity ?? [] };
    else if (u.pathname === '/api/v1/admin/overview') cuerpo = { version: 'test', optimized_streams: 0, max_optimized_streams: 2, counts: {libraries: 1, movies: 1, files: 1, size_bytes: 1, users: 1, pending_genres: 0, pending_items: 0}, tmdb_configured: true, scans: [] };
    else if (u.pathname.includes('/images/')) return route.fulfill({ status: 200, contentType: 'image/svg+xml', body: '<svg xmlns="http://www.w3.org/2000/svg" width="160" height="64"><rect width="160" height="64" fill="white"/></svg>' });
    else if (u.pathname.endsWith('/presentation')) cuerpo = {presentation: {}};
    else if (u.pathname.startsWith('/api/v1/items/')) { const id = u.pathname.split('/')[4]; cuerpo = { item: { ...item(id, 'Título ' + id), ...(opciones.identidad ?? {}), has_logo: !!opciones.logo, ...(opciones.next ? { next_episode: { id: 'I2', title: 'Siguiente', season_number: 1, episode_number: 2 } } : {}) } }; }
    else if (u.pathname === '/api/v1/playback/decide') { const b = route.request().postDataJSON(); cuerpo = plan(b.file_id, b.audio_index ?? 1); if(opciones.transcode){cuerpo.method='transcode';cuerpo.convert_video=true;cuerpo.url=`/api/v1/stream/files/${b.file_id}/transcode?ticket=mock&audio=${b.audio_index??1}`;} if (opciones.directo) { cuerpo.method = 'direct_play'; cuerpo.url = `/api/v1/stream/files/${b.file_id}/direct`; } if(opciones.decoderFallback) { if(b.caps.source_video_unsupported){cuerpo.method='transcode';cuerpo.url=`/api/v1/stream/files/${b.file_id}/transcode?ticket=mock&audio=1`;} else if(b.caps.force_remux){cuerpo.method='direct_stream';} else {cuerpo.method='direct_play';cuerpo.url=`/api/v1/stream/files/${b.file_id}/direct`;} } }
    else if (u.pathname.endsWith('/seek')) cuerpo = { start: Number(u.searchParams.get('t')) };
    else if (u.pathname.endsWith('/chapters')) cuerpo = { chapters: opciones.chapters ?? [] };
    else if (u.pathname.endsWith('/subtitles')) cuerpo = { subtitles: opciones.subtitulos ? [{ id: 'S1', supported: true, codec: 'vtt', language: 'spa', forced: !!opciones.forzados, default: !!opciones.forzados }] : [] };
    else if (u.pathname.endsWith('/subtitles/S1')) return route.fulfill({ status: 200, contentType: 'text/vtt', body: 'WEBVTT\n\n00:00:10.000 --> 00:00:40.000\nPrimera línea del subtítulo\nSegunda línea del subtítulo\n' });
    else if (u.pathname.endsWith('/remux') || u.pathname.endsWith('/transcode') || u.pathname.endsWith('/direct')) return; // el flujo no llega nunca: el vídeo espera sin dar error
    const responder = () => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(cuerpo) }).catch(() => {});
    const clave = [...retener].find((k) => ruta.includes(k));
    if (clave) retenidas.set(ruta, responder);
    else await responder();
  });
  const soltar = async (parte) => {
    for (const [ruta, responder] of [...retenidas]) if (ruta.includes(parte)) { retenidas.delete(ruta); await responder(); }
  };
  const src = () => pagina.locator('video').getAttribute('src');
  const esperarSrc = async (parte) => {
    await pagina.waitForFunction((p) => document.querySelector('video')?.getAttribute('src')?.includes(p), parte, { timeout: 5000 });
  };
  try {
    await prueba({ pagina, retener, soltar, src, esperarSrc, canceladas, pedidas });
    correctos++;
    console.log('✓', nombre);
  } catch (e) {
    await pagina.screenshot({path:"/tmp/velora-e2e-failure.png",fullPage:true});
    console.error(await pagina.locator("body").innerText());
    throw e;
  } finally {
    await navegador.close();
  }
}

if (process.env.VELORA_BASE_URL) {
 for (const mobile of [false,true]) {
  await escenario(`HTTP real de red local: ${mobile?'móvil':'web'}`,async ({pagina,esperarSrc})=>{
   await pagina.goto(`${base}/ver/I1`); await pagina.locator('video[src*="/remux?audio=1"]').waitFor();
   assert.equal(await pagina.evaluate(()=>window.isSecureContext),false);
   assert.equal(await pagina.evaluate(()=>typeof globalThis.crypto?.randomUUID),'undefined');
   assert.equal(await pagina.getByText('Esta pantalla no se ha podido mostrar').count(),0);
  },{viewport:mobile?{width:390,height:844}:{width:1280,height:720}});
 }
}

for (const mode of ['web','tv','mobile']) {
 await escenario(`colecciones y emblema fuera de la portada: ${mode}`,async ({pagina})=>{
  await pagina.goto(`${base}/colecciones`);
  await pagina.getByText('Una saga',{exact:true}).waitFor();
  await pagina.getByText('Una saga',{exact:true}).click();
  await pagina.getByText('Primera película',{exact:true}).waitFor();
  assert.equal(await pagina.locator('[data-uhd-emblem]').count(),1);
  await pagina.getByRole('img',{name:'4K UHD'}).evaluate(im=>im.decode());
  const badge=await pagina.getByRole('img',{name:'4K UHD'}).boundingBox();
  assert.ok(badge.height>15);
  console.log('emblema',badge);
  const poster=await pagina.locator('a[href="/pelicula/I1"] .aspect-poster').boundingBox();
  assert.ok(badge.y+badge.height<=poster.y+1,'emblema fuera de los píxeles de la portada');
  assert.ok(Math.abs(poster.width/poster.height-2/3)<.02,'portada sin recortar');
  assert.ok(await pagina.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
  await pagina.screenshot({path:`/tmp/velora-collections-${mode}.png`,fullPage:true});
  await pagina.getByText('Primera película',{exact:true}).click();
  await pagina.getByRole('heading',{level:1}).waitFor();
 },{tv:mode==='tv',viewport:mode==='mobile'?{width:390,height:844}:{width:1280,height:720},colecciones:[{id:123,name:'Una saga',count:2,poster_id:'I1'}]});
}

for (const noCrypto of [false, true]) {
 for (const mobile of [false, true]) {
  await escenario(`compatibilidad HTTP sin randomUUID: ${mobile ? 'móvil' : 'web'} ${noCrypto ? 'sin crypto' : 'getRandomValues'}`, async ({pagina, esperarSrc}) => {
   const beats=[];
   pagina.on('request', r=>{if(r.url().endsWith('/playback/heartbeat')) beats.push(r.postDataJSON());});
   await pagina.goto(`${base}/ver/I1`); await esperarSrc('/remux?audio=1');
   await pagina.waitForTimeout(5500);
   assert.equal(await pagina.getByText('Esta pantalla no se ha podido mostrar').count(),0);
   const ids=beats.filter(b=>b.state!=='stopped').map(b=>b.id);
   assert.ok(ids.length>=2);
   assert.equal(new Set(ids).size,1);
   assert.match(ids[0],/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
   assert.notEqual(await pagina.evaluate(()=>localStorage.getItem('velora.device-id')),null);
  }, {sinUUID:true,sinCrypto:noCrypto, viewport:mobile?{width:390,height:844}:{width:1280,height:720}});
 }
}

for (const mode of ['web','tv','mobile']) {
 await escenario(`identidad Velora: ${mode}`, async ({pagina})=>{
  await pagina.goto(`${base}/pelicula/I1`);
  await pagina.getByRole('heading',{level:1}).waitFor();
  const logo=pagina.locator('a[aria-label="VELORA, inicio"] img:visible');
  const sources=await logo.evaluateAll(imgs=>imgs.map(im=>im.getAttribute('src')));
  assert.ok(sources.some(s=>s.includes(mode==='mobile'?'velora-icon-64.png':'velora-logo.webp')));
  assert.ok((await pagina.locator('link[rel="icon"][sizes="32x32"]').getAttribute('href')).endsWith('velora-icon-32.png'));
  assert.ok(await pagina.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
 },{tv:mode==='tv',viewport:mode==='mobile'?{width:390,height:844}:{width:1280,height:720}});
}

for (const kind of ['movie', 'series']) {
 for (const tv of [false,true]) {
  await escenario(`cabecera compacta alineada: ${kind} ${tv ? 'TV' : 'web'}`, async ({pagina}) => {
   await pagina.goto(`${base}/${kind === 'movie' ? 'pelicula' : 'serie'}/I1`);
   const poster=pagina.getByRole('img',{name:'Carátula de Título I1'});
   await poster.waitFor();
   const logo=pagina.locator('h1 img');
   const a=await poster.boundingBox(), b=await logo.boundingBox();
   assert.ok(Math.abs(a.y-b.y)<2, 'el póster y logo comienzan a la misma altura');
   assert.ok(a.width>=(tv?144:208)-1, 'el póster mantiene el tamaño sin encogerse');
   assert.ok(Math.abs(a.width/a.height-2/3)<0.02, 'el póster conserva su proporción');
   assert.ok(await pagina.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth));
   await pagina.screenshot({path:`/tmp/velora-detail-${kind}-${tv?'tv':'web'}.png`,fullPage:true});
  },{tv,logo:true,identidad:{kind,has_poster:true,has_backdrop:true,overview:'Una aventura que cambiará para siempre la vida de sus protagonistas. Descubren un secreto mientras intentan proteger a su familia.',genres:['Drama','Aventura']}});
 }
}

await escenario('panel de actividad muestra usuario, episodio y métodos por pista', async ({ pagina }) => {
  await pagina.goto(`${base}/admin`);
  const panel = pagina.getByRole('region', {name: 'Reproduciendo ahora'});
  await panel.getByText('Marta', {exact:true}).waitFor();
  assert.equal(await panel.getByText('Merlín', {exact:true}).count(), 1);
  assert.equal(await panel.getByText('T1 · E2', {exact:true}).count(), 1);
  assert.ok((await panel.innerText()).includes('En pausa'));
  assert.ok((await panel.innerText()).includes('Reproducción directa'));
  await pagina.screenshot({path:'/tmp/velora-now-playing-desktop.png', fullPage:true});
}, {admin:true, activity:[{id:'player',username:'Marta',device:'Android TV',client:'android_tv',ip:'192.168.1.40',state:'paused',position_ms:80000,duration_ms:2500000,item:{...item('I1','Valiant'),kind:'episode',series_title:'Merlín',season_number:1,episode_number:2},method:'direct_play',video:'1080p · H264',audio:'spa · AAC · 2 canales',convert_video:false,convert_audio:false}]});

await escenario('presencia web envía señales y retira la sesión al salir', async ({ pagina, esperarSrc }) => {
  const beats=[];
  pagina.on('request', r=>{if(r.url().endsWith('/playback/heartbeat')) beats.push(r.postDataJSON());});
  await pagina.goto(`${base}/ver/I1`); await esperarSrc('/remux?audio=1');
  await pagina.waitForFunction(()=>!!document.querySelector('video'));
  await pagina.waitForTimeout(5500);
  assert.ok(beats.some(b=>b.file_id==='FI1' && b.state!=='stopped'));
  await pagina.evaluate(()=>window.dispatchEvent(new Event('pagehide')));
  await pagina.waitForTimeout(300);
  assert.ok(beats.some(b=>b.state==='stopped'));
});

for (const logo of [true, false]) {
  await escenario(`identidad del episodio agrupada bajo el logo: ${logo ? 'con' : 'sin'} logo`, async ({ pagina, esperarSrc }) => {
    await pagina.goto(`${base}/ver/I1`);
    await esperarSrc('/remux?audio=1');
    const cabecera = pagina.locator('[data-player-episode]');
    assert.equal(await cabecera.getByText('Temporada 1 · Episodio 2', { exact: true }).count(), 1);
    assert.equal(await cabecera.getByText('La llamada del dragón', { exact: true }).count(), 1);
    assert.equal(await pagina.locator('[data-player-controls]').last().innerText().then(t => t.includes('La llamada del dragón')), false);
    assert.equal(await pagina.getByText('Merlín', { exact: true }).count(), logo ? 0 : 1);
    if (logo) {
      const imagen = await pagina.getByRole('img', { name: /Logo de/ }).boundingBox();
      const texto = await cabecera.boundingBox();
      assert.ok(texto.y >= imagen.y + imagen.height, 'temporada y título debajo del logo');
    }
  }, { logo, identidad: { kind: 'episode', series_title: 'Merlín', title: 'La llamada del dragón', season_number: 1, episode_number: 2 } });
  await escenario(`identidad de película sin duplicar logo: ${logo ? 'con' : 'sin'} logo`, async ({ pagina, esperarSrc }) => {
    await pagina.goto(`${base}/ver/I1`);
    await esperarSrc('/remux?audio=1');
    assert.equal(await pagina.getByText('Título I1', { exact: true }).count(), logo ? 0 : 1);
  }, { logo, identidad: { kind: 'movie' } });
}

await escenario('pantalla completa real y opciones desde el menú común', async ({pagina, esperarSrc}) => {
  await pagina.goto(`${base}/ver/I1`);
  await esperarSrc('/remux?audio=1');
  await pagina.getByRole('button', {name:'Audio y subtítulos',exact:true}).click();
  await pagina.getByRole('button', {name:'Pantalla completa',exact:true}).click();
  await pagina.waitForFunction(() => document.fullscreenElement?.tagName === 'MAIN');
  await pagina.screenshot({path:'/tmp/velora-controles-web-final.png'});
  await pagina.getByRole('button', {name:'Audio y subtítulos',exact:true}).click();
  await pagina.getByRole('button', {name:'Salir de pantalla completa',exact:true}).click();
  await pagina.waitForFunction(() => !document.fullscreenElement);
});

await escenario('vídeo convertido busca exactamente sin calcular fotogramas clave del original', async ({pagina,esperarSrc,pedidas}) => {
  await pagina.goto(`${base}/ver/I1`);
  await esperarSrc('/transcode?ticket=mock');
  await reloj(pagina, 50, true);
  await pagina.locator('body').press('ArrowRight');
  await esperarSrc('&t=80.000');
  assert.equal(pedidas.some(p=>p.includes('/seek?')),false);
  await pagina.locator('body').press('s');
  await pagina.getByRole('button', {name:/Inglés/}).click();
  await esperarSrc('audio=2');
  assert.equal(pedidas.some(p=>p.includes('/seek?')),false);
}, {transcode:true});

await escenario('fallo real de decodificación: reempaquetar antes de convertir vídeo; sin bucles', async ({pagina,esperarSrc,pedidas}) => {
  await pagina.goto(`${base}/ver/I1`);
  await esperarSrc('/direct');
  const error=()=>pagina.locator('video').evaluate(v=>{Object.defineProperty(v,'error',{configurable:true,value:{code:3}});v.dispatchEvent(new Event('error'));});
  await error();await esperarSrc('/remux');
  await error();await esperarSrc('/transcode?ticket=mock');
  await error();await pagina.getByRole('button',{name:'Reintentar',exact:true}).waitFor();
  assert.equal(pedidas.filter(p=>p==='/api/v1/playback/decide').length,3);
}, {decoderFallback:true});
await escenario('un error de red no fuerza conversión de vídeo',async({pagina,esperarSrc,pedidas})=>{
  await pagina.goto(`${base}/ver/I1`);await esperarSrc('/direct');
  await pagina.locator('video').evaluate(v=>{Object.defineProperty(v,'error',{configurable:true,value:{code:2}});v.dispatchEvent(new Event('error'));});
  await pagina.getByRole('button',{name:'Reintentar',exact:true}).waitFor();
  assert.equal(pedidas.filter(p=>p==='/api/v1/playback/decide').length,1);
}, {decoderFallback:true});

await escenario('capítulos y subtítulos lentos (5 s) no retrasan el vídeo', async ({ pagina, retener, soltar, esperarSrc }) => {
  retener.add('/chapters');
  retener.add('/subtitles');
  const inicio = Date.now();
  await pagina.goto(`${base}/ver/I1`);
  await esperarSrc('/remux?audio=1');
  const ms = Date.now() - inicio;
  assert.ok(ms < 3000, `el vídeo debería pedirse sin esperar a las listas: ${ms} ms`);
  await new Promise((ok) => setTimeout(ok, 5000 - ms));
  await soltar('/chapters');
  await soltar('/subtitles');
});

await escenario('saltos con respuestas desordenadas: gana el último y el anterior se cancela', async ({ pagina, retener, soltar, src, esperarSrc, canceladas }) => {
  await pagina.goto(`${base}/ver/I1`);
  await esperarSrc('&t=0.000');
  retener.add('/seek');
  await pagina.locator('body').press('ArrowRight'); // t=30
  await pagina.waitForTimeout(200);
  await pagina.locator('body').press('ArrowLeft'); // t=0 (posición actual 0 − 10)
  await pagina.waitForTimeout(200);
  retener.clear();
  await soltar('t=0.000'); // responde antes el último
  await soltar('t=30.000'); // y después, tarde, el primero
  await pagina.waitForTimeout(500);
  assert.match(await src(), /&t=0\.000&n=2$/, 'el flujo debe ser el del último salto');
  assert.ok(canceladas.some((c) => c.includes('/seek?t=30.000')), `la consulta del salto superado debería cancelarse: ${canceladas}`);
});

await escenario('cambio de audio superado por un salto: no se aplica y su decisión se cancela', async ({ pagina, retener, soltar, src, esperarSrc, canceladas }) => {
  await pagina.goto(`${base}/ver/I1`);
  await esperarSrc('&t=0.000');
  retener.add('/playback/decide');
  await pagina.locator('body').press('s');
  await pagina.getByRole('button', { name: /Inglés/ }).click();
  await pagina.waitForTimeout(200);
  await pagina.locator('body').press('Escape');
  await pagina.locator('body').press('ArrowRight'); // salto a 30 con el audio actual
  await esperarSrc('&t=30.000');
  retener.clear();
  await soltar('/playback/decide'); // la decisión del cambio de audio llega tarde
  await pagina.waitForTimeout(500);
  assert.match(await src(), /remux\?audio=1&convert=false&t=30\.000/, 'debe seguir el audio 1 en el punto del salto');
  assert.ok(canceladas.some((c) => c.includes('/playback/decide')), `la decisión del cambio de audio debería cancelarse: ${canceladas}`);
});

await escenario('cambio de título con la preparación anterior pendiente: se cancela y no se aplica', async ({ pagina, retener, soltar, esperarSrc, canceladas }) => {
  retener.add('/items/I1');
  await pagina.goto(`${base}/ver/I1`);
  await pagina.waitForTimeout(300);
  await pagina.evaluate(() => {
    history.pushState({}, '', '/ver/I2');
    dispatchEvent(new PopStateEvent('popstate'));
  });
  await esperarSrc('/stream/files/FI2/remux');
  retener.clear();
  await soltar('/items/I1');
  await pagina.waitForTimeout(500);
  assert.ok(canceladas.some((c) => c.includes('/items/I1')), `la ficha anterior debería cancelarse: ${canceladas}`);
  assert.ok((await pagina.locator('video').getAttribute('src')).includes('FI2'), 'debe quedar el título nuevo');
  assert.ok(!(await pagina.content()).includes('Título I1'), 'no debe aparecer el título anterior');
});

// Reloj controlado: permite verificar la interfaz y las peticiones sin depender de códecs ni de una película real.
async function reloj(pagina, segundos, reproduciendo = false) {
  await pagina.locator('video').evaluate((v, { segundos, reproduciendo }) => {
    Object.defineProperty(v, 'currentTime', { configurable: true, value: segundos, writable: true });
    Object.defineProperty(v, 'paused', { configurable: true, value: !reproduciendo });
    v.dispatchEvent(new Event('timeupdate'));
    if (reproduciendo) v.dispatchEvent(new Event('playing'));
  }, { segundos, reproduciendo });
}
await escenario('tocar el vídeo solo muestra controles; pausa únicamente en su botón', async ({pagina, esperarSrc}) => {
  await pagina.goto(`${base}/ver/I1`);
  await esperarSrc('/remux?audio=1');
  await reloj(pagina, 20, true);
  await pagina.locator('video').evaluate(v => { window.pauseCalls = 0; v.pause = () => { window.pauseCalls++; }; });
  await pagina.waitForTimeout(3300);
  assert.equal(await pagina.locator('[data-player-buttons]').isVisible(), false);
  await pagina.locator('video').tap({position: {x: 180, y: 190}});
  assert.equal(await pagina.locator('[data-player-buttons]').isVisible(), true);
  await pagina.locator('video').tap({position: {x: 180, y: 190}});
  assert.equal(await pagina.evaluate(() => window.pauseCalls), 0);
  const botones = pagina.locator('[data-player-buttons] button');
  assert.deepEqual(await botones.evaluateAll(items => items.map(b => b.getAttribute('aria-label'))), ['Retroceder 10 segundos', 'Pausa', 'Avanzar 30 segundos', 'Audio y subtítulos']);
  await pagina.getByRole('button', {name: 'Pausa', exact: true}).tap();
  assert.equal(await pagina.evaluate(() => window.pauseCalls), 1);
}, {touch: true, viewport: {width: 390, height: 844}});

for (const mode of ['browser', 'first-tap', 'apk']) {
  await escenario(`pantalla completa móvil y restauración al salir: ${mode}`, async ({pagina, esperarSrc}) => {
    await pagina.goto(`${base}/ver/I1`);
    await esperarSrc('/remux?audio=1');
    assert.deepEqual(await pagina.evaluate(() => window.fullscreenCalls), [mode === 'apk' ? 'app-enter' : 'enter']);
    if (mode === 'first-tap') {
      await pagina.locator('video').tap({position: {x:180,y:190}});
      assert.deepEqual(await pagina.evaluate(() => window.fullscreenCalls), ['enter', 'enter']);
    }
    await pagina.getByRole('button', {name: 'Audio y subtítulos', exact:true}).tap();
    await pagina.getByRole('button', {name: 'Volver a la ficha', exact:true}).tap();
    await pagina.waitForTimeout(100);
    assert.equal(await pagina.evaluate(() => window.fullscreenCalls.at(-1)), mode === 'apk' ? 'app-exit' : 'exit');
  }, {touch:true, viewport:{width:390,height:844}, fullscreen:mode});
}

const marcas = [{ start: 10, end: 40, kind: 'intro', detected: true }, { start: 590, end: 600, kind: 'credits', detected: true }];
for (const directo of [true, false]) {
  await escenario(`intro independiente y salto correcto en ${directo ? 'Directo' : 'Optimizado'}`, async ({ pagina, esperarSrc, pedidas }) => {
    await pagina.goto(`${base}/ver/I1`);
    await esperarSrc(directo ? '/direct' : '&t=0.000');
    await reloj(pagina, 12, true);
    const aviso = pagina.getByRole('button', { name: 'Saltar intro', exact: true });
    await aviso.waitFor({ state: 'visible' });
    await pagina.waitForTimeout(3300);
    assert.equal(await pagina.getByRole('button', { name: 'Retroceder 10 segundos' }).isVisible(), false);
    assert.equal(await aviso.isVisible(), true, 'el aviso sobrevive al ocultado de controles');
    await aviso.focus();
    await aviso.press('Space');
    if (directo) assert.equal(await pagina.locator('video').evaluate(v => v.currentTime), 40);
    else { await esperarSrc('&t=40.000'); assert.ok(pedidas.some(p => p.includes('/seek?t=40.000'))); }
    assert.equal(await aviso.count(), 0);
    assert.equal(await pagina.locator('main').evaluate(el => el === document.activeElement), true);
  }, { directo, chapters: marcas });
}

await escenario('créditos marcados: aviso enfocado solo durante créditos y salto manual', async ({ pagina, esperarSrc, pedidas }) => {
  await pagina.goto(`${base}/ver/I1`);
  await esperarSrc('&t=0.000');
  await reloj(pagina, 575);
  assert.equal(await pagina.getByRole('region', { name: 'Siguiente episodio', exact: true }).count(), 0, 'no hay tarjeta anticipada que sustituya al aviso de créditos');
  assert.equal(await pagina.getByRole('button', { name: 'Siguiente episodio', exact: true }).count(), 0);
  await reloj(pagina, 592);
  const aviso = pagina.getByRole('button', { name: 'Siguiente episodio', exact: true });
  await aviso.waitFor({ state: 'visible' });
  await pagina.locator('body').press('s');
  assert.equal(await aviso.count(), 0, 'el panel tiene prioridad');
  await pagina.locator('body').press('Escape');
  await aviso.click();
  await esperarSrc('/FI2/remux');
  assert.equal(await pagina.locator('[data-player-buttons]').isVisible(), false, 'siguiente episodio entra sin controles');
  assert.ok(pedidas.includes('/api/v1/me/items/I1/played'));
  assert.ok(pedidas.includes('/api/v1/me/items/I1/progress'));
}, { chapters: marcas, next: true });

await escenario('créditos cortos: avance automático antes de que desaparezca la marca', async ({pagina,esperarSrc}) => {
 await pagina.goto(`${base}/ver/I1`);
 await esperarSrc('&t=0.000');
 await reloj(pagina,592,true);
 await pagina.getByRole('button',{name:'Siguiente episodio',exact:true}).waitFor();
 await reloj(pagina,598.8,true);
 await esperarSrc('/FI2/remux');
}, {chapters:marcas,next:true});

await escenario('sin siguiente: saltar créditos marca visto y vuelve a la ficha', async ({ pagina, esperarSrc, pedidas }) => {
  await pagina.goto(`${base}/ver/I1`);
  await esperarSrc('&t=0.000');
  await reloj(pagina, 592);
  await pagina.getByRole('button', { name: 'Saltar créditos', exact: true }).click();
  await pagina.waitForURL('**/ficha/I1');
  assert.ok(pedidas.includes('/api/v1/me/items/I1/progress'));
  assert.ok(pedidas.includes('/api/v1/me/items/I1/played'));
}, { chapters: marcas });

await escenario('cuenta atrás, foco inicial y cancelación local incluso al terminar', async ({ pagina, esperarSrc }) => {
  await pagina.goto(`${base}/ver/I1`);
  await esperarSrc('&t=0.000');
  await reloj(pagina, 571, true);
  const aviso = pagina.getByRole('button', { name: 'Siguiente episodio', exact: true });
  await aviso.waitFor({ state: 'visible' });
  assert.equal(await aviso.evaluate(el => el === document.activeElement), true);
  await aviso.press('ArrowLeft');
  const cancelar = pagina.getByRole('button', {name:'Cancelar avance automático'});
  assert.equal(await cancelar.evaluate(el => el === document.activeElement), true);
  await reloj(pagina, 574, true);
  assert.equal(await cancelar.evaluate(el => el === document.activeElement), true);
  await cancelar.press('Enter');
  await reloj(pagina, 590, true);
  await pagina.locator('video').evaluate(v => v.dispatchEvent(new Event('ended')));
  await pagina.waitForTimeout(100);
  assert.match(pagina.url(), /ver\/I1$/);
  await aviso.click();
  await esperarSrc('/FI2/remux');
}, { next: true });

await escenario('cuenta atrás avanza antes del final y se reinicia en el siguiente episodio', async ({pagina,esperarSrc}) => {
  await pagina.goto(`${base}/ver/I1`);
  await esperarSrc('&t=0.000');
  await reloj(pagina, 571, true);
  await pagina.getByRole('button', {name:'Siguiente episodio',exact:true}).waitFor();
  await reloj(pagina, 580, true);
  assert.match(pagina.url(), /ver\/I1$/);
  await reloj(pagina, 581, true);
  await esperarSrc('/FI2/remux');
}, {next:true});

for (const viewport of [{ width: 360, height: 640 }, { width: 640, height: 360 }, { width: 768, height: 1024 }, { width: 1440, height: 900 }]) {
  await escenario(`controles y aviso utilizables a ${viewport.width}×${viewport.height}`, async ({ pagina, esperarSrc }) => {
    await pagina.goto(`${base}/ver/I1`);
    await esperarSrc('&t=0.000');
    await reloj(pagina, 12);
    const aviso = pagina.getByRole('button', { name: 'Saltar intro', exact: true });
    await aviso.waitFor({ state: 'visible' });
    assert.equal(await pagina.locator('[data-player-buttons]').isVisible(), false, 'aviso exclusivo sin controles');
    const contextual = await aviso.boundingBox();
    assert.ok(contextual.x >= 0 && contextual.y >= 0 && contextual.x + contextual.width <= viewport.width && contextual.y + contextual.height <= viewport.height, 'aviso dentro de pantalla');
    if (viewport.width < 800) await aviso.tap(); else await aviso.click();
    await esperarSrc('&t=40.000');
    assert.equal(await pagina.locator('[data-player-buttons]').isVisible(), false, 'pulsar aviso no muestra controles');
    await reloj(pagina, 50, true);
    await pagina.locator('video').dispatchEvent('pointerdown', {pointerType:'touch'});
    assert.equal(await pagina.locator('[data-player-buttons]').isVisible(), true, 'un toque posterior permite mostrar controles');
    for (const boton of await pagina.locator('[data-player-buttons] button').all()) {
      const caja = await boton.boundingBox();
      assert.ok(caja.x >= 0 && caja.x + caja.width <= viewport.width && caja.y >= 0 && caja.y + caja.height <= viewport.height);
    }
  }, { chapters: marcas, viewport, touch: viewport.width < 800 });
}

await escenario('aviso y subtítulos sin controles ni logo; activarlo mantiene ocultos los controles', async ({pagina, esperarSrc}) => {
  await pagina.goto(`${base}/ver/I1?sub=S1`);
  await esperarSrc('&t=0.000');
  await reloj(pagina, 12, true);
  const aviso = pagina.getByRole('button', {name:'Saltar intro',exact:true});
  const texto = pagina.getByText('Primera línea del subtítulo', {exact:false});
  await texto.waitFor({state:'visible'});
  await aviso.waitFor({state:'visible'});
  await pagina.waitForTimeout(300);
  assert.equal(await pagina.getByRole('img', {name:/Logo de/}).isVisible(), false);
  assert.equal(await pagina.locator('[data-player-buttons]').isVisible(), false);
  const sub = await texto.boundingBox(), boton = await aviso.boundingBox();
  assert.ok(sub.y + sub.height <= boton.y || boton.y + boton.height <= sub.y || sub.x + sub.width <= boton.x || boton.x + boton.width <= sub.x, 'subtítulo y aviso no se pisan');
  await aviso.tap();
  await esperarSrc('&t=40.000');
  await reloj(pagina, 41, true);
  await pagina.waitForTimeout(200);
  assert.equal(await pagina.locator('[data-player-buttons]').isVisible(), false);
}, {chapters:marcas, subtitulos:true, logo:true, viewport:{width:640,height:360},touch:true});

await escenario('subtítulos lentos se preparan en segundo plano sin aviso', async ({pagina,retener,soltar,esperarSrc}) => {
  retener.add('/subtitles/S1');
  await pagina.goto(`${base}/ver/I1?sub=S1`);
  await esperarSrc('/remux?audio=1');
  await pagina.waitForTimeout(500);
  assert.equal(await pagina.getByText(/Preparando subtítulos/).count(), 0);
  await reloj(pagina, 12, true);
  retener.clear();
  await soltar('/subtitles/S1');
  await pagina.getByText('Primera línea del subtítulo', {exact:false}).waitFor();
  assert.equal(await pagina.getByText(/Preparando subtítulos/).count(), 0);
}, {subtitulos:true});

await escenario('los subtítulos aparecen sin esperar a completar la descarga', async ({pagina,esperarSrc}) => {
 await pagina.addInitScript(() => {
  const original = window.fetch;
  window.fetch = (input,init) => {
   if (String(input).endsWith('/subtitles/S1')) {
    const stream = new ReadableStream({start(controller) {
     controller.enqueue(new TextEncoder().encode('WEBVTT\n\n00:00:10.000 --> 00:00:40.000\nFrase progresiva\n\n'));
     window.terminarSubtitulos = () => controller.close();
    }});
    return Promise.resolve(new Response(stream,{headers:{'Content-Type':'text/vtt'}}));
   }
   return original(input,init);
  };
 });
 await pagina.goto(`${base}/ver/I1?sub=S1`);
 await esperarSrc('&t=0.000');
 await reloj(pagina,12,true);
 await pagina.getByText('Frase progresiva',{exact:true}).waitFor({state:'visible'});
 await pagina.evaluate(() => window.terminarSubtitulos());
}, {subtitulos:true});

await escenario('forzados españoles automáticos y desactivación explícita', async ({pagina,pedidas}) => {
 await pagina.goto(`${base}/ver/I1`);
 await pagina.waitForFunction(()=>!!document.querySelector('video')?.getAttribute('src'));
 for(let i=0;i<60&&!pedidas.some(p=>p.endsWith('/subtitles/S1'));i++) await pagina.waitForTimeout(50);
 assert.ok(pedidas.some(p=>p.endsWith('/subtitles/S1')),'se solicita el forzado español');
 pedidas.length=0;
 await pagina.goto(`${base}/ver/I1?sub=off`);
 await pagina.waitForFunction(()=>!!document.querySelector('video')?.getAttribute('src'));
 await pagina.waitForTimeout(400);
 assert.equal(pedidas.some(p=>p.endsWith('/subtitles/S1')),false,'se respeta desactivación del usuario');
}, {subtitulos:true,forzados:true});

servidor.close();
console.log(`reproductor-e2e: ${correctos} escenarios correctos`);
