import { useCatalogRefresh } from '../lib/liveCatalog';
import { FileOptions, type FileSelection } from '../components/fileOptions';
import { ContentInfo } from '../components/contentInfo';
import { TechBadges } from '../components/techBadges';
// S06 · Ficha de serie y temporada (E2 S1; replica/design/screens.md S06 y components.md EpisodeList): fondo de la serie,
// «Siguiente: T2 · E5» como acción principal, pestañas de temporada (especiales al final) y la lista de episodios con su
// miniatura, número, duración, sinopsis, progreso y visto. La temporada abierta vive en la dirección (?temporada=N).
import { useCallback, useEffect, useState } from 'react';
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { ArrowLeft, Check, CheckCircle2, Clapperboard, Play } from 'lucide-react';
import { api, ApiError } from '../lib/api';
import {
  backdropUrl,
  episodeCode,
  fmtDuration,
  fmtRemaining,
  imageUrl,
  onItemChanged,
  posterUrl,
  useMenuLibraries,
  type EpisodeInfo,
  type ItemDetail,
} from '../lib/catalog';
import { Banner, Button, Loading } from '../components/ui';
import { useCardMenu } from '../components/cardMenu';

const tvButton = 'tv:h-9 tv:min-h-0 tv:px-3 tv:text-sm';

export function SeriesDetail() {
  const { id = '' } = useParams();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const [selection, setSelection] = useState<FileSelection | null>(null);
  const [series, setSeries] = useState<ItemDetail | null>(null);
  const [season, setSeason] = useState<ItemDetail | null>(null);
  const [tmdb, setTmdb] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [seasonError, setSeasonError] = useState<string | null>(null);
  const libraries = useMenuLibraries();
  const fichaMenu = useCardMenu({
    item: series ?? ({ id, title: '', position_ms: 0, played: false, kind: 'series' } as unknown as ItemDetail),
    onPlayedChange: () => void reload(),
    onEdited: () => void reload(),
    inDetail: true,
  });

  const loadSeries = useCallback(async () => {
    try {
      const r = await api<{ item: ItemDetail; tmdb_attribution: boolean }>('GET', `/api/v1/items/${id}`);
      if (r.item.kind !== 'series') {
        navigate(`/pelicula/${id}`, { replace: true });
        return null;
      }
      setSeries(r.item);
      setTmdb(r.tmdb_attribution);
      return r.item;
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'No se ha podido cargar la serie.');
      return null;
    }
  }, [id, navigate]);

  // La temporada pedida (?temporada=N), la del episodio que toca o, si no, la primera.
  const seasons = series?.seasons ?? [];
  const wanted = params.get('temporada');
  const current =
    seasons.find((s) => String(s.season_number) === wanted) ??
    seasons.find((s) => s.season_number === series?.next_up?.season_number) ??
    seasons[0];

  const loadSeason = useCallback(async (seasonId: string) => {
    try {
      const r = await api<{ item: ItemDetail }>('GET', `/api/v1/items/${seasonId}`);
      setSeason(r.item);
      setSeasonError(null);
    } catch (e) {
      setSeasonError(e instanceof ApiError ? e.message : 'No se ha podido cargar la temporada.');
    }
  }, []);

  const reload = useCallback(async () => {
    await loadSeries();
    if (current) await loadSeason(current.id);
  }, [loadSeries, loadSeason, current]);

  useCatalogRefresh(() => reload());
  useEffect(() => {
    setSeries(null);
    setSeason(null);
    void loadSeries();
  }, [loadSeries]);
  useEffect(() => {
    if (!current) return;
    if (season?.id !== current.id) setSeason(null);
    void loadSeason(current.id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [current?.id, loadSeason]);
  useEffect(() => (id ? onItemChanged(id, () => void reload()) : undefined), [id, reload]);
  // Al volver del reproductor propio de la app, el progreso ya no es el que era.
  useEffect(() => {
    const again = () => void reload();
    window.addEventListener('velora:volver', again);
    return () => window.removeEventListener('velora:volver', again);
  }, [reload]);

  async function setPlayed(itemId: string, played: boolean) {
    try {
      await api(played ? 'POST' : 'DELETE', `/api/v1/me/items/${itemId}/played`);
    } finally {
      await reload();
    }
  }

  function playEpisode(ep: { id: string; file_id?: string | null }, fromStart = false) {
    const p = new URLSearchParams();
    if (ep.file_id) p.set('file', ep.file_id);
    if (selection?.item === ep.id) {if(selection.file)p.set('file',selection.file); if(selection.audio !== null)p.set('audio',String(selection.audio));p.set('sub',selection.subtitle || 'auto');}
    if (fromStart) p.set('inicio', '1');
    navigate(`/ver/${ep.id}${p.toString() ? `?${p}` : ''}`);
  }

  if (error)
    return (
      <main className="mx-auto w-full max-w-content p-6">
        <Banner tone="error">{error}</Banner>
      </main>
    );
  if (!series) return <Loading />;

  const library = libraries.find((l) => l.id === series.library_id);
  const art = series.has_backdrop ? backdropUrl(series.id, series.image_rev) : series.has_poster ? posterUrl(series.id, series.image_rev) : null;
  const trailer = series.extras.find((x) => x.kind === 'trailer');
  const next = series.next_up;
  const started = seasons.some((s) => s.unplayed_count < s.episode_count);
  const resume = !!next && next.position_ms > 0 && !next.played;
  const nextCode = next ? episodeCode(next) : '';
  const playLabel = resume ? `Continuar ${nextCode}` : started && !series.played ? `Siguiente: ${nextCode}` : `Reproducir ${nextCode}`;
  const regular = seasons.filter((s) => s.season_number !== 0).length;
  const meta = [series.year, regular ? `${regular} ${regular === 1 ? 'temporada' : 'temporadas'}` : null].filter(Boolean).join(' · ');

  return (
    <main className="relative -mt-16 min-h-[calc(100vh-4rem)] pb-8 tv:mt-0 tv:min-h-screen tv:pb-3">
      <div aria-hidden className="absolute inset-x-0 top-0 h-[75vh] overflow-hidden">
        {art && (
          <img
            src={art}
            alt=""
            className={series.has_backdrop ? 'h-full w-full object-cover object-[70%_20%]' : 'h-full w-full scale-110 object-cover opacity-40 blur-2xl'}
          />
        )}
        <div className="absolute inset-0 hidden tablet:block" style={{ background: 'linear-gradient(90deg, var(--color-bg) 0%, color-mix(in srgb, var(--color-bg) 75%, transparent) 40%, transparent 75%)' }} />
        <div className="absolute inset-0" style={{ background: 'linear-gradient(0deg, var(--color-bg) 5%, transparent 55%)' }} />
      </div>

      <div className="relative flex min-h-[55vh] flex-col justify-end gap-4 px-4 pb-6 pt-24 desktop:px-12 tv:min-h-0 tv:gap-2 tv:px-4 tv:pb-3 tv:pt-4">
        <Link to={library ? `/biblioteca/${library.id}` : '/'} className="inline-flex w-fit items-center gap-1 text-sm text-text-muted hover:text-text tv:hidden">
          <ArrowLeft aria-hidden className="h-4 w-4" /> {library?.name ?? 'Inicio'}
        </Link>
        <div className="flex items-start gap-5 tv:gap-4">
          {series.has_poster && (
            <img src={posterUrl(series.id, series.image_rev)} alt={`Carátula de ${series.title}`} className="hidden aspect-poster w-52 shrink-0 rounded-lg object-cover shadow-pop tablet:block tv:w-36" />
          )}
          <div className="flex min-w-0 max-w-[42rem] flex-1 flex-col gap-3 tv:gap-1.5">
            <h1 className="text-3xl tablet:text-4xl font-semibold leading-tight text-text drop-shadow">
              {series.has_logo ? <img src={imageUrl(series.id, 'logo', series.image_rev)} alt={series.title} className="max-h-24 max-w-full object-contain object-left tv:max-h-14" /> : series.title}
            </h1>
            <p className="flex flex-wrap items-center gap-2 text-sm text-text-muted">
              {meta}
              {series.official_rating && <span className="rounded-sm border border-border px-1.5 text-xs">+{series.official_rating}</span>}
              {series.played && (
                <span className="inline-flex items-center gap-1 text-status-verified">
                  <Check aria-hidden className="h-4 w-4" /> Vista
                </span>
              )}
            </p>
            {series.genres.length > 0 && (
              <p data-tv-omitir className="flex flex-wrap gap-2 tv:gap-1" aria-label="Géneros">
                {series.genres.map((g) =>
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
            {series.overview ? (
              <p className="max-w-prose line-clamp-4 text-sm leading-relaxed text-text tv:line-clamp-3 tv:leading-snug">{series.overview}</p>
            ) : (
              <p className="text-sm text-text-subtle">Sin sinopsis. Con la clave de TMDB, VELORA completa sinopsis, carátulas y los datos de cada episodio.</p>
            )}
            {next && (
              <p className="text-sm text-text-muted tv:text-xs">
                <span className="font-semibold text-text">{nextCode}</span> · {next.title}
                {resume && ` · ${fmtRemaining(next)}`}
              </p>
            )}
            <div className="flex flex-wrap items-center gap-2">
              <Button size="md" className={tvButton} onClick={() => next && playEpisode(next)} disabled={!next} autoFocus>
                <Play aria-hidden className="h-5 w-5" fill="currentColor" /> {playLabel}
              </Button>
              <span className="relative">{fichaMenu.trigger}</span>
              {trailer && (
                <Button size="md" variant="secondary" className={tvButton} onClick={() => navigate(`/ver/${series.id}?${new URLSearchParams({ file: trailer.id })}`)}>
                  <Clapperboard aria-hidden className="h-4 w-4" /> Ver tráiler
                </Button>
              )}
              <Button size="md" variant="secondary" className={tvButton} onClick={() => void setPlayed(series.id, !series.played)}>
                <Check aria-hidden className="h-4 w-4" /> {series.played ? 'Marcar como no vista' : 'Marcar como vista'}
              </Button>
            </div>
          </div>
        </div>
      </div>

      {seasons.length > 0 && (
        <section aria-label="Temporadas y episodios" className="relative mx-4 flex max-w-6xl flex-col gap-4 desktop:mx-12 tv:mx-4 tv:gap-2">
          <div role="tablist" aria-label="Temporadas" className="flex gap-2 overflow-x-auto pb-1 [scrollbar-width:none] tv:gap-1.5">
            {seasons.map((s) => {
              const selected = s.id === current?.id;
              return (
                <button
                  key={s.id}
                  type="button"
                  role="tab"
                  aria-selected={selected}
                  onClick={() => {
                    const p = new URLSearchParams(params);
                    p.set('temporada', String(s.season_number ?? 0));
                    setParams(p, { replace: true });
                  }}
                  className={`shrink-0 rounded-full px-4 py-1.5 text-sm font-semibold transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus tv:px-3 tv:py-1 tv:text-xs ${
                    selected ? 'bg-text text-bg' : 'bg-surface-raised text-text-muted hover:text-text'
                  }`}
                >
                  {s.title}
                  {s.unplayed_count === 0 && <Check aria-label="vista" className="ml-1 inline h-3.5 w-3.5" />}
                </button>
              );
            })}
          </div>

          {current && (
            <div className="flex flex-wrap items-center justify-between gap-2 text-sm text-text-muted">
              <span>
                {current.episode_count} {current.episode_count === 1 ? 'episodio' : 'episodios'}
                {current.unplayed_count > 0 && current.unplayed_count < current.episode_count && ` · ${current.unplayed_count} sin ver`}
              </span>
              <button
                type="button"
                data-tv-omitir
                onClick={() => void setPlayed(current.id, current.unplayed_count > 0)}
                className="rounded-md px-2 py-1 hover:text-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus"
              >
                {current.unplayed_count > 0 ? 'Marcar temporada como vista' : 'Marcar temporada como no vista'}
              </button>
            </div>
          )}
          {season?.overview && <p className="max-w-3xl text-sm text-text-muted tv:hidden">{season.overview}</p>}

          {seasonError && <Banner tone="error">{seasonError}</Banner>}
          {!season && !seasonError && <Loading />}
          {season && (
            <ol className="flex flex-col gap-2 tv:gap-1.5" aria-label={`Episodios de ${current?.title ?? 'la temporada'}`}>
              {(season.episodes ?? []).map((ep) => (
                <EpisodeRow
                  key={ep.id}
                  ep={ep}
                  isNext={ep.id === next?.id}
                  onPlay={() => playEpisode(ep)}
                  onTogglePlayed={() => void setPlayed(ep.id, !ep.played)}
                />
              ))}
            </ol>
          )}
        </section>
      )}

      <ContentInfo item={series} options={<FileOptions item={series} episodes={season?.episodes} onChange={setSelection} />} />
      {fichaMenu.menu}
      {fichaMenu.errorNote && <div className="fixed bottom-20 left-1/2 z-40 h-10 w-72 -translate-x-1/2">{fichaMenu.errorNote}</div>}
      {tmdb && (
        <p className="relative mx-4 mt-6 text-xs text-text-subtle desktop:mx-12 tv:mx-4 tv:mt-2">
          Datos e imágenes de TMDB. Este producto usa la API de TMDB, pero no está respaldado ni certificado por TMDB.
        </p>
      )}
    </main>
  );
}

/** Un episodio de la lista: toda la fila reproduce (o continúa); ✓ marca como visto sin abrirlo. */
function EpisodeRow({ ep, isNext, onPlay, onTogglePlayed }: { ep: EpisodeInfo; isNext: boolean; onPlay: () => void; onTogglePlayed: () => void }) {
  const src = ep.has_thumb ? imageUrl(ep.id, 'thumb', ep.image_rev) : ep.has_backdrop ? backdropUrl(ep.id, ep.image_rev) : null;
  const [imgOk, setImgOk] = useState(true);
  useEffect(() => setImgOk(true), [src]);
  const pct = ep.runtime_ms && ep.position_ms && !ep.played ? Math.min(100, (ep.position_ms / ep.runtime_ms) * 100) : 0;
  const number = ep.episode_number ? `${ep.episode_number}${ep.episode_end ? `–${ep.episode_end}` : ''}` : '';
  const facts = [pct ? fmtRemaining(ep) : fmtDuration(ep.runtime_ms), ep.year].filter(Boolean).join(' · ');
  return (
    <li className={`group relative flex items-stretch gap-1 rounded-lg ${isNext ? 'bg-surface-raised' : 'bg-surface'} focus-within:ring-2 focus-within:ring-focus`}>
      <button
        type="button"
        onClick={onPlay}
        aria-label={[`${number ? `Episodio ${number}: ` : ''}${ep.title}`, ep.played ? 'visto' : '', pct ? fmtRemaining(ep).toLowerCase() : ''].filter(Boolean).join(', ')}
        className="flex min-w-0 flex-1 flex-wrap items-start gap-4 rounded-lg p-3 text-left hover:bg-surface-raised focus-visible:outline-none tablet:flex-nowrap tv:flex-nowrap tv:gap-3 tv:p-2"
      >
        <span className="relative aspect-backdrop w-40 shrink-0 overflow-hidden rounded-md bg-surface-overlay tablet:w-52 tv:!w-32">
          {src && imgOk ? (
            <img src={src} alt="" loading="lazy" decoding="async" onError={() => setImgOk(false)} className="h-full w-full object-cover" />
          ) : (
            <span className="flex h-full w-full items-center justify-center text-2xl font-semibold text-text-subtle">{number || '·'}</span>
          )}
          <span className="absolute inset-0 flex items-center justify-center bg-scrim opacity-0 transition-opacity duration-fast group-hover:opacity-100 group-focus-within:opacity-100">
            <span className="flex h-10 w-10 items-center justify-center rounded-full bg-accent text-on-accent tv:h-8 tv:w-8">
              <Play aria-hidden className="ml-0.5 h-4 w-4" fill="currentColor" />
            </span>
          </span>
          {ep.played && <CheckCircle2 aria-hidden className="absolute right-1.5 top-1.5 h-5 w-5 rounded-full bg-scrim text-status-verified" />}
          {pct > 0 && (
            <span className="absolute inset-x-2 bottom-1.5 h-1 overflow-hidden rounded-full bg-progress-track">
              <span className="block h-full rounded-full bg-progress-fill" style={{ width: `${pct}%` }} />
            </span>
          )}
        </span>
        <span className="flex min-w-0 flex-1 flex-col gap-1 tv:gap-0.5">
          <span className="flex items-baseline gap-2">
            <span className="truncate text-base font-semibold text-text tv:text-sm">
              {number && `${number}. `}
              {ep.title}
            </span>
            {isNext && <span className="shrink-0 rounded-sm bg-accent px-1.5 text-xs font-semibold text-on-accent">Siguiente</span>}
          </span>
          {facts && <span className="text-xs text-text-muted">{facts}</span>}
          <span className="my-0.5 hidden tablet:block tv:block">
            <TechBadges badges={ep.tech} size="sm" />
          </span>
          {ep.overview && <span className="line-clamp-2 text-sm text-text-muted tv:text-xs">{ep.overview}</span>}
        </span>
        {/* Móvil: junto a la miniatura no caben; van debajo, en una fila a todo el ancho. */}
        <span className="-mt-2 min-w-0 basis-full tablet:hidden tv:hidden">
          <TechBadges badges={ep.tech} size="sm" />
        </span>
      </button>
      <button
        type="button"
        data-tv-omitir
        onClick={onTogglePlayed}
        aria-label={ep.played ? `Marcar ${ep.title} como no visto` : `Marcar ${ep.title} como visto`}
        aria-pressed={ep.played}
        className={`m-2 flex h-10 w-10 shrink-0 items-center justify-center self-center rounded-full hover:bg-surface-overlay focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus tv:hidden ${
          ep.played ? 'text-status-verified' : 'text-text-subtle'
        }`}
      >
        <Check aria-hidden className="h-5 w-5" />
      </button>
    </li>
  );
}
