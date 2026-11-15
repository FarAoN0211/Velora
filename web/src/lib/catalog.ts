// Tipos y llamadas del catálogo (E1) y detección de capacidades del navegador para el motor de decisión (§5.1).
import { useEffect, useState } from 'react';
import { api } from './api';

export interface ItemCard {
 library_4k?: boolean;
  id: string;
  library_id: string;
  title: string;
  year: number | null;
  has_poster: boolean;
  position_ms: number;
  runtime_ms: number | null;
  played: boolean;
  added_at: string;
  has_backdrop: boolean;
  /** 4K, 1080p, 720p o SD (mejor pista de vídeo). */
  quality?: string;
  /** Tráiler local para «Ver tráiler» en el menú ⋮ (si la película tiene). */
  trailer_id?: string;
  /** Versión de sus imágenes: cambia con cualquiera de ellas, en todos los dispositivos. */
  image_rev?: string;
  has_logo?: boolean;
  /** Imagen destacada propia para la cabecera del inicio. */
  has_banner?: boolean;
  /** E2: película, serie, temporada o episodio. */
  kind: ItemKind;
  series_id?: string;
  series_title?: string;
  /** Del episodio (o de la propia temporada); 0 = especiales. */
  season_number?: number;
  /** Ausente: episodio sin número. */
  episode_number?: number;
  /** Multiepisodio «E01–E02». */
  episode_end?: number;
  /** Miniatura 16:9 propia del episodio. */
  has_thumb?: boolean;
}

export type ItemKind = 'movie' | 'series' | 'season' | 'episode';

/** Temporada en la ficha de su serie. */
export interface SeasonInfo extends ItemCard {
  overview: string | null;
  episode_count: number;
  unplayed_count: number;
}

/** Episodio en la lista de su temporada. */
export interface EpisodeInfo extends ItemCard {
  overview: string | null;
  /** Archivo que se reproduce por defecto. */
  file_id: string | null;
  /** Insignias técnicas de ese archivo. */
  tech?: TechBadge[];
}

/** «T1 · E3», «T1 · E1–E2», «Especiales · E1»; vacío si no es un episodio. */
export function episodeCode(c: Pick<ItemCard, 'kind' | 'season_number' | 'episode_number' | 'episode_end'>): string {
  if (c.kind !== 'episode') return '';
  const season = c.season_number === 0 ? 'Especiales' : `T${c.season_number ?? '?'}`;
  if (!c.episode_number) return season;
  return `${season} · E${c.episode_number}${c.episode_end ? `–E${c.episode_end}` : ''}`;
}

/** Ruta de la ficha según el tipo: la de un episodio o una temporada es la de su serie, abierta en esa temporada. */
export function detailHref(c: Pick<ItemCard, 'id' | 'kind' | 'series_id' | 'season_number'>): string {
  if (c.kind === 'series') return `/serie/${c.id}`;
  if ((c.kind === 'episode' || c.kind === 'season') && c.series_id)
    return `/serie/${c.series_id}${c.season_number !== undefined ? `?temporada=${c.season_number}` : ''}`;
  return `/pelicula/${c.id}`;
}

/** Ruta para reproducir: una serie reproduce el episodio que toca (su «siguiente»). */
export async function playHref(c: Pick<ItemCard, 'id' | 'kind'>): Promise<string> {
  if (c.kind !== 'series') return `/ver/${c.id}`;
  const { item } = await api<{ item: ItemDetail }>('GET', `/api/v1/items/${c.id}`);
  return item.next_up ? `/ver/${item.next_up.id}` : `/serie/${c.id}`;
}

export interface StreamInfo {
  index: number;
  kind: 'video' | 'audio' | 'subtitle';
  codec: string;
  language: string | null;
  title: string | null;
  default: boolean;
  width?: number | null;
  height?: number | null;
  bit_depth?: number | null;
  hdr?: string | null;
  channels?: number | null;
}

export interface FileInfo {
  id: string;
  container: string | null;
  duration_ms: number | null;
  size_bytes: number;
  version_label: string | null;
  missing: boolean;
  streams: StreamInfo[];
  /** Insignias técnicas calculadas por el servidor; ausentes en servidores anteriores. */
  tech?: TechBadge[];
}

/** Insignia técnica (resolución, HDR, códecs, canales, extras, contenedor); lo desconocido no llega. */
export interface TechBadge {
  category: 'resolution' | 'hdr' | 'video' | 'audio' | 'channels' | 'fps' | 'bitdepth' | '3d' | 'imax' | 'container' | (string & {});
  id: string;
  label: string;
}

/** Película de la cabecera destacada del inicio. */
export interface Featured extends ItemCard {
  overview: string | null;
  official_rating: string | null;
}

export interface HomeData {
  featured: Featured[];
  continue: ItemCard[];
  /** E2: «Siguiente episodio» de las series empezadas. */
  next_up?: ItemCard[];
  rows: { library: MenuLibrary; items: ItemCard[] }[];
}

export interface ItemDetail extends ItemCard {
  original_title?: string | null;
  community_rating?: number | null;
  presentation?: import("../components/contentInfo").Presentation;
  overview: string | null;
  tagline: string | null;
  official_rating: string | null;
  genres: string[];
  /** Tráileres y extras locales (E1.3 T1): nunca guardan progreso ni marcan como vista. */
  extras: ExtraInfo[];
  files: FileInfo[];
  /** Serie: temporadas (especiales al final) y el episodio que toca. */
  seasons?: SeasonInfo[];
  next_up?: ItemCard;
  /** Temporada: sus episodios. */
  episodes?: EpisodeInfo[];
  /** Episodio: el siguiente en orden (sin pasar a los especiales). */
  next_episode?: ItemCard;
}

export type ExtraKind = 'trailer' | 'featurette' | 'behindthescenes' | 'deleted' | 'interview' | 'scene' | 'short' | 'extra';

export interface ExtraInfo {
  id: string;
  kind: ExtraKind;
  /** Vacío para «trailer.mkv»: se muestra el nombre del tipo. */
  title: string;
  duration_ms: number | null;
  streams: StreamInfo[];
}

export const EXTRA_LABEL: Record<ExtraKind, string> = {
  trailer: 'Tráiler',
  featurette: 'Reportaje',
  behindthescenes: 'Detrás de las cámaras',
  deleted: 'Escena eliminada',
  interview: 'Entrevista',
  scene: 'Escena',
  short: 'Cortometraje',
  extra: 'Extra',
};

/** Nombre de un extra para la interfaz: su título si lo tiene, si no el del tipo. */
export function extraName(x: ExtraInfo): string {
  return x.title || EXTRA_LABEL[x.kind];
}

/** Un género con el número de títulos visibles (filtro de la biblioteca). */
export interface Genre {
  name: string;
  count: number;
}

export type PathStatus = 'available' | 'unavailable' | 'pending_sync';

export interface LibraryPath {
  id: string;
  path: string;
  kind: 'local' | 'network' | 'remote_fuse';
  status: PathStatus;
  last_scan_at: string | null;
  detached_at?: string;
}

/** Lo que ve cualquier usuario de una biblioteca a la que tiene acceso. */
export interface MenuLibrary {
  id: string;
  name: string;
  kind: 'movies' | 'tv';
  sort_order: number;
  show_in_menu: boolean;
  status: PathStatus;
  items: number;
  /** En la barra lateral de este usuario: su preferencia o, si no la tiene, show_in_menu. */
  in_menu: boolean;
  /** Orden con el que este usuario ve la biblioteca (el último que eligió, en cualquier aparato). */
  view_sort?: string;
  view_reverse?: boolean;
  /** Solo en la vista de administración: biblioteca retirada (invisible para todos, recuperable). */
  retired_at?: string;
}

/** Vista de administración: además, las rutas del servidor. */
export interface Library extends MenuLibrary {
  last_scan_at: string | null;
  paths: LibraryPath[];
  /** Carpetas quitadas pero recuperables: sus fichas y progreso se conservan. */
  detached: LibraryPath[] | null;
}

export const KIND_LABEL: Record<MenuLibrary['kind'], string> = { movies: 'Películas', tv: 'Series' };

// Bibliotecas del menú: se piden una vez y se recargan cuando el administrador cambia algo (invalidateMenu).
let menuCache: MenuLibrary[] | null = null;
let menuGeneration=0;
const menuListeners = new Set<() => void>();

/** Recuerda el orden elegido en una biblioteca: al momento en esta sesión y, en el servidor, para las próximas visitas
 * y para los demás aparatos (la TV incluida). Un fallo al guardar no cambia lo que se ve. */
export function rememberLibraryView(id: string, sort: string, reverse: boolean) {
  const lib = menuCache?.find((l) => l.id === id);
  if (lib) {
    lib.view_sort = sort;
    lib.view_reverse = reverse;
  }
  api('PUT', `/api/v1/me/libraries/${id}/view`, { sort, reverse }).catch(() => undefined);
}

export function invalidateMenu() {
  menuGeneration++;
  menuCache = null;
  menuListeners.forEach((f) => f());
}

export function useMenuLibraries(): MenuLibrary[] {
  const [libs, setLibs] = useState<MenuLibrary[]>(menuCache ?? []);
  useEffect(() => {
    let alive = true;
    const load = () => {
      if (menuCache) {
        setLibs(menuCache);
        return;
      }
      const generation=menuGeneration;
      api<{ libraries: MenuLibrary[] }>('GET', '/api/v1/libraries')
        .then((r) => {
          if(generation!==menuGeneration)return;
          menuCache = r.libraries;
          if (alive) setLibs(r.libraries);
        })
        .catch(() => undefined);
    };
    load();
    menuListeners.add(load);
    return () => {
      alive = false;
      menuListeners.delete(load);
    };
  }, []);
  return libs;
}

export interface Plan {
  audio_dynamics?: 'off' | 'balanced' | 'night';
  method: 'direct_play' | 'direct_stream' | 'transcode' | 'unsupported';
  convert_video?: boolean;
  encoder?: string;
  target_height?: number;
  audio_index: number;
  convert_audio: boolean;
  reasons: string[] | null;
  summary: string;
  url?: string;
  duration_ms: number;
  seekable: boolean;
}

export interface Caps {
  video_transcode?: boolean;
  audio_profiles?: Record<string,string[]>;
  containers: string[];
  video: string[];
  audio: string[];
  force_remux?: boolean;
  source_video_unsupported?: boolean;
  mp4_video?: string[];
  mp4_audio?: string[];
  max_bit_depths?: Record<string, number>;
}

let cachedCaps: Caps | null = null;

// detectCaps pregunta al navegador qué sabe reproducir (no se supone nada: §5.1.1).
export function detectCaps(): Caps {
  if (cachedCaps) return cachedCaps;
  const v = document.createElement('video');
  const can = (type: string) => {
    return v.canPlayType(type) !== ''; // This player uses video.src, not MediaSource.
  };
  const caps: Caps = { video_transcode: true, containers: ['mp4'], video: [], audio: [], mp4_video: [], mp4_audio: [], max_bit_depths: {} };
  if (can('video/webm')) caps.containers.push('webm');
  if (can('video/mp4; codecs="avc1.640028"')) caps.video.push('h264');
  if (can('video/mp4; codecs="hvc1.1.6.L120.90"') || can('video/mp4; codecs="hev1.1.6.L120.90"')) caps.video.push('hevc');
  if (can('video/webm; codecs="vp8"')) caps.video.push('vp8');
  if (can('video/webm; codecs="vp9"')) caps.video.push('vp9');
  if (can('video/mp4; codecs="av01.0.05M.08"')) caps.video.push('av1');
  if (can('audio/mp4; codecs="mp4a.40.2"')) caps.audio.push('aac');
  if (can('audio/mpeg')) caps.audio.push('mp3');
  if (can('audio/webm; codecs="opus"') || can('audio/mp4; codecs="opus"')) caps.audio.push('opus');
  if (can('audio/webm; codecs="vorbis"')) caps.audio.push('vorbis');
  if (can('audio/flac') || can('audio/mp4; codecs="flac"')) caps.audio.push('flac');
  if (can('audio/mp4; codecs="ac-3"')) caps.audio.push('ac3');
  if (can('audio/mp4; codecs="ec-3"')) caps.audio.push('eac3');
  const mp4Video: Record<string,string> = {h264:'avc1.640028',hevc:'hvc1.1.6.L120.90',vp9:'vp09.00.10.08',av1:'av01.0.05M.08'};
  for (const [codec,code] of Object.entries(mp4Video)) if(can(`video/mp4; codecs="${code}"`)) caps.mp4_video!.push(codec);
  const mp4Audio: Record<string,string> = {aac:'mp4a.40.2',mp3:'mp4a.69',opus:'opus',flac:'flac',ac3:'ac-3',eac3:'ec-3'};
  for (const [codec,code] of Object.entries(mp4Audio)) if(can(`audio/mp4; codecs="${code}"`)) caps.mp4_audio!.push(codec);
  caps.max_bit_depths!.h264 = can('video/mp4; codecs="avc1.6E0028"') ? 10 : 8;
  caps.max_bit_depths!.hevc = can('video/mp4; codecs="hvc1.2.4.L153.B0"') ? 10 : 8;
  caps.max_bit_depths!.vp9 = can('video/webm; codecs="vp09.02.10.10"') ? 10 : 8;
  caps.max_bit_depths!.av1 = can('video/mp4; codecs="av01.0.08M.10"') ? 10 : 8;
  caps.audio_profiles={aac:[]};
  for(const [profile,code] of Object.entries({'LC':'mp4a.40.2','Main':'mp4a.40.1','HE-AAC':'mp4a.40.5','HE-AACv2':'mp4a.40.29','LD':'mp4a.40.23','ELD':'mp4a.40.39','xHE-AAC':'mp4a.40.42'})) if(can(`audio/mp4; codecs="${code}"`)) caps.audio_profiles.aac.push(profile);
  cachedCaps = caps;
  return caps;
}

export async function playbackCaps(file?: FileInfo): Promise<Caps> {
  const base=detectCaps();
  const caps:Caps={...base,video:[...base.video],max_bit_depths:{...base.max_bit_depths}};
  const v=file?.streams.find(t=>t.kind==='video');
  if(!v?.width||!v.height||!navigator.mediaCapabilities?.decodingInfo) return caps;
  const ten=(v.bit_depth??8)>8;
  const codecs:Record<string,string>={h264:ten?(v.height>1080?'avc1.6E0033':'avc1.6E0028'):(v.height>1080?'avc1.640033':'avc1.640028'),hevc:ten?'hvc1.2.4.L153.B0':'hvc1.1.6.L120.90',vp9:ten?'vp09.02.10.10':'vp09.00.10.08',av1:ten?'av01.0.08M.10':'av01.0.05M.08'};
  const codec=codecs[v.codec];if(!codec)return caps;
  try {
    const container=v.codec==='vp9'?'webm':'mp4';
    const result=await Promise.race([navigator.mediaCapabilities.decodingInfo({type:'file',video:{contentType:`video/${container}; codecs="${codec}"`,width:v.width,height:v.height,bitrate:Math.max(128000,Math.round((file?.size_bytes??0)*8000/(file?.duration_ms||1))),framerate:30}}),new Promise<null>(resolve=>setTimeout(()=>resolve(null),500))]);
    if(result&&!result.supported) caps.source_video_unsupported=true;
    else if(result?.supported) caps.max_bit_depths![v.codec]=Math.max(v.bit_depth??8,caps.max_bit_depths![v.codec]??8);
  } catch { /* Older browsers may reject the detailed query; retain their type declarations. */ }
  return caps;
}

export type ImageKind = 'poster' | 'backdrop' | 'banner' | 'logo' | 'thumb' | 'art' | 'disc' | 'box' | 'boxrear' | 'menu';

export const imageUrl = (id: string, kind: ImageKind, rev?: string) => `/api/v1/items/${id}/images/${kind}?w=${kind === 'poster' ? 320 : kind === 'logo' || kind === 'thumb' ? 640 : 1280}${rev ? `&v=${encodeURIComponent(rev)}` : ''}`;
export const posterUrl = (id: string, rev?: string) => imageUrl(id, 'poster', rev);
export const backdropUrl = (id: string, rev?: string) => imageUrl(id, 'backdrop', rev);

const ITEM_CHANGED = 'velora:item-changed';

/** Tras identificar o cambiar imágenes: vuelve a leer la ficha y avisa a las tarjetas y fichas abiertas de ese título. */
export async function notifyItemChanged(id: string) {
  try {
    const { item } = await api<{ item: ItemDetail }>('GET', `/api/v1/items/${id}`);
    if(item?.id===id)window.dispatchEvent(new CustomEvent(ITEM_CHANGED, { detail: item }));
  } catch {
    // Sin ficha visible (serie, temporada o episodio): no hay tarjetas que actualizar.
  }
}

/** Escucha los cambios de un título; devuelve la función para dejar de escuchar. */
export function onItemChanged(id: string, run: (item: ItemDetail) => void) {
  const on = (e: Event) => {
    const item = (e as CustomEvent<ItemDetail>).detail;
    if (item?.id === id) run(item);
  };
  window.addEventListener(ITEM_CHANGED, on);
  return () => window.removeEventListener(ITEM_CHANGED, on);
}

export function fmtDuration(ms: number | null | undefined): string {
  if (!ms) return '';
  const min = Math.max(1, Math.round(ms / 60000));
  const h = Math.floor(min / 60);
  return h ? `${h} h ${min % 60} min` : `${min} min`;
}

/** «Quedan 1 h 34 min» a partir de la posición y la duración. */
export function fmtRemaining(item: ItemCard): string {
  if (!item.runtime_ms || !item.position_ms) return '';
  return `Quedan ${fmtDuration(item.runtime_ms - item.position_ms)}`;
}

export function fmtClock(sec: number): string {
  if (!isFinite(sec) || sec < 0) sec = 0;
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const s = Math.floor(sec % 60);
  const mm = String(m).padStart(h ? 2 : 1, '0');
  return `${h ? `${h}:` : ''}${mm}:${String(s).padStart(2, '0')}`;
}

const LANGS: Record<string, string> = {
  spa: 'Castellano', es: 'Castellano', eng: 'Inglés', en: 'Inglés', cat: 'Catalán', baq: 'Euskera', eus: 'Euskera',
  glg: 'Gallego', fre: 'Francés', fra: 'Francés', ger: 'Alemán', deu: 'Alemán', ita: 'Italiano', por: 'Portugués',
  jpn: 'Japonés', kor: 'Coreano', chi: 'Chino', zho: 'Chino', lat: 'Latino',
};
export const langName = (code: string | null) => (code ? LANGS[code.toLowerCase()] ?? code.toUpperCase() : 'Sin idioma');

/** Calidad por el ancho O el alto (la misma regla que el servidor, qualityRank): 1920×800 panorámico es 1080p. */
export function qualityLabel(width?: number | null, height?: number | null): string | null {
  const w = width ?? 0;
  const h = height ?? 0;
  if (!w && !h) return null;
  if (w >= 3200 || h >= 1600) return '4K';
  if (w >= 1800 || h >= 1000) return '1080p';
  if (w >= 1200 || h >= 700) return '720p';
  return 'SD';
}

// Chips del «Archivo original» (diseño rev. 2, S05): lo que ES el fichero, no lo que recibirá el dispositivo.
export function fileChips(f: FileInfo): string[] {
  const chips: string[] = [];
  const v = f.streams.find((s) => s.kind === 'video');
  const a = f.streams.find((s) => s.kind === 'audio' && s.default) ?? f.streams.find((s) => s.kind === 'audio');
  const q = qualityLabel(v?.width, v?.height);
  // La etiqueta y, al lado, las dimensiones reales (1920×800 panorámico es 1080p: que se vea por qué).
  if (q) chips.push(v?.width && v?.height ? `${q} · ${v.width}×${v.height}` : q);
  if (v) chips.push(v.codec.toUpperCase() + (v.bit_depth && v.bit_depth >= 10 ? ' 10 bits' : ''));
  if (v?.hdr && v.hdr !== 'sdr') chips.push(v.hdr === 'dovi' ? 'Dolby Vision' : v.hdr.toUpperCase());
  if (a) chips.push(`${a.codec.toUpperCase()}${a.channels ? ` ${a.channels > 2 ? `${a.channels - 1}.1` : '2.0'}` : ''}`);
  if (f.container) chips.push(f.container.toUpperCase());
  return chips;
}

/** Tamaño legible: MB hasta 1 GB, luego GB (y TB) con un decimal. */
export function fmtBytes(n: number): string {
  const gb = n / (1 << 30);
  if (gb >= 1024) return `${(gb / 1024).toLocaleString('es-ES', { maximumFractionDigits: 1 })} TB`;
  if (n >= 1 << 30) return `${gb.toLocaleString('es-ES', { maximumFractionDigits: 1 })} GB`;
  return `${Math.round(n / (1 << 20)).toLocaleString('es-ES')} MB`;
}
