// Parte de S20 en E0: copias de seguridad verificadas (D-8) y crecimiento del almacenamiento.
import { useCallback, useEffect, useState } from 'react';
import { AlertTriangle, CheckCircle2 } from 'lucide-react';
import { api, ApiError } from '../lib/api';
import { fmtBytes } from '../lib/catalog';
import { AdminTabs } from '../components/shell';
import { Banner, Button, Loading } from '../components/ui';

interface BackupMeta {
  file: string;
  created_at: string;
  size_bytes: number;
  verified: boolean;
  verify_error?: string;
  images_count?: number;
  incomplete?: boolean;
  images_missing?: string[];
}
interface Job {
  id: number;
  state: string;
  last_error?: string;
}
interface Overview {
  backups: BackupMeta[] | null;
  jobs: Job[] | null;
  free_bytes: number;
  margin_bytes: number;
  storage: { warn_margin_invasion?: boolean } | null;
}

const fmtDate = (s: string) => new Date(s).toLocaleString('es-ES', { dateStyle: 'short', timeStyle: 'short' });

export function Backups() {
  const [data, setData] = useState<Overview | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      setData(await api<Overview>('GET', '/api/v1/admin/backups'));
      setError(null);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'No se han podido cargar las copias.');
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const running = data?.jobs?.some((j) => j.state === 'queued' || j.state === 'running');
  useEffect(() => {
    if (!running) return;
    const t = setInterval(() => void load(), 3000);
    return () => clearInterval(t);
  }, [running, load]);

  async function start() {
    setBusy(true);
    setNotice(null);
    try {
      await api('POST', '/api/v1/admin/backup');
      setNotice('Copia en marcha. Se verificará restaurándola en una base temporal.');
      await load();
    } catch (e) {
      setNotice(e instanceof ApiError ? e.message : 'No se ha podido iniciar la copia.');
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="mx-auto flex w-full max-w-content flex-col gap-6 p-6">
      <AdminTabs />
      <div className="flex flex-wrap items-center justify-between gap-4">
        <h1 className="text-xl">Copias de seguridad</h1>
        <Button onClick={start} loading={busy || running}>
          {running ? 'Copia en curso…' : 'Hacer copia ahora'}
        </Button>
      </div>
      {notice && <Banner>{notice}</Banner>}
      {error && <Banner tone="error">{error}</Banner>}
      {!data && !error && <Loading />}
      {data && (
        <>
          <p className="text-sm text-text-muted">
            Espacio libre: {fmtBytes(data.free_bytes)} · margen de seguridad: {fmtBytes(data.margin_bytes)}. La copia diaria se hace a las
            04:00 y se conservan 7 diarias y 4 semanales.
          </p>
          {data.storage?.warn_margin_invasion && (
            <Banner tone="error">Al ritmo actual, en 30 días el espacio libre bajaría del margen de seguridad.</Banner>
          )}
          {!data.backups?.length ? (
            <p className="rounded-lg bg-surface p-8 text-center text-text-muted">Todavía no hay copias.</p>
          ) : (
            <table className="w-full text-left text-sm">
              <thead className="text-text-muted">
                <tr>
                  <th className="py-2 font-semibold">Fecha</th>
                  <th className="py-2 font-semibold">Tamaño</th>
                  <th className="py-2 font-semibold">Verificación</th>
                </tr>
              </thead>
              <tbody>
                {data.backups.map((b) => (
                  <tr key={b.file} className="border-t border-border">
                    <td className="py-2">{fmtDate(b.created_at)}</td>
                    <td className="py-2">
                      {fmtBytes(b.size_bytes)}
                      {b.images_count ? ` · ${b.images_count} ${b.images_count === 1 ? 'imagen propia' : 'imágenes propias'}` : ''}
                    </td>
                    <td className="py-2">
                      {b.verified && b.incomplete ? (
                        <span className="inline-flex items-center gap-1 text-status-error">
                          <AlertTriangle aria-hidden className="h-4 w-4" /> Incompleta: {b.images_missing?.length === 1 ? 'falta 1 imagen propia' : `faltan ${b.images_missing?.length} imágenes propias`}
                        </span>
                      ) : b.verified ? (
                        <span className="inline-flex items-center gap-1 text-status-verified">
                          <CheckCircle2 aria-hidden className="h-4 w-4" /> Verificada
                        </span>
                      ) : (
                        <span className="inline-flex items-center gap-1 text-status-error" title={b.verify_error}>
                          <AlertTriangle aria-hidden className="h-4 w-4" /> No verificada
                        </span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </>
      )}
    </main>
  );
}
