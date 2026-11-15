// E1.3 I1 · Administración de una ficha (solo administradores): «Editar información» con candados y «Ver archivos».
// Editar un campo lo bloquea solo (como en Plex): ni TMDB ni el escaneo lo vuelven a cambiar. El candado se abre o
// cierra a mano. Nada de esto toca los archivos: solo la ficha de VELORA. Rutas siempre relativas a la biblioteca.
import { useEffect, useRef, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { Cloud, Lock, LockOpen, X } from 'lucide-react';
import { api, ApiError } from '../lib/api';
import { EXTRA_LABEL, fmtBytes, fmtDuration, type ExtraKind } from '../lib/catalog';
import { Banner, Button } from './ui';

interface Editable {
  title: string;
  sort_title: string;
  original_title: string;
  year: number | null;
  tagline: string;
  overview: string;
  official_rating: string;
  rating_age: number | null;
  genres: string[];
  locked_fields: string[];
  locked: boolean;
}

type Field = 'title' | 'sort_title' | 'original_title' | 'year' | 'tagline' | 'overview' | 'official_rating' | 'genres';

// Atrás cierra la ventana: mientras haya una abierta hay una entrada propia en el historial. Una página recargada no debe
// heredar esa marca, porque al cerrar la siguiente ventana se saldría de la página.
if (window.history.state?.veloraModal) window.history.replaceState({ ...window.history.state, veloraModal: false }, '');
let openModals = 0;

function markHistory() {
  if (!window.history.state?.veloraModal) window.history.pushState({ ...window.history.state, veloraModal: true }, '');
}

/** Ventana modal accesible: pantalla completa en el móvil, centrada en escritorio. Se cierra con Escape, Atrás o la X. */
export function Modal({ title, onClose, children, footer }: { title: string; onClose: () => void; children: ReactNode; footer?: ReactNode }) {
  const box = useRef<HTMLDivElement>(null);
  const latestClose = useRef(onClose);
  latestClose.current = onClose;
  useEffect(() => {
    const prev = document.activeElement as HTMLElement | null;
    box.current?.querySelector<HTMLElement>('input, textarea, button')?.focus();
    const esc = (e: KeyboardEvent) => e.key === 'Escape' && latestClose.current();
    let mounted = true;
    const back = () => {
      latestClose.current();
      // Si sigue abierta (hay cambios sin guardar y no ha querido salir), Atrás debe seguir sirviendo.
      window.setTimeout(() => mounted && markHistory(), 0);
    };
    openModals++;
    markHistory();
    document.addEventListener('keydown', esc);
    window.addEventListener('popstate', back);
    return () => {
      mounted = false;
      openModals--;
      document.removeEventListener('keydown', esc);
      window.removeEventListener('popstate', back);
      // En diferido: si se pasa de una ventana a otra, la entrada del historial sigue valiendo.
      window.setTimeout(() => {
        if (openModals === 0 && window.history.state?.veloraModal) window.history.back();
      }, 0);
      prev?.focus();
    };
  }, []);
  return createPortal(
    <div
      className="fixed inset-0 z-50 flex items-stretch justify-center bg-scrim tablet:items-center tablet:p-6"
      onMouseDown={(e) => e.target === e.currentTarget && onClose()}
      // React propaga los eventos a través del portal: dentro de la ventana no deben activar los gestos de la tarjeta.
      onPointerDown={(e) => e.stopPropagation()}
      onContextMenu={(e) => e.stopPropagation()}
    >
      <div
        ref={box}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className="flex w-full max-w-2xl flex-col bg-surface-overlay shadow-pop tablet:max-h-[90vh] tablet:rounded-xl"
      >
        <div className="flex items-center justify-between gap-3 border-b border-border px-5 py-3">
          <h2 className="truncate text-lg font-semibold">{title}</h2>
          <button type="button" aria-label="Cerrar" onClick={onClose} className="rounded-full p-1 text-text-muted hover:text-text">
            <X aria-hidden className="h-5 w-5" />
          </button>
        </div>
        <div className="flex-1 overflow-y-auto px-5 py-4">{children}</div>
        {footer && <div className="flex justify-end gap-2 border-t border-border px-5 py-3">{footer}</div>}
      </div>
    </div>,
    document.body,
  );
}

export const inputClass =
  'w-full rounded-md border border-border-input bg-surface px-3 py-2 text-sm text-text placeholder:text-text-subtle focus:outline-none focus:ring-2 focus:ring-focus';

export function EditMetadataDialog({ itemId, title, onClose, onSaved }: { itemId: string; title: string; onClose: () => void; onSaved: (m: Editable) => void }) {
  const [orig, setOrig] = useState<Editable | null>(null);
  const [m, setM] = useState<Editable | null>(null);
  const [genreText, setGenreText] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api<{ metadata: Editable }>('GET', `/api/v1/admin/items/${itemId}/metadata`)
      .then((r) => {
        setOrig(r.metadata);
        setM(r.metadata);
        setGenreText(r.metadata.genres.join(', '));
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : 'No se ha podido cargar la ficha.'));
  }, [itemId]);

  const dirty = !!m && !!orig && JSON.stringify({ ...m, genres: splitGenres(genreText) }) !== JSON.stringify(orig);
  const close = () => {
    if (dirty && !window.confirm('Hay cambios sin guardar. ¿Salir sin guardarlos?')) return;
    onClose();
  };

  function set<K extends Field>(field: K, value: Editable[K]) {
    setM((cur) => {
      if (!cur) return cur;
      const next = { ...cur, [field]: value } as Editable;
      // Editar bloquea el campo (como en Plex); el título para ordenar acompaña al título si nadie lo tocó.
      if (!next.locked_fields.includes(field)) next.locked_fields = [...next.locked_fields, field];
      if (field === 'title' && !cur.locked_fields.includes('sort_title')) next.sort_title = '';
      return next;
    });
  }
  function toggleLock(field: Field) {
    setM((cur) =>
      cur && {
        ...cur,
        locked_fields: cur.locked_fields.includes(field) ? cur.locked_fields.filter((f) => f !== field) : [...cur.locked_fields, field],
      },
    );
  }

  async function save() {
    if (!m) return;
    setBusy(true);
    setError(null);
    try {
      const r = await api<{ metadata: Editable }>('PATCH', `/api/v1/admin/items/${itemId}/metadata`, { ...m, genres: splitGenres(genreText) });
      onSaved(r.metadata);
      onClose();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'No se ha podido guardar.');
    } finally {
      setBusy(false);
    }
  }

  const lock = (field: Field, label: string) => {
    const locked = !!m?.locked_fields.includes(field);
    return (
      <button
        type="button"
        onClick={() => toggleLock(field)}
        aria-pressed={locked}
        aria-label={locked ? `${label}: bloqueado (TMDB no lo cambia). Pulsa para desbloquear` : `${label}: desbloqueado. Pulsa para bloquear`}
        title={locked ? 'Bloqueado: TMDB y el escaneo no lo cambian' : 'Desbloqueado: TMDB puede actualizarlo'}
        className={`rounded-full p-1.5 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus ${locked ? 'text-accent' : 'text-text-subtle hover:text-text'}`}
      >
        {locked ? <Lock aria-hidden className="h-4 w-4" /> : <LockOpen aria-hidden className="h-4 w-4" />}
      </button>
    );
  };
  const row = (field: Field, label: string, control: ReactNode) => (
    <div className="flex flex-col gap-1">
      <div className="flex items-center justify-between">
        <label htmlFor={`f-${field}`} className="text-sm font-semibold">
          {label}
        </label>
        {lock(field, label)}
      </div>
      {control}
    </div>
  );

  return (
    <Modal
      title={`Editar información · ${title}`}
      onClose={close}
      footer={
        <>
          <Button variant="ghost" onClick={close}>
            Cancelar
          </Button>
          <Button onClick={() => void save()} disabled={!m || !dirty || busy} loading={busy}>
            Guardar
          </Button>
        </>
      }
    >
      {error && <Banner tone="error">{error}</Banner>}
      {!m && !error && <p className="text-sm text-text-muted">Cargando…</p>}
      {m && (
        <form className="flex flex-col gap-4" onSubmit={(e) => (e.preventDefault(), void save())}>
          <p className="text-xs text-text-muted">
            Al cambiar un campo se cierra su candado: ni TMDB ni el escaneo lo volverán a cambiar. Los archivos no se tocan.
          </p>
          {row('title', 'Título', <input id="f-title" className={inputClass} value={m.title} maxLength={300} onChange={(e) => set('title', e.target.value)} />)}
          {row(
            'sort_title',
            'Título para ordenar',
            <input
              id="f-sort_title"
              className={inputClass}
              value={m.sort_title}
              maxLength={300}
              placeholder="Se calcula del título"
              onChange={(e) => set('sort_title', e.target.value)}
            />,
          )}
          {row(
            'original_title',
            'Título original',
            <input id="f-original_title" className={inputClass} value={m.original_title} maxLength={300} onChange={(e) => set('original_title', e.target.value)} />,
          )}
          <div className="grid gap-4 tablet:grid-cols-2">
            {row(
              'year',
              'Año',
              <input
                id="f-year"
                className={inputClass}
                inputMode="numeric"
                value={m.year ?? ''}
                onChange={(e) => set('year', e.target.value.trim() === '' ? null : Number(e.target.value.replace(/\D/g, '')) || null)}
              />,
            )}
            {row(
              'official_rating',
              'Clasificación por edades',
              <div className="flex gap-2">
                <input
                  id="f-official_rating"
                  className={inputClass}
                  value={m.official_rating}
                  maxLength={20}
                  placeholder="p. ej. 12"
                  onChange={(e) => set('official_rating', e.target.value)}
                />
                <input
                  aria-label="Edad recomendada (0 a 21)"
                  className={`${inputClass} w-20`}
                  inputMode="numeric"
                  value={m.rating_age ?? ''}
                  placeholder="edad"
                  onChange={(e) => {
                    const v = e.target.value.replace(/\D/g, '');
                    set('official_rating', m.official_rating);
                    setM((cur) => cur && { ...cur, rating_age: v === '' ? null : Number(v) });
                  }}
                />
              </div>,
            )}
          </div>
          {row('tagline', 'Lema', <input id="f-tagline" className={inputClass} value={m.tagline} maxLength={500} onChange={(e) => set('tagline', e.target.value)} />)}
          {row(
            'overview',
            'Sinopsis',
            <textarea id="f-overview" className={`${inputClass} min-h-32`} value={m.overview} maxLength={5000} onChange={(e) => set('overview', e.target.value)} />,
          )}
          {row(
            'genres',
            'Géneros (separados por comas)',
            <input
              id="f-genres"
              className={inputClass}
              value={genreText}
              onChange={(e) => {
                setGenreText(e.target.value);
                set('genres', splitGenres(e.target.value));
              }}
            />,
          )}
          <label className="flex items-center gap-3 rounded-md bg-surface p-3 text-sm">
            <input type="checkbox" checked={m.locked} onChange={(e) => setM({ ...m, locked: e.target.checked })} className="h-5 w-5 accent-[var(--color-accent)]" />
            <span>
              <span className="font-semibold">Bloquear toda la ficha</span>
              <span className="block text-xs text-text-muted">Nada se actualizará automáticamente, aunque haya campos desbloqueados.</span>
            </span>
          </label>
        </form>
      )}
    </Modal>
  );
}

function splitGenres(v: string): string[] {
  return v
    .split(',')
    .map((g) => g.trim())
    .filter(Boolean);
}

interface AdminFile {
  library: string;
  rel_path: string;
  kind: 'movie' | ExtraKind;
  size_bytes: number;
  container: string | null;
  duration_ms: number | null;
  missing: boolean;
  remote: boolean;
}

export function FilesDialog({ itemId, title, onClose }: { itemId: string; title: string; onClose: () => void }) {
  const [files, setFiles] = useState<AdminFile[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    api<{ files: AdminFile[] }>('GET', `/api/v1/admin/items/${itemId}/files`)
      .then((r) => setFiles(r.files))
      .catch((e) => setError(e instanceof ApiError ? e.message : 'No se han podido leer los archivos.'));
  }, [itemId]);
  return (
    <Modal title={`Archivos · ${title}`} onClose={onClose} footer={<Button onClick={onClose}>Cerrar</Button>}>
      {error && <Banner tone="error">{error}</Banner>}
      {!files && !error && <p className="text-sm text-text-muted">Cargando…</p>}
      {files && (
        <ul className="flex flex-col gap-3">
          {files.map((f) => (
            <li key={f.library + f.rel_path} className="rounded-md bg-surface p-3 text-sm">
              <p className="break-all font-semibold text-text">
                {f.remote && <Cloud aria-label="Montaje remoto" className="mr-1 inline h-4 w-4 text-accent" />}
                {f.library} › {f.rel_path}
              </p>
              <p className="text-xs text-text-muted">
                {[
                  f.kind === 'movie' ? 'Película' : EXTRA_LABEL[f.kind as ExtraKind],
                  fmtBytes(f.size_bytes),
                  f.container?.toUpperCase(),
                  fmtDuration(f.duration_ms),
                  f.missing ? 'No disponible ahora' : null,
                ]
                  .filter(Boolean)
                  .join(' · ')}
              </p>
            </li>
          ))}
          {files.length === 0 && <li className="text-sm text-text-muted">Esta ficha no tiene archivos.</li>}
        </ul>
      )}
    </Modal>
  );
}

export type { Editable };
