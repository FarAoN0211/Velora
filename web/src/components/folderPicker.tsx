// E1.3 · Explorador de carpetas para las bibliotecas (panel de control). Empieza en las carpetas de primer nivel de los
// medios montados en VELORA y permite entrar en subcarpetas (p. ej. telegram_media/Peliculas del montaje de Teldrive).
// El servidor solo deja navegar dentro de las raíces de medios; aquí solo se elige una ruta, no se crea nada.
import { useEffect, useState } from 'react';
import { ChevronLeft, ChevronRight, Cloud, Folder } from 'lucide-react';
import { api, ApiError } from '../lib/api';

interface Entry {
  name: string;
  path: string;
  kind: 'local' | 'remote_fuse';
}

export function FolderPicker({
  value,
  onChange,
  used,
}: {
  value: string;
  onChange: (path: string, name: string) => void;
  /** Carpetas que ya están en alguna biblioteca: se ven pero no se pueden elegir. */
  used: Set<string>;
}) {
  const [dir, setDir] = useState<string>('');
  const [data, setData] = useState<{ folders: Entry[]; parent: string; truncated: boolean } | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setData(null);
    setError(null);
    api<{ folders: Entry[] | null; parent: string; truncated: boolean }>('GET', `/api/v1/admin/media-folders${dir ? `?path=${encodeURIComponent(dir)}` : ''}`)
      .then((r) => !cancelled && setData({ folders: r.folders ?? [], parent: r.parent, truncated: r.truncated }))
      .catch((e) => !cancelled && setError(e instanceof ApiError ? e.message : 'No se ha podido leer la carpeta.'));
    return () => {
      cancelled = true;
    };
  }, [dir]);

  const crumbs = dir ? dir.split('/').filter(Boolean) : [];
  return (
    <div className="flex flex-col gap-2 rounded-md border border-border-input bg-surface p-2">
      <div className="flex items-center gap-2 px-1 text-sm text-text-muted">
        {dir && (
          <button
            type="button"
            onClick={() => setDir(data?.parent ?? '')}
            aria-label="Subir a la carpeta anterior"
            className="rounded-full p-1 hover:bg-surface-raised hover:text-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus"
          >
            <ChevronLeft aria-hidden className="h-4 w-4" />
          </button>
        )}
        <span className="truncate">{dir ? crumbs.slice(-3).join(' › ') : 'Carpetas del servidor'}</span>
      </div>
      {!dir && <p className="px-1 text-xs text-text-muted">
        Las rutas /host corresponden a las carpetas de tu servidor (por ejemplo, /host/home o /host/mnt).
        El montaje /media sigue disponible. Solo se muestran carpetas accesibles con los permisos del servidor.
      </p>}
      {error && <p className="px-1 text-sm text-danger">{error}</p>}
      {!data && !error && <p className="px-1 text-sm text-text-muted">Leyendo…</p>}
      {data && data.folders.length === 0 && <p className="px-1 text-sm text-text-muted">No hay subcarpetas accesibles. Comprueba el montaje y los permisos de lectura en Docker.</p>}
      {data && data.folders.length > 0 && (
        <ul role="listbox" aria-label="Carpetas" className="flex max-h-64 flex-col overflow-y-auto">
          {data.folders.map((f) => {
            const taken = used.has(f.path);
            const selected = value === f.path;
            return (
              <li key={f.path} className="flex items-center gap-1">
                <button
                  type="button"
                  role="option"
                  aria-selected={selected}
                  disabled={taken}
                  onClick={() => onChange(f.path, f.name)}
                  className={`flex min-w-0 flex-1 items-center gap-2 rounded-sm px-2 py-2 text-left text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus disabled:opacity-50 ${
                    selected ? 'bg-surface-overlay font-semibold text-text' : 'text-text hover:bg-surface-raised'
                  }`}
                >
                  {f.kind === 'remote_fuse' ? (
                    <Cloud aria-label="Montaje remoto" className="h-4 w-4 shrink-0 text-accent" />
                  ) : (
                    <Folder aria-hidden className="h-4 w-4 shrink-0 text-text-muted" />
                  )}
                  <span className="truncate">{f.name}</span>
                  <span className="ml-auto max-w-[45%] truncate text-xs text-text-subtle" title={f.path}>{f.path}</span>
                  {taken && <span className="ml-auto shrink-0 text-xs text-text-subtle">ya en una biblioteca</span>}
                </button>
                <button
                  type="button"
                  onClick={() => setDir(f.path)}
                  aria-label={`Abrir ${f.name}`}
                  className="rounded-full p-2 text-text-muted hover:bg-surface-raised hover:text-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus"
                >
                  <ChevronRight aria-hidden className="h-4 w-4" />
                </button>
              </li>
            );
          })}
        </ul>
      )}
      {data?.truncated && (
        <p className="px-1 text-xs text-status-unavailable">
          Lista incompleta: esta carpeta tiene más de 2000 subcarpetas y solo se muestran las 2000 primeras. Para una biblioteca
          elige la carpeta que las contiene, no una de ellas.
        </p>
      )}
      {value && <p className="truncate px-1 text-xs text-text-muted">Elegida: {value}</p>}
    </div>
  );
}
