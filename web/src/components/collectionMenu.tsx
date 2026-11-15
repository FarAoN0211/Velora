import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Check, Clapperboard, CloudDownload, FileText, Image, Info, Pencil, Play, RefreshCcw, SearchCheck, Undo2 } from 'lucide-react';
import { api } from '../lib/api';
import { playHref, posterUrl, type ItemCard } from '../lib/catalog';
import { useSession } from '../lib/session';
import { useCardMenu, type Option } from './cardMenu';
import { FilesDialog, Modal } from './itemAdmin';
import { IdentifyDialog } from './itemIdentify';
import { Banner, Button } from './ui';
export interface Collection { id:number;name:string;count:number;poster_id:string;overview:string;poster_custom?:boolean;library_4k?:boolean;poster_rev?:string }
export function collectionQuery(library:string){return library?`?library_id=${encodeURIComponent(library)}`:'';}
export function useCollectionMenu(c:Collection,library:string,onChanged:()=>void,inDetail=false){
 const navigate=useNavigate();const {user}=useSession();
 const [dialog,setDialog]=useState('');const [items,setItems]=useState<ItemCard[]>([]);const [chosen,setChosen]=useState<ItemCard|null>(null);
 const [title,setTitle]=useState(c.name);const [overview,setOverview]=useState(c.overview??'');const [error,setError]=useState('');const [busy,setBusy]=useState(false);
 const query=collectionQuery(library);const href=`/coleccion/${c.id}${query}`;
 const getItems=async()=>{const r=await api<{items:ItemCard[]}>('GET',`/api/v1/collections/${c.id}${query}`);return r.items;};
 const run=async(fn:()=>Promise<void>)=>{try{setBusy(true);setError('');await fn();}catch(e){setError(e instanceof Error?e.message:'No se ha podido completar.');}finally{setBusy(false);}};
 const open=async(kind:string)=>{await run(async()=>{setItems(await getItems());setChosen(null);setTitle(c.name);setOverview(c.overview??'');setDialog(kind);});};
 const mark=async(played:boolean)=>run(async()=>{for(const i of await getItems())await api(played?'POST':'DELETE',`/api/v1/me/items/${i.id}/played`);onChanged();});
 const refresh=async(mode:string)=>run(async()=>{if(mode==='all'&&!window.confirm('¿Actualizar desde TMDB todas las películas de esta colección en la biblioteca seleccionada? Se respetan los campos bloqueados y las imágenes personalizadas.'))return;for(const i of await getItems())await api('POST',`/api/v1/admin/items/${i.id}/refresh`,{mode});onChanged();});
 const save=async(poster:string|null)=>run(async()=>{await api('PATCH',`/api/v1/admin/collections/${c.id}${query}`,{title:dialog==='edit'?title:c.name,overview:dialog==='edit'?overview:c.overview??'',poster_id:poster});setDialog('');onChanged();});
 const options:Option[]=[
  {key:'play',label:'Reproducir primera película pendiente',icon:<Play/>,run:()=>run(async()=>{const list=await getItems();const i=list.find(i=>!i.played)??list[0];if(i)navigate(await playHref(i));})},
  {key:'trailer',label:'Ver tráiler de una película…',icon:<Clapperboard/>,run:()=>open('trailer')},
  {key:'played',label:'Marcar colección como vista',icon:<Check/>,run:()=>mark(true)},
  {key:'unplayed',label:'Marcar colección como no vista',icon:<Undo2/>,run:()=>mark(false)},
 ];
 if(!inDetail)options.push({key:'detail',label:'Ir a la colección',icon:<Info/>,run:()=>navigate(href)});
 if(user?.is_admin)options.push(
  {key:'identify',label:'Identificar películas de la colección…',icon:<SearchCheck/>,admin:true,run:()=>open('identify')},
  {key:'images',label:'Editar portada de la colección…',icon:<Image/>,admin:true,run:()=>open('images')},
  {key:'edit',label:'Editar información de la colección…',icon:<Pencil/>,admin:true,run:()=>open('edit')},
  {key:'missing',label:'Completar películas (TMDB)',icon:<CloudDownload/>,admin:true,run:()=>refresh('missing')},
  {key:'refresh',label:'Actualizar películas (TMDB)',icon:<RefreshCcw/>,admin:true,run:()=>refresh('all')},
  {key:'files',label:'Ver archivos de la colección…',icon:<FileText/>,admin:true,run:()=>open('files')});
 const menu=useCardMenu({item:{id:String(c.id),title:c.name,position_ms:0,played:false} as ItemCard,optionsOverride:options,inDetail});
 const chooser=dialog&&<Modal title={`${c.name} · ${dialog==='images'?'Portada':dialog==='edit'?'Información':'Elegir película'}`} onClose={()=>setDialog('')} footer={<Button variant="secondary" onClick={()=>setDialog('')}>Cerrar</Button>}>
  {error&&<Banner tone="error">{error}</Banner>}
  {dialog==='edit'?<div className="flex flex-col gap-4"><label>Nombre<input aria-label="Nombre de la colección" className="mt-1 w-full rounded bg-surface-raised p-3" value={title} onChange={e=>setTitle(e.target.value)}/></label><label>Sinopsis<textarea aria-label="Sinopsis de la colección" className="mt-1 w-full rounded bg-surface-raised p-3" value={overview} onChange={e=>setOverview(e.target.value)}/></label><Button loading={busy} onClick={()=>void save(c.poster_custom?c.poster_id:null)}>Guardar</Button></div>:
   dialog==='images'?<><p className="mb-4 text-text-muted">Elige una portada de tus películas o recupera la portada automática de la colección en TMDB.</p><Button loading={busy} onClick={()=>void save(null)}>Usar portada de TMDB</Button><div className="mt-4 grid grid-cols-3 gap-3">{items.map(i=><button disabled={busy} key={i.id} onClick={()=>void save(i.id)} aria-label={`Usar portada de ${i.title}`}><img className="aspect-poster w-full rounded object-cover" src={posterUrl(i.id)} alt={i.title}/></button>)}</div></>:
   <div className="flex flex-col gap-2">{items.filter(i=>dialog!=='trailer'||i.trailer_id).map(i=><Button variant="secondary" key={i.id} onClick={()=>{if(dialog==='trailer'){setDialog('');navigate(`/ver/${i.id}?file=${i.trailer_id}`);}else{setChosen(i);}}}>{i.title} {i.quality?`· ${i.quality}`:''}</Button>)}{dialog==='trailer'&&!items.some(i=>i.trailer_id)&&<p>No hay tráileres disponibles en esta colección.</p>}</div>}
 </Modal>;
 return {...menu,errorNote:error?<Banner tone="error">{error}</Banner>:menu.errorNote,menu:<>{menu.menu}{chosen&&dialog==='files'?<FilesDialog itemId={chosen.id} title={chosen.title} onClose={()=>setChosen(null)}/>:chosen&&dialog==='identify'?<IdentifyDialog itemId={chosen.id} title={chosen.title} onClose={()=>{setChosen(null);onChanged();}} onEditManually={()=>setChosen(null)}/>:chooser}</>};
}
