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

const kinds=['poster','art','backdrop','banner','box','boxrear','disc','logo','menu','thumb'];
const slots=kinds.map(kind=>({kind,source:''}));
const image=(provider,kind)=>({provider,kind,file_path:`https://assets.fanart.tv/${provider}-${kind}.png`,width:800,height:310,language:'es'});
const sample={id:'I1',kind:'movie',library_id:'L',title:'Película de prueba',year:2020,position_ms:0,played:false,has_poster:false,has_backdrop:false,runtime_ms:600000};
try{
 for(const viewport of [{width:1440,height:900},{width:390,height:844}]){
  const page=await browser.newPage({viewport});let selected;const errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  let configs=['fanart','tvdb','omdb'].map(id=>({id,enabled:false,configured:false}));
  await page.route('**/api/v1/**',async route=>{
   const u=new URL(route.request().url());let data={};
   if(u.pathname==='/api/v1/system/info')data={setup_required:false};
   else if(u.pathname==='/api/v1/me')data={user:{id:'U',username:'u',display_name:'U',is_admin:true,can_play:true}};
   else if(u.pathname==='/api/v1/catalog/revision')data={revision:'1'};
   else if(u.pathname==='/api/v1/libraries')data={libraries:[{id:'L',name:'Películas',kind:'movies',in_menu:true}]};
   else if(u.pathname==='/api/v1/home')data={featured:[],continue:[],rows:[{library:{id:'L',name:'Películas'},items:[sample]}]};
   else if(u.pathname.endsWith('/images/preview')||u.pathname.endsWith('/metadata/thumb')||u.pathname.startsWith('/api/v1/items/I1/images/'))return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="800" height="310"><rect width="800" height="310" fill="#adc"/></svg>'});
   else if(u.pathname==='/api/v1/items/I1')data={item:sample};
   else if(u.pathname==='/api/v1/admin/items/I1/images/gallery')data={identified:true,gallery:{posters:[],backdrops:[],stills:[],logos:[{file_path:'/logo.png',width:800,height:310,language:'es'}]},external:[image('fanart','logo'),image('tvdb','logo'),image('fanart','disc'),image('fanart','art')],warnings:['OMDb no está disponible; las otras fuentes siguen disponibles.']};
   else if(u.pathname==='/api/v1/admin/items/I1/images/select'){selected=route.request().postDataJSON();slots.find(s=>s.kind===selected.kind).source='user';data={slots,image_rev:'2'};}
   else if(u.pathname==='/api/v1/admin/items/I1/images')data={slots,image_rev:'1',limits:{max_bytes:10000000,max_side:6000,max_pixels:40000000}};
   else if(u.pathname==='/api/v1/admin/metadata/image-providers')data={providers:configs};
   else if(u.pathname.startsWith('/api/v1/admin/metadata/image-providers/')){const id=u.pathname.split('/').pop();const body=route.request().postDataJSON();configs=configs.map(p=>p.id===id?{...p,configured:!!body.api_key,enabled:body.enabled}:p);data={providers:configs};}
   else if(u.pathname==='/api/v1/admin/metadata/tmdb')data={configured:true};
   else if(u.pathname==='/api/v1/admin/metadata/opensubtitles')data={configured:false};
   await route.fulfill({contentType:'application/json',body:JSON.stringify(data)});
  });
  await page.goto(base);
  await page.getByRole('button',{name:'Más opciones de Película de prueba'}).click();
  await page.getByRole('menuitem',{name:'Editar imágenes…'}).click();
  const dialog=page.getByRole('dialog',{name:/Editar imágenes/});
  await dialog.getByRole('combobox',{name:'Tipo de imagen'}).selectOption('logo');
  await dialog.getByRole('combobox',{name:'Fuente'}).selectOption('all');
  assert.equal(await dialog.getByRole('combobox',{name:'Tipo de imagen'}).locator('option').count(),10);
  assert.equal(await dialog.getByRole('combobox',{name:'Fuente'}).locator('option').count(),5);
  assert.equal(await dialog.getByRole('button',{name:/Ver logo de/}).count(),3,'combina TMDB y fuentes externas aunque otra falle');
  await dialog.getByRole('combobox',{name:'Fuente'}).selectOption('fanart');
  assert.equal(await dialog.getByRole('button',{name:/Ver logo de/}).count(),1);
  await dialog.getByRole('combobox',{name:'Tipo de imagen'}).selectOption('disc');
  await dialog.getByRole('button',{name:/Ver disco de/}).click();
  await dialog.getByRole('button',{name:'Usar como disco'}).click();
  await dialog.getByText('Imagen elegida.',{exact:false}).waitFor();
  assert.equal(selected.provider,'fanart');assert.equal(selected.kind,'disc');
  await page.screenshot({path:`/tmp/velora-fuentes-${viewport.width}.png`});
  await dialog.getByRole('button',{name:'Cerrar',exact:true}).last().click();
  await page.goto(base+'/admin/metadatos');
  await page.getByRole('heading',{name:'Fichas y carátulas · Fuentes de imágenes',exact:true}).waitFor();
  const fanart=page.locator('form').filter({has:page.getByRole('heading',{name:'Fanart.tv',exact:true})});
  await fanart.getByLabel('Activar fuente').check();
  await fanart.getByLabel('Clave API',{exact:true}).fill('clave-de-prueba');
  await fanart.getByRole('button',{name:'Comprobar y guardar'}).click();
  await fanart.getByText('Configuración guardada.').waitFor();
  assert.equal(await fanart.getByLabel('Clave API (guardada; deja vacío para conservarla)').inputValue(),'');
  assert.deepEqual(errors,[]);await page.close();console.log(`PASS editor y configuración a ${viewport.width}px: fuentes, diez tipos, fallo parcial y elección manual`);
 }
}finally{await browser.close();await new Promise(ok=>server.close(ok));}
