import { useCatalogRefresh } from '../lib/liveCatalog';
// S03 · Inicio (E1.2): cabecera destacada, «Continuar viendo» con miniaturas horizontales y «Novedades» de cada biblioteca,
// como en la imagen de referencia. En la app de TV, la cabecera se compacta y deja a la vista la primera fila.
import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, ApiError } from '../lib/api';
import type { HomeData } from '../lib/catalog';
import { useSession } from '../lib/session';
import { Hero, MediaCard, MediaRow, WideCard } from '../components/media';
import { Banner, Button } from '../components/ui';

function HomeSkeleton() {
  return (
    <div aria-hidden className="flex animate-pulse flex-col gap-8 motion-reduce:animate-none tv:gap-3">
      <div className="mx-4 h-[62vh] max-h-[640px] min-h-[420px] rounded-xl bg-surface desktop:mx-8 tv:mx-3 tv:h-[34vh] tv:min-h-0" />
      {[0, 1].map((r) => (
        <div key={r} className="flex flex-col gap-3 px-4 desktop:px-8 tv:gap-2 tv:px-3">
          <div className="h-5 w-48 rounded bg-surface" />
          <div className="flex gap-4 overflow-hidden">
            {[0, 1, 2, 3, 4, 5].map((c) => (
              <div key={c} className="aspect-poster w-40 shrink-0 rounded-md bg-surface tv:w-24" />
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}

export function Home() {
  const { user } = useSession();
  const [data, setData] = useState<HomeData | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setError(null);
    try {
      setData(await api<HomeData>('GET', '/api/v1/home'));
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'No se ha podido cargar el inicio.');
    }
  }, []);
  useCatalogRefresh(() => load());
  useEffect(() => {
    void load();
  }, [load]);

  // Al volver del reproductor propio de la app, «Continuar viendo» ya no es el que era.
  useEffect(() => {
    const again = () => void load();
    window.addEventListener('velora:volver', again);
    return () => window.removeEventListener('velora:volver', again);
  }, [load]);

  const empty = data && data.featured.length === 0 && data.continue.length === 0 && data.rows.length === 0;
  return (
    <main className="flex flex-col gap-8 pb-10 pt-2 tv:gap-3 tv:pb-4">
      {error && (
        <div className="px-4 desktop:px-8">
          <Banner tone="error">
            {error}{' '}
            <button className="underline" onClick={() => void load()}>
              Reintentar
            </button>
          </Banner>
        </div>
      )}
      {!data && !error && <HomeSkeleton />}
      {data && <Hero items={data.featured} />}
      {data && (
        <MediaRow title="Continuar viendo" count={data.continue.length}>
          {data.continue.map((i) => (
            <WideCard key={i.id} item={i} onRemoved={() => void load()} />
          ))}
        </MediaRow>
      )}
      {data?.next_up && (
        <MediaRow title="Siguiente episodio" count={data.next_up.length}>
          {data.next_up.map((i) => (
            <WideCard key={i.id} item={i} nextUp />
          ))}
        </MediaRow>
      )}
      {data?.rows.map((r) => (
        <MediaRow key={r.library.id} title={`Novedades en ${r.library.name}`} to={`/biblioteca/${r.library.id}`} count={r.items.length}>
          {r.items.map((i) => (
            <MediaCard key={i.id} item={i} emblemSpace={r.items.some(v=>v.library_4k)} />
          ))}
        </MediaRow>
      ))}
      {empty && (
        <section className="mx-4 flex flex-col items-center gap-3 rounded-xl bg-surface p-12 text-center desktop:mx-8">
          <h2 className="text-lg">Aún no hay nada que ver</h2>
          <p className="max-w-md text-sm text-text-muted">
            {user?.is_admin
              ? 'Añade una biblioteca con tus carpetas de películas o de series y VELORA las reconocerá.'
              : 'Pide al administrador que te dé acceso a alguna biblioteca.'}
          </p>
          {user?.is_admin && (
            <Link to="/admin/bibliotecas">
              <Button>Añadir biblioteca</Button>
            </Link>
          )}
        </section>
      )}
    </main>
  );
}
