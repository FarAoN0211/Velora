import assert from 'node:assert/strict';
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
const require=createRequire(path.join(process.env.VELORA_MODULOS||'/tmp/velora-qa/node_modules','qa.js'));
const {chromium}=require('playwright');
const dist=fileURLToPath(new URL('../dist',import.meta.url));
const nativeNav=fs.readFileSync(fileURLToPath(new URL('../../clientes/android/app/src/main/assets/navegacion.js',import.meta.url)),'utf8');
const server=http.createServer((req,res)=>{const p=path.join(dist,new URL(req.url,'http://x').pathname);const f=fs.existsSync(p)&&fs.statSync(p).isFile()?p:path.join(dist,'index.html');res.setHeader('Content-Type',({'.js':'text/javascript','.css':'text/css','.html':'text/html','.woff2':'font/woff2'})[path.extname(f)]||'application/octet-stream');fs.createReadStream(f).pipe(res);});
await new Promise(ok=>server.listen(0,'127.0.0.1',ok));
const base=`http://127.0.0.1:${server.address().port}`;
const browser=await chromium.launch({executablePath:process.env.VELORA_CHROME||'/usr/bin/google-chrome',args:['--no-sandbox']});
const libraries=Array.from({length:25},(_,i)=>({id:`L${i+1}`,name:`Biblioteca ${i+1}`,kind:i%2?'tv':'movies',in_menu:true,status:'available',sort_order:i}));
libraries.push({id:'oculta',name:'Oculta',kind:'movies',in_menu:false},{id:'retirada',name:'Retirada',kind:'movies',in_menu:true,retired_at:'2026-01-01'});
let count=0;
async function pageFor({admin=true,app=false,tv=false,viewport={width:1440,height:900},bridge=false}={}) {
 const page=await browser.newPage({viewport,...(app?{userAgent:`Mozilla/5.0 Android VeloraCliente${tv?' VeloraTV':''}`}:{})});
 const errors=[],adminRequests=[];
 page.on('pageerror',e=>errors.push(e.message));
 if(app||bridge)await page.addInitScript(({nav,tv,bridge})=>{window.__VELORA_TV=tv;if(bridge)window.VeloraApp={mantenerPantalla(){}};window.addEventListener('DOMContentLoaded',()=>{(0,eval)(nav);});},{nav:nativeNav,tv,bridge});
 await page.route('**/api/v1/**',async route=>{
  const u=new URL(route.request().url());let body={};
  if(u.pathname.startsWith('/api/v1/admin/'))adminRequests.push(u.pathname);
  if(u.pathname==='/api/v1/system/info')body={name:'Velora',setup_required:false};
  else if(u.pathname==='/api/v1/me')body={user:{id:'U',username:'marta',display_name:'Marta',is_admin:admin,can_play:true}};
  else if(u.pathname==='/api/v1/libraries')body={libraries};
  else if(u.pathname==='/api/v1/home')body={featured:[],continue:[],rows:[]};
  else if(u.pathname==='/api/v1/items')body={items:[],total:0};
  else if(u.pathname==='/api/v1/genres')body={genres:[]};
  else if(u.pathname==='/api/v1/admin/overview')body={version:'test',counts:{libraries:25,movies:0,files:0,size_bytes:0,users:1,pending_genres:0,pending_items:0},optimized_streams:0,max_optimized_streams:2,scans:[]};
  else if(u.pathname==='/api/v1/admin/playback')body={audio_language:'es-ES'};
  await route.fulfill({contentType:'application/json',body:JSON.stringify(body)});
 });
 return {page,errors,adminRequests};
}
try {
 for(const options of [{admin:true},{admin:false},{app:true},{app:true,viewport:{width:390,height:844}},{admin:false,app:true,viewport:{width:390,height:844}},{app:true,tv:true,viewport:{width:960,height:540}},{bridge:true}]){
  const {page,errors,adminRequests}=await pageFor(options);
  await page.goto(base);
  // Solo uno de los perfiles de barra/lateral es visible en cada composición.
  const visibleProfile=page.locator('button[aria-haspopup="menu"]:visible');
  await visibleProfile.click();
  const allowed=options.admin!==false&&!options.tv;
  assert.equal(await page.getByRole('menuitem',{name:'Panel de control'}).count(),allowed?1:0);
  assert.equal(await page.locator('aside nav a[href="/admin"]').count(),0,'el lateral no tiene administración');
  if(allowed){await page.getByRole('menuitem',{name:'Panel de control'}).click();await page.getByRole('heading',{name:'Resumen',exact:true}).waitFor();}
  else {await page.goto(`${base}/admin`);await page.waitForURL(base+'/');assert.deepEqual(adminRequests,[],'un enlace directo no abre administración en TV/perfil normal');}
  assert.deepEqual(errors,[]);await page.close();count++;console.log(`PASS perfil ${JSON.stringify(options)}`);
 }
 for(const app of [false,true]){
  const {page,errors}=await pageFor({app,viewport:{width:390,height:844}});
  await page.goto(base);
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,'cabecera móvil sin desbordamiento');
  assert.equal(await page.locator('nav[aria-label="Principal"]:visible').count(),0,'sin barra inferior');
  const trigger=page.getByRole('button',{name:'Abrir menú de bibliotecas'});
  await trigger.click();
  const menu=page.getByRole('dialog',{name:'Menú de bibliotecas'});
  await menu.waitFor();
  const nav=menu.getByRole('navigation',{name:'Bibliotecas'});
  assert.equal(await nav.getByRole('link').count(),27,'Inicio, Buscar y todas las bibliotecas');
  assert.equal(await nav.getByText('Oculta',{exact:true}).count(),0);
  assert.equal(await nav.getByText('Retirada',{exact:true}).count(),0);
  assert.equal(await page.evaluate(()=>document.body.style.overflow),'hidden');
  assert.equal(await nav.evaluate(el=>el.scrollHeight>el.clientHeight),true);
  await menu.getByRole('link',{name:'Biblioteca 25',exact:true}).scrollIntoViewIfNeeded();
  await page.screenshot({path:`/tmp/velora-menu-${app?'apk':'web'}.png`});
  await menu.getByRole('link',{name:'Biblioteca 25',exact:true}).click();
  await page.waitForURL('**/biblioteca/L25');assert.equal(await menu.count(),0);
  assert.equal(await page.evaluate(()=>document.body.style.overflow),'');
  await trigger.click();
  await page.keyboard.press('Shift+Tab');
  assert.equal(await page.evaluate(()=>document.activeElement.closest('dialog')!==null),true,'foco contenido en el menú modal');
  if(app)assert.equal(await page.evaluate(()=>window.__veloraAtras()),true);
  else await page.keyboard.press('Escape');
  assert.equal(await menu.count(),0);assert.equal(await trigger.evaluate(el=>el===document.activeElement),true);
  await trigger.click();await page.mouse.click(380,400);assert.equal(await menu.count(),0,'cerrar al tocar fuera');
  await page.setViewportSize({width:844,height:390});await trigger.click();
  assert.equal(await nav.evaluate(el=>el.scrollHeight>el.clientHeight),true);
  await page.setViewportSize({width:1280,height:800});await menu.waitFor({state:'detached'});
  assert.equal(await page.evaluate(()=>document.body.style.overflow),'');
  assert.equal(await page.locator('nav[aria-label="Principal"]:visible').count(),1);
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
  assert.deepEqual(errors,[]);await page.close();count++;console.log(`PASS móvil ${app?'APK':'web'}: scroll, 25 bibliotecas, cerrar, Atrás y giro`);
 }
 console.log(`navegacion-e2e: ${count} escenarios correctos`);
} finally {await browser.close();await new Promise(ok=>server.close(ok));}
