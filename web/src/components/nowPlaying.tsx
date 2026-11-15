import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api } from '../lib/api';
import { detailHref, fmtClock, posterUrl, type ItemCard } from '../lib/catalog';
import { Card } from './ui';

type Playback = {
 id: string; username: string; device: string; client: string; ip: string;
 state: 'playing' | 'paused' | 'buffering'; position_ms: number; duration_ms: number;
 item: ItemCard; method: string; video: string; audio: string;
 convert_video: boolean; convert_audio: boolean; encoder?: string;
};
const states = { playing: 'Reproduciendo', paused: 'En pausa', buffering: 'Cargando' };
function delivery(p: Playback, convert: boolean) {
 if (convert) return 'Transcodificación';
 return p.method === 'direct_play' ? 'Reproducción directa' : p.method ? 'Copia sin conversión' : 'Método no disponible';
}
export function NowPlaying() {
 const [items, setItems] = useState<Playback[]>([]);
 const [error, setError] = useState(false);
 useEffect(() => {
  const abort = new AbortController();
  let pending = false;
  const load = async () => {
   if (pending) return;
   pending = true;
   try { const r = await api<{items: Playback[]}>('GET', '/api/v1/admin/now-playing', undefined, abort.signal); setItems(r.items); setError(false); }
   catch { if (!abort.signal.aborted) setError(true); }
   finally { pending = false; }
  };
  void load(); const t = setInterval(() => void load(), 5000);
  return () => { abort.abort(); clearInterval(t); };
 }, []);
 return <section aria-label="Reproduciendo ahora" className="flex flex-col gap-3">
  <h2 className="text-lg">Reproduciendo ahora <span className="text-text-muted">({items.length})</span></h2>
  {error ? <p role="status" className="text-sm text-danger">No se ha podido actualizar la actividad. Reintentando…</p>
   : !items.length && <p className="text-sm text-text-muted">No hay reproducciones activas.</p>}
  <div className="grid gap-4 tablet:grid-cols-2 desktop:grid-cols-3">
   {items.map(p => <Card key={p.id} className="flex flex-col gap-3">
    <Link to={detailHref(p.item)} className="flex gap-3">
     {p.item.has_poster && <img src={posterUrl(p.item.id, p.item.image_rev)} alt="" className="h-28 w-20 rounded object-cover" />}
     <div className="min-w-0"><p className="font-semibold">{p.item.series_title || p.item.title}</p>
      {p.item.kind === 'episode' && <><p className="text-sm text-text-muted">T{p.item.season_number} · E{p.item.episode_number}</p><p className="text-sm">{p.item.title}</p></>}
      <p className="mt-2 text-sm text-text-muted">{fmtClock(p.position_ms / 1000)} / {fmtClock(p.duration_ms / 1000)}</p>
     </div>
    </Link>
    <progress aria-label={`Progreso de ${p.item.title}`} max={Math.max(1, p.duration_ms)} value={p.position_ms} className="h-1 w-full accent-accent" />
    <div className="text-sm"><p className="font-semibold">{p.username}</p><p>{p.device} · {p.client}</p><p className="text-text-muted">{states[p.state] ?? p.state} · {p.ip}</p></div>
    <div className="border-t border-white/10 pt-2 text-sm"><p>Vídeo · {p.video || 'Sin datos'}</p><p className="text-text-muted">{delivery(p, p.convert_video)}{p.convert_video && p.encoder ? ` · ${p.encoder === 'software' ? 'CPU' : 'GPU'}` : ''}</p></div>
    {p.audio && <div className="text-sm"><p>Audio · {p.audio}</p><p className="text-text-muted">{delivery(p, p.convert_audio)}</p></div>}
   </Card>)}
  </div>
 </section>;
}
