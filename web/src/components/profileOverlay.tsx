import {useEffect,useRef,type ReactNode} from 'react';
import {createPortal} from 'react-dom';

/** Native top layer escapes sidebar stacking contexts; its transparent backdrop keeps the catalog visible. */
export function ProfileOverlay({children,onClose,anchor,label,menu=false,placement="below"}:{children:ReactNode;onClose:()=>void;anchor?:DOMRect;label:string;menu?:boolean;placement?:"above"|"below"}){
 const ref=useRef<HTMLDialogElement>(null);
 useEffect(()=>{
  const previous=document.activeElement as HTMLElement|null;
  const dialog=ref.current;dialog?.showModal();
  dialog?.querySelector<HTMLElement>('button:not([disabled]),a[href],input:not([disabled])')?.focus();
  return()=>{dialog?.close();previous?.focus({preventScroll:true});};
 },[]);
 const left=anchor?Math.max(8,Math.min(anchor.left,window.innerWidth-272)):undefined;
 const position=anchor?{
  left,margin:0,
  ...(placement==='above'
   ?{top:'auto',bottom:window.innerHeight-anchor.top+8,maxHeight:Math.max(0,anchor.top-16)}
   :{top:anchor.bottom+8,bottom:'auto',maxHeight:Math.max(0,window.innerHeight-anchor.bottom-16)}),
 }:undefined;
 return createPortal(<dialog ref={ref} aria-label={label} role={menu?'menu':'dialog'} aria-modal="true"
  onCancel={e=>{e.preventDefault();onClose();}}
  onKeyDown={e=>{if(e.key==='Escape'){e.preventDefault();e.stopPropagation();onClose();}}}
  onClick={e=>{if(e.target===e.currentTarget){const r=e.currentTarget.getBoundingClientRect();if(e.clientX<r.left||e.clientX>r.right||e.clientY<r.top||e.clientY>r.bottom)onClose();}}}
  style={position}
  className={`fixed max-h-[85dvh] overflow-y-auto overscroll-contain rounded-xl border border-border bg-surface-overlay text-text shadow-pop backdrop:bg-transparent ${menu?'w-64 p-1':'w-[min(94vw,36rem)] p-5'}`}>
  {children}
 </dialog>,document.body);
}
