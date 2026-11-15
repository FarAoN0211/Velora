import { useCatalogRefresh } from '../lib/liveCatalog';
import { FileOptions, type FileSelection } from '../components/fileOptions';
import { ContentInfo } from '../components/contentInfo';
import { TechBadges } from '../components/techBadges';
// Ficha: archivo, audio y subtítulos antes del reparto; decisión de reproducción al abrir el vídeo.
import { useCallback, useEffect, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { ArrowLeft, Check, Clapperboard, Play, RotateCcw } from 'lucide-react';
import { api, ApiError } from '../lib/api';
import {
  backdropUrl,
  imageUrl,
  onItemChanged,
  EXTRA_LABEL,
  extraName,
  fmtDuration,
  fmtRemaining,
  posterUrl,
  useMenuLibraries,
  type ItemDetail,
} from '../lib/catalog';
import { Banner, Button, Loading } from '../components/ui';
import { useCardMenu } from '../components/cardMenu';

const tvButton = 'tv:h-9 tv:min-h-0 tv:px-3 tv:text-sm';

export function MovieDetail() {
  const { id = '' } = useParams();
  const navigate = useNavigate();
  const [item, setItem] = useState<ItemDetail | null>(null);
  const [tmdb, setTmdb] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fileId, setFileId] = useState('');
  const [selection, setSelection] = useState<FileSelection | null>(null);
  const libraries = useMenuLibraries();
  // Menú ⋮ de la ficha (el mismo de las carátulas, con la sección de administración para administradores).
  const fichaMenu = useCardMenu({
    item: item ?? ({ id, title: '', position_ms: 0, played: false } as unknown as ItemDetail),
    onPlayedChange: () => void load(),
    onEdited: () => void load(),
    inDetail: true,
  });

  const load = useCallback(async () => {
    try {
      const r = await api<{ item: ItemDetail; tmdb_attribution: boolean }>('GET', `/api/v1/items/${id}`);
      setItem(r.item);
      setTmdb(r.tmdb_attribution);
      const first = r.item.files.find((f) => !f.missing) ?? r.item.files[0];
      setFileId(first?.id ?? '');
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'No se ha podido cargar la ficha.');
    }
  }, [id]);
  useCatalogRefresh(() => load());
  useEffect(() => {
    void load();
  }, [load]);
  useEffect(() => (id ? onItemChanged(id, () => void load()) : undefined), [id, load]);
  // Al volver del reproductor propio de la app, el progreso ya no es el que era.
  useEffect(() => {
    const again = () => void load();
    window.addEventListener('velora:volver', again);
    return () => window.removeEventListener('velora:volver', again);
  }, [load]);

  const file = item?.files.find((f) => f.id === fileId);

  const selectFile = useCallback((s: FileSelection) => {setSelection(s); if(s.file) setFileId(s.file);}, []);

  async function setPlayed(played: boolean) {
    await api(played ? 'POST' : 'DELETE', `/api/v1/me/items/${id}/played`);
    await load();
  }

  // Tráileres y extras: el reproductor de siempre, sin progreso ni «vista» (lo controla el servidor).
  function playExtra(extraId: string) {
    navigate(`/ver/${id}?${new URLSearchParams({ file: extraId })}`);
  }

  function play(fromStart: boolean) {
    const params = new URLSearchParams({ file: fileId });
    if (selection?.audio != null) params.set('audio', String(selection.audio));
    params.set('sub', selection?.subtitle || 'auto');
    if (fromStart) params.set('inicio', '1');
    navigate(`/ver/${id}?${params}`);
  }

  if (error)
    return (
      <main className="mx-auto w-full max-w-content p-6">
        <Banner tone="error">{error}</Banner>
      </main>
    );
  if (!item) return <Loading />;

  const resume = item.position_ms > 0 && !item.played;
  const blocked = !file || file.missing;
  const library = libraries.find((l) => l.id === item.library_id);
  const art = item.has_backdrop ? backdropUrl(item.id, item.image_rev) : item.has_poster ? posterUrl(item.id, item.image_rev) : null;
  const trailer = item.extras.find((x) => x.kind === 'trailer');
  const showExtras = item.extras.length > 1 || item.extras.some((x) => x.kind !== 'trailer');
  return (
    <main className="relative -mt-16 min-h-[calc(100vh-4rem)] tv:mt-0 tv:min-h-screen">
      {/* Fondo: la imagen de fondo nítida o, sin ella, la carátula desenfocada. Degradados para que el texto se lea. */}
      <div aria-hidden className="absolute inset-x-0 top-0 h-[85vh] overflow-hidden">
        {art && (
          <img
            src={art}
            alt=""
            className={item.has_backdrop ? 'h-full w-full object-cover object-[70%_20%]' : 'h-full w-full scale-110 object-cover opacity-40 blur-2xl'}
          />
        )}
        <div className="absolute inset-0 hidden tablet:block" style={{ background: 'linear-gradient(90deg, var(--color-bg) 0%, color-mix(in srgb, var(--color-bg) 75%, transparent) 40%, transparent 75%)' }} />
        <div className="absolute inset-0" style={{ background: 'linear-gradient(0deg, var(--color-bg) 5%, transparent 55%)' }} />
      </div>

      <div className="relative flex min-h-[55vh] flex-col justify-end gap-4 px-4 pb-8 pt-24 desktop:px-12 tv:min-h-0 tv:gap-2 tv:px-4 tv:pb-3 tv:pt-4">
        <Link
          to={library ? `/biblioteca/${library.id}` : '/'}
          className="inline-flex w-fit items-center gap-1 text-sm text-text-muted hover:text-text tv:hidden"
        >
          <ArrowLeft aria-hidden className="h-4 w-4" /> {library?.name ?? 'Inicio'}
        </Link>
        <div className="flex items-start gap-5 tv:gap-4">
          {item.has_poster && (
            <img src={posterUrl(item.id, item.image_rev)} alt={`Carátula de ${item.title}`} className="hidden aspect-poster w-52 shrink-0 rounded-lg object-cover shadow-pop tablet:block tv:w-36" />
          )}
          <div className="flex min-w-0 max-w-[42rem] flex-1 flex-col gap-3 tv:gap-1.5">
            <h1 className="text-3xl tablet:text-4xl font-semibold leading-tight text-text drop-shadow">
              {item.has_logo ? <img src={imageUrl(item.id, 'logo', item.image_rev)} alt={item.title} className="max-h-24 max-w-full object-contain object-left tv:max-h-14" /> : item.title}
            </h1>
            <p className="flex flex-wrap items-center gap-2 text-sm text-text-muted">
              {[item.year, fmtDuration(item.runtime_ms)].filter(Boolean).join(' · ')}
              {item.official_rating && <span className="rounded-sm border border-border px-1.5 text-xs">+{item.official_rating}</span>}
              {item.quality && !file?.tech?.length && <span className="rounded-sm bg-surface-overlay px-1.5 text-xs font-semibold text-text">{item.quality}</span>}
              {item.played && (
                <span className="inline-flex items-center gap-1 text-status-verified">
                  <Check aria-hidden className="h-4 w-4" /> Vista
                </span>
              )}
            </p>
            <TechBadges badges={file?.tech} />
            {item.genres.length > 0 && (
              <p data-tv-omitir className="flex flex-wrap gap-2 tv:gap-1" aria-label="Géneros">
                {item.genres.map((g) =>
                  library ? (
                    <Link
                      key={g}
                      to={`/biblioteca/${library.id}?genero=${encodeURIComponent(g)}`}
                      className="rounded-full border border-border px-3 py-0.5 text-xs text-text-muted hover:border-text-muted hover:text-text tv:px-2 tv:py-0"
                    >
                      {g}
                    </Link>
                  ) : (
                    <span key={g} className="rounded-full border border-border px-3 py-0.5 text-xs text-text-muted">
                      {g}
                    </span>
                  ),
                )}
              </p>
            )}
            {item.tagline && <p className="text-sm italic text-text-muted tv:hidden">{item.tagline}</p>}
            {item.overview ? (
              <p className="max-w-prose text-sm leading-relaxed text-text tv:line-clamp-4 tv:leading-snug">{item.overview}</p>
            ) : (
              <p className="text-sm text-text-subtle">Sin sinopsis. Con la clave de TMDB, VELORA completa sinopsis y carátulas.</p>
            )}
            {resume && (
              <div className="flex max-w-md items-center gap-3 text-sm text-text-muted">
                <div className="h-1 flex-1 overflow-hidden rounded-full bg-progress-track">
                  <div className="h-full bg-progress-fill" style={{ width: `${Math.min(100, (item.position_ms / (item.runtime_ms || 1)) * 100)}%` }} />
                </div>
                {fmtRemaining(item)}
              </div>
            )}
            <div className="flex flex-wrap items-center gap-2">
              <Button size="md" className={tvButton} onClick={() => play(false)} disabled={blocked} autoFocus>
                <Play aria-hidden className="h-5 w-5" fill="currentColor" />
                {resume ? 'Continuar' : 'Reproducir'}
              </Button>
              {resume && (
                <Button size="md" variant="secondary" className={tvButton} onClick={() => play(true)} disabled={blocked}>
                  <RotateCcw aria-hidden className="h-4 w-4" /> Desde el principio
                </Button>
              )}
              <span className="relative">{fichaMenu.trigger}</span>
              {trailer && (
                <Button size="md" variant="secondary" className={tvButton} onClick={() => playExtra(trailer.id)}>
                  <Clapperboard aria-hidden className="h-4 w-4" /> Ver tráiler
                </Button>
              )}
              <Button size="md" variant="secondary" className={tvButton} onClick={() => void setPlayed(!item.played)}>
                <Check aria-hidden className="h-4 w-4" /> {item.played ? 'Marcar como no vista' : 'Marcar como vista'}
              </Button>
            </div>
          </div>
        </div>
      </div>

      <ContentInfo item={item} file={file} options={<FileOptions item={item} onChange={selectFile} />} />
      {showExtras && (
        <section aria-label="Extras" className="relative mx-4 mt-6 flex max-w-5xl flex-col gap-3 desktop:mx-12 tv:mx-4 tv:mt-3 tv:gap-2">
          <h2 className="text-lg font-semibold">Extras</h2>
          <ul className="grid gap-3 tablet:grid-cols-2 desktop:grid-cols-3">
            {item.extras.map((x) => (
              <li key={x.id}>
                <button
                  type="button"
                  onClick={() => playExtra(x.id)}
                  className="group flex w-full items-center gap-3 rounded-lg bg-surface p-3 text-left hover:bg-surface-raised focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus"
                >
                  <span aria-hidden className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-surface-overlay text-text group-hover:bg-accent group-hover:text-on-accent">
                    <Play className="ml-0.5 h-4 w-4" fill="currentColor" />
                  </span>
                  <span className="min-w-0">
                    <span className="block truncate text-sm font-semibold text-text">{extraName(x)}</span>
                    <span className="block text-xs text-text-muted">
                      {[x.title ? EXTRA_LABEL[x.kind] : null, fmtDuration(x.duration_ms)].filter(Boolean).join(' · ')}
                    </span>
                  </span>
                </button>
              </li>
            ))}
          </ul>
        </section>
      )}
      {fichaMenu.menu}
      {fichaMenu.errorNote && <div className="fixed bottom-20 left-1/2 z-40 h-10 w-72 -translate-x-1/2">{fichaMenu.errorNote}</div>}
      {tmdb && (
        <p className="relative mx-4 mt-4 pb-8 text-xs text-text-subtle desktop:mx-12 tv:mx-4 tv:mt-2 tv:pb-3">
          Datos e imágenes de TMDB. Este producto usa la API de TMDB, pero no está respaldado ni certificado por TMDB.
        </p>
      )}
    </main>
  );
}
