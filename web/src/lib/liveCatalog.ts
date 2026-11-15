import { useEffect, useRef, useState } from 'react';
import { api } from './api';
import { invalidateMenu } from './catalog';
const event = 'velora:catalogo';
/** Un único sondeo secuencial para la pantalla, sin descargar catálogos si no han cambiado. */
export function useLiveCatalog() {
 useEffect(() => {
  let stopped=false, timer: ReturnType<typeof setTimeout> | undefined, previous: string | undefined;
  let busy=false;
  const controller=new AbortController();
  const poll=async()=>{
   if(stopped||busy)return;
   clearTimeout(timer);
   busy=true;
   let delay=2000;
   try {
    if(document.visibilityState==='hidden')return;
    const {revision}=await api<{revision:string}>('GET','/api/v1/catalog/revision',undefined,controller.signal);
    if(stopped)return;
    if(typeof revision==='string'&&revision!==previous){
     previous=revision;
     invalidateMenu();window.dispatchEvent(new Event(event));
    }
   } catch {delay=10000;} finally {busy=false;if(!stopped)timer=setTimeout(()=>void poll(),delay);}
  };
  const resume=()=>{if(document.visibilityState!=='hidden')void poll();};
  void poll();
  document.addEventListener('visibilitychange',resume);window.addEventListener('focus',resume);
  return()=>{stopped=true;controller.abort();clearTimeout(timer);document.removeEventListener('visibilitychange',resume);window.removeEventListener('focus',resume);};
 },[]);
}
/** Refresca datos sin desmontar la pantalla ni volver a abrir su estado de carga. */
export function useCatalogRefresh(refresh:()=>unknown) {
 const callback=useRef(refresh);callback.current=refresh;
 useEffect(()=>{const update=()=>{void callback.current();};window.addEventListener(event,update);return()=>window.removeEventListener(event,update);},[]);
}
export function useCatalogRevision(){
 const [revision,setRevision]=useState(0);
 useCatalogRefresh(()=>setRevision(n=>n+1));
 return revision;
}
