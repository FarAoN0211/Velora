import type { AudioDynamics } from '../lib/audioGraph';
import { useAudioDynamics } from '../lib/audioDynamics';
import { newClientId } from '../lib/clientId';
import { PlaybackLogo } from '../components/contentInfo';
// S07 · Reproductor web. Controles inferiores como en TV, tanto en Directo como en Optimizado:
// reproducir/pausa, saltos de 10 y 30 s, barra con lo cargado y tiempo restante, volumen, pantalla completa, atajos de
// teclado y controles que se ocultan solos. En Optimizado buscar relanza el MP4 fragmentado desde ese segundo (?t=).
// El progreso se guarda cada 10 s, al pausar y al salir. B2/B3: menú de audio (cambia de pista en el mismo punto) y
// subtítulos de texto dibujados por la web sobre el vídeo.
import { useCallback, useEffect, useRef, useState, type KeyboardEvent, type PointerEvent } from 'react';
import { useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { ArrowLeft, Check, ChevronsRight, Info, Loader2, Maximize, Minimize, Pause, PictureInPicture2, Play, RotateCcw, RotateCw, Volume1, Volume2, VolumeX } from 'lucide-react';
import { Acciones, type Accion } from '../lib/acciones';
import { api, ApiError, csrfToken, deviceId } from '../lib/api';
import { playbackCaps, extraName, fmtClock, langName, type ItemDetail, type Plan, type Caps } from '../lib/catalog';
import { activeCues, channelsLabel, defaultSubtitle, parseVTT, type Cue, type SubTrack } from '../lib/subtitles';
import { Button, Loading } from '../components/ui';
import { isTv } from '../lib/tv';
import { useMobilePlayerFullscreen } from '../lib/playerFullscreen';

const SAVE_EVERY_MS = 10_000;
/** Aviso durante créditos o, sin marcas, en los últimos 30 segundos. */
const NEXT_UP_BEFORE_S = 30;
const HIDE_AFTER_MS = 3000;
const VOLUME_KEY = 'velora.volumen';

function readVolume(): number {
  try {
    const v = Number(localStorage.getItem(VOLUME_KEY));
    return Number.isFinite(v) && v > 0 && v <= 1 ? v : 1;
  } catch {
    return 1;
  }
}

interface Chapter {
  start: number;
  end: number;
  title?: string;
  kind?: 'intro' | 'credits';
  /** Intro o créditos detectados comparando episodios, no un capítulo del archivo. */
  detected?: boolean;
}

const RATES = [0.5, 0.75, 1, 1.25, 1.5, 2];

/** Sale del reproductor sin dejarlo en el historial: Atrás no debe volver a un vídeo ya cerrado. */
function useLeavePlayer(id: string) {
  const navigate = useNavigate();
  return useCallback(() => {
    if ((window.history.state?.idx ?? 0) > 0) navigate(-1);
    else navigate(`/ficha/${id}`, { replace: true }); // película, o la serie en la temporada del episodio
  }, [id, navigate]);
}

/** El APK usa Media3 en todos los dispositivos; el navegador y los APK antiguos conservan el respaldo web. */
export function Player() {
  const { id = '' } = useParams();
  const [params] = useSearchParams();
  const leave = useLeavePlayer(id);
  const [nativeRejected, setNativeRejected] = useState(false);
  const native = !!window.VeloraApp?.reproducir && !params.get('web') && !nativeRejected;
  useMobilePlayerFullscreen(!native);
  useEffect(() => {
    if (!native) return;
    const audio = params.get('audio');
    const started = window.VeloraApp?.reproducir?.(
      JSON.stringify({ item: id, file: params.get('file') ?? '', audio: audio === null ? -1 : Number(audio), inicio: !!params.get('inicio'), device: deviceId(), sub: params.get('sub') ?? '' }),
    );
    if (started) leave();
    else setNativeRejected(true);
  }, [native, id, params, leave]);
  // key: al pasar al episodio siguiente el reproductor empieza de cero (plan, pistas, capítulos y progreso).
  return native ? <Loading /> : <WebPlayer key={id} />;
}

function WebPlayer() {
  const { id = '' } = useParams();
  const navigate = useNavigate();
  const leave = useLeavePlayer(id);
  const [params] = useSearchParams();
  const video = useRef<HTMLVideoElement>(null);
  const shell = useRef<HTMLElement>(null);
  const contextualFocused = useRef(false);
  const controlsRef = useRef<HTMLDivElement>(null);
  const topControlsRef = useRef<HTMLDivElement>(null);
  const contextualRef = useRef<HTMLDivElement>(null);
  const nextButtonRef = useRef<HTMLButtonElement>(null);
  const cancelButtonRef = useRef<HTMLButtonElement>(null);
  const focusedNotice = useRef('');
  const [nextCancelled, setNextCancelled] = useState(false);
  const [nextDeadline, setNextDeadline] = useState<number | null>(null);
  const [layout, setLayout] = useState({ height: window.innerHeight, top: 80, noticeHeight: 48, noticeWidth: 160 });
  const subtitlesRef = useRef<HTMLDivElement>(null);
  const [controlsHeight, setControlsHeight] = useState(160);
  const [subtitlesHeight, setSubtitlesHeight] = useState(0);
  const [item, setItem] = useState<ItemDetail | null>(null);
  const [plan, setPlan] = useState<Plan | null>(null);
  const [audioDynamics,setAudioDynamics]=useState<AudioDynamics|null>(()=>{try{const m=localStorage.getItem('velora.audio-dynamics');return m==='off'||m==='balanced'||m==='night'?m:null;}catch{return null;}});
  const planRef = useRef<Plan | null>(null);
  planRef.current = plan;
  const [fatal, setFatal] = useState<string | null>(null); // no se puede reproducir (antes de empezar)
  const [streamError, setStreamError] = useState(false); // fallo durante la reproducción
  const [offset, setOffset] = useState(0); // Optimizado: segundo del archivo que corresponde al 0 del flujo
  const [seekT, setSeekT] = useState(0); // Optimizado: segundo pedido al servidor (el mismo con el que se midió offset)
  const [current, setCurrent] = useState(0);
  const [buffered, setBuffered] = useState(0);
  const [playing, setPlaying] = useState(false);
  const [waiting, setWaiting] = useState(true);
  const [volume, setVolume] = useState(readVolume);
  const [muted, setMuted] = useState(false);
  const [fullscreen, setFullscreen] = useState(false);
  const [visible, setVisible] = useState(!params.has("controles_ocultos"));
  const controlsOnlyOnInput = useRef(params.has("controles_ocultos"));
  const contextualNow = useRef(false);
  const [showInfo, setShowInfo] = useState(false);
  const [scrub, setScrub] = useState<number | null>(null); // posición mientras se arrastra la barra
  const startAt = useRef(0);
  const lastSaved = useRef(0);
  const retries = useRef(0);
  const decoderFallbacks = useRef(0);
  const clientCaps = useRef<Caps>();
  const [fileId, setFileId] = useState('');
  const [menuOpen, setMenuOpen] = useState(false);
  const menuOpenRef = useRef(false);
  menuOpenRef.current = menuOpen;
  const [subs, setSubs] = useState<SubTrack[]>([]);
  const subtitleRequest = useRef(0);
  const subtitleAbort = useRef<AbortController>();
  useEffect(() => () => subtitleAbort.current?.abort(), []);
  const [subId, setSubId] = useState<string | null>(null);
  const [cues, setCues] = useState<Cue[]>([]);
  const [subNote, setSubNote] = useState<string | null>(null); // Solo errores; la preparación ocurre en segundo plano.
  const [audioNote, setAudioNote] = useState<string | null>(null);
  const prevPlan = useRef<Plan | null>(null); // para volver a la pista anterior si la nueva falla
  const autoplay = useRef(true);
  const [chapters, setChapters] = useState<Chapter[]>([]);
  const [rate, setRate] = useState(1);
  const [nonce, setNonce] = useState(0); // cada flujo optimizado nuevo lleva un número distinto (fuerza la recarga)
  const fileRef = useRef('');
  // Tráiler o extra (E1.3 T1): mismo reproductor, pero sin reanudar, sin progreso y sin «vista». El servidor rechaza
  // además cualquier progreso que indique un extra.
  const extraRef = useRef(false);

  // En Optimizado el flujo empieza en un fotograma clave anterior al punto pedido (con -c:v copy no puede empezar en
  // otro), y el audio arranca en ese mismo punto para ir sincronizado. Se pregunta al servidor a qué segundo del
  // archivo corresponde el 0 del flujo: lo mide FFmpeg con el mismo salto y la misma pista de audio, así que el reloj,
  // la barra y los subtítulos coinciden con la imagen. El flujo se pide luego con ese mismo segundo. Si no responde, se
  // usa el punto pedido. Solo vale el último salto: uno nuevo cancela la consulta del anterior, y una respuesta que llega
  // tarde se descarta (nunca abre un flujo viejo encima del nuevo).
  // Dos carriles: la preparación (ficha y decisión) y lo que el usuario hace al reproducir (saltar, cambiar o restaurar
  // el audio, reintentar). Empezar una acción cancela las peticiones de la anterior del mismo carril, y cada acción
  // comprueba que sigue vigente antes de publicar nada: una respuesta tardía o desordenada nunca pisa a una posterior.
  const prep = useRef(new Acciones()).current;
  const acciones = useRef(new Acciones()).current;
  // Abre el flujo optimizado en sec dentro de la acción a. Devuelve false si la acción ya no vale: quien la llamó no sigue.
  async function openAt(a: Accion, sec: number, audioIndex = planRef.current?.audio_index ?? -1, exact = planRef.current?.method === 'transcode'): Promise<boolean> {
    sec = Math.max(0, sec);
    let start = sec;
    try {
      const r = exact ? { start: sec } : await api<{ start: number }>('GET', `/api/v1/stream/files/${fileRef.current}/seek?t=${sec.toFixed(3)}&audio=${audioIndex}`, undefined, a.signal);
      if (Number.isFinite(r.start) && r.start <= sec + 0.01) start = r.start;
    } catch {
      /* cancelado o sin respuesta: se usa sec */
    }
    if (!a.vigente()) return false;
    setNonce((n) => n + 1);
    setSeekT(sec);
    setOffset(start);
    return true;
  } // un salto en Optimizado abre un flujo nuevo: solo arranca solo si se estaba reproduciendo
  const hideTimer = useRef<number>();

  const remux = (plan?.method === 'direct_stream' || plan?.method === 'transcode');
  const duration = (plan?.duration_ms ?? 0) / 1000;
  // «Saltar intro/créditos» solo dentro de un capítulo marcado como tal (nunca por una duración supuesta).
  const special = chapters.find((c) => (c.kind === 'intro' || c.kind === 'credits') && current >= c.start && current < c.end - 1);
  const nextEp = item?.extras.some((x) => x.id === fileId) ? undefined : item?.next_episode;
  const creditsAt = chapters.find((c) => c.kind === 'credits')?.start;
  const finalNotice = !special && creditsAt === undefined && !!nextEp && duration > NEXT_UP_BEFORE_S * 2 && current >= duration - NEXT_UP_BEFORE_S && current < duration;
  const contextual = !streamError && !menuOpen && (special || finalNotice);

  contextualNow.current = !!contextual;
  const controlsShown = visible && !contextual;
  const nextNotice = !!contextual && special?.kind !== 'intro' && !!nextEp;
  const noticeKey = contextual ? `${special?.kind ?? 'final'}:${special?.start ?? 0}` : '';
  const countdown = nextDeadline === null ? 10 : Math.max(0, Math.ceil(nextDeadline - current));
  useEffect(() => {
    if (menuOpen) { setNextDeadline(null); return; }
    if (nextNotice && !nextCancelled && nextDeadline === null) {
      setNextDeadline(current + Math.min(10, Math.max(0.25, Math.min(duration, special?.end ?? duration) - current - 1.25)));
    }
    if (nextNotice && !nextCancelled && nextDeadline !== null && current >= nextDeadline && playing) goNext();
  }, [nextNotice, nextCancelled, nextDeadline, current, playing, menuOpen]);
  useEffect(() => {
    if (noticeKey) { controlsOnlyOnInput.current = true; setVisible(false); }
    if (noticeKey && focusedNotice.current !== noticeKey) {
      focusedNotice.current = noticeKey;
      nextButtonRef.current?.focus({ preventScroll: true });
    }
    if (!noticeKey && !menuOpen) focusedNotice.current = '';
  }, [noticeKey, menuOpen]);

  const position = useCallback(
    () => (remux ? offset + (video.current?.currentTime ?? 0) : (video.current?.currentTime ?? 0)),
    [remux, offset],
  );

  const save = useCallback(
    (keepalive = false) => {
      const pos = Math.floor(position() * 1000);
      if (!plan || pos <= 0 || extraRef.current) return;
      lastSaved.current = Date.now();
      const body = JSON.stringify({ file_id: fileRef.current, position_ms: pos, duration_ms: plan.duration_ms, client_time: new Date().toISOString() });
      void fetch(`/api/v1/me/items/${id}/progress`, {
        method: 'PUT',
        body,
        keepalive,
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json', 'X-Velora-Client': 'web', 'X-Velora-Device-Id': deviceId(), 'X-CSRF-Token': csrfToken() },
      }).catch(() => undefined);
    },
    [id, plan, position],
  );

  const [presenceId] = useState(newClientId);
  useEffect(() => {
    const a = prep.begin();
    const stale = () => !a.vigente();
    const listas = new AbortController(); // capítulos y subtítulos de este archivo: se cortan al cambiar de título o salir
    (async () => {
      try {
        const { item } = await api<{ item: ItemDetail }>('GET', `/api/v1/items/${id}`, undefined, a.signal);
        if (stale()) return; // otro título u otros parámetros: esta preparación ya no vale
        setItem(item);
        const fileId = params.get('file') ?? item.files.find((f) => !f.missing)?.id ?? '';
        setFileId(fileId);
        setChapters([]);
        fileRef.current = fileId;
        extraRef.current = item.extras.some((x) => x.id === fileId);
        // No retrasan el vídeo; si llegan cuando ya se reproduce otro archivo, se descartan.
        const mismo = () => !listas.signal.aborted && fileRef.current === fileId;
        api<{ chapters: Chapter[] }>('GET', `/api/v1/stream/files/${fileId}/chapters`, undefined, listas.signal)
          .then((r) => mismo() && setChapters(r.chapters))
          .catch(() => mismo() && setChapters([]));
        api<{ subtitles: SubTrack[]; provider_error?: string }>('GET', `/api/v1/stream/files/${fileId}/subtitles`, undefined, listas.signal)
          .then((r) => { if (mismo()) { setSubs(r.subtitles ?? []); const preference = params.get('sub'); const requested = !preference || preference === 'auto' ? defaultSubtitle(r.subtitles ?? []) : preference; if (requested && requested !== 'off' && (r.subtitles ?? []).some(t => t.id === requested && t.supported)) void chooseSub(requested, fileId); if (r.provider_error) setSubNote(r.provider_error); } })
          .catch(() => mismo() && setSubs([]));
        const audio = params.get('audio');
        clientCaps.current = await playbackCaps(item.files.find(f=>f.id===fileId));
        if (stale()) return;
        const p = await api<Plan>('POST', '/api/v1/playback/decide', {
          file_id: fileId, player_id: presenceId,
          caps: clientCaps.current,
          audio_index: audio === null ? null : Number(audio),
        }, a.signal);
        if (stale()) return;
        if (p.method === 'unsupported') {
          setFatal(p.reasons?.join(' ') ?? 'Este archivo no se puede reproducir en este dispositivo todavía.');
          return;
        }
        const resume = extraRef.current || params.get('inicio') ? 0 : item.played ? 0 : item.position_ms / 1000;
        startAt.current = resume;
        // La primera apertura ya es una acción de reproducción: un salto o un cambio de audio posterior la sustituye.
        if ((p.method === 'direct_stream' || p.method === 'transcode') && !(await openAt(acciones.begin(), resume - 2, p.audio_index, p.method === 'transcode'))) return;
        if (stale()) return;
        setPlan(p);
      } catch (e) {
        if (stale()) return;
        setFatal(e instanceof ApiError ? e.message : 'No se ha podido preparar la reproducción.');
      }
    })();
    return () => {
      // Al desmontar o cambiar de título: nada pendiente se aplica y sus peticiones se cortan.
      prep.cancel();
      acciones.cancel();
      listas.abort();
    };
  }, [id, params]);

  // Presence is independent from resume history, including paused and buffering players.
  const presencePosition = useRef(position);
  presencePosition.current = position;
  useEffect(() => {
    if (!plan || !fileId) return;
    const send = (stopped = false) => {
      const v = video.current;
      const state = stopped || v?.ended ? 'stopped' : v?.paused ? 'paused' : !v || v.readyState < 3 ? 'buffering' : 'playing';
      void fetch('/api/v1/playback/heartbeat', {
        method: 'POST', credentials: 'same-origin', keepalive: stopped,
        headers: { 'Content-Type': 'application/json', 'X-Velora-Client': 'web', 'X-Velora-Device-Id': deviceId(), 'X-CSRF-Token': csrfToken() },
        body: JSON.stringify({ id: presenceId, file_id: fileId, state, position_ms: Math.max(0, Math.floor(presencePosition.current() * 1000)) }),
      }).then(async response=>{if(response.status===429&&!stopped){const e=await response.json().catch(()=>null);video.current?.pause();setPlan(null);setFatal(e?.error?.message||e?.message||'Se ha alcanzado el límite de reproducciones simultáneas.');}}).catch(() => undefined);
    };
    const tick = () => send();
    const stop = () => send(true);
    const v = video.current;
    send();
    const timer = setInterval(tick, 5000);
    for (const ev of ['play', 'pause', 'waiting', 'playing', 'ended']) v?.addEventListener(ev, tick);
    window.addEventListener('pagehide', stop);
    return () => {
      clearInterval(timer);
      for (const ev of ['play', 'pause', 'waiting', 'playing', 'ended']) v?.removeEventListener(ev, tick);
      window.removeEventListener('pagehide', stop);
      stop();
    };
  }, [fileId, !!plan]);

  // Guardado periódico y al salir de la página.
  useEffect(() => {
    if (!plan) return;
    const t = setInterval(() => {
      if (!video.current?.paused && Date.now() - lastSaved.current >= SAVE_EVERY_MS) save();
    }, 2000);
    const onHide = () => save(true);
    window.addEventListener('pagehide', onHide);
    return () => {
      clearInterval(t);
      window.removeEventListener('pagehide', onHide);
      save(true);
    };
  }, [plan, save]);

  // Volumen recordado entre películas.
  useEffect(() => {
    try {
      localStorage.setItem(VOLUME_KEY, String(volume));
    } catch {
      /* sin almacenamiento: no pasa nada */
    }
  }, [volume, muted]);

  useEffect(() => {
    const onFs = () => setFullscreen(!!document.fullscreenElement);
    document.addEventListener('fullscreenchange', onFs);
    return () => document.removeEventListener('fullscreenchange', onFs);
  }, []);

  // Controles que se ocultan solos mientras se reproduce; cualquier movimiento, toque o tecla los muestra.
  const poke = useCallback(() => {
    if (contextualNow.current || controlsOnlyOnInput.current) { setVisible(false); return; }
    setVisible(true);
    window.clearTimeout(hideTimer.current);
    hideTimer.current = window.setTimeout(() => {
      if (!video.current?.paused && !menuOpenRef.current) setVisible(false);
    }, HIDE_AFTER_MS);
  }, []);
  useEffect(() => () => window.clearTimeout(hideTimer.current), []);
  useEffect(() => {
    if (!playing && !controlsOnlyOnInput.current && !contextualNow.current) setVisible(true);
    else poke();
  }, [playing, poke]);
  useEffect(() => shell.current?.focus(), [plan]);
  // Con el mando, al abrir el menú se entra en él y al cerrarlo se vuelve al vídeo.
  const menuRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (menuOpen) { controlsOnlyOnInput.current = false; setVisible(true); }
    if (menuOpen) menuRef.current?.querySelector<HTMLElement>('button:not([disabled])')?.focus();
    else shell.current?.focus();
  }, [menuOpen]);

  function seekTo(sec: number) {
    sec = Math.max(0, Math.min(Math.max(0, duration - 1), sec));
    setCurrent(sec);
    // También en Directo: un salto deja sin efecto un cambio de audio que aún esperaba al servidor.
    const a = acciones.begin();
    if (!remux) {
      if (video.current) video.current.currentTime = sec;
      return;
    }
    autoplay.current = !!video.current && !video.current.paused;
    setWaiting(autoplay.current);
    setPlaying(autoplay.current); // el flujo nuevo no emite «pause» si nace en pausa
    void openAt(a, sec); // relanza el flujo optimizado desde ese punto
  }

  function toggle() {
    const v = video.current;
    if (!v) return;
    if (v.paused) void v.play();
    else v.pause();
  }

  // B2: cambiar de pista de audio en el mismo punto y con el mismo estado (pausa o reproducción). Puede tardar unos
  // segundos: en Optimizado se abre un flujo nuevo (y el servidor cierra el anterior de esta sesión).
  async function switchAudio(index: number) {
    if (!plan || index === plan.audio_index) return;
    const pos = position();
    const wasPlaying = !!video.current && !video.current.paused;
    setAudioNote('Cambiando el audio…');
    const a = acciones.begin();
    const stale = () => {
      if (a.vigente()) return false;
      setAudioNote(null); // un salto u otro cambio ha pasado delante: este se abandona
      return true;
    };
    try {
      const p = await api<Plan>('POST', '/api/v1/playback/decide', { file_id: fileId, player_id: presenceId, caps: clientCaps.current ?? await playbackCaps(item?.files.find(f=>f.id===fileId)), audio_index: index }, a.signal);
      if (stale()) return;
      if (p.method === 'unsupported') {
        setAudioNote('Esa pista de audio no se puede reproducir en este dispositivo.');
        return;
      }
      prevPlan.current = plan;
      autoplay.current = wasPlaying;
      setPlaying(wasPlaying);
      setWaiting(wasPlaying);
      startAt.current = pos;
      if ((p.method === 'direct_stream' || p.method === 'transcode') && !(await openAt(a, pos, p.audio_index, p.method === 'transcode'))) return void stale();
      if (stale()) return;
      setPlan(p);
      setAudioNote(null);
    } catch (e) {
      if (stale()) return;
      setAudioNote(e instanceof ApiError ? e.message : 'No se ha podido cambiar el audio.');
    }
  }

  function restorePrevious() {
    const p = prevPlan.current;
    if (!p) return;
    prevPlan.current = null;
    const pos = position();
    setStreamError(false);
    setWaiting(true);
    autoplay.current = true;
    startAt.current = pos;
    const a = acciones.begin();
    if (p.method === 'direct_stream' || p.method === 'transcode') void openAt(a, pos, p.audio_index, p.method === 'transcode');
    setPlan(p);
  }

  // Las pistas internas llegan por frases mientras se extraen y se conservan en caché.
  async function chooseSub(id: string | null, sourceFile = fileId) {
    subtitleAbort.current?.abort();
    const controller = new AbortController();
    subtitleAbort.current = controller;
    const request = ++subtitleRequest.current;
    setSubId(id);
    setCues([]);
    setSubNote(null);
    if (!id) return;
    try {
      const res = await fetch(`/api/v1/stream/files/${sourceFile}/subtitles/${id}`, { signal: controller.signal, credentials: 'same-origin', headers: { 'X-Velora-Client': 'web' } });
      if (!res.ok) throw new Error();
      const reader = res.body?.getReader();
      const decoder = new TextDecoder();
      let text = '';
      const publish = (complete: boolean) => {
        if (request !== subtitleRequest.current || fileRef.current !== sourceFile) return;
        const end = complete ? text.length : text.lastIndexOf('\n\n') + 2;
        const parsed = parseVTT(text.slice(0, end));
        setCues(parsed);
        if (parsed.length || complete) setSubNote(parsed.length ? null : 'Este subtítulo está vacío.');
      };
      if (reader) {
        for (;;) {
          const {value,done} = await reader.read();
          text += decoder.decode(value, {stream: !done});
          publish(done);
          if (done) break;
        }
      } else { text = await res.text(); publish(true); }
    } catch {
      if (request !== subtitleRequest.current) return;
      setSubNote('No se ha podido cargar este subtítulo.');
    }
  }

  // Créditos de una película: se da por vista y se vuelve a la ficha.
  // Pasa al episodio siguiente: este queda visto (también si se salta en los créditos, antes del 90 %).
  const leavingRef = useRef(false);
  function goNext() {
    const next = item?.next_episode;
    if (!next || leavingRef.current) return;
    leavingRef.current = true;
    save(true);
    void api('POST', `/api/v1/me/items/${id}/played`)
      .catch(() => undefined)
      .finally(() => navigate(`/ver/${next.id}?controles_ocultos=1`, { replace: true }));
  }

  function skipCredits() {
    if (!plan) return;
    if (extraRef.current) {
      leave();
      return;
    }
    if (item?.next_episode) {
      goNext();
      return;
    }
    if (leavingRef.current) return;
    leavingRef.current = true;
    save(true);
    void api('POST', `/api/v1/me/items/${id}/played`)
      .catch(() => undefined)
      .finally(() => leave());
  }

  function setSpeed(r: number) {
    setRate(r);
    if (video.current) video.current.playbackRate = r;
  }

  function togglePip() {
    if (document.pictureInPictureElement) void document.exitPictureInPicture();
    else void video.current?.requestPictureInPicture?.();
  }

  function toggleFullscreen() {
    if (document.fullscreenElement) void document.exitFullscreen();
    else void shell.current?.requestFullscreen?.();
  }

  async function recoverDecoder(at: number, a: Accion, code: number): Promise<boolean> {
    const previous=planRef.current;
    if ((code!==3&&code!==4)||!previous||previous.method==='transcode'||decoderFallbacks.current>=2||!clientCaps.current) return false;
    const forceVideo=previous.method!=='direct_play'||decoderFallbacks.current>0;
    decoderFallbacks.current++;
    const caps={...clientCaps.current,force_remux:true,...(forceVideo?{source_video_unsupported:true}:{})};
    try {
      const p=await api<Plan>('POST','/api/v1/playback/decide',{file_id:fileRef.current,caps,audio_index:previous.audio_index},a.signal);
      if(!a.vigente()) return true;
      if(p.method==='unsupported'||p.method==='direct_play') return false;
      clientCaps.current=caps;
      autoplay.current=true;
      if(!await openAt(a,at,p.audio_index,p.method==='transcode')) return true;
      setStreamError(false);setWaiting(true);setPlan(p);
      return true;
    } catch {return !a.vigente();}
  }

  function retry() {
    retries.current = 0;
    setStreamError(false);
    setWaiting(true);
    const at = Math.max(0, position() - 1);
    const a = acciones.begin();
    if (remux) void openAt(a, at);
    else if (video.current) {
      startAt.current = at;
      video.current.load();
    }
  }

  // Atajos en toda la página: si el foco se pierde (p. ej. al desaparecer un botón mientras carga), siguen funcionando.
  // Las barras de posición y volumen detienen la propagación para usar sus propias flechas.
  const onKeyRef = useRef<(e: globalThis.KeyboardEvent) => void>(() => undefined);
  useEffect(() => {
    const h = (e: globalThis.KeyboardEvent) => onKeyRef.current(e);
    document.addEventListener('keydown', h);
    return () => document.removeEventListener('keydown', h);
  }, []);

  function onKey(e: globalThis.KeyboardEvent | KeyboardEvent) {
    if (e.ctrlKey || e.metaKey || e.altKey) return;
    if (!contextualNow.current && !(e.target as HTMLElement | null)?.closest('[data-player-notice]')) controlsOnlyOnInput.current = false;
    // Con el menú abierto, las flechas son para moverse por sus opciones (también con el mando).
    if (menuOpen && e.key !== 'Escape') return;
    const el = e.target as HTMLElement | null;
    if (el && (el.isContentEditable || ['TEXTAREA', 'SELECT'].includes(el.tagName) || (el.tagName === 'INPUT' && (el as HTMLInputElement).type !== 'range')))
      return;
    if (el?.closest('[data-player-notice]') && (e.key === 'ArrowLeft' || e.key === 'ArrowRight')) {
      e.preventDefault();
      (e.key === 'ArrowLeft' ? cancelButtonRef.current ?? nextButtonRef.current : nextButtonRef.current)?.focus();
      return;
    }
    // Espacio y Enter activan el botón enfocado con su comportamiento nativo.
    if (el?.closest('button') && (e.key === ' ' || e.key === 'Enter')) return;
    const keys: Record<string, () => void> = {
      ' ': toggle,
      k: toggle,
      // Botones multimedia del mando.
      MediaPlayPause: toggle,
      MediaPlay: () => video.current?.paused && toggle(),
      MediaPause: () => video.current?.paused === false && toggle(),
      MediaRewind: () => seekTo(position() - 10),
      MediaFastForward: () => seekTo(position() + 30),
      ArrowLeft: () => seekTo(position() - 10),
      ArrowRight: () => seekTo(position() + 30),
      j: () => seekTo(position() - 10),
      l: () => seekTo(position() + 30),
      ArrowUp: () => setVolume((v) => Math.min(1, Math.round((v + 0.1) * 10) / 10)),
      ArrowDown: () => setVolume((v) => Math.max(0, Math.round((v - 0.1) * 10) / 10)),
      f: toggleFullscreen,
      m: () => setMuted((m) => !m),
      Escape: () => setMenuOpen(false),
      s: () => setMenuOpen((o) => !o),
    };
    const fn = keys[e.key];
    if (fn) {
      e.preventDefault();
      fn();
      poke();
    }
  }

  // La fila puede ocupar más de una línea en móvil; reserva su altura real.
  useEffect(() => {
    const observer = new ResizeObserver(() => {
      setControlsHeight(controlsRef.current?.getBoundingClientRect().height ?? 160);
      setSubtitlesHeight(subtitlesRef.current?.getBoundingClientRect().height ?? 0);
      const notice = contextualRef.current?.getBoundingClientRect();
      setLayout({
        height: shell.current?.getBoundingClientRect().height ?? window.innerHeight,
        top: topControlsRef.current?.getBoundingClientRect().height ?? 80,
        noticeHeight: notice?.height ?? 48,
        noticeWidth: notice?.width ?? 160,
      });
    });
    for (const element of [shell.current, topControlsRef.current, contextualRef.current]) {
      if (element) observer.observe(element);
    }
    if (controlsRef.current) observer.observe(controlsRef.current);
    if (subtitlesRef.current) observer.observe(subtitlesRef.current);
    return () => observer.disconnect();
  }, [!!plan, !!contextual, !!subId && activeCues(cues, scrub ?? current).length > 0]);

  // Al ocultar o retirar un control enfocado, devuelve el foco a la reproducción.
  useEffect(() => {
    const active = document.activeElement;
    if ((!visible && active?.closest('[data-player-controls]')) ||
        (contextualFocused.current && active === document.body)) {
      contextualFocused.current = false;
      shell.current?.focus({ preventScroll: true });
    }
  }, [visible, menuOpen, current, chapters]);

  onKeyRef.current = onKey;

  const src = plan ? (remux ? `${plan.url}&t=${seekT.toFixed(3)}&n=${nonce}` : plan.url ?? '') : '';
  const audioStatus = useAudioDynamics(video, src, audioDynamics ?? plan?.audio_dynamics ?? 'balanced', volume, muted);

  if (fatal)
    return (
      <main className="mx-auto flex min-h-full max-w-lg flex-col justify-center gap-4 p-6">
        <h1 className="text-xl">No se puede reproducir</h1>
        <p className="rounded-lg bg-surface p-4 text-text-muted">{fatal}</p>
        <div className="flex gap-2">
          <Button variant="secondary" onClick={() => leave()}>
            <ArrowLeft aria-hidden className="h-4 w-4" /> Volver a la ficha
          </Button>
          <Button onClick={() => window.location.reload()}>Reintentar</Button>
        </div>
      </main>
    );
  if (!plan || !item) return <Loading />;

  // El mismo segundo con el que se midió offset (/seek): el servidor cae exactamente en el mismo punto.
  const shown = Math.min(duration, scrub ?? current);
  const remaining = Math.max(0, duration - shown);
  const extra = item.extras.find((x) => x.id === fileId);
  const audios = (extra ?? item.files.find((f) => f.id === fileId))?.streams.filter((x) => x.kind === 'audio') ?? [];
  const lines = subId ? activeCues(cues, shown) : [];
  // La lista solo muestra los capítulos del archivo.
  const fileChapters = chapters.filter((c) => !c.detected);
  const overlayBottom = controlsShown ? controlsHeight + 16 : 28;
  const desiredNoticeBottom = overlayBottom;
  const noticeCeiling = layout.height - (controlsShown ? layout.top + 8 : 8) - layout.noticeHeight;
  // Con poca altura, comparte franja con los subtítulos en lugar de invadir la barra superior.
  const besideSubtitles = !!contextual && lines.length > 0 && overlayBottom + layout.noticeHeight + subtitlesHeight + 16 > layout.height - layout.top - 8;
  const noticeBottom = Math.max(0, Math.min(besideSubtitles ? overlayBottom : desiredNoticeBottom, noticeCeiling));
  const VolIcon = muted || volume === 0 ? VolumeX : volume < 0.5 ? Volume1 : Volume2;
  const controlsCls = `transition-opacity duration-base ${controlsShown ? 'opacity-100' : 'invisible pointer-events-none opacity-0'}`;
  return (
    <main
      ref={shell}
      tabIndex={-1}
      aria-label={`Reproductor: ${extra ? `${extraName(extra)} de ${item.title}` : item.title}`}
      className={`fixed inset-0 flex select-none flex-col bg-scrim outline-none ${controlsShown ? '' : 'cursor-none'}`}
      onPointerMove={(e: PointerEvent) => {
        if (e.pointerType !== 'mouse' || (e.target as HTMLElement).closest('[data-player-notice]')) return;
        if (!contextualNow.current) controlsOnlyOnInput.current = false;
        poke();
      }}
      onPointerDown={e => {
        if ((e.target as HTMLElement).closest('[data-player-notice]')) return;
        if (!contextualNow.current) controlsOnlyOnInput.current = false;
        poke();
      }}
      onFocusCapture={poke}
    >
      <video
        key={src}
        ref={video}
        src={src}
        autoPlay={autoplay.current}
        preload="auto"
        playsInline
        className="h-full w-full flex-1 bg-scrim object-contain"
        onClick={poke}
        onLoadedMetadata={() => {
          if (video.current) {
            video.current.playbackRate = rate;
            if (!remux && startAt.current > 0) video.current.currentTime = startAt.current;
          }
        }}
        onTimeUpdate={() => setCurrent(Math.min(duration, position()))}
        onProgress={() => {
          const v = video.current;
          if (v && v.buffered.length) setBuffered((remux ? offset : 0) + v.buffered.end(v.buffered.length - 1));
        }}
        onWaiting={() => setWaiting(true)}
        onCanPlay={() => setWaiting(false)}
        onLoadedData={() => setWaiting(false)}
        onPlaying={() => {
          setWaiting(false);
          setPlaying(true);
          retries.current = 0;
        }}
        onPause={() => {
          setPlaying(false);
          save();
        }}
        onEnded={() => {
          // Un flujo optimizado cortado por el servidor (pausa larga) termina antes de tiempo: se continúa desde ahí.
          if (remux && position() < duration - 5) {
            void openAt(acciones.begin(), position() - 1);
            return;
          }
          setPlaying(false);
          if (extraRef.current) {
            leave(); // un tráiler acaba y se vuelve a la ficha
            return;
          }
          save();
          if (item.next_episode && !nextCancelled) goNext();
        }}
        onError={() => {
          const code=video.current?.error?.code??0;
          // Solo se reintenta un fallo pasajero: si la sesión o el permiso ya no valen (401/404), no se insiste.
          const at = Math.max(0, position() - 1);
          const a = acciones.begin();
          void fetch(`/api/v1/items/${id}`, { credentials: 'same-origin', headers: { 'X-Velora-Client': 'web' }, signal: a.signal })
            .then((r) => r.ok)
            .catch(() => true)
            .then(async (allowed) => {
              if (!a.vigente()) return; // otra acción ha pasado delante mientras se comprobaba
              if (allowed && await recoverDecoder(at,a,code)) return;
              if (allowed && remux && code!==3 && code!==4 && retries.current < 3) {
                retries.current += 1;
                void openAt(a, at);
                return;
              }
              setWaiting(false);
              setStreamError(true);
            });
        }}
      />

      {/* Cargando: «Preparando…» al empezar y al buscar en Optimizado. */}
      {waiting && !streamError && (
        <div role="status" className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center gap-3 text-text">
          <Loader2 aria-hidden className="h-12 w-12 animate-spin text-accent motion-reduce:animate-none" />
          <span className="text-sm text-text-muted">{current > 0 ? 'Cargando…' : 'Preparando…'}</span>
        </div>
      )}

      {/* Subtítulos: encima de la barra inferior cuando está visible. */}
      {lines.length > 0 && (
        <div
          ref={subtitlesRef}
          style={{ bottom: contextual && !besideSubtitles ? overlayBottom + layout.noticeHeight + 8 : overlayBottom, right: besideSubtitles ? layout.noticeWidth + 32 : 0 }}
          aria-live="off"
          className="pointer-events-none absolute left-0 flex justify-center px-6 transition-all duration-base"
        >
          <p className="max-w-4xl whitespace-pre-line rounded-md bg-scrim px-3 py-1 text-center text-lg font-semibold text-text desktop:text-2xl">
            {lines.join('\n')}
          </p>
        </div>
      )}
      {contextual && (
        <div ref={contextualRef} data-player-notice style={{ bottom: noticeBottom }}
          className="absolute right-4 z-10 flex max-w-[calc(100%-2rem)] items-center gap-2 desktop:right-8"
          onFocus={() => { contextualFocused.current = true; }} onBlur={() => { contextualFocused.current = false; }}>
          {nextNotice && !nextCancelled && <button type="button" ref={cancelButtonRef}
            onClick={() => { setNextCancelled(true); nextButtonRef.current?.focus(); }}
            className="rounded-full border border-text bg-scrim px-4 py-3 text-sm font-semibold text-text focus:bg-accent focus:text-scrim focus:outline focus:outline-2 focus:outline-text">
            Cancelar avance automático
          </button>}
          <button type="button" data-player-contextual ref={nextButtonRef}
            aria-label={special?.kind === 'intro' ? 'Saltar intro' : nextEp ? 'Siguiente episodio' : 'Saltar créditos'}
            onClick={() => { controlsOnlyOnInput.current = true; setVisible(false); shell.current?.focus(); special?.kind === 'intro' ? seekTo(special.end) : skipCredits(); }}
            className="flex items-center gap-2 rounded-full border border-text bg-scrim px-4 py-3 text-sm font-semibold text-text shadow-pop hover:bg-surface-raised focus:bg-accent focus:text-scrim focus:outline focus:outline-2 focus:outline-text">
            <ChevronsRight aria-hidden className="h-5 w-5" /> {special?.kind === 'intro' ? 'Saltar intro' : nextEp ? `Siguiente episodio${!nextCancelled ? ` (${countdown} s)` : ''}` : 'Saltar créditos'}
          </button>
        </div>
      )}
      {(subNote || audioNote) && (
        <div role="status" className="absolute left-1/2 top-20 -translate-x-1/2 rounded-full bg-surface-overlay px-4 py-2 text-sm text-text shadow-pop">
          {audioNote ?? subNote}
        </div>
      )}

      {streamError && (
        <div role="alert" className="absolute inset-0 flex items-center justify-center bg-scrim p-6">
          <div className="flex max-w-md flex-col gap-4 rounded-xl bg-surface-overlay p-6 text-center shadow-pop">
            <p className="text-lg font-semibold">Se ha interrumpido la reproducción</p>
            <p className="text-sm text-text-muted">El servidor o la conexión no han respondido. Puedes reintentar desde {fmtClock(position())}.</p>
            <div className="flex justify-center gap-2">
              <Button variant="secondary" onClick={() => leave()}>
                Volver a la ficha
              </Button>
              {prevPlan.current && (
                <Button variant="secondary" onClick={restorePrevious}>
                  Volver al audio anterior
                </Button>
              )}
              <Button onClick={retry}>Reintentar</Button>
            </div>
          </div>
        </div>
      )}

      {/* Menú de audio y subtítulos: mientras está abierto, los controles no se ocultan. */}
      {menuOpen && (
        <div
          ref={menuRef}
          style={{ bottom: controlsHeight + 16, maxHeight: `calc(100dvh - ${controlsHeight + 32}px)` }}
          role="dialog"
          aria-label={isTv ? 'Audio y subtítulos' : 'Audio, subtítulos y velocidad'}
          className="absolute bottom-24 right-4 z-10 flex max-h-[60vh] w-80 max-w-[calc(100%-2rem)] flex-col gap-4 overflow-y-auto rounded-xl bg-surface-overlay p-4 text-sm shadow-pop desktop:right-8"
        >
          <section aria-label="Opciones de reproducción" className="flex flex-wrap gap-2">
            <Button variant="ghost" aria-label="Volver a la ficha" onClick={() => leave()}><ArrowLeft aria-hidden className="h-5 w-5" />Volver</Button>
            <Button variant="ghost" aria-label="Información de reproducción" aria-pressed={showInfo} onClick={() => { setShowInfo(v => !v); setMenuOpen(false); }}><Info aria-hidden className="h-5 w-5" />Información</Button>
            {!isTv && <Button variant="ghost" aria-label={fullscreen ? 'Salir de pantalla completa' : 'Pantalla completa'} onClick={() => { toggleFullscreen(); setMenuOpen(false); }}>{fullscreen ? <Minimize aria-hidden className="h-5 w-5" /> : <Maximize aria-hidden className="h-5 w-5" />}Pantalla completa</Button>}
            {document.pictureInPictureEnabled && <Button variant="ghost" aria-label="Imagen dentro de imagen" onClick={togglePip}><PictureInPicture2 aria-hidden className="h-5 w-5" />Ventana flotante</Button>}
            <Button variant="ghost" aria-label={muted ? 'Activar sonido' : 'Silenciar'} onClick={() => setMuted(m => !m)}><VolIcon aria-hidden className="h-5 w-5" />Sonido</Button>
            <label className="flex w-full items-center gap-3 text-text-muted">Volumen<input type="range" min={0} max={1} step={0.05} value={muted ? 0 : volume} onChange={e => { setVolume(Number(e.target.value)); setMuted(false); }} onKeyDown={e => e.stopPropagation()} aria-label="Volumen" className="min-w-0 flex-1 accent-[var(--color-accent)]" /></label>
          </section>
          <section>
            <h2 className="mb-2 font-semibold text-text">Audio</h2>
            <label className="mb-2 flex flex-col gap-2">Volumen automático<select className="rounded-lg border border-border bg-surface p-2 text-text" value={audioDynamics??plan.audio_dynamics??'balanced'} onKeyDown={e=>e.stopPropagation()} onChange={e=>{const mode=e.target.value as AudioDynamics;setAudioDynamics(mode);try{localStorage.setItem('velora.audio-dynamics',mode);}catch{/* Per-playback only when storage unavailable. */}}}><option value="off">Original</option><option value="balanced">Equilibrado · diálogos y acción</option><option value="night">Nocturno · menos sonidos fuertes</option></select></label>
            <p className="mb-2 text-xs text-text-muted" role="status">{audioStatus}</p>
            <ul className="flex flex-col gap-1">
              {audios.map((a) => (
                <li key={a.index}>
                  <button
                    type="button"
                    aria-pressed={a.index === plan.audio_index}
                    onClick={() => void switchAudio(a.index)}
                    className="flex w-full items-center gap-2 rounded-md px-2 py-2 text-left hover:bg-surface-raised"
                  >
                    <Check aria-hidden className={`h-4 w-4 shrink-0 ${a.index === plan.audio_index ? 'text-accent' : 'invisible'}`} />
                    <span className="flex-1">
                      {langName(a.language)}
                      <span className="block text-xs text-text-muted">
                        {[a.codec.toUpperCase(), channelsLabel(a.channels), a.default ? 'predeterminada' : '', a.title].filter(Boolean).join(' · ')}
                      </span>
                    </span>
                  </button>
                </li>
              ))}
              {audios.length === 0 && <li className="text-text-muted">Sin pistas de audio.</li>}
            </ul>
          </section>
          {!isTv && <section>
            <h2 className="mb-2 font-semibold text-text">Velocidad</h2>
            <div className="flex flex-wrap gap-1">
              {RATES.map((r) => (
                <button
                  key={r}
                  type="button"
                  aria-pressed={rate === r}
                  onClick={() => setSpeed(r)}
                  className={`rounded-full px-3 py-1 ${rate === r ? 'bg-accent text-on-accent' : 'bg-surface-raised text-text hover:bg-surface'}`}
                >
                  {r === 1 ? 'Normal' : `${String(r).replace('.', ',')}×`}
                </button>
              ))}
            </div>
          </section>}
          {!isTv && fileChapters.length > 1 && (
            <section>
              <h2 className="mb-2 font-semibold text-text">Capítulos</h2>
              <ul className="flex flex-col gap-1">
                {fileChapters.map((c, i) => (
                  <li key={c.start}>
                    <button
                      type="button"
                      onClick={() => seekTo(c.start)}
                      className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left hover:bg-surface-raised"
                    >
                      <span className="w-12 shrink-0 tabular-nums text-text-muted">{fmtClock(c.start)}</span>
                      <span className="truncate">{c.title || `Capítulo ${i + 1}`}</span>
                    </button>
                  </li>
                ))}
              </ul>
            </section>
          )}
          <section>
            <h2 className="mb-2 font-semibold text-text">Subtítulos</h2>
            <ul className="flex flex-col gap-1">
              <li>
                <button
                  type="button"
                  aria-pressed={subId === null}
                  onClick={() => void chooseSub(null)}
                  className="flex w-full items-center gap-2 rounded-md px-2 py-2 text-left hover:bg-surface-raised"
                >
                  <Check aria-hidden className={`h-4 w-4 ${subId === null ? 'text-accent' : 'invisible'}`} /> Desactivados
                </button>
              </li>
              {subs.map((t) => (
                <li key={t.id}>
                  <button
                    type="button"
                    disabled={!t.supported}
                    aria-pressed={subId === t.id}
                    onClick={() => void chooseSub(t.id)}
                    className="flex w-full items-center gap-2 rounded-md px-2 py-2 text-left hover:bg-surface-raised disabled:cursor-not-allowed disabled:opacity-50"
                  >
                    <Check aria-hidden className={`h-4 w-4 shrink-0 ${subId === t.id ? 'text-accent' : 'invisible'}`} />
                    <span className="flex-1">
                      {t.label || `${langName(t.language ?? null)} · ${t.forced ? 'Forzados' : 'Completos'}`}
                      {t.provider && t.title && <span className="block text-xs text-text-muted">{t.title}</span>}
                      <span className="block text-xs text-text-muted">
                        {t.supported
                          ? [t.codec.toUpperCase(), t.provider || (t.external ? 'archivo aparte' : 'en el archivo'), t.default ? 'predeterminados' : ''].filter(Boolean).join(' · ')
                          : 'Subtítulo de imagen: llegará con la conversión de vídeo (E2)'}
                      </span>
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          </section>
        </div>
      )}

      {/* Barra superior */}
      <div ref={topControlsRef} data-player-controls className={`absolute inset-x-0 top-0 flex items-start px-7 py-6 ${controlsCls}`} style={{ background: 'linear-gradient(180deg, var(--color-scrim), transparent)' }}>
        <div className="min-w-0 flex-1 [&>img]:max-w-full">
          <PlaybackLogo key={item.id} item={item} />
          {item.kind === 'episode' && !extra && <div data-player-episode className="mt-2 text-text">
            {!item.has_logo && item.series_title && <p className="truncate text-lg font-semibold">{item.series_title}</p>}
            <p className="text-sm font-semibold">{item.season_number === 0 ? 'Especiales' : `Temporada ${item.season_number ?? '?'}`} · Episodio {item.episode_number ?? '?'}{item.episode_end ? `–${item.episode_end}` : ''}</p>
            {item.title && item.title !== item.series_title && <p className="mt-1 line-clamp-2 text-sm text-text-muted">{item.title}</p>}
          </div>}
        </div>

      </div>
      {showInfo && (
        <div role="status" className="absolute right-4 top-20 max-w-sm rounded-lg bg-surface-overlay p-4 text-sm shadow-pop">
          <p className="font-semibold">{plan.summary}</p>
          {plan.reasons?.map((r) => (
            <p key={r} className="mt-1 text-text-muted">
              {r}
            </p>
          ))}
        </div>
      )}

      {/* Barra inferior */}
      <div
        ref={controlsRef}
        data-player-controls
        className={`absolute inset-x-0 bottom-0 flex flex-col gap-2 px-4 pb-5 pt-5 desktop:px-8 ${controlsCls}`}
        style={{ background: 'linear-gradient(0deg, var(--color-scrim), transparent)' }}
      >
        {((item.kind !== 'episode' && !item.has_logo) || extra) && <p className="truncate text-lg font-semibold text-text">
          {item.title}
          {extra && <span className="font-normal text-text-muted"> · {extraName(extra)}</span>}
        </p>}
        <div className="relative flex h-5 items-center">
          <div className="pointer-events-none absolute inset-x-0 h-1 rounded-full bg-progress-track">
            <div className="absolute inset-y-0 left-0 rounded-full bg-text-subtle" style={{ width: `${duration ? Math.min(100, (buffered / duration) * 100) : 0}%` }} />
            <div className="absolute inset-y-0 left-0 rounded-full bg-progress-fill" style={{ width: `${duration ? Math.min(100, (shown / duration) * 100) : 0}%` }} />
            {duration > 0 &&
              fileChapters.slice(1).map((c) => (
                <span key={c.start} aria-hidden className="absolute inset-y-0 w-0.5 bg-scrim" style={{ left: `${(c.start / duration) * 100}%` }} />
              ))}
          </div>
          <input
            type="range"
            min={0}
            max={Math.max(1, Math.floor(duration))}
            step={1}
            value={Math.floor(shown)}
            onChange={(e) => setScrub(Number(e.target.value))}
            onPointerUp={() => {
              if (scrub !== null) {
                seekTo(scrub);
                setScrub(null);
              }
            }}
            onKeyUp={() => {
              if (scrub !== null) {
                seekTo(scrub);
                setScrub(null);
              }
            }}
            onKeyDown={(e) => e.stopPropagation()}
            aria-label="Posición"
            aria-valuetext={`${fmtClock(shown)} de ${fmtClock(duration)}`}
            className="relative h-5 w-full cursor-pointer appearance-none bg-transparent [&::-moz-range-thumb]:h-4 [&::-moz-range-thumb]:w-4 [&::-moz-range-thumb]:rounded-full [&::-moz-range-thumb]:border-0 [&::-moz-range-thumb]:bg-text [&::-webkit-slider-thumb]:h-4 [&::-webkit-slider-thumb]:w-4 [&::-webkit-slider-thumb]:appearance-none [&::-webkit-slider-thumb]:rounded-full [&::-webkit-slider-thumb]:bg-text"
          />
        </div>
        <div className="flex items-center justify-between gap-2 text-sm text-text">
          <span className="tabular-nums text-text-muted">
            {fmtClock(shown)} / {fmtClock(duration)}
          </span>
          <span className="ml-auto tabular-nums text-text-muted" aria-label={`Quedan ${fmtClock(remaining)}`}>
            −{fmtClock(remaining)}
          </span>
        </div>
        <div data-player-buttons className="flex items-center gap-2 pt-2 text-text">
          <button type="button" className="player-control" aria-label="Retroceder 10 segundos" onClick={() => seekTo(position() - 10)}>
            <RotateCcw aria-hidden className="h-6 w-6" />
          </button>
          <button type="button" className="player-control player-play" aria-label={playing ? 'Pausa' : 'Reproducir'} onClick={toggle}>
            {playing ? <Pause aria-hidden className="h-6 w-6 fill-current" /> : <Play aria-hidden className="h-6 w-6 fill-current" />}
          </button>
          <button type="button" className="player-control" aria-label="Avanzar 30 segundos" onClick={() => seekTo(position() + 30)}>
            <RotateCw aria-hidden className="h-6 w-6" />
          </button>
          <button type="button" className="player-control player-tracks" aria-label="Audio y subtítulos" aria-expanded={menuOpen} onClick={() => setMenuOpen((o) => !o)}>
            Audio y subtítulos
          </button>
        </div>
      </div>
    </main>
  );
}
