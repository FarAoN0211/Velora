// E1.2 · Tarjetas, filas y cabecera destacada (replica/design/components.md y la imagen de referencia de VELORA).
// Sin imágenes de TMDB nada queda vacío: se usa la carátula local o un fondo compuesto con el título.
import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { Check, CheckCircle2, ChevronLeft, ChevronRight, Info, Play } from 'lucide-react';
import { backdropUrl, detailHref, episodeCode, fmtDuration, fmtRemaining, imageUrl, onItemChanged, playHref, posterUrl, type Featured, type ItemCard } from '../lib/catalog';
import { Button } from './ui';
import { useCardMenu } from './cardMenu';

function useImage(src: string | null) {
  const [ok, setOk] = useState(true);
  useEffect(() => setOk(true), [src]);
  return { show: !!src && ok, onError: () => setOk(false) };
}

/** Fondo sin imagen: degradado con el título, para que nunca haya huecos. */
function TitleArt({ title, wide = false }: { title: string; wide?: boolean }) {
  return (
    <div className="flex h-full w-full items-end bg-gradient-to-br from-surface-overlay via-surface-raised to-bg p-3">
      <span className={`line-clamp-3 font-semibold text-text ${wide ? 'text-base' : 'text-sm'}`}>{title}</span>
    </div>
  );
}

function ProgressBar({ item }: { item: ItemCard }) {
  const pct = item.runtime_ms && item.position_ms ? Math.min(100, (item.position_ms / item.runtime_ms) * 100) : 0;
  if (!pct || item.played) return null;
  return (
    <div className="absolute inset-x-3 bottom-2 h-1 overflow-hidden rounded-full bg-progress-track">
      <div className="h-full rounded-full bg-progress-fill" style={{ width: `${pct}%` }} />
    </div>
  );
}

/** Estado local de la tarjeta: marcar vista/no vista desde la propia tarjeta la actualiza sin recargar la página. */
function useCardItem(initial: ItemCard) {
  const [item, setItem] = useState(initial);
  useEffect(() => setItem(initial), [initial]);
  // Identificación o imágenes cambiadas desde cualquier tarjeta de este título.
  useEffect(() => onItemChanged(initial.id, (c) => setItem((i) => ({ ...i, title: c.title, year: c.year, has_poster: c.has_poster, has_backdrop: c.has_backdrop, image_rev: c.image_rev }))), [initial.id]);
  const onPlayedChange = (played: boolean) => setItem((i) => ({ ...i, played, position_ms: played ? 0 : i.position_ms }));
  const onEdited = (m: { title: string; year: number | null }) => setItem((i) => ({ ...i, title: m.title, year: m.year }));
  return { item, onPlayedChange, onEdited };
}

/** Botones de la esquina inferior derecha: ✓ (vista/no vista) y ⋮. En escritorio aparecen al pasar el ratón o al enfocar
 * la tarjeta; en pantallas táctiles se ven siempre (replica/RECON_MENU_CARATULA.md). */
function CardActions({ menu, played, title }: { menu: ReturnType<typeof useCardMenu>; played: boolean; title: string }) {
  return (
    <div
      // En TV no se seleccionan ni se ven: lo mismo está en el menú de la carátula. El ⋮ sigue ahí para situar el menú.
      data-tv-omitir
      className={`absolute bottom-3 right-2 z-10 flex gap-1 transition-opacity duration-fast group-hover:opacity-100 group-focus-within:opacity-100 [@media(hover:none)]:opacity-100 tv:pointer-events-none tv:!opacity-0 ${
        menu.isOpen ? 'opacity-100' : 'opacity-0'
      }`}
    >
      <button
        type="button"
        aria-label={played ? `Marcar ${title} como no vista` : `Marcar ${title} como vista`}
        aria-pressed={played}
        onClick={(e) => {
          e.preventDefault();
          e.stopPropagation();
          void menu.setPlayed(!played);
        }}
        className={`flex h-10 w-10 items-center justify-center rounded-full bg-scrim hover:bg-surface-overlay focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus ${
          played ? 'text-status-verified' : 'text-text'
        }`}
      >
        <Check aria-hidden className="h-5 w-5" />
      </button>
      {menu.trigger}
    </div>
  );
}

/** Carátula vertical (Novedades, bibliotecas, búsqueda), con ✓ y menú ⋮. */
export function MediaCard({ item: initial, fluid = false, emblemSpace = false }: { item: ItemCard; fluid?: boolean; emblemSpace?: boolean }) {
  const { item, onPlayedChange, onEdited } = useCardItem(initial);
  const poster = posterUrl(item.id, item.image_rev);
  const img = useImage(item.has_poster ? poster : null);
  const menu = useCardMenu({ item, onPlayedChange, onEdited });
  return (
    <div {...menu.cardHandlers} data-tv-tarjeta className={`group relative flex flex-col gap-2 tv:gap-1 ${fluid ? 'w-full' : 'w-36 shrink-0 desktop:w-40 tv:w-24'}`}>
      <div className="relative">
        <Link
          to={detailHref(item)}
          aria-label={[item.title, item.year, item.played ? 'vista' : ''].filter(Boolean).join(', ')}
          className="block rounded-md focus-visible:outline-none"
        >
          {(item.library_4k || emblemSpace) && <div data-uhd-emblem={item.library_4k || undefined} className="flex h-10 items-center justify-center pb-1 tv:h-8">{item.library_4k && <img src="/brand/4k-uhd.png" alt="4K UHD" className="h-full max-w-full object-contain" />}</div>}
          <div className="relative aspect-poster overflow-hidden rounded-md bg-surface-raised shadow-card ring-accent transition duration-base group-hover:scale-[1.03] group-focus-within:ring-2 motion-reduce:transform-none tv:group-focus-within:scale-[1.04]">
            {img.show ? (
              <img src={poster} alt="" loading="lazy" decoding="async" onError={img.onError} className="h-full w-full object-cover" />
            ) : (
              <TitleArt title={item.title} />
            )}
            {item.played && <CheckCircle2 aria-hidden className="absolute right-2 top-2 h-5 w-5 rounded-full bg-scrim text-status-verified" />}
            <ProgressBar item={item} />
          </div>
        </Link>
        <CardActions menu={menu} played={item.played} title={item.title} />
        {menu.errorNote}
      </div>
      <Link to={detailHref(item)} tabIndex={-1} className="min-w-0 px-0.5">
        <p className="truncate text-sm text-text tv:text-xs">{item.title}</p>
        {item.year && <p className="text-xs text-text-muted tv:hidden">{item.year}</p>}
      </Link>
      {menu.menu}
    </div>
  );
}

/** Tarjeta horizontal de «Continuar viendo»: fondo (o carátula), progreso y tiempo restante. Un clic reanuda; el menú ⋮
 * permite además quitarla de la fila. */
export function WideCard({ item: initial, onRemoved, nextUp = false }: { item: ItemCard; onRemoved?: () => void; nextUp?: boolean }) {
  const { item, onPlayedChange, onEdited } = useCardItem(initial);
  // Un episodio: su miniatura propia y, si no tiene, el fondo o la carátula de la serie.
  const src = item.has_thumb
    ? imageUrl(item.id, 'thumb', item.image_rev)
    : item.has_backdrop
      ? backdropUrl(item.id, item.image_rev)
      : item.has_poster
        ? posterUrl(item.id, item.image_rev)
        : null;
  const wideArt = item.has_thumb || item.has_backdrop;
  const img = useImage(src);
  const remaining = fmtRemaining(item);
  const episode = item.kind === 'episode';
  const heading = episode && item.series_title ? item.series_title : item.title;
  const sub = episode ? [episodeCode(item), item.title].filter(Boolean).join(' · ') : [item.year, remaining].filter(Boolean).join(' · ');
  // Vista desde aquí: deja de estar «en curso», así que sale de la fila.
  const menu = useCardMenu({ item, inContinue: !nextUp, onPlayedChange: (p) => (p ? onRemoved?.() : onPlayedChange(p)), onRemoved, onEdited });
  return (
    <div {...menu.cardHandlers} data-tv-tarjeta className="group relative flex w-64 shrink-0 flex-col gap-2 desktop:w-72 tv:w-44 tv:gap-1">
      <div className="relative">
        <Link
          to={`/ver/${item.id}`}
          aria-label={`${nextUp ? 'Ver' : 'Continuar'} ${heading}${episode ? `, ${sub}` : ''}${remaining ? `, ${remaining.toLowerCase()}` : ''}`}
          className="block rounded-md focus-visible:outline-none"
        >
          <div className="relative aspect-backdrop overflow-hidden rounded-md bg-surface-raised shadow-card ring-accent transition duration-base group-hover:scale-[1.03] group-focus-within:ring-2 motion-reduce:transform-none tv:group-focus-within:scale-[1.04]">
            {img.show ? (
              <img
                src={src!}
                alt=""
                loading="lazy"
                decoding="async"
                onError={img.onError}
                className={`h-full w-full object-cover ${wideArt ? '' : 'object-[50%_20%]'}`}
              />
            ) : (
              <TitleArt title={heading} wide />
            )}
            <div className="absolute inset-0 flex items-center justify-center bg-scrim opacity-0 transition-opacity duration-fast group-hover:opacity-100 group-focus-within:opacity-100">
              <span className="flex h-12 w-12 items-center justify-center rounded-full bg-accent text-on-accent">
                <Play aria-hidden className="ml-0.5 h-5 w-5" fill="currentColor" />
              </span>
            </div>
            {remaining && (
              <span className="absolute left-3 top-3 rounded-sm bg-scrim px-2 py-0.5 text-xs text-text">
                {fmtDuration((item.runtime_ms ?? 0) - item.position_ms)}
              </span>
            )}
            <ProgressBar item={item} />
          </div>
        </Link>
        <CardActions menu={menu} played={item.played} title={item.title} />
        {menu.errorNote}
      </div>
      <Link to={`/ver/${item.id}`} tabIndex={-1} className="min-w-0 px-0.5">
        <p className="truncate text-sm font-semibold text-text tv:text-xs">{heading}</p>
        <p className={`truncate text-xs text-text-muted ${episode ? 'tv:text-[0.65rem]' : 'tv:hidden'}`}>{sub}</p>
      </Link>
      {menu.menu}
    </div>
  );
}

/** Fila con desplazamiento horizontal y flechas en escritorio. */
export function MediaRow({ title, children, to, count }: { title: string; children: ReactNode; to?: string; count: number }) {
  const scroller = useRef<HTMLDivElement>(null);
  if (count === 0) return null;
  const scroll = (dir: number) => scroller.current?.scrollBy({ left: dir * scroller.current.clientWidth * 0.85, behavior: 'smooth' });
  return (
    <section aria-label={title} className="group/row relative flex flex-col gap-3 tv:gap-1">
      <div data-tv-omitir className="flex items-center justify-between px-4 desktop:px-8 tv:px-3">
        <h2 className="text-lg font-semibold tv:text-base">{to ? <Link to={to} className="hover:underline">{title}</Link> : title}</h2>
        {to && (
          <Link to={to} aria-label={`Ver todo en ${title}`} className="rounded-full p-1 text-text-muted hover:text-text tv:hidden">
            <ChevronRight aria-hidden className="h-5 w-5" />
          </Link>
        )}
      </div>
      <div ref={scroller} data-catalog-row={title} className="flex gap-4 overflow-x-auto scroll-smooth px-4 pb-2 [scrollbar-width:none] desktop:px-8 tv:gap-2.5 tv:px-3 tv:pt-1.5 tv:scroll-auto">
        {children}
      </div>
      <button
        type="button"
        aria-label="Anteriores"
        data-tv-omitir
        onClick={() => scroll(-1)}
        className="absolute left-1 top-1/2 hidden h-10 w-10 items-center justify-center rounded-full bg-surface-overlay text-text opacity-0 shadow-pop transition-opacity group-hover/row:opacity-100 desktop:flex tv:!hidden"
      >
        <ChevronLeft aria-hidden className="h-5 w-5" />
      </button>
      <button
        type="button"
        aria-label="Siguientes"
        data-tv-omitir
        onClick={() => scroll(1)}
        className="absolute right-1 top-1/2 hidden h-10 w-10 items-center justify-center rounded-full bg-surface-overlay text-text opacity-0 shadow-pop transition-opacity group-hover/row:opacity-100 desktop:flex tv:!hidden"
      >
        <ChevronRight aria-hidden className="h-5 w-5" />
      </button>
    </section>
  );
}

const ROTATE_MS = 9000;

function prefersReducedMotion() {
  return typeof window !== 'undefined' && window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
}

/** Cabecera destacada rotativa del inicio. Se detiene al pasar el ratón, al enfocar y con «reducir movimiento». */
export function Hero({ items: initial }: { items: Featured[] }) {
  const navigate = useNavigate();
  const [index, setIndex] = useState(0);
  const [items, setItems] = useState(initial);
  useEffect(() => setItems(initial), [initial]);
  useEffect(() => {
    const stops = initial.map((it) =>
      onItemChanged(it.id, (c) => setItems((cur) => cur.map((x) => (x.id === c.id ? { ...x, title: c.title, year: c.year, overview: c.overview, has_poster: c.has_poster, has_backdrop: c.has_backdrop, has_banner: c.has_banner, image_rev: c.image_rev } : x)))),
    );
    return () => stops.forEach((stop) => stop());
  }, [initial]);
  const [paused, setPaused] = useState(prefersReducedMotion());
  const next = useCallback(() => setIndex((i) => (i + 1) % items.length), [items.length]);
  useEffect(() => {
    if (paused || items.length < 2) return;
    const t = setTimeout(next, ROTATE_MS);
    return () => clearTimeout(t);
  }, [index, paused, next, items.length]);
  if (items.length === 0) return null;
  const item = items[index];
  const hasBackdrop = item.has_banner || item.has_backdrop;
  const art = item.has_banner ? imageUrl(item.id, 'banner', item.image_rev) : item.has_backdrop ? backdropUrl(item.id, item.image_rev) : posterUrl(item.id, item.image_rev);
  const meta = [item.year, fmtDuration(item.runtime_ms)].filter(Boolean).join(' · ');
  const resume = item.position_ms > 0 && !item.played;
  return (
    <section
      aria-roledescription="carrusel"
      aria-label="Destacadas"
      onMouseEnter={() => setPaused(true)}
      onMouseLeave={() => setPaused(prefersReducedMotion())}
      onFocusCapture={() => setPaused(true)}
      className="relative mx-4 overflow-hidden rounded-xl bg-surface desktop:mx-8 tv:mx-3 tv:shrink-0"
    >
      <div className="relative h-[62vh] max-h-[640px] min-h-[420px] w-full tv:h-[34vh] tv:max-h-none tv:min-h-0">
        {/* Sin fondo: la carátula desenfocada llena la cabecera y la nítida va a la derecha. */}
        {!hasBackdrop && <img src={art} alt="" aria-hidden className="absolute inset-0 h-full w-full scale-110 object-cover opacity-40 blur-2xl" />}
        <img
          key={item.id}
          src={art}
          alt=""
          className={
            hasBackdrop
              ? 'absolute inset-0 h-full w-full object-cover object-[70%_25%] animate-[fadein_600ms_ease]'
              : 'absolute bottom-0 right-6 top-0 hidden h-full py-6 shadow-pop tablet:block'
          }
        />
        <div className="absolute inset-0 hidden tablet:block" style={{ background: 'linear-gradient(90deg, var(--color-bg) 0%, color-mix(in srgb, var(--color-bg) 70%, transparent) 38%, transparent 70%)' }} />
        <div className="absolute inset-0" style={{ background: 'linear-gradient(0deg, var(--color-bg) 0%, transparent 45%)' }} />
        <div className="absolute inset-y-0 left-0 flex w-full flex-col justify-end gap-3 p-6 pb-12 tablet:w-[min(42rem,70%)] desktop:p-10 desktop:pb-14 tv:gap-1 tv:p-3 tv:pb-3 tablet:tv:w-[70%]">
          <h1 className="line-clamp-2 text-display font-semibold leading-tight text-text drop-shadow tv:line-clamp-1">{item.title}</h1>
          <p className="flex flex-wrap items-center gap-2 text-sm text-text-muted">
            {meta}
            {item.official_rating && <span className="rounded-sm border border-border px-1.5 text-xs">+{item.official_rating}</span>}
            {item.quality && <span className="rounded-sm bg-surface-overlay px-1.5 text-xs font-semibold text-text">{item.quality}</span>}
          </p>
          {item.overview && <p className="line-clamp-3 max-w-2xl text-sm text-text tablet:text-base tv:line-clamp-2 tv:!text-sm">{item.overview}</p>}
          <div className="mt-2 flex flex-wrap gap-3 tv:mt-1 tv:gap-2">
            <Button size="lg" className="tv:h-9 tv:min-h-0 tv:px-3 tv:text-sm" onClick={() => void playHref(item).then((to) => navigate(to)).catch(() => navigate(detailHref(item)))}>
              <Play aria-hidden className="h-5 w-5" fill="currentColor" /> {resume ? 'Continuar' : 'Reproducir'}
            </Button>
            <Button size="lg" variant="secondary" className="tv:h-9 tv:min-h-0 tv:px-3 tv:text-sm" onClick={() => navigate(detailHref(item))}>
              <Info aria-hidden className="h-5 w-5" /> Más información
            </Button>
          </div>
        </div>
        {items.length > 1 && (
          <div data-tv-omitir className="absolute bottom-4 right-6 flex gap-2">
            {items.map((it, i) => (
              <button
                key={it.id}
                type="button"
                aria-label={`Ver ${it.title}`}
                aria-current={i === index}
                onClick={() => setIndex(i)}
                className={`h-2 rounded-full transition-all duration-base ${i === index ? 'w-6 bg-text' : 'w-2 bg-text-subtle hover:bg-text-muted'}`}
              />
            ))}
          </div>
        )}
      </div>
    </section>
  );
}
