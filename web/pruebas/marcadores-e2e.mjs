import assert from 'node:assert/strict';
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
const require=createRequire(process.env.VELORA_MODULOS?path.join(process.env.VELORA_MODULOS,'qa.js'):import.meta.url);
const {chromium}=require('playwright');
const dist=fileURLToPath(new URL('../dist',import.meta.url));
const server=http.createServer((req,res)=>{const name=path.join(dist,new URL(req.url,'http://x').pathname);const f=fs.existsSync(name)&&fs.statSync(name).isFile()?name:path.join(dist,'index.html');res.setHeader('Content-Type',({'.js':'text/javascript','.css':'text/css','.html':'text/html','.woff2':'font/woff2'})[path.extname(f)]||'application/octet-stream');fs.createReadStream(f).pipe(res);});
await new Promise(ok=>server.listen(0,'127.0.0.1',ok));
const browser=await chromium.launch({executablePath:process.env.VELORA_CHROME||undefined,args:['--no-sandbox']});
try{
 for(const [name,viewport] of [['desktop',{width:1440,height:900}],['mobile',{width:390,height:844}],['tv',{width:960,height:540}]]){
  const page=await browser.newPage({viewport});let status={total:100,done:75,pending:25,intros:70,credits:65,paused:false,playback:false,running:true};const methods=[];const errors=[];let scans=0;page.on('pageerror',e=>errors.push(e.message));
  await page.route('**/api/v1/**',async route=>{const u=new URL(route.request().url());let data={};
   if(u.pathname==='/api/v1/system/info')data={setup_required:false};
   else if(u.pathname==='/api/v1/me')data={user:{id:'U',username:'admin',display_name:'Admin',is_admin:true}};
   else if(u.pathname==='/api/v1/libraries')data={libraries:[]};
   else if(u.pathname==='/api/v1/admin/scans'){scans++;data={scans:{}};}
   else if(u.pathname==='/api/v1/admin/markers'){const m=route.request().method();methods.push(m);if(m==='DELETE')status={...status,paused:true,running:false};if(m==='POST')status={...status,paused:false,running:true};data=status;}
   await route.fulfill({contentType:'application/json',body:JSON.stringify(data)});
  });
  await page.goto(`http://127.0.0.1:${server.address().port}/admin/bibliotecas`);
  const panel=page.getByRole('region',{name:'Detección de intros y créditos'});await panel.getByText('75 de 100 episodios analizados · 25 pendientes').waitFor();
  await panel.getByRole('button',{name:'Detener detección'}).click();await panel.getByText('Detenido manualmente.').waitFor();
  await panel.getByRole('button',{name:'Reanudar detección'}).click();await panel.getByText('Analizando episodios…').waitFor();assert.ok(methods.includes('DELETE')&&methods.includes('POST'));
  await page.waitForTimeout(300);assert.equal(scans,1,'los cambios de estado no reinician el sondeo del escaneo');
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);assert.deepEqual(errors,[]);
  await page.screenshot({path:`/tmp/velora-marcadores-${name}.png`,fullPage:true});console.log(`PASS ${name}: progreso, detener y reanudar`);await page.close();
 }
}finally{await browser.close();await new Promise(ok=>server.close(ok));}
