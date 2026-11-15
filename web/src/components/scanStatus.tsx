// E1.3 · Progreso de los escaneos de bibliotecas (panel de control). Consulta /admin/scans cada 2 s mientras haya alguno
// en cola o en marcha; si no, cada 20 s (por si lo lanza la tarea programada o el botón de otra pantalla).
import { useCallback, useEffect, useRef, useState } from 'react';
import { api } from '../lib/api';

export interface ScanJob {
  id: number;
  state: 'queued' | 'running' | 'done' | 'failed' | 'cancelled';
  last_error?: string;
  created_at: string;
  finished_at?: string;
  progress?: { phase: 'listing' | 'analyzing' | 'extras' | 'metadata'; path?: number; paths?: number; done: number; total: number; item?: string };
  result?: { scan?: { files: number; items: number; errors: number; missing: number; extras?: number; pending?: number }; enriched?: number; omitido?: string };
}

export const scanActive = (j?: ScanJob) => j?.state === 'queued' || j?.state === 'running';

/** Estado de los escaneos por biblioteca. `refresh` fuerza una consulta (tras pulsar «Escanear»). onFinished avisa cuando
 * un escaneo deja de estar activo (para recargar los recuentos de títulos). */
export function useScans(onFinished?: () => void) {
  const [scans, setScans] = useState<Record<string, ScanJob>>({});
  const prev = useRef<Record<string, ScanJob>>({});
  const timer = useRef<number>();
  const finishedCallback = useRef(onFinished);
  finishedCallback.current = onFinished;
  const mounted = useRef(false);

  const refresh = useCallback(async () => {
    window.clearTimeout(timer.current);
    let next: Record<string, ScanJob> = prev.current;
    try {
      next = (await api<{ scans: Record<string, ScanJob> }>('GET', '/api/v1/admin/scans')).scans ?? {};
    } catch {
      /* se reintenta en el siguiente ciclo */
    }
    if (!mounted.current) return;
    const finished = Object.keys(prev.current).some((id) => scanActive(prev.current[id]) && !scanActive(next[id]));
    prev.current = next;
    setScans(next);
    if (finished) finishedCallback.current?.();
    const anyActive = Object.values(next).some(scanActive);
    timer.current = window.setTimeout(() => void refresh(), anyActive ? 2000 : 20000);
  }, []);

  useEffect(() => {
    mounted.current = true;
    void refresh();
    return () => { mounted.current = false; window.clearTimeout(timer.current); };
  }, [refresh]);

  return { scans, refresh };
}

const PHASE: Record<NonNullable<ScanJob['progress']>['phase'], string> = {
  listing: 'Buscando archivos',
  analyzing: 'Analizando películas',
  extras: 'Tráileres y extras',
  metadata: 'Completando fichas (TMDB)',
};

const fmtDate = (s: string) => new Date(s).toLocaleString('es-ES', { dateStyle: 'short', timeStyle: 'short' });

/** Porcentaje de un escaneo en marcha (null = indeterminado: en cola o buscando archivos). */
export function scanPercent(j?: ScanJob): number | null {
  const p = j?.progress;
  if (j?.state !== 'running' || !p || p.phase === 'listing' || !p.total) return null;
  return Math.min(100, Math.round((p.done / p.total) * 100));
}

export function ProgressBar({ percent, label }: { percent: number | null; label: string }) {
  return (
    <div
      role="progressbar"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={percent ?? undefined}
      className="relative h-2 w-full overflow-hidden rounded-full bg-progress-track"
    >
      {percent === null ? (
        <div className="absolute inset-y-0 w-1/3 animate-[indeterminado_1.4s_ease-in-out_infinite] rounded-full bg-progress-fill motion-reduce:animate-none" />
      ) : (
        <div className="h-full rounded-full bg-progress-fill transition-[width] duration-base" style={{ width: `${percent}%` }} />
      )}
    </div>
  );
}

/** Línea de estado y barra del último escaneo de una biblioteca. */
export function ScanStatus({ job, name }: { job?: ScanJob; name: string }) {
  if (!job) return <p className="text-sm text-text-muted">Todavía no se ha escaneado.</p>;
  if (job.state === 'queued')
    return (
      <div className="flex flex-col gap-1" aria-live="polite">
        <p className="text-sm text-text-muted">En cola: empezará cuando acaben los escaneos anteriores.</p>
        <ProgressBar percent={null} label={`Escaneo de ${name} en cola`} />
      </div>
    );
  if (job.state === 'running') {
    const p = job.progress;
    const pct = scanPercent(job);
    const where = p?.paths && p.paths > 1 && p.phase !== 'metadata' ? ` · carpeta ${p.path} de ${p.paths}` : '';
    const text = !p
      ? 'Empezando…'
      : p.phase === 'listing'
        ? `${PHASE.listing}… ${p.done.toLocaleString('es-ES')} encontrados${where}`
        : `${PHASE[p.phase]}: ${p.done.toLocaleString('es-ES')} de ${p.total.toLocaleString('es-ES')} (${pct ?? 0} %)${where}`;
    return (
      <div className="flex flex-col gap-1" aria-live="polite">
        <p className="text-sm text-text">Escaneando · {text}</p>
        <ProgressBar percent={pct} label={`Escaneo de ${name}`} />
        {p?.item && <p className="truncate text-xs text-text-subtle">{p.item}</p>}
      </div>
    );
  }
  if (job.state === 'cancelled') return <p className="text-sm text-text-muted">Escaneo cancelado{job.finished_at ? ` (${fmtDate(job.finished_at)})` : ''}.</p>;
  if (job.state === 'failed')
    return (
      <p className="text-sm text-danger">
        Error en el último escaneo{job.finished_at ? ` (${fmtDate(job.finished_at)})` : ''}: {job.last_error ?? 'error desconocido'}. No se ha
        borrado ninguna ficha.
      </p>
    );
  const r = job.result?.scan;
  return (
    <p className="text-sm text-text-muted">
      Completado: {fmtDate(job.finished_at ?? job.created_at)}
      {r &&
        ` · ${r.files.toLocaleString('es-ES')} archivos${r.extras ? `, ${r.extras} extras` : ''}${r.missing ? `, ${r.missing} desaparecidos` : ''}${
          r.errors ? `, ${r.errors} con errores` : ''
        }`}
      {job.result?.enriched ? ` · ${job.result.enriched} fichas completadas` : ''}
      {job.result?.omitido ? ` · ${job.result.omitido}` : ''}
    </p>
  );
}
