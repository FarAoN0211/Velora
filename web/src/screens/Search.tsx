import { useCatalogRevision } from '../lib/liveCatalog';
// S09 · Búsqueda (E1.2): resultados en vivo desde el buscador de la barra superior, en todas las bibliotecas con acceso.
// En TV no hay barra superior: el campo está en la propia pantalla, a la que se llega desde el menú lateral.
import { useEffect, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { api, ApiError } from '../lib/api';
import type { ItemCard } from '../lib/catalog';
import { MediaCard } from '../components/media';
import { SearchBox } from '../components/shell';
import { isTv } from '../lib/tv';
import { Banner, Loading } from '../components/ui';

export function Search() {
  const revision = useCatalogRevision();
  const [params] = useSearchParams();
  const q = (params.get('q') ?? '').trim();
  const [items, setItems] = useState<ItemCard[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!q) {
      setItems(null);
      return;
    }
    let cancelled = false;
    api<{ items: ItemCard[] }>('GET', `/api/v1/items?limit=60&q=${encodeURIComponent(q)}`)
      .then((r) => !cancelled && (setItems(r.items), setError(null)))
      .catch((e) => !cancelled && setError(e instanceof ApiError ? e.message : 'No se ha podido buscar.'));
    return () => {
      cancelled = true;
    };
  }, [q, revision]);

  return (
    <main className="flex flex-col gap-6 px-4 pb-10 pt-2 desktop:px-8 tv:gap-3 tv:px-4 tv:pt-3">
      {isTv && <SearchBox autoFocus={!q} />}
      <h1 className="text-xl">{q ? `Resultados para «${q}»` : 'Buscar'}</h1>
      {!q && <p className="text-text-muted">Escribe en el buscador de arriba el título de una película o una serie.{isTv && ' Pulsa OK para abrir el teclado.'}</p>}
      {error && <Banner tone="error">{error}</Banner>}
      {q && !items && !error && <Loading />}
      {items && items.length === 0 && <p className="rounded-lg bg-surface p-8 text-center text-text-muted">No hay resultados para «{q}».</p>}
      {items && items.length > 0 && (
        <div className="grid grid-cols-[repeat(auto-fill,minmax(9rem,1fr))] gap-x-4 gap-y-6 desktop:grid-cols-[repeat(auto-fill,minmax(10.5rem,1fr))] tv:grid-cols-[repeat(auto-fill,minmax(6.5rem,1fr))] tv:gap-x-3 tv:gap-y-3">
          {items.map((i) => (
            <MediaCard key={i.id} item={i} fluid emblemSpace={items.some(v=>v.library_4k)} />
          ))}
        </div>
      )}
    </main>
  );
}
