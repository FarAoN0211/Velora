import { useCatalogPosition } from '../lib/catalogPosition';
import { Link } from 'react-router-dom';
import { useCatalogRevision } from '../lib/liveCatalog';
// S04 · Una biblioteca (cualquier nombre que le dé el administrador): rejilla con búsqueda sin tildes, varios órdenes
// (como Plex y Emby) y filtros por género y por vistas. Orden y filtros viven en la dirección: se conservan al volver de
// una ficha y se pueden compartir.
import { useEffect, useRef, useState } from 'react';
import { useParams, useSearchParams } from 'react-router-dom';
import { isTv } from '../lib/tv';
import { ArrowDownUp, Search, X } from 'lucide-react';
import { api, ApiError } from '../lib/api';
import { rememberLibraryView, useMenuLibraries, type Genre, type ItemCard } from '../lib/catalog';
import { MediaCard } from '../components/media';
import { Banner, Button, Loading } from '../components/ui';
import { Choice } from '../components/choice';

const PAGE = 120;

/** Criterios de orden; `natural` es el sentido en que se ven sin invertir (lo que esperaría el usuario). */
const SORTS = [
  { id: 'title', label: 'Título', natural: 'A → Z', reverse: 'Z → A' },
  { id: 'added', label: 'Añadidas recientemente', natural: 'Más recientes', reverse: 'Más antiguas' },
  { id: 'year', label: 'Año de estreno', natural: 'Más recientes', reverse: 'Más antiguas' },
  { id: 'rating', label: 'Puntuación', natural: 'Mejor puntuadas', reverse: 'Peor puntuadas' },
  { id: 'runtime', label: 'Duración', natural: 'Más largas', reverse: 'Más cortas' },
  { id: 'last_played', label: 'Vistas recientemente', natural: 'Más recientes', reverse: 'Más antiguas' },
  { id: 'resolution', label: 'Calidad de imagen', natural: 'Mayor calidad', reverse: 'Menor calidad' },
] as const;

const PLAYED = [
  { id: '', label: 'Todas' },
  { id: 'unplayed', label: 'No vistas' },
  { id: 'played', label: 'Vistas' },
] as const;

const selectClass = 'h-10 rounded-md border border-border-input bg-surface px-2 text-sm text-text';

export function Movies() {
  const revision = useCatalogRevision();
  const { id = '' } = useParams();
  const libraries = useMenuLibraries();
  const library = libraries.find((l) => l.id === id);
  const [params, setParams] = useSearchParams();
  // El orden: el de la dirección si viene en ella (enlace, atrás) y, si no, el que este usuario eligió la última vez en
  // esta biblioteca (se guarda en el servidor). Hasta saberlo no se pide el listado, para no cargarlo dos veces.
  const explicit = params.has('orden');
  const sort = SORTS.find((s) => s.id === (explicit ? params.get('orden') : library?.view_sort)) ?? SORTS[0];
  const reverse = explicit ? params.get('sentido') === 'inverso' : !!library?.view_reverse;
  const ready = explicit || libraries.length > 0; // con la lista ya cargada (aunque esta no estuviera en ella)
  const genre = params.get('genero') ?? '';
  const played = PLAYED.find((p) => p.id === params.get('estado'))?.id ?? '';
  const [q, setQ] = useState(params.get('q') ?? '');
  const [items, setItems] = useState<ItemCard[] | null>(null);
  const itemsRef = useRef(items); itemsRef.current = items;
  const loadedQuery = useRef('');
  const [genres, setGenres] = useState<Genre[]>([]);
  const [more, setMore] = useState(false);
  const [error, setError] = useState<string | null>(null);

  function setOrder(sortId: string, rev: boolean) {
    const next = new URLSearchParams(params);
    next.set('orden', sortId);
    if (rev) next.set('sentido', 'inverso');
    else next.delete('sentido');
    setParams(next, { replace: true });
    rememberLibraryView(id, sortId, rev);
  }

  function setParam(key: string, value: string) {
    const next = new URLSearchParams(params);
    if (value) next.set(key, value);
    else next.delete(key);
    setParams(next, { replace: true });
  }

  const query = (offset: number) => {
    const p = new URLSearchParams({ library_id: id, limit: String(PAGE), offset: String(offset), sort: sort.id });
    if (reverse) p.set('order', 'reverse');
    if (genre) p.set('genre', genre);
    if (played) p.set('played', played);
    if (q.trim()) p.set('q', q.trim());
    return `/api/v1/items?${p}`;
  };

  const restoreCount=useCatalogPosition(items!==null&&loadedQuery.current===query(0),items?.length??0);

  useEffect(() => {
    api<{ genres: Genre[] }>('GET', `/api/v1/genres?library_id=${id}`)
      .then((r) => setGenres(r.genres))
      .catch(() => setGenres([]));
  }, [id, revision]);

  useEffect(() => {
    if (!ready) return;
    const ctrl = { cancelled: false };
    const t = setTimeout(async () => {
      try {
        const key = query(0);
        const pages = loadedQuery.current === key ? Math.max(1, Math.ceil((itemsRef.current?.length ?? 0) / PAGE)) : Math.max(1,Math.ceil(restoreCount/PAGE));
        const results = await Promise.all(Array.from({length:pages},(_,page)=>api<{items:ItemCard[]}>('GET',query(page*PAGE))));
        const r = {items:results.flatMap(r=>r.items)};
        if (!ctrl.cancelled) {
          loadedQuery.current = key;
          setItems(r.items);
          setMore(results[results.length-1].items.length === PAGE);
          setError(null);
        }
      } catch (e) {
        if (!ctrl.cancelled) setError(e instanceof ApiError ? e.message : 'No se han podido cargar las películas.');
      }
    }, 250); // búsqueda en vivo con 250 ms de espera (S09)
    return () => {
      ctrl.cancelled = true;
      clearTimeout(t);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id, q, sort.id, reverse, genre, played, ready, revision]);

  // La búsqueda también queda en la dirección (sin crear una entrada de historial por tecla).
  useEffect(() => {
    if ((params.get('q') ?? '') !== q.trim()) setParam('q', q.trim());
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [q]);

  // Otra biblioteca: la búsqueda escrita no se arrastra (el resto de filtros van en la dirección de cada una).
  useEffect(() => setQ(params.get('q') ?? ''), [id]); // eslint-disable-line react-hooks/exhaustive-deps

  async function loadMore() {
    if (!items) return;
    const r = await api<{ items: ItemCard[] }>('GET', query(items.length));
    setItems([...items, ...r.items]);
    setMore(r.items.length === PAGE);
  }

  const filtered = !!genre || !!played || !!q.trim();
  function clearFilters() {
    setQ('');
    const next = new URLSearchParams(params);
    ['genero', 'estado', 'q'].forEach((k) => next.delete(k));
    setParams(next, { replace: true });
  }

  return (
    <div className="flex min-h-full flex-col">
      <main className="mx-auto flex w-full max-w-content flex-1 flex-col gap-4 p-4 desktop:p-6">
        <div className="flex flex-wrap items-center justify-between gap-4">
          <div className="flex items-center gap-4"><h1 className="text-xl">{library?.name ?? 'Biblioteca'}</h1>{library?.kind==='movies'&&<Link className="text-sm text-accent" to={`/colecciones?library_id=${id}`}>Colecciones</Link>}</div>
          {/* En TV se busca solo desde el menú lateral. */}
          {!isTv && (
          <label className="relative">
            <span className="sr-only">Buscar en la biblioteca</span>
            <Search aria-hidden className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-text-subtle" />
            <input
              type="search"
              value={q}
              onChange={(e) => setQ(e.target.value)}
              placeholder="Buscar…"
              className="h-10 w-56 rounded-md border border-border-input bg-surface pl-9 pr-3 text-sm text-text placeholder:text-text-subtle focus:outline-none focus:ring-2 focus:ring-accent"
            />
          </label>
          )}
        </div>

        <div role="group" aria-label="Orden y filtros" className="flex flex-wrap items-center gap-3">
          <label className="flex items-center gap-2 text-sm text-text-muted">
            Ordenar por
            <Choice aria-label="Ordenar por" value={sort.id} onChange={(e) => setOrder(e.target.value, reverse)} className={selectClass}>
              {SORTS.filter((s) => library?.kind !== 'tv' || (s.id !== 'runtime' && s.id !== 'resolution')).map((s) => (
                <option key={s.id} value={s.id}>
                  {s.label}
                </option>
              ))}
            </Choice>
          </label>
          <button
            type="button"
            onClick={() => setOrder(sort.id, !reverse)}
            aria-label={`Invertir orden (ahora: ${reverse ? sort.reverse : sort.natural})`}
            className="flex h-10 items-center gap-2 rounded-md border border-border-input bg-surface px-3 text-sm text-text hover:bg-surface-raised focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus"
          >
            <ArrowDownUp aria-hidden className="h-4 w-4" />
            {reverse ? sort.reverse : sort.natural}
          </button>
          <label className="flex items-center gap-2 text-sm text-text-muted">
            Género
            <Choice aria-label="Género" value={genre} onChange={(e) => setParam('genero', e.target.value)} className={selectClass} disabled={genres.length === 0 && !genre}>
              <option value="">{genres.length === 0 && !genre ? 'Sin géneros (necesita TMDB)' : 'Todos'}</option>
              {genre && !genres.some((g) => g.name === genre) && <option value={genre}>{genre}</option>}
              {genres.map((g) => (
                <option key={g.name} value={g.name}>
                  {g.name} ({g.count})
                </option>
              ))}
            </Choice>
          </label>
          <label className="flex items-center gap-2 text-sm text-text-muted">
            Mostrar
            <Choice aria-label="Mostrar" value={played} onChange={(e) => setParam('estado', e.target.value)} className={selectClass}>
              {PLAYED.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.label}
                </option>
              ))}
            </Choice>
          </label>
          {filtered && (
            <button type="button" onClick={clearFilters} className="flex h-10 items-center gap-1 rounded-md px-2 text-sm text-text-muted hover:text-text">
              <X aria-hidden className="h-4 w-4" /> Quitar filtros
            </button>
          )}
        </div>

        {error && <Banner tone="error">{error}</Banner>}
        {!items && !error && <Loading />}
        {items && items.length === 0 && (
          <p className="rounded-lg bg-surface p-8 text-center text-text-muted">
            {q.trim()
              ? `No hay resultados para «${q.trim()}».`
              : filtered
                ? 'Ningún título cumple estos filtros.'
                : library?.kind === 'tv'
                  ? 'Todavía no hay series en esta biblioteca. Cada serie va en su carpeta, con los episodios sueltos o en «Season 1», «Season 2»…'
                  : 'Todavía no hay títulos en esta biblioteca.'}
          </p>
        )}
        {items && items.length > 0 && (
          <div className="grid grid-cols-[repeat(auto-fill,minmax(9rem,1fr))] gap-x-4 gap-y-6 desktop:grid-cols-[repeat(auto-fill,minmax(10.5rem,1fr))] tv:grid-cols-[repeat(auto-fill,minmax(6.5rem,1fr))] tv:gap-x-3 tv:gap-y-3">
            {items.map((i) => (
              <MediaCard key={i.id} item={i} fluid emblemSpace={items.some(v=>v.library_4k)} />
            ))}
          </div>
        )}
        {more && (
          <div className="flex justify-center">
            <Button variant="secondary" onClick={() => void loadMore()}>
              Cargar más
            </Button>
          </div>
        )}
      </main>
    </div>
  );
}
