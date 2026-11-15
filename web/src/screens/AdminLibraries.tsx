import { MarkerScan } from '../components/markerScan';
// S15 · Bibliotecas (E1.1): nombre y tipo libres, orden, «Mostrar en el menú» y varias carpetas (TMDB: Metadatos).
// Ocultar del menú no es quitar el acceso: los permisos se gestionan en Usuarios. Las carpetas se eligen de las montadas.
import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react';
import { ArrowDown, ArrowUp, Cloud, CloudOff, Eye, EyeOff, FolderPlus, Pencil, RefreshCw, Trash2, X } from 'lucide-react';
import { api, ApiError } from '../lib/api';
import { invalidateMenu, KIND_LABEL, type Library, type MenuLibrary } from '../lib/catalog';
import { AdminTabs } from '../components/shell';
import { FolderPicker } from '../components/folderPicker';
import { ProgressBar, ScanStatus, scanActive, scanPercent, useScans, type ScanJob } from '../components/scanStatus';
import { Banner, Button, Card, Loading, TextField } from '../components/ui';
import { Choice } from '../components/choice';

const STATUS: Record<Library['status'], string> = {
  available: 'Disponible',
  pending_sync: 'Pendiente de escanear',
  unavailable: 'Inaccesible',
};

const selectClass = 'h-10 rounded-md border border-border-input bg-surface px-2 font-normal text-text';

export function AdminLibraries() {
  const [libs, setLibs] = useState<Library[] | null>(null);
  const [name, setName] = useState('');
  const [kind, setKind] = useState<MenuLibrary['kind']>('movies');
  const [path, setPath] = useState('');
  const [editing, setEditing] = useState<{ id: string; name: string } | null>(null);
  const [addingPath, setAddingPath] = useState<{ id: string; path: string } | null>(null);
  const [notice, setNotice] = useState<{ tone: 'info' | 'error'; text: string } | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      const l = await api<{ libraries: Library[] }>('GET', '/api/v1/libraries');
      setLibs(l.libraries ?? []);
    } catch (e) {
      setNotice({ tone: 'error', text: e instanceof ApiError ? e.message : 'No se han podido cargar las bibliotecas.' });
    }
  }, []);
  useEffect(() => {
    void load();
  }, [load]);
  const { scans, refresh: refreshScans } = useScans(() => void load());

  async function scan(id?: string) {
    setNotice(null);
    try {
      if (id) {
        await api('POST', `/api/v1/libraries/${id}/scan`);
      } else {
        const r = await api<{ started: number; already: number }>('POST', '/api/v1/admin/libraries/scan');
        setNotice({
          tone: 'info',
          text: `${r.started} ${r.started === 1 ? 'escaneo iniciado' : 'escaneos iniciados'}${r.already ? ` · ${r.already} ya en marcha` : ''}. Se hacen de uno en uno.`,
        });
      }
    } catch (e) {
      setNotice({ tone: 'error', text: e instanceof ApiError ? e.message : 'No se ha podido iniciar el escaneo.' });
    } finally {
      void refreshScans();
    }
  }

  async function run(action: () => Promise<unknown>, ok: string) {
    setBusy(true);
    setNotice(null);
    try {
      await action();
      setNotice({ tone: 'info', text: ok });
      invalidateMenu();
      await load();
    } catch (e) {
      setNotice({ tone: 'error', text: e instanceof ApiError ? e.message : 'La operación ha fallado.' });
    } finally {
      setBusy(false);
    }
  }

  const active = libs?.filter((l) => !l.retired_at) ?? null;
  const retired = libs?.filter((l) => l.retired_at) ?? [];
  const usedPaths = new Set(libs?.flatMap((l) => [...l.paths, ...(l.detached ?? [])].map((p) => p.path)) ?? []);

  function addLibrary(e: FormEvent) {
    e.preventDefault();
    void run(async () => {
      await api('POST', '/api/v1/libraries', { name: name.trim(), kind, path });
      setName('');
      setPath('');
    }, 'Biblioteca añadida. Nadie salvo los administradores la verá hasta que des acceso en Usuarios.');
  }

  function move(index: number, delta: number) {
    if (!active) return;
    const ids = active.map((l) => l.id);
    const [id] = ids.splice(index, 1);
    ids.splice(index + delta, 0, id);
    void run(() => api('PUT', '/api/v1/admin/library-order', { ids }), 'Orden guardado.');
  }

  return (
    <div className="flex min-h-full flex-col">
      <main className="mx-auto flex w-full max-w-content flex-col gap-6 p-4 desktop:p-6">
        <AdminTabs />
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h1 className="text-xl">Bibliotecas</h1>
          {active && active.length > 0 && (
            <Button variant="secondary" onClick={() => void scan()} disabled={active.every((l) => scanActive(scans[l.id]))}>
              <RefreshCw aria-hidden className="h-4 w-4" /> Escanear todas
            </Button>
          )}
        </div>
        {active && active.some((l) => scanActive(scans[l.id])) && (
          <OverallProgress libs={active.map((l) => ({ id: l.id, name: l.name, items: l.items, job: scans[l.id] }))} />
        )}
        <MarkerScan />
        {notice && <Banner tone={notice.tone}>{notice.text}</Banner>}
        {!libs && <Loading />}
        {active && active.length > 0 && (
          <ul className="flex flex-col gap-3">
            {active.map((l, i) => (
              <li key={l.id} className="flex flex-col gap-3 rounded-lg bg-surface p-4">
                <div className="flex flex-wrap items-center gap-3">
                  <div className="flex flex-col">
                    <Button variant="ghost" size="sm" aria-label={`Subir ${l.name}`} disabled={busy || i === 0} onClick={() => move(i, -1)}>
                      <ArrowUp aria-hidden className="h-4 w-4" />
                    </Button>
                    <Button variant="ghost" size="sm" aria-label={`Bajar ${l.name}`} disabled={busy || i === active.length - 1} onClick={() => move(i, 1)}>
                      <ArrowDown aria-hidden className="h-4 w-4" />
                    </Button>
                  </div>
                  {editing?.id === l.id ? (
                    <form
                      className="flex min-w-0 flex-1 items-end gap-2"
                      onSubmit={(e) => {
                        e.preventDefault();
                        void run(async () => {
                          await api('PATCH', `/api/v1/libraries/${l.id}`, { name: editing.name.trim() });
                          setEditing(null);
                        }, 'Nombre cambiado. El contenido y los permisos no cambian.');
                      }}
                    >
                      <TextField label="Nombre" value={editing.name} maxLength={60} autoFocus onChange={(e) => setEditing({ id: l.id, name: e.target.value })} />
                      <Button type="submit" size="sm" disabled={!editing.name.trim() || busy}>
                        Guardar
                      </Button>
                      <Button type="button" variant="ghost" size="sm" aria-label="Cancelar" onClick={() => setEditing(null)}>
                        <X aria-hidden className="h-4 w-4" />
                      </Button>
                    </form>
                  ) : (
                    <div className="min-w-0 flex-1">
                      <p className="font-semibold">
                        {l.name} <span className="text-sm font-normal text-text-muted">· {KIND_LABEL[l.kind]}</span>
                      </p>
                      <p className="text-sm text-text-muted">
                        {l.items} títulos · {STATUS[l.status]}
                        {l.last_scan_at &&
                          ` · ${new Date(l.last_scan_at).toLocaleString('es-ES', { dateStyle: 'short', timeStyle: 'short' })}`}
                      </p>
                    </div>
                  )}
                  <Button
                    variant="ghost"
                    size="sm"
                    aria-pressed={l.show_in_menu}
                    disabled={busy}
                    onClick={() =>
                      void run(
                        () => api('PATCH', `/api/v1/libraries/${l.id}`, { show_in_menu: !l.show_in_menu }),
                        l.show_in_menu ? 'Oculta del menú. Quien tenga acceso la sigue viendo en Inicio y en la búsqueda.' : 'Visible en el menú.',
                      )
                    }
                  >
                    {l.show_in_menu ? <Eye aria-hidden className="h-4 w-4" /> : <EyeOff aria-hidden className="h-4 w-4" />}
                    {l.show_in_menu ? 'En el menú' : 'Oculta del menú'}
                  </Button>
                  <Button variant="ghost" size="sm" aria-label={`Renombrar ${l.name}`} disabled={busy} onClick={() => setEditing({ id: l.id, name: l.name })}>
                    <Pencil aria-hidden className="h-4 w-4" />
                  </Button>
                  <Button variant="secondary" size="sm" disabled={busy || scanActive(scans[l.id])} onClick={() => void scan(l.id)}>
                    <RefreshCw aria-hidden className={`h-4 w-4 ${scans[l.id]?.state === 'running' ? 'animate-spin motion-reduce:animate-none' : ''}`} />
                    {scanActive(scans[l.id]) ? 'Escaneando…' : 'Escanear'}
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    disabled={busy}
                    aria-label={`Retirar ${l.name}`}
                    onClick={() => {
                      if (window.confirm(`¿Retirar «${l.name}»? Dejará de verse para todos, pero fichas, permisos e historial se conservan y podrás recuperarla.`))
                        void run(() => api('DELETE', `/api/v1/libraries/${l.id}`), 'Biblioteca retirada. Puedes recuperarla más abajo.');
                    }}
                  >
                    <Trash2 aria-hidden className="h-4 w-4" />
                  </Button>
                </div>
                <ScanStatus job={scans[l.id]} name={l.name} />
                <ul className="flex flex-col gap-1 pl-12 text-sm">
                  {l.paths.map((p) => (
                    <li key={p.id} className="flex items-center gap-2 text-text-muted">
                      {p.kind === 'remote_fuse' && <Cloud aria-label="Remota" className="h-4 w-4 text-status-remote" />}
                      {p.status === 'unavailable' && <CloudOff aria-label="Inaccesible" className="h-4 w-4 text-status-unavailable" />}
                      <span className="truncate">{p.path}</span>
                      {p.status === 'unavailable' && (
                        <span className="shrink-0 text-xs text-status-unavailable">
                          {p.kind === 'remote_fuse'
                            ? 'no responde: comprueba rclone/Teldrive; si se volvió a montar, reinicia VELORA. No se ha borrado nada.'
                            : 'no responde: comprueba el disco. No se ha borrado nada.'}
                        </span>
                      )}
                      {l.paths.length > 1 && (
                        <Button
                          variant="ghost"
                          size="sm"
                          aria-label={`Quitar la carpeta ${p.path}`}
                          disabled={busy}
                          onClick={() => {
                            if (window.confirm(`¿Quitar ${p.path} de «${l.name}»? Su contenido dejará de verse, pero fichas y progreso se conservan y podrás recuperarla.`))
                              void run(() => api('DELETE', `/api/v1/libraries/${l.id}/paths/${p.id}`), 'Carpeta quitada. Puedes recuperarla más abajo.');
                          }}
                        >
                          <X aria-hidden className="h-4 w-4" />
                        </Button>
                      )}
                    </li>
                  ))}
                  {l.detached?.map((p) => (
                    <li key={p.id} className="flex flex-wrap items-center gap-2 text-text-subtle">
                      <span className="truncate line-through">{p.path}</span>
                      <span>(quitada; fichas y progreso conservados)</span>
                      <Button
                        variant="secondary"
                        size="sm"
                        disabled={busy}
                        onClick={() => void run(() => api('POST', `/api/v1/libraries/${l.id}/paths/${p.id}/restore`), 'Carpeta recuperada; se volverá a escanear.')}
                      >
                        Recuperar
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        disabled={busy}
                        onClick={() => {
                          const answer = window.prompt(
                            `Esto borra para siempre las fichas, el historial y el progreso de ${p.path} en VELORA (los archivos no se tocan). Escribe BORRAR para confirmar.`,
                          );
                          if (answer === 'BORRAR')
                            void run(
                              () => api('POST', `/api/v1/libraries/${l.id}/paths/${p.id}/purge`, { confirm: 'BORRAR' }),
                              'Fichas de la carpeta borradas definitivamente.',
                            );
                        }}
                      >
                        Borrar fichas
                      </Button>
                    </li>
                  ))}
                  <li>
                    {addingPath?.id === l.id ? (
                      <form
                        className="flex items-start gap-2"
                        onSubmit={(e) => {
                          e.preventDefault();
                          void run(async () => {
                            await api('POST', `/api/v1/libraries/${l.id}/paths`, { path: addingPath.path });
                            setAddingPath(null);
                          }, 'Carpeta añadida; se escaneará ahora.');
                        }}
                      >
                        <div className="min-w-0 flex-1">
                          <FolderPicker value={addingPath.path} used={usedPaths} onChange={(p) => setAddingPath({ id: l.id, path: p })} />
                        </div>
                        <Button type="submit" size="sm" disabled={!addingPath.path || busy}>
                          Añadir
                        </Button>
                        <Button type="button" variant="ghost" size="sm" aria-label="Cancelar" onClick={() => setAddingPath(null)}>
                          <X aria-hidden className="h-4 w-4" />
                        </Button>
                      </form>
                    ) : (
                      <Button variant="ghost" size="sm" disabled={busy} onClick={() => setAddingPath({ id: l.id, path: '' })}>
                        <FolderPlus aria-hidden className="h-4 w-4" /> Añadir carpeta
                      </Button>
                    )}
                  </li>
                </ul>
              </li>
            ))}
          </ul>
        )}

        {retired.length > 0 && (
          <section aria-labelledby="retiradas" className="flex flex-col gap-2">
            <h2 id="retiradas" className="text-lg">
              Bibliotecas retiradas
            </h2>
            <p className="text-sm text-text-muted">
              No las ve nadie. Al recuperarlas vuelven con sus permisos, pero ocultas del menú hasta que decidas mostrarlas.
            </p>
            {retired.map((l) => (
              <div key={l.id} className="flex flex-wrap items-center gap-3 rounded-lg bg-surface p-4">
                <p className="min-w-0 flex-1 text-text-muted">
                  {l.name} · {l.items} títulos
                </p>
                <Button
                  variant="secondary"
                  size="sm"
                  disabled={busy}
                  onClick={() => void run(() => api('POST', `/api/v1/libraries/${l.id}/restore`), 'Biblioteca recuperada (oculta del menú).')}
                >
                  Recuperar
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={busy}
                  onClick={() => {
                    const answer = window.prompt(
                      `Esto borra para siempre «${l.name}» con sus fichas, permisos, historial y progreso en VELORA (los archivos no se tocan). Escribe BORRAR para confirmar.`,
                    );
                    if (answer === 'BORRAR')
                      void run(() => api('POST', `/api/v1/libraries/${l.id}/purge`, { confirm: 'BORRAR' }), 'Biblioteca borrada definitivamente.');
                  }}
                >
                  Borrar definitivamente
                </Button>
              </div>
            ))}
          </section>
        )}

        <Card>
          <form onSubmit={addLibrary} className="flex flex-col gap-4">
            <h2 className="text-lg">Añadir biblioteca</h2>
            <p className="text-sm text-text-muted">
              El nombre es libre (por ejemplo, «Películas de animación» o «Cine en la nube»). Solo se listan las carpetas montadas
              en VELORA, siempre en solo lectura. Una biblioteca nueva no se concede a ningún usuario: lo decides en Usuarios.
            </p>
            <TextField label="Nombre" value={name} maxLength={60} onChange={(e) => setName(e.target.value)} />
            <label className="flex flex-col gap-1 text-sm font-semibold">
              Tipo
              <Choice aria-label="Tipo" value={kind} onChange={(e) => setKind(e.target.value as MenuLibrary['kind'])} className={selectClass}>
                <option value="movies">Películas</option>
                <option value="tv">Series</option>
              </Choice>
              <span className="text-xs font-normal text-text-muted">
                VELORA procesa <strong>películas</strong> y <strong>series</strong> (cada serie en su carpeta, con los episodios sueltos o
                en «Season 1», «Season 2»…). Música, audiolibros, vídeos personales y fotos todavía no tienen tipo: no los añadas como películas.
              </span>
            </label>
            <div className="flex flex-col gap-1 text-sm font-semibold">
              Carpeta
              <FolderPicker
                value={path}
                used={usedPaths}
                onChange={(p, folderName) => {
                  setPath(p);
                  if (!name) setName(folderName);
                }}
              />
              <span className="text-xs font-normal text-text-muted">
                Pulsa › para entrar en una carpeta (por ejemplo, la de Teldrive). La nube indica un montaje remoto: se escanea con
                más calma y, si deja de responder, no se borra nada.
              </span>
            </div>
            <Button type="submit" disabled={!name.trim() || !path || busy} loading={busy}>
              Añadir y escanear
            </Button>
          </form>
        </Card>
      </main>
    </div>
  );
}

/** Progreso conjunto de los escaneos en marcha. No promedia porcentajes: pondera cada biblioteca por su trabajo (los
 * archivos que está analizando o, si aún no se sabe, sus títulos), y cuenta como hechas las que ya terminaron en esta
 * tanda. Mientras alguna esté buscando archivos o en cola sin tamaño conocido, la estimación se indica como tal. */
function OverallProgress({ libs }: { libs: { id: string; name: string; items: number; job?: ScanJob }[] }) {
  const batch = useRef<Map<string, number>>(new Map());
  const activeNow = libs.filter((l) => scanActive(l.job));
  for (const l of activeNow) {
    const p = l.job?.progress;
    const weight = Math.max(1, p && p.phase !== 'listing' && p.phase !== 'metadata' ? p.total : l.items);
    batch.current.set(l.id, Math.max(batch.current.get(l.id) ?? 0, weight));
  }
  let total = 0;
  let done = 0;
  for (const [id, weight] of batch.current) {
    const l = libs.find((x) => x.id === id);
    total += weight;
    if (!l || !scanActive(l.job)) done += weight; // terminada en esta tanda
    else if (l.job?.state === 'running') done += weight * ((scanPercent(l.job) ?? 0) / 100);
  }
  const finished = [...batch.current.keys()].filter((id) => !scanActive(libs.find((x) => x.id === id)?.job)).length;
  const running = activeNow.find((l) => l.job?.state === 'running');
  const pct = total ? Math.round((done / total) * 100) : null;
  return (
    <div className="flex flex-col gap-1 rounded-lg bg-surface p-4" aria-live="polite">
      <p className="text-sm text-text">
        Escaneo de bibliotecas: {finished} de {batch.current.size} completadas
        {running ? ` · ahora: ${running.name}` : ' · esperando turno'} · {pct ?? 0} % (estimado por tamaño de cada biblioteca)
      </p>
      <ProgressBar percent={pct} label="Progreso conjunto del escaneo" />
    </div>
  );
}
