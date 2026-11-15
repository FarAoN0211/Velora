import { useCatalogRefresh } from '../lib/liveCatalog';
// Ficha de una persona del reparto: foto, nacimiento, lugar, biografía y lo que hay suyo en tus bibliotecas, en filas de
// «Películas» y «Series» como las del inicio. Fondo: su foto a la derecha, fundida con el fondo de la aplicación. Se abre
// desde el reparto de una ficha, en la web y en el cliente de TV (APK 0.6.1 en adelante, que la abre como página web).
import { useEffect, useState } from 'react';
import { useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { ArrowLeft } from 'lucide-react';
import { api, ApiError } from '../lib/api';
import type { ItemCard } from '../lib/catalog';
import { MediaCard, MediaRow } from '../components/media';
import { Banner, Button, Loading } from '../components/ui';

interface PersonInfo {
  id: number;
  name: string;
  biography: string;
  birthday: string;
  deathday: string;
  place_of_birth: string;
  has_photo: boolean;
}

const fecha = (d: string) => new Date(`${d}T00:00:00`).toLocaleDateString('es-ES', { day: 'numeric', month: 'long', year: 'numeric' });

function edad(desde: string, hasta?: string): number | null {
  const a = new Date(`${desde}T00:00:00`);
  const b = hasta ? new Date(`${hasta}T00:00:00`) : new Date();
  if (Number.isNaN(a.getTime()) || Number.isNaN(b.getTime())) return null;
  let n = b.getFullYear() - a.getFullYear();
  if (b.getMonth() < a.getMonth() || (b.getMonth() === a.getMonth() && b.getDate() < a.getDate())) n--;
  return n >= 0 ? n : null;
}

/** Foto de la persona: la grande de su ficha; si no hay, la del reparto de alguna ficha suya (la de la que se viene,
 * ?desde=, o la de sus títulos); sin ninguna, la inicial. */
function Portrait({ person, name, candidates }: { person: string; name: string; candidates: string[] }) {
  const sources = [`/api/v1/people/${encodeURIComponent(person)}/photo`, ...candidates.map((c) => `/api/v1/items/${encodeURIComponent(c)}/people/${encodeURIComponent(person)}/image`)];
  const [i, setI] = useState(0);
  useEffect(() => setI(0), [person, candidates.join(',')]);
  const box = 'aspect-poster w-36 shrink-0 overflow-hidden rounded-lg bg-surface-overlay shadow-pop tablet:w-52 desktop:w-60 tv:!w-40';
  if (i >= sources.length)
    return (
      <span aria-hidden className={`${box} flex items-center justify-center text-6xl font-semibold text-text-subtle`}>
        {name.trim()[0]?.toUpperCase() ?? '?'}
      </span>
    );
  return <img key={sources[i]} src={sources[i]} alt={`Foto de ${name}`} onError={() => setI((n) => n + 1)} className={`${box} object-cover`} />;
}

export function Person() {
  const { id = '' } = useParams();
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const desde = params.get('desde');
  const [info, setInfo] = useState<PersonInfo | null>(null);
  const [bgOk, setBgOk] = useState(true);
  const [fullBio, setFullBio] = useState(false);
  const [items, setItems] = useState<ItemCard[]>([]);
  const [loading, setLoading] = useState(true);
  const [more, setMore] = useState(false);
  const [error, setError] = useState('');
  const name = info?.name || params.get('nombre') || 'Reparto';

  async function load(offset: number, signal?: AbortSignal) {
    setLoading(true);
    setError('');
    try {
      const r = await api<{ items: ItemCard[] }>('GET', `/api/v1/items?person=${encodeURIComponent(id)}&limit=100&offset=${offset}`, undefined, signal);
      if (signal?.aborted) return;
      setItems((old) => (offset ? [...old, ...r.items] : r.items));
      setMore(r.items.length === 100);
    } catch (e) {
      if (!signal?.aborted) setError(e instanceof ApiError ? e.message : 'No se ha podido cargar la filmografía.');
    } finally {
      if (!signal?.aborted) setLoading(false);
    }
  }
  useCatalogRefresh(() => load(0));
  useEffect(() => {
    setItems([]);
    setInfo(null);
    setBgOk(true);
    setFullBio(false);
    const c = new AbortController();
    void load(0, c.signal);
    // Los datos de la persona son un extra: sin TMDB o con un fallo, la página sigue con el nombre y sus títulos.
    api<{ person: PersonInfo }>('GET', `/api/v1/people/${encodeURIComponent(id)}`, undefined, c.signal)
      .then((r) => setInfo(r.person))
      .catch(() => {});
    return () => c.abort();
  }, [id]);

  const movies = items.filter((i) => i.kind === 'movie');
  const series = items.filter((i) => i.kind !== 'movie');
  const candidates = [...new Set([desde, ...items.map((i) => i.id)].filter((x): x is string => !!x))].slice(0, 12);
  const years = info?.birthday ? edad(info.birthday, info.deathday || undefined) : null;
  const bio = info?.biography?.trim() ?? '';

  return (
    <main className="relative -mt-16 min-h-[calc(100vh-4rem)] overflow-hidden pb-10 tv:mt-0 tv:min-h-screen tv:pb-4">
      {/* Fondo: su foto grande a la derecha, fundida con el fondo hacia la izquierda y hacia abajo. */}
      <div aria-hidden className="absolute inset-0 overflow-hidden">
        {info?.has_photo && bgOk && (
          <img
            src={`/api/v1/people/${encodeURIComponent(id)}/photo`}
            alt=""
            onError={() => setBgOk(false)}
            className="absolute right-0 top-0 h-[95vh] w-full object-cover object-top opacity-30 tablet:w-[60%]"
          />
        )}
        <div className="absolute inset-0" style={{ background: 'linear-gradient(90deg, var(--color-bg) 30%, color-mix(in srgb, var(--color-bg) 70%, transparent) 55%, transparent 85%)' }} />
        <div className="absolute inset-0" style={{ background: 'linear-gradient(0deg, var(--color-bg) 15%, transparent 60%)' }} />
      </div>

      <div className="relative flex flex-col gap-4 px-4 pb-8 pt-24 desktop:px-8 tv:gap-2 tv:px-3 tv:pb-4 tv:pt-4">
        <button
          type="button"
          onClick={() => navigate(-1)}
          aria-label="Volver"
          className="flex h-9 w-9 items-center justify-center rounded-full text-text-muted hover:bg-surface-overlay hover:text-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus tv:hidden"
        >
          <ArrowLeft aria-hidden className="h-5 w-5" />
        </button>
        <div className="flex flex-col gap-6 tablet:flex-row tablet:items-start tv:flex-row tv:gap-5">
          <Portrait person={id} name={name} candidates={candidates} />
          <div className="flex min-w-0 max-w-3xl flex-col gap-3 tv:gap-1.5">
            <h1 className="text-display font-semibold leading-tight text-text drop-shadow">{name}</h1>
            <div className="flex flex-col gap-1 text-sm text-text-muted">
              {info?.birthday && (
                <p>
                  Nacimiento: <span className="text-text">{fecha(info.birthday)}</span>
                  {years !== null && !info.deathday && ` (edad ${years})`}
                </p>
              )}
              {info?.deathday && (
                <p>
                  Fallecimiento: <span className="text-text">{fecha(info.deathday)}</span>
                  {years !== null && ` (edad ${years})`}
                </p>
              )}
              {info?.place_of_birth && <p className="text-text">{info.place_of_birth}</p>}
            </div>
            {bio && (
              <div className="flex flex-col items-start gap-1">
                <p className={`whitespace-pre-line text-base text-text tv:text-sm ${fullBio ? '' : 'line-clamp-5 tv:line-clamp-4'}`}>{bio}</p>
                {bio.length > 320 && (
                  <button
                    type="button"
                    onClick={() => setFullBio((v) => !v)}
                    className="rounded-sm text-sm font-semibold text-text-muted hover:text-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus"
                  >
                    {fullBio ? 'Menos' : 'Más'}
                  </button>
                )}
              </div>
            )}
          </div>
        </div>
      </div>

      <div className="relative flex flex-col gap-8 tv:gap-4">
        {error && (
          <div className="px-4 desktop:px-8 tv:px-3">
            <Banner tone="error">{error}</Banner>
          </div>
        )}
        {!loading && !error && !items.length && (
          <p className="px-4 text-sm text-text-muted desktop:px-8 tv:px-3">No hay películas ni series suyas en tus bibliotecas.</p>
        )}
        <MediaRow title="Películas" count={movies.length}>
          {movies.map((item) => (
            <MediaCard key={item.id} item={item} emblemSpace={movies.some(v=>v.library_4k)} />
          ))}
        </MediaRow>
        <MediaRow title="Series" count={series.length}>
          {series.map((item) => (
            <MediaCard key={item.id} item={item} />
          ))}
        </MediaRow>
        <div className="px-4 desktop:px-8 tv:px-3">
          {loading ? (
            <Loading />
          ) : error ? (
            <Button className="w-fit" onClick={() => void load(items.length)}>
              Reintentar
            </Button>
          ) : (
            more && (
              <Button variant="secondary" className="w-fit" onClick={() => void load(items.length)}>
                Cargar más
              </Button>
            )
          )}
        </div>
      </div>
    </main>
  );
}
