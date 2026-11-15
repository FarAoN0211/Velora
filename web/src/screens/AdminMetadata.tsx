// S16 · Metadatos: la clave de TMDB (antes dentro de Bibliotecas). Sin clave, VELORA usa el nombre de la carpeta y las
// carátulas locales; con ella completa sinopsis, carátulas, clasificación por edad y géneros.
import { useCallback, useEffect, useState, type FormEvent } from 'react';
import { api, ApiError } from '../lib/api';
import { ImageProviderSettings } from '../components/imageProviderSettings';
import { OpenSubtitlesSettings } from '../components/openSubtitlesSettings';
import { AdminTabs } from '../components/shell';
import { Banner, Button, Card, Loading, TextField } from '../components/ui';

export function AdminMetadata() {
  const [tmdb, setTmdb] = useState<boolean | null>(null);
  const [apiKey, setApiKey] = useState('');
  const [notice, setNotice] = useState<{ tone: 'info' | 'error'; text: string } | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      setTmdb((await api<{ configured: boolean }>('GET', '/api/v1/admin/metadata/tmdb')).configured);
    } catch (e) {
      setNotice({ tone: 'error', text: e instanceof ApiError ? e.message : 'No se ha podido cargar el estado de TMDB.' });
    }
  }, []);
  useEffect(() => {
    void load();
  }, [load]);

  async function run(action: () => Promise<unknown>, ok: string) {
    setBusy(true);
    setNotice(null);
    try {
      await action();
      setNotice({ tone: 'info', text: ok });
      await load();
    } catch (e) {
      setNotice({ tone: 'error', text: e instanceof ApiError ? e.message : 'La operación ha fallado.' });
    } finally {
      setBusy(false);
    }
  }

  function saveKey(e: FormEvent) {
    e.preventDefault();
    void run(async () => {
      await api('PUT', '/api/v1/admin/metadata/tmdb', { api_key: apiKey });
      setApiKey('');
    }, 'Clave de TMDB guardada. Vuelve a escanear las bibliotecas (Resumen) para completar fichas, carátulas y géneros.');
  }

  return (
    <div className="flex min-h-full flex-col">
      <main className="mx-auto flex w-full max-w-content flex-col gap-6 p-4 desktop:p-6">
        <AdminTabs />
        <h1 className="text-xl">Metadatos</h1>
        {notice && <Banner tone={notice.tone}>{notice.text}</Banner>}
        {tmdb === null && !notice && <Loading />}
        {tmdb !== null && (
          <Card>
            <form onSubmit={saveKey} className="flex max-w-xl flex-col gap-4">
              <h2 className="text-lg">Fichas y carátulas (TMDB)</h2>
              <p className="text-sm text-text-muted">
                {tmdb
                  ? 'TMDB está configurado.'
                  : 'Opcional. Sin clave, VELORA usa el nombre de la carpeta y las carátulas locales (poster.jpg). Con una clave gratuita de TMDB, completa sinopsis, carátulas, clasificación por edad y géneros.'}
              </p>
              <TextField
                label="Clave de la API o token de lectura de TMDB"
                type="password"
                autoComplete="off"
                value={apiKey}
                onChange={(e) => setApiKey(e.target.value)}
                hint="Se guarda en el servidor (fichero 0600) y nunca se muestra de nuevo."
              />
              <div className="flex gap-2">
                <Button type="submit" disabled={!apiKey.trim() || busy}>
                  Comprobar y guardar
                </Button>
                {tmdb && (
                  <Button
                    type="button"
                    variant="ghost"
                    disabled={busy}
                    onClick={() => void run(() => api('DELETE', '/api/v1/admin/metadata/tmdb'), 'Clave de TMDB quitada.')}
                  >
                    Quitar clave
                  </Button>
                )}
              </div>
              <p className="text-xs text-text-subtle">Este producto usa la API de TMDB, pero no está respaldado ni certificado por TMDB.</p>
            </form>
          </Card>
        )}
        <ImageProviderSettings />
        <OpenSubtitlesSettings />
      </main>
    </div>
  );
}
