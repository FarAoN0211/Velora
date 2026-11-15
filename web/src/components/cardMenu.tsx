// E1.3 · Menú «⋮» de cada carátula (replica/RECON_MENU_CARATULA.md). Solo opciones que ya funcionan; la sección de
// administración llegará con I1/I2. Se abre con el botón ⋮ (siempre presente), clic derecho, pulsación larga o
// Menú/Mayús+F10 con la tarjeta enfocada; nunca navega por sí solo. Escritorio: lista junto al botón, recolocada para no
// salirse de la pantalla. Táctil o pantalla estrecha: hoja inferior desplazable. Teclado y mando: flechas, Inicio/Fin,
// Intro; Escape cierra y devuelve el foco al ⋮.
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type KeyboardEvent,
  type MouseEvent,
  type PointerEvent,
  type ReactNode,
} from 'react';
import { createPortal } from 'react-dom';
import { useNavigate } from 'react-router-dom';
import { Check, Clapperboard, CloudDownload, EyeOff, FileText, Image as ImageIcon, Info, MoreVertical, Pencil, Play, RefreshCcw, RotateCcw, SearchCheck, Undo2 } from 'lucide-react';
import { api, ApiError } from '../lib/api';
import { detailHref, playHref, type ItemCard } from '../lib/catalog';
import { useSession } from '../lib/session';
import { isTv } from '../lib/tv';
import { EditMetadataDialog, FilesDialog, type Editable } from './itemAdmin';
import { IdentifyDialog, ImagesDialog } from './itemIdentify';

export interface CardMenuProps {
  item: ItemCard;
  optionsOverride?: Option[];
  /** La tarjeta está en «Continuar viendo»: ofrece quitarla de esa fila. */
  inContinue?: boolean;
  /** Tras marcar vista/no vista: el estado nuevo, para actualizar la tarjeta sin recargar. */
  onPlayedChange?: (played: boolean) => void;
  /** Tras quitarla de «Continuar viendo». */
  onRemoved?: () => void;
  /** Tras editar la información (administración): los datos guardados. */
  onEdited?: (m: Editable) => void;
  /** El menú está en la propia ficha: sin «Ir a la ficha». */
  inDetail?: boolean;
}

export interface Option {
  key: string;
  label: string;
  icon: ReactNode;
  run: () => Promise<void> | void;
  admin?: boolean; // va en la sección «Administración»
}

const LONG_PRESS_MS = 500;

function useCoarse() {
  const [coarse, setCoarse] = useState(() => typeof window !== 'undefined' && window.matchMedia?.('(hover: none), (max-width: 767px)').matches);
  useEffect(() => {
    const m = window.matchMedia?.('(hover: none), (max-width: 767px)');
    if (!m) return;
    const on = () => setCoarse(m.matches);
    m.addEventListener('change', on);
    return () => m.removeEventListener('change', on);
  }, []);
  return coarse;
}

/** Estado y gestos del menú de una tarjeta: devuelve el botón ⋮, el menú y los manejadores para la tarjeta. */
export function useCardMenu({ item, inContinue, onPlayedChange, onRemoved, onEdited, inDetail, optionsOverride }: CardMenuProps) {
  const navigate = useNavigate();
  const { user } = useSession();
  const [dialog, setDialog] = useState<null | 'edit' | 'files' | 'identify' | 'images'>(null);
  const [info, setInfo] = useState<string | null>(null);
  const [open, setOpen] = useState<null | { x: number; y: number } | 'button'>(null);
  const [error, setError] = useState<string | null>(null);
  const button = useRef<HTMLButtonElement>(null);
  const longPress = useRef<number>();
  const suppressClick = useRef(false);
  const resume = item.position_ms > 0 && !item.played;

  const fail = (e: unknown) => {
    const msg =
      e instanceof ApiError && e.status === 404 ? 'Ya no tienes acceso a este título.' : e instanceof ApiError ? e.message : 'No se ha podido completar.';
    setError(msg);
    window.setTimeout(() => setError(null), 4000);
  };

  async function setPlayed(played: boolean) {
    try {
      await api(played ? 'POST' : 'DELETE', `/api/v1/me/items/${item.id}/played`);
      onPlayedChange?.(played);
    } catch (e) {
      fail(e);
    }
  }

  const options: Option[] = [
    {
      key: 'play',
      label: resume ? 'Continuar' : 'Reproducir',
      icon: <Play className="h-4 w-4" fill="currentColor" />,
      run: () => playHref(item).then((to) => navigate(to)).catch(fail),
    },
  ];
  if (resume) options.push({ key: 'restart', label: 'Desde el principio', icon: <RotateCcw className="h-4 w-4" />, run: () => navigate(`/ver/${item.id}?inicio=1`) });
  if (item.trailer_id)
    options.push({
      key: 'trailer',
      label: 'Ver tráiler',
      icon: <Clapperboard className="h-4 w-4" />,
      run: () => navigate(`/ver/${item.id}?${new URLSearchParams({ file: item.trailer_id! })}`),
    });
  options.push(
    item.played
      ? { key: 'unplayed', label: 'Marcar como no vista', icon: <Undo2 className="h-4 w-4" />, run: () => setPlayed(false) }
      : { key: 'played', label: 'Marcar como vista', icon: <Check className="h-4 w-4" />, run: () => setPlayed(true) },
  );
  if (inContinue)
    options.push({
      key: 'hide',
      label: 'Quitar de «Continuar viendo»',
      icon: <EyeOff className="h-4 w-4" />,
      run: async () => {
        try {
          await api('POST', `/api/v1/me/items/${item.id}/continue-hidden`);
          onRemoved?.();
        } catch (e) {
          fail(e);
        }
      },
    });
  if (!inDetail) options.push({ key: 'detail', label: 'Ir a la ficha', icon: <Info className="h-4 w-4" />, run: () => navigate(detailHref(item)) });
  // Administración (I1): solo para administradores; la API vuelve a comprobarlo (404 para el resto).
  // Temporadas y episodios se identifican y editan por su serie (la API responde 422 a lo demás).
  if (user?.is_admin && (item.kind === 'movie' || item.kind === 'series' || item.kind === undefined)) {
    const refresh = async (mode: 'missing' | 'all') => {
      try {
        await api('POST', `/api/v1/admin/items/${item.id}/refresh`, { mode });
        setInfo(mode === 'missing' ? 'Completando lo que falta desde TMDB…' : 'Volviendo a descargar la información de TMDB…');
        window.setTimeout(() => setInfo(null), 4000);
      } catch (e) {
        fail(e);
      }
    };
    options.push(
      { key: 'identify', label: 'Identificar…', icon: <SearchCheck className="h-4 w-4" />, run: () => setDialog('identify'), admin: true },
      { key: 'images', label: 'Editar imágenes…', icon: <ImageIcon className="h-4 w-4" />, run: () => setDialog('images'), admin: true },
      { key: 'edit', label: 'Editar información…', icon: <Pencil className="h-4 w-4" />, run: () => setDialog('edit'), admin: true },
      { key: 'refresh-missing', label: 'Completar lo que falta (TMDB)', icon: <CloudDownload className="h-4 w-4" />, run: () => refresh('missing'), admin: true },
      {
        key: 'refresh-all',
        label: 'Volver a descargar información (TMDB)',
        icon: <RefreshCcw className="h-4 w-4" />,
        run: () => {
          if (
            window.confirm(
              'Volver a descargar desde TMDB:\n\n• Se sustituyen los campos SIN candado (título, sinopsis, lema, año, clasificación, géneros).\n• Se vuelven a bajar la carátula y el fondo de TMDB.\n\nNo cambia nada de lo bloqueado, ni tus imágenes elegidas o subidas, ni las de tu carpeta (poster.jpg, fanart.jpg), que siguen teniendo prioridad. Los archivos no se tocan. ¿Seguir?',
            )
          )
            return refresh('all');
        },
        admin: true,
      },
      { key: 'files', label: 'Ver archivos…', icon: <FileText className="h-4 w-4" />, run: () => setDialog('files'), admin: true },
    );
  }

  const close = useCallback((refocus = true) => {
    setOpen(null);
    if (!refocus) return;
    // En TV el ⋮ de la carátula no se selecciona: el foco vuelve a la propia carátula.
    const card = isTv && !inDetail ? button.current?.closest('[data-tv-tarjeta]')?.querySelector<HTMLElement>('a[href]:not([tabindex="-1"])') : null;
    (card ?? button.current)?.focus();
  }, [inDetail]);

  // Accesos adicionales sobre la tarjeta (el botón ⋮ sigue siendo el principal).
  const cardHandlers = {
    onContextMenu: (e: MouseEvent) => {
      e.preventDefault();
      setOpen({ x: e.clientX, y: e.clientY });
    },
    onKeyDown: (e: KeyboardEvent) => {
      if (e.key === 'ContextMenu' || (e.key === 'F10' && e.shiftKey)) {
        e.preventDefault();
        setOpen('button');
      }
    },
    onPointerDown: (e: PointerEvent) => {
      if (e.pointerType !== 'touch') return;
      const { clientX: x, clientY: y } = e;
      longPress.current = window.setTimeout(() => {
        suppressClick.current = true; // la pulsación larga no debe abrir además la película
        setOpen({ x, y });
      }, LONG_PRESS_MS);
    },
    onPointerUp: () => window.clearTimeout(longPress.current),
    onPointerMove: () => window.clearTimeout(longPress.current),
    onPointerCancel: () => window.clearTimeout(longPress.current),
    onClickCapture: (e: MouseEvent) => {
      if (suppressClick.current) {
        suppressClick.current = false;
        e.preventDefault();
        e.stopPropagation();
      }
    },
  };

  const trigger = (
    <button
      ref={button}
      type="button"
      aria-label={`Más opciones de ${item.title}`}
      aria-haspopup="menu"
      aria-expanded={!!open}
      onClick={(e) => {
        e.preventDefault();
        e.stopPropagation();
        setOpen(open ? null : 'button');
      }}
      // 40×40: área táctil cómoda aunque el icono sea pequeño.
      className="group/menu-trigger flex h-10 w-10 items-center justify-center rounded-full text-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus"
    >
      <span className={`flex items-center justify-center rounded-full bg-scrim group-hover/menu-trigger:bg-surface-overlay ${inDetail ? 'h-10 w-10' : 'h-7 w-7'}`}><MoreVertical aria-hidden className={inDetail ? 'h-5 w-5' : 'h-4 w-4'} /></span>
    </button>
  );

  const menu = open ? (
    <MenuPopup
      title={item.title}
      anchor={open === 'button' ? button.current?.getBoundingClientRect() ?? null : null}
      point={open !== 'button' ? open : null}
      options={optionsOverride ?? options}
      onClose={close}
    />
  ) : null;

  const errorNote =
    error || info ? (
      <p role={error ? 'alert' : 'status'} className={`absolute inset-x-1 bottom-1 z-10 rounded-sm bg-surface-overlay px-2 py-1 text-xs ${error ? 'text-danger' : 'text-text'}`}>
        {error ?? info}
      </p>
    ) : null;

  const dialogs =
    dialog === 'edit' ? (
      <EditMetadataDialog itemId={item.id} title={item.title} onClose={() => setDialog(null)} onSaved={(m) => onEdited?.(m)} />
    ) : dialog === 'files' ? (
      <FilesDialog itemId={item.id} title={item.title} onClose={() => setDialog(null)} />
    ) : dialog === 'identify' ? (
      <IdentifyDialog itemId={item.id} title={item.title} onClose={() => setDialog(null)} onEditManually={() => setDialog('edit')} />
    ) : dialog === 'images' ? (
      <ImagesDialog itemId={item.id} title={item.title} onClose={() => setDialog(null)} />
    ) : null;

  return {
    trigger,
    menu: (
      <>
        {menu}
        {dialogs}
      </>
    ),
    cardHandlers,
    errorNote,
    setPlayed,
    isOpen: !!open || !!dialog,
  };
}

function MenuPopup({
  title,
  anchor,
  point,
  options,
  onClose,
}: {
  title: string;
  anchor: DOMRect | null;
  point: { x: number; y: number } | null;
  options: Option[];
  onClose: (refocus?: boolean) => void;
}) {
  const coarse = useCoarse();
  const list = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState<{ left: number; top: number } | null>(null);
  const [busy, setBusy] = useState(false);

  // Se coloca junto al botón (o donde se hizo clic derecho) sin salirse de la ventana.
  useLayoutEffect(() => {
    if (coarse || !list.current) return;
    const { width, height } = list.current.getBoundingClientRect();
    const margin = 8;
    let left = anchor ? anchor.right - width : (point?.x ?? 0);
    let top = anchor ? anchor.bottom + 4 : (point?.y ?? 0);
    if (anchor && top + height > window.innerHeight - margin) top = anchor.top - height - 4; // mejor encima
    left = Math.min(Math.max(margin, left), window.innerWidth - width - margin);
    top = Math.min(Math.max(margin, top), window.innerHeight - height - margin);
    setPos({ left, top });
  }, [anchor, point, coarse]);

  useEffect(() => {
    list.current?.querySelector<HTMLButtonElement>('[role="menuitem"]')?.focus();
  }, []);

  function onKeyDown(e: KeyboardEvent) {
    const items = [...(list.current?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]') ?? [])];
    const i = items.indexOf(document.activeElement as HTMLButtonElement);
    const go = (n: number) => items[(n + items.length) % items.length]?.focus();
    if (e.key === 'ArrowDown') go(i + 1);
    else if (e.key === 'ArrowUp') go(i - 1);
    else if (e.key === 'Home') go(0);
    else if (e.key === 'End') go(items.length - 1);
    else if (e.key === 'Escape') onClose(true);
    else if (e.key === 'Tab') onClose(false);
    else return;
    e.preventDefault();
    e.stopPropagation();
  }

  async function choose(o: Option) {
    setBusy(true);
    try {
      await o.run();
    } finally {
      setBusy(false);
      onClose(false);
    }
  }

  return createPortal(
    <>
      {/* Capa que captura el clic fuera: cierra sin que llegue a la carátula de debajo. */}
      <div
        aria-hidden
        className={`fixed inset-0 z-40 ${coarse ? 'bg-scrim' : ''}`}
        onPointerDown={(e) => {
          e.preventDefault();
          e.stopPropagation();
        }}
        onClick={(e) => {
          e.preventDefault();
          e.stopPropagation();
          onClose(true);
        }}
        onContextMenu={(e) => {
          e.preventDefault();
          onClose(true);
        }}
      />
      <div
        ref={list}
        role="menu"
        aria-label={`Opciones de ${title}`}
        onKeyDown={onKeyDown}
        style={coarse ? undefined : { left: pos?.left ?? -9999, top: pos?.top ?? -9999 }}
        className={
          coarse
            ? 'fixed inset-x-0 bottom-0 z-50 max-h-[70vh] overflow-y-auto rounded-t-xl bg-surface-overlay p-2 pb-[max(0.5rem,env(safe-area-inset-bottom))] shadow-pop'
            : 'fixed z-50 min-w-56 rounded-md bg-surface-overlay p-1 shadow-pop tv:max-h-[calc(100vh-1rem)] tv:overflow-y-auto'
        }
      >
        {coarse && <p className="truncate px-3 pb-2 pt-1 text-sm font-semibold text-text-muted">{title}</p>}
        {options.map((o, i) => [
          o.admin && !options[i - 1]?.admin && (
            <p key={`h-${o.key}`} role="presentation" className="mt-1 border-t border-border px-3 pb-1 pt-2 text-xs font-semibold uppercase tracking-wider text-text-subtle">
              Administración
            </p>
          ),
          <button
            key={o.key}
            type="button"
            role="menuitem"
            disabled={busy}
            onClick={() => void choose(o)}
            className={`flex w-full items-center gap-3 rounded-sm px-3 text-left text-sm text-text hover:bg-surface-raised focus:bg-surface-raised focus:outline-none disabled:opacity-60 ${
              coarse ? 'h-12' : 'h-10 tv:h-8'
            }`}
          >
            <span aria-hidden className="text-text-muted">
              {o.icon}
            </span>
            {o.label}
          </button>,
        ])}
      </div>
    </>,
    document.body,
  );
}
