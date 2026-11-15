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

const library={id:'L1',name:'Películas',kind:'movies',in_menu:true,status:'available',view_sort:'title'};
const card=(id,title)=>({id,title,kind:'movie',library_id:'L1',year:2020,has_poster:false,has_backdrop:false,has_logo:false,position_ms:0,played:false,added_at:'2026-01-01',runtime_ms:600000});
const old=Array.from({length:260},(_,i)=>card('I'+i,'Prueba '+i));
let revision=0;
async function setup(app=false){
 const page=await browser.newPage({viewport:{width:1280,height:800},...(app?{userAgent:'Mozilla/5.0 Android VeloraCliente VeloraTV'}:{})});
 const errors=[],requests=[];
 page.on('pageerror',e=>errors.push(e.message));
 await page.route('**/api/v1/**',async route=>{
  const u=new URL(route.request().url());requests.push(u.pathname+u.search);let data={};
  if(u.pathname==='/api/v1/me')data={user:{id:'U',username:'u',display_name:'U',is_admin:false,can_play:true}};
  else if(u.pathname==='/api/v1/system/info')data={name:'Velora',setup_required:false};
  else if(u.pathname==='/api/v1/libraries')data={libraries:[library]};
  else if(u.pathname==='/api/v1/catalog/revision')data={revision:String(revision)};
  else if(u.pathname==='/api/v1/home')data={featured:[],continue:[],rows:revision?[{library,items:[card('new','Prueba nueva')]}]:[]};
  else if(u.pathname==='/api/v1/genres')data={genres:[{name:'Drama',count:260}]};
  else if(u.pathname==='/api/v1/items'){const list=revision?[card('new','Prueba nueva'),...old]:old;const offset=Number(u.searchParams.get('offset')||0),limit=Number(u.searchParams.get('limit')||120);data={items:list.slice(offset,offset+limit)};}
  await route.fulfill({contentType:'application/json',body:JSON.stringify(data)});
 });
 return {page,errors,requests};
}
try{
 for(const app of [false,true]){
  revision=0;const {page,errors,requests}=await setup(app);
  await page.goto(base);
  await page.waitForTimeout(300);
  const initial=requests.filter(r=>r==='/api/v1/home').length;
  const profile=page.getByRole('button',{name:/Menú de U/});await profile.focus();
  revision=1;
  await page.getByText('Prueba nueva',{exact:true}).first().waitFor({timeout:6000});
  assert.equal(page.url(),base+'/');assert.equal(await profile.evaluate(el=>el===document.activeElement),true);
  const loaded=requests.filter(r=>r==='/api/v1/home').length;
  assert.ok(loaded>initial);await page.waitForTimeout(2300);
  assert.equal(requests.filter(r=>r==='/api/v1/home').length,loaded,'sin cambios no vuelve a descargar el inicio');
  assert.deepEqual(errors,[]);await page.close();console.log(`PASS inicio en vivo ${app?'TV':'web'} sin navegación ni pérdida de foco`);
 }
 revision=0;const {page,errors,requests}=await setup();
 const url=base+'/biblioteca/L1?q=Prueba&genero=Drama&estado=unplayed&orden=title';
 await page.goto(url);await page.locator('[data-tv-tarjeta]').nth(119).waitFor();
 await page.getByRole('button',{name:'Cargar más'}).click();await page.locator('[data-tv-tarjeta]').nth(239).waitFor();
 revision=2;await page.getByText('Prueba nueva',{exact:true}).first().waitFor({timeout:6000});
 assert.equal(await page.locator('[data-tv-tarjeta]').count(),240,'conserva las páginas cargadas');
 assert.equal(page.url(),url,'conserva filtros y biblioteca');
 assert.ok(requests.filter(r=>r.startsWith('/api/v1/items?')).every(r=>r.includes('library_id=L1')&&r.includes('genre=Drama')&&r.includes('played=unplayed')&&r.includes('q=Prueba')));
 assert.deepEqual(errors,[]);await page.close();console.log('PASS biblioteca en vivo: conserva filtros y 240 títulos cargados');
}finally{await browser.close();await new Promise(ok=>server.close(ok));}
