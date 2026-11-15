import { useEffect, useLayoutEffect, useRef } from 'react';
import { useLocation } from 'react-router-dom';
type Position = { y: number; href: string; count: number; rows: Record<string,number> };
const positions = new Map<string, Position>();
export function useCatalogPosition(ready: boolean, count: number) {
 const location=useLocation();
 const key=location.pathname+location.search;
 const current=useRef({count});current.current.count=count;
 const restoring=useRef(false);
 useLayoutEffect(()=>{
  window.history.scrollRestoration='manual';
  restoring.current=positions.has(key);
  const save=()=>{if(restoring.current)return;const a=document.activeElement?.closest('a[href]');positions.set(key,{y:window.scrollY,href:a?.getAttribute('href')??'',count:current.current.count,rows:Object.fromEntries(Array.from(document.querySelectorAll<HTMLElement>('[data-catalog-row]')).map(e=>[e.dataset.catalogRow!,e.scrollLeft]))});};
  const click=(e:MouseEvent)=>{const a=(e.target as Element)?.closest('a[href]');if(a){save();const p=positions.get(key);if(p)p.href=a.getAttribute('href')??'';}};
  window.addEventListener('scroll',save,{passive:true,capture:true});document.addEventListener('click',click,true);
  return()=>{window.removeEventListener('scroll',save,true);document.removeEventListener('click',click,true);};
 },[key]);
 useEffect(()=>{if(!ready||!restoring.current)return;const p=positions.get(key);if(!p||(count===0&&p.count>0))return;
  const frame=requestAnimationFrame(()=>{window.scrollTo(0,p.y);document.querySelectorAll<HTMLElement>('[data-catalog-row]').forEach(e=>{const behavior=e.style.scrollBehavior;e.style.scrollBehavior='auto';e.scrollLeft=p.rows[e.dataset.catalogRow!]??0;e.style.scrollBehavior=behavior;});const a=Array.from(document.querySelectorAll<HTMLAnchorElement>('a[href]')).find(a=>a.getAttribute('href')===p.href);a?.focus({preventScroll:true});restoring.current=false;});return()=>cancelAnimationFrame(frame);
 },[ready,key,count]);
 return positions.get(key)?.count??0;
}
