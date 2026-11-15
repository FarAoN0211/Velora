import { useCallback, useEffect, useState } from 'react';
import { Link, useParams, useSearchParams } from 'react-router-dom';
import { api, ApiError } from '../lib/api';
import { posterUrl, useMenuLibraries, type ItemCard } from '../lib/catalog';
import { useCatalogRefresh } from '../lib/liveCatalog';
import { useCatalogPosition } from '../lib/catalogPosition';
import { collectionQualityRows } from '../lib/collectionQuality';
import { MediaCard, MediaRow } from '../components/media';
import { collectionQuery, useCollectionMenu, type Collection } from '../components/collectionMenu';
import { Banner, Loading } from '../components/ui';
function CollectionCard({c,library,refresh}:{c:Collection;library:string;refresh:()=>void}){
 const menu=useCollectionMenu(c,library,refresh);
 const libraries=useMenuLibraries();const uhd=!!c.library_4k||(!!library&&/4\s*k|uhd/i.test(libraries.find(l=>l.id===library)?.name??''));
 return <div {...menu.cardHandlers} data-tv-tarjeta data-collection-card={c.id} className="group relative flex min-w-0 flex-col gap-2">
  <Link aria-label={c.name} to={`/coleccion/${c.id}${collectionQuery(library)}`} className="rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus"><div className="relative flex aspect-poster flex-col overflow-hidden rounded-md bg-surface-raised">{uhd&&<div className="flex h-8 shrink-0 items-center justify-center pb-1"><img src="/brand/4k-uhd.png" alt="4K UHD" className="h-full max-w-full object-contain"/></div>}<img src={`/api/v1/collections/${c.id}/poster${collectionQuery(library)}${library?'&':'?'}v=${encodeURIComponent(c.poster_rev??'auto')}`} alt="" loading="lazy" className={`min-h-0 w-full flex-1 ${uhd?'object-contain':'object-cover'}`} onError={e=>{if(!e.currentTarget.dataset.fallback){e.currentTarget.dataset.fallback='1';e.currentTarget.src=posterUrl(c.poster_id);}}}/></div></Link>
  <div className="absolute bottom-14 right-2">{menu.trigger}</div><p className="truncate text-sm">{c.name}</p><p className="text-xs text-text-muted">{c.count} películas</p>{menu.errorNote}{menu.menu}
 </div>;
}
function CollectionHeading({c,library,refresh}:{c:Collection;library:string;refresh:()=>void}){const menu=useCollectionMenu(c,library,refresh,true);return <><div className="flex items-center gap-3"><h1 className="text-2xl font-semibold">{c.name}</h1>{menu.trigger}</div>{c.overview&&<p className="max-w-3xl text-text-muted">{c.overview}</p>}{menu.errorNote}{menu.menu}</>;}
export function Collections(){
 const {id}=useParams();const [params,setParams]=useSearchParams();const library=params.get('library_id')??'';const libraries=useMenuLibraries().filter(l=>l.kind==='movies');
 const [cards,setCards]=useState<Collection[]|null>(null);const [items,setItems]=useState<ItemCard[]>([]);const [collection,setCollection]=useState<Collection|null>(null);const [error,setError]=useState('');
 const [loadedFor,setLoadedFor]=useState('');
 useCatalogPosition(cards!==null&&loadedFor===`${id??''}|${library}`,id?items.length:cards?.length??0);
 const load=useCallback(async(signal?:AbortSignal)=>{try{const q=collectionQuery(library);if(id){const r=await api<{collection:Collection;items:ItemCard[]}>('GET',`/api/v1/collections/${id}${q}`,undefined,signal);setCollection(r.collection);setItems(r.items);setCards([]);}else{const r=await api<{collections:Collection[]|null}>('GET',`/api/v1/collections${q}`,undefined,signal);setCards(r.collections??[]);setCollection(null);}setLoadedFor(`${id??''}|${library}`);setError('');}catch(e){if(!signal?.aborted)setError(e instanceof ApiError?e.message:'No se han podido cargar las colecciones.');}},[id,library]);
 useEffect(()=>{const c=new AbortController();setCards(null);setCollection(null);setItems([]);void load(c.signal);return()=>c.abort();},[load]);
 useCatalogRefresh(()=>load());
 useEffect(()=>{const c=new AbortController();let busy=false;const t=setInterval(()=>{if(document.visibilityState!=='hidden'&&!busy){busy=true;void load(c.signal).finally(()=>busy=false);}},10000);return()=>{c.abort();clearInterval(t);};},[load]);
 const refresh=()=>void load();
 return <main className="mx-auto flex w-full max-w-content flex-col gap-5 p-4 desktop:p-8 tv:gap-3 tv:p-4">
  {id&&<Link to={`/colecciones${collectionQuery(library)}`} className="w-fit text-sm text-text-muted">← Colecciones</Link>}
  {collection?<CollectionHeading c={collection} library={library} refresh={refresh}/>:<h1 className="text-2xl font-semibold">Colecciones</h1>}
  <label className="flex flex-wrap items-center gap-3 text-sm">Biblioteca<select aria-label="Biblioteca de las colecciones" className="rounded-md border border-border bg-surface-raised p-2" value={library} onChange={e=>setParams(e.target.value?{library_id:e.target.value}:{})}><option value="">Todas las bibliotecas</option>{libraries.map(l=><option key={l.id} value={l.id}>{l.name}</option>)}</select></label>
  <p className="text-sm text-text-muted">{collection?`${collection.count} películas distintas · ${items.length} versiones disponibles`:'Sagas con al menos dos películas distintas en la biblioteca seleccionada.'}</p>
  {error?<Banner tone="error">{error}</Banner>:cards===null?<Loading/>:id?<div className="flex flex-col gap-7 tv:gap-4">{collectionQualityRows(items).map(row=><MediaRow key={row.title} title={row.title} count={row.items.length}>{row.items.map(item=><div key={item.id} className="w-36 shrink-0 desktop:w-40 tv:w-24"><MediaCard item={item} fluid emblemSpace={row.items.some(i=>i.library_4k)}/>{!library&&<p className="mt-1 truncate text-xs text-text-muted">{libraries.find(l=>l.id===item.library_id)?.name}</p>}</div>)}</MediaRow>)}</div>:cards.length?<div className="grid grid-cols-2 gap-4 tablet:grid-cols-4 desktop:grid-cols-6 tv:grid-cols-7 tv:gap-3">{cards.map(c=><CollectionCard key={c.id} c={c} library={library} refresh={refresh}/>)}</div>:<p className="text-text-muted">No hay colecciones con dos películas disponibles en esta biblioteca.</p>}
 </main>;
}
