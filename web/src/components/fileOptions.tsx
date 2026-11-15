import { useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { api } from '../lib/api';
import { fileChips, langName, type ItemDetail, type EpisodeInfo } from '../lib/catalog';
import { defaultSubtitle, type SubTrack } from '../lib/subtitles';
import { Button } from './ui';
import { Choice } from './choice';
import { isTv } from '../lib/tv';

export interface FileSelection { item: string; file: string; audio: number | null; subtitle: string }
const selectClass = 'h-10 w-full rounded-md border border-border-input bg-surface-raised px-2 text-sm text-text tv:h-9';
export function FileOptions({ item, episodes, onChange }: { item: ItemDetail; episodes?: EpisodeInfo[]; onChange?: (s: FileSelection) => void }) {
  const navigate = useNavigate();
  const native = isTv && !!window.VeloraApp?.reproducir;
  const [episode, setEpisode] = useState('');
  const target = item.kind === 'series' ? (episodes?.find(e => e.id === episode)?.id || episodes?.find(e => e.id === item.next_up?.id)?.id || episodes?.[0]?.id || item.next_up?.id || '') : item.id;
  const [detail, setDetail] = useState<ItemDetail | null>(item.kind === 'series' ? null : item);
  const [fileId, setFile] = useState('');
  const [audio, setAudio] = useState<number | null>(null);
  const [subtitle, setSubtitle] = useState('auto');
  const [subs, setSubs] = useState<SubTrack[]>([]);
  const [note, setNote] = useState('');
  const previousTarget = useRef(target);
  const [loading, setLoading] = useState(false);
  useEffect(() => {
    const c = new AbortController(); const changed=previousTarget.current!==target; previousTarget.current=target;
    if(changed){setFile('');setDetail(null);} setNote('');
    if (!target) return;
    const apply = (d: ItemDetail) => { setDetail(d); setFile(old => !changed && d.files.some(f=>f.id===old&&!f.missing) ? old : (d.files.find(f => !f.missing) || d.files[0])?.id || ''); };
    if (item.kind !== 'series') apply(item);
    else api<{item: ItemDetail}>('GET', `/api/v1/items/${target}`, undefined, c.signal).then(r => apply(r.item)).catch(() => { if (!c.signal.aborted) setNote('No se ha podido cargar el archivo.'); });
    return () => c.abort();
  }, [target, item]);
  useEffect(() => {
    setAudio(null); setSubtitle('auto'); setSubs([]);
    if (!fileId) return;
    const c = new AbortController(); setLoading(true); setNote('');
    api<{subtitles: SubTrack[]; provider_error?: string}>('GET', `/api/v1/stream/files/${fileId}/subtitles`, undefined, c.signal)
      .then(r => {setSubs(r.subtitles ?? []); setSubtitle(defaultSubtitle(r.subtitles ?? [], native) || 'auto'); setNote(r.provider_error || '');})
      .catch(() => {if (!c.signal.aborted) setNote('No se han podido consultar los subtítulos. Puedes elegirlos en el reproductor.');})
      .finally(() => {if (!c.signal.aborted) setLoading(false);});
    return () => c.abort();
  }, [fileId]);
  useEffect(() => { onChange?.({item: target, file: fileId, audio, subtitle}); }, [target, fileId, audio, subtitle, onChange]);
  const file = detail?.files.find(f => f.id === fileId);
  const audios = file?.streams.filter(t => t.kind === 'audio') || [];
  return <section aria-label="Archivo y reproducción" className="space-y-4 rounded-xl bg-surface p-5 tv:p-3">
    <h2 className="text-sm font-semibold text-text-muted">Archivo original</h2>
    {item.kind === 'series' && episodes?.length ? <label className="block text-sm">Episodio<Choice aria-label="Episodio del archivo" className={selectClass} value={target} onChange={e => setEpisode(e.target.value)}>{episodes.map(ep => <option key={ep.id} value={ep.id}>{ep.episode_number}. {ep.title}</option>)}</Choice></label> : null}
    {detail && detail.files.length > 1 && <label className="block text-sm">Versión<Choice aria-label="Versión" className={selectClass} value={fileId} onChange={e => setFile(e.target.value)}>{detail.files.map((f,i) => <option key={f.id} value={f.id}>{f.version_label || `Versión ${i+1}`}{f.missing ? ' (no disponible)' : ''}</option>)}</Choice></label>}
    <div className="flex flex-wrap gap-2">{file && fileChips(file).map(c => <span key={c} className="rounded-sm bg-surface-raised px-2 py-1 text-xs font-semibold">{c}</span>)}</div>
    <div className="grid gap-4 tablet:grid-cols-2">
      <label className="space-y-1 text-sm">Audio<Choice aria-label="Audio" className={selectClass} value={audio ?? ''} onChange={e => setAudio(e.target.value === '' ? null : Number(e.target.value))}><option value="">Audio por defecto</option>{audios.map(t => <option key={t.index} value={t.index}>{langName(t.language ?? null)} · {t.codec.toUpperCase()}{t.title ? ` · ${t.title}` : ''}</option>)}</Choice></label>
      <label className="space-y-1 text-sm">Subtítulos<Choice aria-label="Subtítulos" className={selectClass} value={subtitle} disabled={loading || !fileId} onChange={e => setSubtitle(e.target.value)}><option value="auto">Automático · forzados en español</option><option value="off">Desactivados</option>{subs.map(t => <option key={t.id} value={t.id} disabled={!t.supported && !native}>{t.label || `${langName(t.language ?? null)} · ${t.forced ? 'Forzados' : 'Completos'}`}{t.provider ? ` · ${t.provider}` : ''}{!t.supported && !native ? ' (solo reproductor nativo)' : ''}</option>)}</Choice></label>
    </div>
    {loading && <p className="text-xs text-text-muted">Consultando subtítulos…</p>}{note && <p role="status" className="text-xs text-text-muted">{note}</p>}
    {file?.missing && <p className="text-sm text-status-error">Este archivo ya no está en la biblioteca.</p>}
    {item.kind === 'series' && <Button disabled={!file || file.missing} onClick={() => {const p = new URLSearchParams({file:fileId,sub:subtitle || 'auto'});if(audio !== null)p.set('audio',String(audio));navigate(`/ver/${target}?${p}`);}}>Reproducir este episodio</Button>}
  </section>;
}
