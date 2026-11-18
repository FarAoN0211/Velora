// E1.3 I2 · «Identificar» y «Editar imágenes» (solo administradores). Identificar corrige los datos de la ficha: nunca el
// archivo, su ruta ni el progreso, y nada se aplica sin elegir un resultado y confirmarlo. Las imágenes elegidas o subidas
// se guardan aparte de los medios y ni TMDB ni el escaneo las sustituyen; se puede volver a la automática.
import { useEffect, useMemo, useRef, useState, type FormEvent, type KeyboardEvent, type PointerEvent } from 'react';
import { Image as ImageIcon, Upload } from 'lucide-react';
import { api, ApiError } from '../lib/api';
import { imageUrl, notifyItemChanged, type ImageKind } from '../lib/catalog';
import { Banner, Button } from './ui';
import { inputClass, Modal, type Editable } from './itemAdmin';

const message = (e: unknown, fallback: string) => (e instanceof ApiError ? e.message : fallback);
const thumbUrl = (path: string, size = 'w185') => `/api/v1/admin/metadata/thumb?${new URLSearchParams({ path, size })}`;

/** Flechas entre botones de una lista o rejilla (teclado y mando de televisión). */
function arrowNav(e: KeyboardEvent<HTMLElement>) {
  if (!['ArrowDown', 'ArrowUp', 'ArrowLeft', 'ArrowRight'].includes(e.key)) return;
  const items = [...e.currentTarget.querySelectorAll<HTMLElement>('button')];
  const i = items.indexOf(document.activeElement as HTMLElement);
  if (i < 0) return;
  e.preventDefault();
  items[(i + (e.key === 'ArrowDown' || e.key === 'ArrowRight' ? 1 : -1) + items.length) % items.length]?.focus();
}

interface Candidate {
  tmdb_id: number;
  title: string;
  original_title: string;
  year?: number;
  overview: string;
  poster_path?: string;
  in_library: boolean;
  current: boolean;
}

interface Preview {
  current: { title: string; year: number | null; overview: string; tmdb_id?: string };
  candidate: { tmdb_id: number; title: string; original_title: string; year: number; overview: string; poster_path: string; backdrop_path: string };
  locked_fields: string[];
  locked: boolean;
  custom_images: string[];
}

const FIELD_LABEL: Record<string, string> = {
  title: 'título',
  sort_title: 'título para ordenar',
  original_title: 'título original',
  year: 'año',
  tagline: 'lema',
  overview: 'sinopsis',
  official_rating: 'clasificación',
  genres: 'géneros',
};

function Poster({ path, label }: { path?: string; label: string }) {
  return path ? (
    <img src={thumbUrl(path)} alt={label} loading="lazy" className="aspect-poster w-16 shrink-0 rounded-sm bg-surface-raised object-cover" />
  ) : (
    <div aria-label={`${label}: sin carátula`} className="flex aspect-poster w-16 shrink-0 items-center justify-center rounded-sm bg-surface-raised text-text-subtle">
      <ImageIcon aria-hidden className="h-5 w-5" />
    </div>
  );
}

interface ItemSource {
  library: string;
  folder: string;
  filename?: string;
  missing: boolean;
}

function ContentSource({ sources, kind }: { sources: ItemSource[]; kind?: string }) {
  return <section aria-label="Origen del contenido" className="rounded-md border border-border bg-surface p-3">
    <h3 className="text-sm font-semibold">Origen del contenido</h3>
    <p className="mt-1 text-xs text-text-muted">Nombres originales de tu biblioteca. Compáralos con la ficha antes de confirmar.</p>
    <ul className="mt-3 flex max-h-48 flex-col gap-3 overflow-y-auto">
      {sources.map((source, index) => <li key={`${source.library}:${source.folder}:${source.filename ?? ''}:${index}`} className="text-sm">
        <p className="text-xs text-text-muted">Biblioteca: {source.library}{source.missing ? ' · No disponible ahora' : ''}</p>
        <dl className="mt-1 grid min-w-0 grid-cols-[auto_1fr] gap-x-3 gap-y-1">
          <dt className="text-text-muted">Carpeta</dt>
          <dd className="min-w-0 select-text break-all">{!source.folder || source.folder === '.' ? 'Raíz de la biblioteca' : source.folder}</dd>
          {kind !== 'series' && source.filename && <><dt className="text-text-muted">Archivo</dt><dd className="min-w-0 select-text break-all">{source.filename}</dd></>}
        </dl>
      </li>)}
    </ul>
    {sources.length === 0 && <p className="mt-2 text-sm text-text-muted">No hay un origen registrado para esta ficha.</p>}
  </section>;
}

export function IdentifyDialog({ itemId, title, onClose, onEditManually }: { itemId: string; title: string; onClose: () => void; onEditManually: () => void }) {
  const base = `/api/v1/admin/items/${itemId}`;
  const [meta, setMeta] = useState<(Editable & { can_undo?: boolean; match_source?: string; kind?: string }) | null>(null);
  const [sources, setSources] = useState<ItemSource[] | null>(null);
  const [query, setQuery] = useState('');
  const [year, setYear] = useState('');
  const [tmdbId, setTmdbId] = useState('');
  const [results, setResults] = useState<Candidate[] | null>(null);
  const [preview, setPreview] = useState<Preview | null>(null);
  const [replaceImages, setReplaceImages] = useState(true);
  const [replaceLocked, setReplaceLocked] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState<string | null>(null);

  const load = async () => {
    const r = await api<{ metadata: NonNullable<typeof meta>; sources?: ItemSource[] }>('GET', `${base}/metadata`);
    setMeta(r.metadata);
    setSources(r.sources ?? []);
    return r.metadata;
  };
  useEffect(() => {
    load()
      .then((m) => {
        setQuery(m.title);
        setYear(m.year ? String(m.year) : '');
      })
      .catch((e) => setError(message(e, 'No se ha podido cargar la ficha.')));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [itemId]);

  async function run(action: () => Promise<void>) {
    setBusy(true);
    setError(null);
    try {
      await action();
    } catch (e) {
      setError(message(e, 'No se ha podido completar. La ficha no ha cambiado.'));
    } finally {
      setBusy(false);
    }
  }

  const search = (e: FormEvent) => {
    e.preventDefault();
    void run(async () => {
      const params = new URLSearchParams();
      if (tmdbId.trim()) params.set('tmdb_id', tmdbId.trim());
      else params.set('q', query.trim());
      if (year.trim() && !tmdbId.trim()) params.set('year', year.trim());
      setResults(null);
      const r = await api<{ candidates: Candidate[] }>('GET', `${base}/identify/search?${params}`);
      setResults(r.candidates);
    });
  };
  const choose = (c: Candidate) => run(async () => setPreview(await api<Preview>('GET', `${base}/identify/preview?tmdb_id=${c.tmdb_id}`)));
  const apply = () =>
    run(async () => {
      await api('POST', `${base}/identify`, { tmdb_id: preview!.candidate.tmdb_id, replace_images: replaceImages, replace_locked: replaceLocked });
      await notifyItemChanged(itemId);
      await load();
      setPreview(null);
      setResults(null);
      setDone('Identificación corregida. Los archivos y el progreso no se han tocado.');
    });
  const undo = () =>
    run(async () => {
      await api('POST', `${base}/identify/undo`);
      await notifyItemChanged(itemId);
      const m = await load();
      setDone(`Se ha vuelto a los datos anteriores: ${m.title}.`);
    });
  const reset = () => {
    if (!window.confirm('Volver a la identificación automática:\n\n• La ficha vuelve al nombre de la carpeta y se abren todos los candados.\n• TMDB volverá a buscarla por su cuenta.\n\nLos archivos no se tocan y se puede deshacer. ¿Seguir?')) return;
    const images = window.confirm('¿Quitar también las imágenes que elegiste o subiste para este título?\n\nAceptar: las quita. Cancelar: las conserva.');
    void run(async () => {
      await api('POST', `${base}/identify/reset`, { restore_images: images });
      await notifyItemChanged(itemId);
      await load();
      setResults(null);
      setDone('La ficha ha vuelto a la identificación automática.');
    });
  };

  const lockedList = preview?.locked_fields.map((f) => FIELD_LABEL[f] ?? f).join(', ');
  const footer = preview ? (
    <>
      <Button variant="ghost" onClick={() => setPreview(null)}>
        Volver a los resultados
      </Button>
      <Button onClick={() => void apply()} loading={busy} disabled={busy || preview.locked}>
        Confirmar
      </Button>
    </>
  ) : (
    <>
      {meta?.can_undo && (
        <Button variant="ghost" onClick={() => void undo()} disabled={busy}>
          Deshacer
        </Button>
      )}
      {meta?.match_source === 'manual' && (
        <Button variant="ghost" onClick={reset} disabled={busy}>
          Restablecer
        </Button>
      )}
      <Button variant="secondary" onClick={onClose}>
        {done ? 'Cerrar' : 'Conservar la ficha actual'}
      </Button>
    </>
  );

  return (
    <Modal title={`Identificar · ${title}`} onClose={onClose} footer={footer}>
      <div className="flex flex-col gap-4">
        {error && <Banner tone="error">{error}</Banner>}
        {done && !error && <Banner>{done}</Banner>}
        {sources && <ContentSource sources={sources} kind={meta?.kind} />}
        {preview ? (
          <>
            <p className="text-sm text-text-muted">Comprueba que es el título correcto. Solo cambia la ficha: el archivo, su ruta y el progreso se quedan igual.</p>
            <div className="grid gap-3 tablet:grid-cols-2">
              <section className="rounded-md bg-surface p-3" aria-label="Ahora">
                <h3 className="mb-2 text-sm font-semibold text-text-muted">Ahora</h3>
                <p className="font-semibold">
                  {preview.current.title} {preview.current.year ? `(${preview.current.year})` : ''}
                </p>
                <p className="mt-1 line-clamp-6 text-sm text-text-muted">{preview.current.overview || 'Sin sinopsis.'}</p>
              </section>
              <section className="rounded-md bg-surface p-3" aria-label="Nuevo">
                <h3 className="mb-2 text-sm font-semibold text-accent">Nuevo</h3>
                <div className="flex gap-3">
                  <Poster path={preview.candidate.poster_path} label={`Carátula de ${preview.candidate.title}`} />
                  <div className="min-w-0">
                    <p className="font-semibold">
                      {preview.candidate.title} {preview.candidate.year ? `(${preview.candidate.year})` : ''}
                    </p>
                    {preview.candidate.original_title !== preview.candidate.title && <p className="text-xs text-text-muted">{preview.candidate.original_title}</p>}
                    <p className="text-xs text-text-subtle">TMDB {preview.candidate.tmdb_id}</p>
                  </div>
                </div>
                <p className="mt-2 line-clamp-6 text-sm text-text-muted">{preview.candidate.overview || 'Sin sinopsis.'}</p>
              </section>
            </div>
            {preview.locked && <Banner tone="error">Esta ficha está bloqueada entera. Desbloquéala en «Editar información» para identificarla.</Banner>}
            <label className="flex items-start gap-3 rounded-md bg-surface p-3 text-sm">
              <input type="checkbox" checked={replaceImages} onChange={(e) => setReplaceImages(e.target.checked)} className="mt-0.5 h-5 w-5 accent-[var(--color-accent)]" />
              <span>
                <span className="font-semibold">Sustituir la carátula y el fondo de TMDB</span>
                <span className="block text-xs text-text-muted">
                  {preview.custom_images.length > 0 ? 'Las imágenes que elegiste o subiste se conservan y siguen mostrándose.' : 'Las imágenes de tu carpeta siguen teniendo prioridad.'}
                </span>
              </span>
            </label>
            {preview.locked_fields.length > 0 && (
              <label className="flex items-start gap-3 rounded-md bg-surface p-3 text-sm">
                <input type="checkbox" checked={replaceLocked} onChange={(e) => setReplaceLocked(e.target.checked)} className="mt-0.5 h-5 w-5 accent-[var(--color-accent)]" />
                <span>
                  <span className="font-semibold">Sustituir también los campos bloqueados</span>
                  <span className="block text-xs text-text-muted">Si no lo marcas, se conservan: {lockedList}.</span>
                </span>
              </label>
            )}
          </>
        ) : (
          <>
            <form className="flex flex-col gap-3" onSubmit={search}>
              <div className="flex flex-col gap-1">
                <label htmlFor="i-q" className="text-sm font-semibold">
                  Título
                </label>
                <input id="i-q" className={inputClass} value={query} maxLength={300} onChange={(e) => setQuery(e.target.value)} placeholder="También tmdb-603 o imdb-tt0133093" />
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div className="flex flex-col gap-1">
                  <label htmlFor="i-year" className="text-sm font-semibold">
                    Año de estreno
                  </label>
                  <input id="i-year" className={inputClass} inputMode="numeric" maxLength={4} value={year} onChange={(e) => setYear(e.target.value.replace(/\D/g, ''))} />
                </div>
                <div className="flex flex-col gap-1">
                  <label htmlFor="i-tmdb" className="text-sm font-semibold">
                    Identificador TMDB
                  </label>
                  <input id="i-tmdb" className={inputClass} inputMode="numeric" maxLength={9} value={tmdbId} onChange={(e) => setTmdbId(e.target.value.replace(/\D/g, ''))} placeholder="Si lo conoces" />
                </div>
              </div>
              <div>
                <Button type="submit" loading={busy} disabled={busy || (!query.trim() && !tmdbId.trim())}>
                  Buscar
                </Button>
              </div>
            </form>
            {results && results.length > 0 && (
              <ul className="flex flex-col gap-2" aria-label="Resultados" onKeyDown={arrowNav}>
                {results.map((c) => (
                  <li key={c.tmdb_id}>
                    <button
                      type="button"
                      disabled={busy}
                      onClick={() => void choose(c)}
                      className="flex w-full gap-3 rounded-md bg-surface p-3 text-left hover:bg-surface-raised focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus"
                    >
                      <Poster path={c.poster_path} label={`Carátula de ${c.title}`} />
                      <span className="min-w-0 flex-1">
                        <span className="block font-semibold text-text">
                          {c.title} {c.year ? `(${c.year})` : '(año desconocido)'}
                        </span>
                        {c.original_title && c.original_title !== c.title && <span className="block text-xs text-text-muted">{c.original_title}</span>}
                        <span className="mt-1 line-clamp-3 block text-xs text-text-muted">{c.overview || 'Sin sinopsis.'}</span>
                        <span className="mt-1 block text-xs text-text-subtle">
                          {[`TMDB ${c.tmdb_id}`, c.current ? 'Es la actual' : null, c.in_library ? 'Ya en tu biblioteca' : null].filter(Boolean).join(' · ')}
                        </span>
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
            )}
            {(results?.length === 0 || (error && !results)) && (
              <div className="rounded-md bg-surface p-3 text-sm">
                {results?.length === 0 && <p className="mb-2">No hay resultados. Prueba sin el año, con el título original o con el identificador.</p>}
                <Button variant="secondary" onClick={onEditManually}>
                  Editar la información a mano
                </Button>
              </div>
            )}
            <p className="text-xs text-text-subtle">Búsqueda de películas y series con datos de TMDB. Nada cambia hasta que confirmes.</p>
          </>
        )}
      </div>
    </Modal>
  );
}

const KIND_LABEL: Record<ImageKind, string> = { poster: 'Principal', art:'Arte con transparencia', backdrop:'Imagen de fondo', banner:'Pancarta', box:'Caja', boxrear:'Caja (trasera)', disc:'Disco', logo:'Logo', menu:'Menú', thumb:'Miniatura' };
const KIND_HINT: Record<ImageKind, string> = {
  art:'Arte transparente. Se conserva para personalizar la ficha.',
  box:'Caja o cubierta. Disponible según la fuente; también puedes subir una propia.',
  boxrear:'Parte trasera de la caja. Disponible según la fuente o mediante una imagen propia.',
  disc:'Imagen del disco; conserva la transparencia.',
  menu:'Imagen de menú. Disponible según la fuente o mediante una imagen propia.',
  poster: 'Vertical. Se ve en las tarjetas y en la ficha.',
  backdrop: 'Horizontal. Es el fondo de la ficha.',
  banner: 'Horizontal. Se usa en la cabecera del inicio en lugar del fondo.',
  logo: 'Se muestra en la ficha en lugar del título. Conserva la transparencia.',
  thumb: 'Horizontal. Miniatura de temporadas y episodios.',
};
const SOURCE_LABEL: Record<string, string> = { user: 'Tu imagen', local: 'De la carpeta de la película', tmdb: 'De TMDB',fanart:'De Fanart',tvdb:'De TheTVDB',omdb:'De OMDb' };
/** Proporción de recorte; el logotipo no se recorta. */
const ASPECT: Record<ImageKind, number | null> = { poster: 2 / 3, backdrop: 16 / 9, banner: 16 / 9, thumb: 16 / 9, logo: null, art:null, disc:1, box:null, boxrear:null, menu:16/9 };
const OUTPUT_WIDTH: Record<ImageKind, number> = { poster: 1000, backdrop: 1920, banner: 1920, thumb: 1280, logo: 1200, art:1600,disc:1600,box:1600,boxrear:1600,menu:1920 };

interface GalleryImage {
  file_path: string;
  width: number;
  height: number;
  language?: string;
  provider?:string;
  kind?:ImageKind;
}
interface Gallery {
  posters: GalleryImage[];
  backdrops: GalleryImage[];
  logos: GalleryImage[];
  stills: GalleryImage[];
}
interface ImagesState {
  slots: { kind: ImageKind; source?: string }[];
  image_rev: string;
  tmdb_configured: boolean;
  limits: { max_bytes: number; max_side: number; max_pixels: number };
}

const listFor = (g: Gallery, kind: ImageKind) => (kind === 'poster' ? g.posters : kind === 'logo' ? g.logos : kind === 'thumb' ? [...g.stills, ...g.backdrops] : kind==='backdrop'||kind==='banner' ? g.backdrops : []);
const transparent=(kind:ImageKind)=>['logo','art','disc','box'].includes(kind);
const PROVIDERS:Record<string,string>={tmdb:'TheMovieDb',fanart:'Fanart',tvdb:'TheTVDB',omdb:'The Open Movie Database'};

/** Recorte propio en el navegador: arrastrar (ratón o dedo), flechas para mover y deslizador o +/- para acercar. */
function Cropper({ bitmap, kind, onCancel, onSave, busy }: { bitmap: ImageBitmap; kind: ImageKind; onCancel: () => void; onSave: (b: Blob | null) => void; busy: boolean }) {
  const aspect = ASPECT[kind];
  const canvas = useRef<HTMLCanvasElement>(null);
  const [zoom, setZoom] = useState(1);
  const [pan, setPan] = useState({ x: 0.5, y: 0.5 }); // centro del recorte, en proporción de la imagen
  const drag = useRef<{ x: number; y: number; pan: { x: number; y: number } } | null>(null);
  const boxW = aspect && aspect < 1 ? 240 : 480;
  const boxH = aspect ? Math.round(boxW / aspect) : Math.round(boxW * (bitmap.height / bitmap.width));

  // Trozo de la imagen original que queda dentro del recorte.
  const rect = useMemo(() => {
    if (!aspect) return { sx: 0, sy: 0, sw: bitmap.width, sh: bitmap.height };
    const imgAspect = bitmap.width / bitmap.height;
    const sw = (imgAspect > aspect ? bitmap.height * aspect : bitmap.width) / zoom;
    const sh = sw / aspect;
    const clamp = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, v));
    return { sx: clamp(pan.x * bitmap.width - sw / 2, 0, bitmap.width - sw), sy: clamp(pan.y * bitmap.height - sh / 2, 0, bitmap.height - sh), sw, sh };
  }, [aspect, bitmap, zoom, pan]);

  useEffect(() => {
    const c = canvas.current;
    if (!c) return;
    c.width = boxW;
    c.height = boxH;
    const ctx = c.getContext('2d');
    ctx?.clearRect(0, 0, boxW, boxH);
    ctx?.drawImage(bitmap, rect.sx, rect.sy, rect.sw, rect.sh, 0, 0, boxW, boxH);
  }, [bitmap, rect, boxW, boxH]);

  const move = (dx: number, dy: number) => setPan((p) => ({ x: Math.min(1, Math.max(0, p.x + dx)), y: Math.min(1, Math.max(0, p.y + dy)) }));
  const onPointerDown = (e: PointerEvent<HTMLCanvasElement>) => {
    e.currentTarget.setPointerCapture(e.pointerId);
    drag.current = { x: e.clientX, y: e.clientY, pan };
  };
  const onPointerMove = (e: PointerEvent<HTMLCanvasElement>) => {
    const d = drag.current;
    if (!d) return;
    // Arrastrar la imagen mueve el recorte en sentido contrario, a la escala de lo que se ve.
    setPan({
      x: Math.min(1, Math.max(0, d.pan.x - ((e.clientX - d.x) / boxW) * (rect.sw / bitmap.width))),
      y: Math.min(1, Math.max(0, d.pan.y - ((e.clientY - d.y) / boxH) * (rect.sh / bitmap.height))),
    });
  };
  const onKey = (e: KeyboardEvent) => {
    const step = 0.03 / zoom;
    if (e.key === 'ArrowLeft') move(-step, 0);
    else if (e.key === 'ArrowRight') move(step, 0);
    else if (e.key === 'ArrowUp') move(0, -step);
    else if (e.key === 'ArrowDown') move(0, step);
    else if (e.key === '+' || e.key === '=') setZoom((z) => Math.min(4, z + 0.1));
    else if (e.key === '-') setZoom((z) => Math.max(1, z - 0.1));
    else return;
    e.preventDefault();
  };
  function save() {
    const w = Math.round(Math.min(OUTPUT_WIDTH[kind], rect.sw));
    const out = document.createElement('canvas');
    out.width = w;
    out.height = Math.round((w * rect.sh) / rect.sw);
    out.getContext('2d')?.drawImage(bitmap, rect.sx, rect.sy, rect.sw, rect.sh, 0, 0, out.width, out.height);
    const type = transparent(kind) ? 'image/png' : 'image/jpeg';
    out.toBlob(onSave, type, 0.9);
  }
  return (
    <div className="flex flex-col items-center gap-3 rounded-md bg-surface p-3">
      <canvas
        ref={canvas}
        tabIndex={aspect ? 0 : -1}
        role="img"
        aria-label={aspect ? `Vista previa del recorte: ${KIND_LABEL[kind]}. Flechas para mover, más y menos para acercar.` : `Vista previa: ${KIND_LABEL[kind]}`}
        onKeyDown={aspect ? onKey : undefined}
        onPointerDown={aspect ? onPointerDown : undefined}
        onPointerMove={aspect ? onPointerMove : undefined}
        onPointerUp={() => (drag.current = null)}
        onPointerCancel={() => (drag.current = null)}
        style={{ width: boxW, maxWidth: '100%', touchAction: 'none' }}
        className={`rounded-sm ring-1 ring-border focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus ${aspect ? 'cursor-move' : ''}`}
      />
      {aspect && (
        <label className="flex w-full max-w-sm items-center gap-3 text-sm">
          Acercar
          <input type="range" min={1} max={4} step={0.05} value={zoom} onChange={(e) => setZoom(Number(e.target.value))} className="flex-1 accent-[var(--color-accent)]" />
        </label>
      )}
      <p className="text-xs text-text-muted">
        {aspect ? 'Arrastra para encuadrar. Así se guardará.' : 'La imagen se guarda entera, conservando su transparencia.'} ({bitmap.width}×{bitmap.height})
      </p>
      <div className="flex gap-2">
        <Button variant="ghost" onClick={onCancel}>
          Descartar
        </Button>
        <Button onClick={save} loading={busy} disabled={busy}>
          Guardar como {KIND_LABEL[kind].toLowerCase()}
        </Button>
      </div>
    </div>
  );
}

export function ImagesDialog({ itemId, title, onClose }: { itemId: string; title: string; onClose: () => void }) {
  const base = `/api/v1/admin/items/${itemId}`;
  const [state, setState] = useState<ImagesState | null>(null);
  const [kind, setKind] = useState<ImageKind>('poster');
  const [gallery, setGallery] = useState<Gallery | null>(null);
  const [external,setExternal]=useState<GalleryImage[]>([]);
  const [source,setSource]=useState('all');
  const [identified, setIdentified] = useState(true);
  const [galleryError, setGalleryError] = useState<string | null>(null);
  const [zoomed, setZoomed] = useState<GalleryImage | null>(null);
  const [bitmap, setBitmap] = useState<ImageBitmap | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState<string | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);

  useEffect(() => {
    api<ImagesState>('GET', `${base}/images`)
      .then((s) => {
        setState(s);
        if (s.slots.length > 0) setKind(s.slots[0].kind);
      })
      .catch((e) => setError(message(e, 'No se han podido cargar las imágenes.')));
    api<{ gallery: Gallery; identified: boolean; external?:GalleryImage[]; warnings?:string[] }>('GET', `${base}/images/gallery`)
      .then((r) => {
        setGallery(r.gallery);
        setExternal(r.external??[]);
        if(r.warnings?.length)setGalleryError(r.warnings.join(' · '));
        setIdentified(r.identified);
      })
      .catch((e) => setGalleryError(message(e, 'No se ha podido leer la galería de imágenes.')));
  }, [base]);
  useEffect(() => () => bitmap?.close(), [bitmap]);

  const slot = state?.slots.find((s) => s.kind === kind);
  const languageRank=(language?:string)=>{const lang=(language??'').toLowerCase().replace('_','-');if(/^(es|spa)(-|$)/.test(lang))return 0;if(/^(en|eng)(-|$)/.test(lang))return 1;if(['','00','xx','und','null'].includes(lang))return 2;return 3;};
  const list = [...(gallery?listFor(gallery,kind).map(im=>({...im,provider:'tmdb'})):[]),...external.filter(im=>im.kind===kind)].filter(im=>(source==='all'||im.provider===source)&&languageRank(im.language)<3).sort((a,b)=>languageRank(a.language)-languageRank(b.language));
  const previewUrl=(img:GalleryImage,large=false)=>img.provider&&img.provider!=='tmdb'?`${base}/images/preview?${new URLSearchParams({provider:img.provider,kind,path:img.file_path,...(large?{large:'1'}:{})})}`:thumbUrl(img.file_path,large?'w780':kind==='poster'?'w185':'w300');
  const horizontal = kind !== 'poster';

  async function run(action: () => Promise<ImagesState>, ok: string) {
    setBusy(true);
    setError(null);
    setDone(null);
    try {
      setState(await action());
      setDone(ok);
      setZoomed(null);
      setBitmap(null);
      void notifyItemChanged(itemId);
    } catch (e) {
      setError(message(e, 'No se ha podido guardar. La imagen no ha cambiado.'));
    } finally {
      setBusy(false);
    }
  }

  async function pick(file: File | undefined) {
    if (!file || !state) return;
    setError(null);
    setDone(null);
    setBitmap(null);
    if (!['image/jpeg', 'image/png', 'image/webp'].includes(file.type)) return setError('Solo se admiten imágenes JPEG, PNG o WebP.');
    if (file.size > state.limits.max_bytes) return setError('La imagen pesa demasiado: el máximo son 10 MB.');
    try {
      const b = await createImageBitmap(file); // ya tiene en cuenta la orientación de las fotos del móvil
      if (b.width > state.limits.max_side || b.height > state.limits.max_side || b.width * b.height > state.limits.max_pixels) {
        b.close();
        return setError('La imagen es demasiado grande: hasta 6000 píxeles por lado y 40 megapíxeles.');
      }
      setBitmap(b);
    } catch {
      setError('Este navegador no puede leer el archivo. Comprueba que es una imagen JPEG, PNG o WebP válida.');
    }
  }

  const upload = (blob: Blob | null) => {
    if (!blob) return setError('Este navegador no ha podido preparar la imagen. Prueba con otra o con un encuadre más pequeño.');
    return run(() => {
      const form = new FormData();
      form.set('kind', kind);
      form.set('file', blob, transparent(kind) ? 'imagen.png' : 'imagen.jpg');
      return api<ImagesState>('POST', `${base}/images/upload`, form);
    }, 'Imagen guardada. Ya se ve en todos los dispositivos.');
  };

  const select = (img: GalleryImage) =>
    run(() => api<ImagesState>('POST', `${base}/images/select`, { kind, file_path: img.file_path, provider:img.provider??'tmdb' }), 'Imagen elegida. Las actualizaciones automáticas no la sustituirán.');
  const restore = () => run(() => api<ImagesState>('DELETE', `${base}/images/${kind}`), 'Se ha vuelto a la imagen automática.');

  return (
    <Modal title={`Editar imágenes · ${title}`} onClose={onClose} footer={<Button onClick={onClose}>Cerrar</Button>}>
      <p className="mb-3 text-sm text-text-muted">Primero español, después inglés. Las imágenes sin idioma o sin texto aparecen al final; se ocultan los demás idiomas.</p>
      <div className="flex flex-col gap-4">
        {error && <Banner tone="error">{error}</Banner>}
        {done && !error && <Banner>{done}</Banner>}

        {!state && !error && <p className="text-sm text-text-muted">Cargando…</p>}
        {state && (
          <>
            <div className="flex flex-wrap gap-3">
              <label className="flex flex-col gap-1 text-sm">Fuente<select aria-label="Fuente" value={source} onChange={e=>{setSource(e.target.value);setZoomed(null);}} className={inputClass}>
                <option value="all">Todo</option>{Object.entries(PROVIDERS).map(([value,label])=><option key={value} value={value}>{label}</option>)}
              </select></label>
              <label className="flex flex-col gap-1 text-sm">Tipo de imagen<select aria-label="Tipo de imagen" value={kind} onChange={e=>{setKind(e.target.value as ImageKind);setZoomed(null);setDone(null);}} className={inputClass}>
                {state.slots.map(s=><option key={s.kind} value={s.kind}>{KIND_LABEL[s.kind]}</option>)}
              </select></label>
            </div>
            <p className="text-xs text-text-muted">{KIND_HINT[kind]}</p>

            {bitmap ? (
              <Cropper key={kind} bitmap={bitmap} kind={kind} busy={busy} onCancel={() => setBitmap(null)} onSave={(b) => void upload(b)} />
            ) : zoomed ? (
              <div className="flex flex-col items-center gap-3 rounded-md bg-surface p-3">
                <img
                  src={previewUrl(zoomed,true)}
                  alt={`Vista previa: ${KIND_LABEL[kind]}`}
                  className={`max-h-[50vh] rounded-sm object-contain ${horizontal ? 'w-full' : ''}`}
                />
                <p className="text-xs text-text-muted">
                  {PROVIDERS[zoomed.provider??'tmdb']} {zoomed.width>0&&zoomed.height>0?` · ${zoomed.width}×${zoomed.height}`:''}
                  {zoomed.language ? ` · ${zoomed.language}` : ''}
                </p>
                <div className="flex gap-2">
                  <Button variant="ghost" onClick={() => setZoomed(null)}>
                    Volver a la galería
                  </Button>
                  <Button onClick={() => void select(zoomed)} loading={busy} disabled={busy}>
                    Usar como {KIND_LABEL[kind].toLowerCase()}
                  </Button>
                </div>
              </div>
            ) : (
              <>
                <div className="flex flex-wrap items-center gap-3 rounded-md bg-surface p-3">
                  {slot?.source ? (
                    <img
                      src={imageUrl(itemId, kind, state.image_rev)}
                      alt={`${KIND_LABEL[kind]} actual`}
                      className={`rounded-sm bg-surface-raised object-contain ${horizontal ? 'h-20 max-w-40' : 'aspect-poster w-16 object-cover'}`}
                    />
                  ) : (
                    <div className={`flex items-center justify-center rounded-sm bg-surface-raised text-text-subtle ${horizontal ? 'h-20 w-36' : 'aspect-poster w-16'}`}>
                      <ImageIcon aria-hidden className="h-5 w-5" />
                    </div>
                  )}
                  <div className="min-w-32 flex-1 text-sm">
                    <p className="font-semibold">{KIND_LABEL[kind]} actual</p>
                    <p className="text-xs text-text-muted">{slot?.source ? SOURCE_LABEL[slot.source] ?? slot.source : 'Sin imagen'}</p>
                  </div>
                  {slot?.source === 'user' && (
                    <Button variant="ghost" onClick={() => void restore()} disabled={busy}>
                      Volver a la automática
                    </Button>
                  )}
                  <Button variant="secondary" onClick={() => fileInput.current?.click()} disabled={busy}>
                    <Upload aria-hidden className="h-4 w-4" /> Subir imagen propia
                  </Button>
                  <input
                    ref={fileInput}
                    type="file"
                    aria-label="Elegir imagen de este dispositivo"
                    accept="image/jpeg,image/png,image/webp"
                    className="hidden"
                    onChange={(e) => {
                      void pick(e.target.files?.[0]);
                      e.target.value = ''; // permite volver a elegir el mismo archivo
                    }}
                  />
                </div>
                <section aria-label="Imágenes disponibles" className="flex flex-col gap-2">
                  <h3 className="text-sm font-semibold">Imágenes disponibles</h3>
                  {galleryError && <p className="text-sm text-text-muted">{galleryError} Puedes subir una imagen propia.</p>}
                  {gallery && !identified && <p className="text-sm text-text-muted">Esta ficha aún no está identificada. Identifícala para ver sus imágenes, o sube una propia.</p>}
                  {gallery && identified && list.length === 0 && <p className="text-sm text-text-muted">No hay imágenes de este tipo en las fuentes seleccionadas. Puedes configurar otras en Metadatos o subir una propia.</p>}
                  <div className={`grid gap-2 ${horizontal ? 'grid-cols-2 tablet:grid-cols-3' : 'grid-cols-3 tablet:grid-cols-5'}`} onKeyDown={arrowNav}>
                    {list.map((img) => (
                      <button
                        key={`${img.provider}:${img.file_path}`}
                        type="button"
                        onClick={() => setZoomed(img)}
                        aria-label={`Ver ${KIND_LABEL[kind].toLowerCase()} de ${img.width}×${img.height}${img.language ? `, ${img.language}` : ''}`}
                        className="overflow-hidden rounded-sm bg-surface-raised focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus"
                      >
                        <img
                          src={previewUrl(img)}
                          alt=""
                          loading="lazy"
                          className={`w-full ${kind === 'poster' ? 'aspect-poster object-cover' : kind === 'logo' ? 'aspect-backdrop object-contain p-2' : 'aspect-backdrop object-cover'}`}
                        />
                        <span className="block truncate px-2 py-1 text-xs text-text-muted">{PROVIDERS[img.provider??'tmdb']}{img.language?` · ${img.language}`:''}</span>
                      </button>
                    ))}
                  </div>
                  <p className="text-xs text-text-subtle">Las imágenes pertenecen a sus titulares y se obtienen de la fuente indicada. Lo que elijas o subas no se guarda junto a tus películas ni cambia ningún archivo.</p>
                </section>
              </>
            )}
          </>
        )}
      </div>
    </Modal>
  );
}
