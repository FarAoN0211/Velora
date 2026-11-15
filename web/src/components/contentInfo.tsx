import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { FileOptions } from './fileOptions';
import { api } from '../lib/api';
import { fmtDuration, imageUrl, langName, type FileInfo, type ItemDetail } from '../lib/catalog';
import { isTv } from '../lib/tv'; // en TV, el mando va directo a cada actor (una lista enfocable lo atrapaba)

export interface CreditPerson { id: number; name: string; character?: string; job?: string; profile_path?: string }
export interface Presentation {
  belongs_to_collection?: {id:number;name:string}|null;
  credits?: { cast?: CreditPerson[]; crew?: CreditPerson[] };
  production_companies?: { name: string }[];
  production_countries?: { name: string }[];
  original_language?: string;
  status?: string;
}
const jobs: Record<string, string> = { Director: 'Dirección', Writer: 'Guion', Screenplay: 'Guion', Creator: 'Creación', 'Original Music Composer': 'Música' };
const statuses: Record<string, string> = { Released: 'Estrenada', 'Returning Series': 'En emisión', Ended: 'Finalizada', Canceled: 'Cancelada', 'In Production': 'En producción', 'Post Production': 'Posproducción', Planned: 'Planificada' };
function Fact({ label, value }: { label: string; value?: string | number | null }) {
  return value ? <div><dt className="text-xs text-text-muted">{label}</dt><dd className="mt-1 break-words text-sm text-text">{value}</dd></div> : null;
}
function Portrait({ person, itemId }: { person: CreditPerson; itemId: string }) {
  const [failed, setFailed] = useState(false);
  const path = person.profile_path;
  return <div className="aspect-[2/3] overflow-hidden rounded-lg bg-surface-raised">
    {!failed && path && /^\/[a-zA-Z0-9]+\.(jpg|png)$/.test(path) ? <img src={`/api/v1/items/${itemId}/people/${person.id}/image?v=${encodeURIComponent(path)}`} alt="" loading="lazy" decoding="async" referrerPolicy="no-referrer" onError={() => setFailed(true)} className="h-full w-full object-cover" /> : <div aria-hidden className="flex h-full items-center justify-center text-3xl text-text-muted">{person.name.slice(0, 1)}</div>}
  </div>;
}
export function ContentInfo({ item, file, options }: { item: ItemDetail; file?: FileInfo; options?: React.ReactNode }) {
  const [p, setPresentation] = useState<Presentation>(item.presentation ?? {});
  useEffect(() => {
    setPresentation(item.presentation ?? {});
    if (item.kind !== 'movie' && item.kind !== 'series') return;
    if (item.presentation && Object.keys(item.presentation).length) return;
    const controller = new AbortController();
    api<{ presentation: Presentation }>('GET', `/api/v1/items/${item.id}/presentation`, undefined, controller.signal)
      .then(r => setPresentation(r.presentation ?? {})).catch(() => { /* Optional metadata never blocks the detail. */ });
    return () => controller.abort();
  }, [item.id, item.kind, item.presentation]);
  const [collectionVisible,setCollectionVisible] = useState(false);
  const collectionID = p.belongs_to_collection?.id;
  useEffect(()=>{
    setCollectionVisible(false);
    if(item.kind!=='movie'||!collectionID)return;
    const c=new AbortController();
    api<{collections:{id:number}[]|null}>('GET','/api/v1/collections',undefined,c.signal)
      .then(r=>setCollectionVisible((r.collections??[]).some(v=>v.id===collectionID))).catch(()=>undefined);
    return()=>c.abort();
  },[item.kind,collectionID]);
  const people = [...(p.credits?.cast ?? []), ...(p.credits?.crew ?? [])];
  return <div className="relative space-y-8 px-4 py-8 desktop:px-12 tv:space-y-5 tv:py-4">
    {collectionVisible && <Link to={`/coleccion/${collectionID}`} className="inline-flex rounded-full border border-border bg-surface px-4 py-2 text-sm text-accent">Ver colección: {p.belongs_to_collection?.name}</Link>}
    {options ?? <FileOptions item={item} />}
    {people.length > 0 && <section aria-label="Reparto y equipo técnico">
      <h2 className="mb-4 text-lg font-semibold">Reparto y equipo técnico</h2>
      <ul tabIndex={isTv ? undefined : 0} aria-label="Desplazar reparto y equipo técnico" className="flex gap-3 overflow-x-auto pb-3 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus">
        {people.map((person, i) => <li key={`${person.id}-${i}`} className="w-28 shrink-0 tv:w-24">
          {!person.job ? <Link to={`/persona/${person.id}?nombre=${encodeURIComponent(person.name)}&desde=${encodeURIComponent(item.id)}`} className="block rounded-lg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus hover:text-accent"><Portrait person={person} itemId={item.id} /><p className="mt-2 text-sm font-medium">{person.name}</p><p className="text-xs text-text-muted">{person.character}</p></Link> : <><Portrait person={person} itemId={item.id} /><p className="mt-2 text-sm font-medium">{person.name}</p><p className="text-xs text-text-muted">{jobs[person.job] || person.job}</p></>}
        </li>)}
      </ul>
    </section>}
    <section aria-label="Acerca del contenido"><h2 className="mb-4 text-lg font-semibold">Acerca de {item.series_title || item.title}</h2>
      <dl className="grid max-w-5xl gap-x-8 gap-y-4 tablet:grid-cols-2 desktop:grid-cols-3">
        <Fact label="Título original" value={item.original_title} /><Fact label="Estreno" value={item.year} />
        <Fact label="Duración" value={fmtDuration(item.runtime_ms)} /><Fact label="Clasificación" value={item.official_rating} />
        <Fact label="Puntuación TMDB" value={item.community_rating ? `${item.community_rating.toFixed(1)} / 10` : null} />
        <Fact label="Géneros" value={item.genres.join(' · ')} /><Fact label="Idioma original" value={p.original_language ? langName(p.original_language) : null} />
        <Fact label="Estudios" value={p.production_companies?.map(c => c.name).join(' · ')} /><Fact label="Países de producción" value={p.production_countries?.map(c => c.name).join(' · ')} />
        <Fact label="Estado" value={p.status ? statuses[p.status] || p.status : null} />
      </dl>
    </section>
    {file && <section aria-label="Información multimedia"><h2 className="mb-4 text-lg font-semibold">Información multimedia</h2>
      <div className="grid gap-3 tablet:grid-cols-2 desktop:grid-cols-3">
        <article className="rounded-lg bg-surface p-4"><h3 className="mb-3 font-medium">Archivo</h3><dl className="space-y-3">
          <Fact label="Contenedor" value={file.container?.toUpperCase()} /><Fact label="Tamaño" value={`${(file.size_bytes / 1024 ** 3).toLocaleString('es-ES', { maximumFractionDigits: 2 })} GiB`} />
          <Fact label="Duración" value={fmtDuration(file.duration_ms)} /><Fact label="Bitrate medio" value={file.duration_ms ? `${(file.size_bytes * 8 / file.duration_ms / 1000).toFixed(2)} Mb/s` : null} />
        </dl></article>
        {file.streams.map(track => <article key={track.index} className="rounded-lg bg-surface p-4"><h3 className="mb-3 font-medium">{track.kind === 'video' ? 'Vídeo' : track.kind === 'audio' ? 'Audio' : 'Subtítulos'} · {track.index + 1}</h3>
          <dl className="space-y-3"><Fact label="Título" value={track.title} /><Fact label="Códec" value={track.codec.toUpperCase()} /><Fact label="Idioma" value={track.language ? langName(track.language) : null} />
            <Fact label="Resolución" value={track.width && track.height ? `${track.width} × ${track.height}` : null} /><Fact label="Profundidad" value={track.bit_depth ? `${track.bit_depth} bits` : null} />
            <Fact label="Rango dinámico" value={track.hdr?.toUpperCase()} /><Fact label="Canales" value={track.channels} /><Fact label="Pista predeterminada" value={track.default ? 'Sí' : 'No'} />
          </dl></article>)}
      </div>
    </section>}
  </div>;
}
/** The item image endpoint also resolves series logos inherited by episodes. */
export function PlaybackLogo({ item }: { item: ItemDetail }) {
  const [failed, setFailed] = useState(false);
  if (!item.has_logo || failed) return null;
  return <img src={imageUrl(item.id, 'logo', item.image_rev)} alt={`Logo de ${item.series_title || item.title}`} onError={() => setFailed(true)} className="max-h-16 w-40 shrink-0 object-contain object-left tablet:w-52" />;
}
