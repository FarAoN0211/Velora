import assert from 'node:assert/strict';
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
const require = createRequire(process.env.VELORA_MODULOS ? path.join(process.env.VELORA_MODULOS, 'qa.js') : import.meta.url);
const { chromium } = require('playwright');
const dist = fileURLToPath(new URL('../dist', import.meta.url));
const server = http.createServer((req, res) => {
 const file = path.join(dist, decodeURIComponent(new URL(req.url, 'http://x').pathname));
 const target = fs.existsSync(file) && fs.statSync(file).isFile() ? file : path.join(dist, 'index.html');
 res.setHeader('Content-Type', ({'.js':'text/javascript','.css':'text/css','.html':'text/html','.woff2':'font/woff2'})[path.extname(target)] || 'application/octet-stream');fs.createReadStream(target).pipe(res);
});
await new Promise(ok => server.listen(0, '127.0.0.1', ok));
const base = `http://127.0.0.1:${server.address().port}`;
const movie = {id:'M',kind:'movie',library_id:'L',title:'Resident Evil',original_title:'Resident Evil',year:2002,has_poster:true,has_backdrop:true,has_logo:true,image_rev:'v1',runtime_ms:6000000,position_ms:0,played:false,quality:'1080p',official_rating:'16',community_rating:7.2,overview:'Una agente debe detener la propagación de un virus en un laboratorio subterráneo.',tagline:'Un experimento fuera de control.',genres:['Ciencia ficción','Acción'],provider_ids:{tmdb:'1576'},extras:[],presentation:{credits:{cast:[{id:1,name:'Milla Jovovich',character:'Alice',profile_path:'/test.jpg'},{id:2,name:'Michelle Rodriguez',character:'Rain',profile_path:'/test2.jpg'}],crew:[{id:3,name:'Paul W. S. Anderson',job:'Director'}]},production_companies:[{name:'Constantin Film'}],production_countries:[{name:'Alemania'}],original_language:'en',status:'Released'},files:[{id:'F',container:'mkv',size_bytes:4000000000,duration_ms:6000000,missing:false,version_label:null,streams:[{index:0,kind:'video',codec:'h264',width:1920,height:1080,bit_depth:8,hdr:'sdr',default:true},{index:1,kind:'audio',codec:'ac3',language:'spa',channels:6,default:true},{index:2,kind:'subtitle',codec:'subrip',language:'eng',default:false}]}]};
const browser = await chromium.launch({executablePath:process.env.VELORA_CHROME || undefined, args:['--no-sandbox']});
try {
 for (const [name, viewport] of [['desktop',{width:1440,height:1000}],['mobile',{width:390,height:844}],['tv',{width:960,height:540}]]) {
  const page=await browser.newPage({viewport,userAgent:name==='tv'?'Mozilla/5.0 VeloraCliente VeloraTV':undefined});const errors=[];page.on('pageerror',e=>errors.push(e.message));const requests=[];
  await page.route('**/api/v1/**',async route=>{
   const u=new URL(route.request().url());requests.push(u.pathname+u.search);let data={};
   if(u.pathname.includes('/images/') || u.pathname.includes('/people/')) return route.fulfill({contentType:'image/svg+xml',body:`<svg xmlns="http://www.w3.org/2000/svg" width="400" height="240"><rect width="400" height="240" fill="#172734"/><text x="20" y="120" fill="#dceef1" font-size="30">RESIDENT EVIL</text></svg>`});
   if(u.pathname==='/api/v1/system/info')data={setup_required:false,name:'Velora',version:'test'};
   else if(u.pathname==='/api/v1/me')data={user:{id:'U',display_name:'Pruebas',username:'user',is_admin:false,can_play:true}};
   else if(u.pathname==='/api/v1/libraries')data={libraries:[{id:'L',name:'Películas',kind:'movies',in_menu:true,sort_order:1}]};
   else if(u.pathname.endsWith('/subtitles'))data={subtitles:[{id:'i2',label:'Español · Descriptivos (SDH/CC)',language:'es',type:'sdh',codec:'subrip',supported:true,hearing_impaired:true,external:false,default:false,forced:false}]};
   else if(u.pathname.endsWith('/chapters'))data={chapters:[]};
   else if(u.pathname==='/api/v1/playback/decide')data={method:'direct_play',url:'/api/v1/stream/files/F',duration_ms:6000000,audio_index:1,summary:'Reproducción directa',reasons:[]};
   else if(u.pathname==='/api/v1/stream/files/F')return;
   else if(u.pathname==='/api/v1/items' && u.searchParams.has('person'))data={items:[movie,{...movie,id:'S',title:'Serie de prueba',kind:'series'}]};
   else if(u.pathname.endsWith('/subtitles/i2'))return route.fulfill({contentType:'text/vtt',body:'WEBVTT\n\n00:00:00.000 --> 00:00:05.000\nSubtítulo seleccionado\n'});
   else if(u.pathname==='/api/v1/items/S')data={item:{...movie,id:'S',kind:'series',title:'Serie de prueba',files:[],seasons:[{id:'T',season_number:1,title:'Temporada 1'}],next_up:{id:'E1',season_number:1}},tmdb_attribution:true};
   else if(u.pathname==='/api/v1/items/T')data={item:{...movie,id:'T',kind:'season',episodes:[{...movie,id:'E1',title:'Episodio uno',kind:'episode',episode_number:1,file_id:'F1'},{...movie,id:'E2',title:'Episodio dos',kind:'episode',episode_number:2,file_id:'F2'}]}};
   else if(u.pathname==='/api/v1/items/E1'||u.pathname==='/api/v1/items/E2'){const n=u.pathname.endsWith('E1')?'1':'2';data={item:{...movie,id:'E'+n,kind:'episode',files:[{...movie.files[0],id:'F'+n}]}};}
   else if(u.pathname==='/api/v1/items/M')data={item:movie,tmdb_attribution:true};
   return route.fulfill({contentType:'application/json',body:JSON.stringify(data)});
  });
  await page.goto(`${base}/pelicula/M`);await page.getByRole('heading',{name:'Reparto y equipo técnico'}).waitFor();
  assert.ok(await page.getByText('Milla Jovovich',{exact:true}).count());assert.ok(await page.getByText('Constantin Film',{exact:true}).count());
  assert.ok(requests.some(r=>r.includes('/images/poster?w=320')) || requests.some(r=>r.includes('/images/backdrop?w=1280')));
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,`${name}: horizontal overflow`);
  const block=page.getByRole('region',{name:'Archivo y reproducción'}); const cast=page.getByRole('region',{name:'Reparto y equipo técnico',exact:true});
  assert.ok(await block.evaluate((node,other)=>!!(node.compareDocumentPosition(other)&Node.DOCUMENT_POSITION_FOLLOWING),await cast.elementHandle()));
  assert.equal(await page.getByText('En este dispositivo (estimación)',{exact:true}).count(),0);
  await page.getByLabel('Subtítulos',{exact:true}).selectOption('i2');
  await page.screenshot({path:`/tmp/velora-ficha-${name}.png`,fullPage:true});
  await page.getByRole('button',{name:'Reproducir',exact:true}).click(); await page.waitForURL('**/ver/M?**'); assert.equal(new URL(page.url()).searchParams.get('sub'),'i2'); await page.waitForFunction(()=>true); for(let retry=0;retry<50&&!requests.some(r=>r.endsWith('/subtitles/i2'));retry++)await new Promise(ok=>setTimeout(ok,50)); assert.ok(requests.some(r=>r.endsWith('/subtitles/i2'))); await page.getByAltText('Logo de Resident Evil').first().waitFor();
  await page.evaluate(()=>{const video=document.querySelector('video'); Object.defineProperty(video,'paused',{configurable:true,get:()=>false}); video.dispatchEvent(new Event('playing'));});
  await page.waitForFunction(()=>{const logo=document.querySelector('img[alt="Logo de Resident Evil"]');return logo && getComputedStyle(logo.parentElement).opacity==='0';});
  assert.equal(await page.getByAltText('Logo de Resident Evil').count(),1);
  await page.locator('main').dispatchEvent('pointermove',{pointerType:'mouse'});
  await page.waitForFunction(()=>{const logo=document.querySelector('img[alt="Logo de Resident Evil"]');return logo && getComputedStyle(logo.parentElement).opacity==='1';});
  await page.goto(`${base}/pelicula/M`); await page.getByRole('link',{name:/Milla Jovovich/}).click(); await page.getByRole('heading',{name:'En tus bibliotecas'}).waitFor(); await page.locator('a[href="/pelicula/M"]').first().waitFor(); await page.locator('a[href="/serie/S"]').first().waitFor();
  await page.locator('a[href="/serie/S"]').first().click();
  await page.getByLabel('Episodio del archivo').selectOption('E2');
  await page.getByRole('button',{name:'Reproducir este episodio'}).waitFor();
  await page.getByLabel('Subtítulos',{exact:true}).selectOption('i2');
  await page.getByRole('button',{name:'Reproducir este episodio'}).click(); await page.waitForURL('**/ver/E2?**');
  assert.equal(new URL(page.url()).searchParams.get('file'),'F2'); assert.equal(new URL(page.url()).searchParams.get('sub'),'i2');
  assert.equal(errors.length,0,errors.join('\n'));console.log(`PASS ${name}: película/serie, opciones, subtítulos, reparto, permisos de navegación y logo sincronizado`);await page.close();
 }
} finally {await browser.close();await new Promise(ok=>server.close(ok));}
