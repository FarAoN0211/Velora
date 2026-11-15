// S14 · Panel de control: resumen del servidor (como el «Panel» de Emby o los ajustes del servidor de Plex). Se abre desde
// el menú del usuario; solo para administradores.
import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { CheckCircle2, Clock, Film, HardDrive, MonitorPlay, RefreshCw, Users, XCircle } from 'lucide-react';
import type { ReactNode } from 'react';
import { api, ApiError } from '../lib/api';
import { fmtBytes, invalidateMenu } from '../lib/catalog';
import { NowPlaying } from '../components/nowPlaying';
import { PlaybackSettings } from '../components/playbackSettings';
import { AdminTabs } from '../components/shell';
import { Banner, Button, Card, Loading } from '../components/ui';

interface ScanJob {
  id: number;
  state: string;
  last_error?: string;
  created_at: string;
  finished_at?: string;
  result?: { enriched?: number };
}
interface PendingItem {
  id: string;
  title: string;
  year: number | null;
  library: string;
  content_path: string;
  extras: number;
}
interface Overview {
  version: string;
  counts: { libraries: number; movies: number; files: number; size_bytes: number; users: number; pending_genres: number; pending_items: number };
  tmdb_configured: boolean;
  optimized_streams: number;
  max_optimized_streams: number;
  scans: ScanJob[] | null;
}

const fmtDate = (s: string) => new Date(s).toLocaleString('es-ES', { dateStyle: 'short', timeStyle: 'short' });
const STATE: Record<string, string> = { queued: 'En cola', running: 'Escaneando', done: 'Terminado', failed: 'Con errores', abandoned: 'Abandonado' };

function Stat({ icon, label, value, hint }: { icon: ReactNode; label: string; value: ReactNode; hint?: ReactNode }) {
  return (
    <div className="flex items-start gap-3 rounded-lg bg-surface p-4">
      <span aria-hidden className="mt-0.5 text-text-muted">
        {icon}
      </span>
      <div className="min-w-0">
        <p className="text-sm text-text-muted">{label}</p>
        <p className="text-xl font-semibold text-text">{value}</p>
        {hint && <p className="text-xs text-text-subtle">{hint}</p>}
      </div>
    </div>
  );
}

export function AdminDashboard() {
  const [data, setData] = useState<Overview | null>(null);
  const [notice, setNotice] = useState<{ tone: 'info' | 'error'; text: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [pending, setPending] = useState<PendingItem[] | null>(null);

  const load = useCallback(async () => {
    try {
      setData(await api<Overview>('GET', '/api/v1/admin/overview'));
    } catch (e) {
      setNotice({ tone: 'error', text: e instanceof ApiError ? e.message : 'No se ha podido cargar el resumen.' });
    }
  }, []);
  useEffect(() => {
    void load();
  }, [load]);

  const scanning = data?.scans?.some((j) => j.state === 'queued' || j.state === 'running');
  useEffect(() => {
    if (!scanning) return;
    const t = setInterval(() => void load(), 4000);
    return () => clearInterval(t);
  }, [scanning, load]);

  async function scanAll() {
    setBusy(true);
    setNotice(null);
    try {
      // El servidor no duplica los escaneos que ya están en cola o en marcha.
      const r = await api<{ started: number; already: number }>('POST', '/api/v1/admin/libraries/scan');
      setNotice({
        tone: 'info',
        text: `${r.started} ${r.started === 1 ? 'escaneo iniciado' : 'escaneos iniciados'}${r.already ? ` · ${r.already} ya en marcha` : ''}. El progreso se ve en Bibliotecas.`,
      });
      invalidateMenu();
      await load();
    } catch (e) {
      setNotice({ tone: 'error', text: e instanceof ApiError ? e.message : 'No se ha podido iniciar el escaneo.' });
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex min-h-full flex-col">
      <main className="mx-auto flex w-full max-w-content flex-col gap-6 p-4 desktop:p-6">
        <AdminTabs />
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h1 className="text-xl">Resumen</h1>
          <Button variant="secondary" onClick={() => void scanAll()} disabled={busy || scanning} loading={busy}>
            <RefreshCw aria-hidden className="h-4 w-4" /> Escanear todas las bibliotecas
          </Button>
        </div>
        <NowPlaying />
        <PlaybackSettings />
        {notice && <Banner tone={notice.tone}>{notice.text}</Banner>}
        {!data && !notice && <Loading />}
        {data && (
          <>
            <section aria-label="Estado del servidor" className="grid gap-3 tablet:grid-cols-2 desktop:grid-cols-4">
              <Stat icon={<Film className="h-5 w-5" />} label="Películas" value={data.counts.movies.toLocaleString('es-ES')} hint={`${data.counts.libraries} ${data.counts.libraries === 1 ? 'biblioteca' : 'bibliotecas'}`} />
              <Stat
                icon={<HardDrive className="h-5 w-5" />}
                label="Archivos"
                value={data.counts.files.toLocaleString('es-ES')}
                hint={fmtBytes(data.counts.size_bytes)}
              />
              <Stat icon={<Users className="h-5 w-5" />} label="Usuarios activos" value={data.counts.users} />
              <Stat
                icon={<MonitorPlay className="h-5 w-5" />}
                label="Reproducciones optimizadas ahora"
                value={`${data.optimized_streams} de ${data.max_optimized_streams}`}
                hint="Las directas no ocupan hueco"
              />
            </section>

            <div className="grid gap-6 desktop:grid-cols-2">
              <Card className="flex flex-col gap-3">
                <h2 className="text-lg">Fichas y carátulas</h2>
                {data.tmdb_configured ? (
                  <p className="flex items-center gap-2 text-sm text-text">
                    <CheckCircle2 aria-hidden className="h-4 w-4 text-status-verified" /> TMDB está configurado.
                  </p>
                ) : (
                  <p className="flex items-center gap-2 text-sm text-text-muted">
                    <XCircle aria-hidden className="h-4 w-4" /> Sin clave de TMDB: no hay sinopsis, carátulas descargadas ni géneros.
                  </p>
                )}
                {data.tmdb_configured && data.counts.pending_genres > 0 && (
                  <p className="text-sm text-text-muted">
                    Faltan los géneros de {data.counts.pending_genres} películas: se completan en el próximo escaneo.
                  </p>
                )}
                <Link to="/admin/metadatos" className="text-sm text-accent hover:underline">
                  {data.tmdb_configured ? 'Gestionar la clave de TMDB' : 'Añadir la clave de TMDB'}
                </Link>
              </Card>

              <Card className="flex flex-col gap-3">
                <h2 className="text-lg">Últimos escaneos</h2>
                {(data.scans ?? []).length === 0 && <p className="text-sm text-text-muted">Todavía no se ha escaneado ninguna biblioteca.</p>}
                <ul className="flex flex-col gap-2">
                  {(data.scans ?? []).map((j) => (
                    <li key={j.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
                      {j.state === 'done' ? (
                        <CheckCircle2 aria-hidden className="h-4 w-4 text-status-verified" />
                      ) : j.state === 'failed' || j.state === 'abandoned' ? (
                        <XCircle aria-hidden className="h-4 w-4 text-danger" />
                      ) : (
                        <Clock aria-hidden className="h-4 w-4 text-text-muted" />
                      )}
                      <span className="text-text">{STATE[j.state] ?? j.state}</span>
                      <span className="text-text-muted">{fmtDate(j.finished_at ?? j.created_at)}</span>
                      {!!j.result?.enriched && <span className="text-text-muted">· {j.result.enriched} fichas completadas</span>}
                      {j.last_error && <span className="w-full text-xs text-danger">{j.last_error}</span>}
                    </li>
                  ))}
                </ul>
              </Card>
            </div>
            {data.counts.pending_items > 0 && (
              <Card className="flex flex-col gap-3">
                <h2 className="text-lg">Carpetas sin película ({data.counts.pending_items})</h2>
                <p className="text-sm text-text-muted">
                  Solo tienen el tráiler. Están ocultas a los usuarios y conservan su ficha; cuando aparezca la película en la
                  carpeta, el siguiente escaneo la mostrará con su tráiler. VELORA no borra ni mueve nada.
                </p>
                {pending === null ? (
                  <Button
                    variant="secondary"
                    className="w-fit"
                    onClick={() =>
                      void api<{ items: PendingItem[] }>('GET', '/api/v1/admin/pending-items')
                        .then((r) => setPending(r.items))
                        .catch(() => setNotice({ tone: 'error', text: 'No se ha podido cargar la lista.' }))
                    }
                  >
                    Ver la lista
                  </Button>
                ) : (
                  <ul className="grid gap-1 text-sm tablet:grid-cols-2">
                    {pending.map((p) => (
                      <li key={p.id} className="truncate text-text">
                        {p.content_path} <span className="text-text-subtle">· {p.library}</span>
                      </li>
                    ))}
                  </ul>
                )}
              </Card>
            )}
            <p className="text-xs text-text-subtle">VELORA {data.version}</p>
          </>
        )}
      </main>
    </div>
  );
}
