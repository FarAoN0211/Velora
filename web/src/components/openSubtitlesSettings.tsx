import { useEffect, useState, type FormEvent } from 'react';
import { api, ApiError } from '../lib/api';
import { Banner, Button, Card, TextField } from './ui';

export function OpenSubtitlesSettings() {
  const [configured, setConfigured] = useState(false);
  const [enabled, setEnabled] = useState(false);
  const [key, setKey] = useState('');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [languages, setLanguages] = useState('');
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState('');
  const [error, setError] = useState(false);
  const path = '/api/v1/admin/metadata/opensubtitles';
  async function load() {
    const r = await api<{configured: boolean; enabled: boolean; username: string; languages: string}>('GET', path);
    setConfigured(r.configured); setEnabled(r.enabled); setUsername(r.username); setLanguages(r.languages);
  }
  useEffect(() => { void load().catch(() => { setError(true); setMessage('No se pudo consultar la configuración de OpenSubtitles.'); }); }, []);
  async function save(e: FormEvent) {
    e.preventDefault(); setBusy(true); setMessage('');
    try {
      await api('PUT', path, {enabled, api_key: key, username, password, languages});
      setKey(''); setPassword(''); await load(); setError(false); setMessage('OpenSubtitles comprobado y guardado para todas las bibliotecas.');
    } catch (e) { setError(true); setMessage(e instanceof ApiError ? e.message : 'No se pudo guardar OpenSubtitles.'); }
    finally { setBusy(false); }
  }
  return <Card><form onSubmit={save} className="flex max-w-xl flex-col gap-4">
    <h2 className="text-lg">Subtítulos · OpenSubtitles</h2>
    <p className="text-sm text-text-muted">Configuración global para todas las bibliotecas. Velora prioriza los subtítulos del archivo y los que estén junto al vídeo. Si no hay ninguno, busca alternativas en OpenSubtitles.</p>
    <p className="text-sm text-text-muted">Usa la API actual de <a href="https://www.opensubtitles.com/consumers" target="_blank" rel="noreferrer" className="underline">OpenSubtitles.com</a>. La cuenta y la clave de API tienen sus propias cuotas de descarga.</p>
    {message && <Banner tone={error ? 'error' : 'info'}>{message}</Banner>}
    <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={enabled} onChange={e => setEnabled(e.target.checked)} /> Buscar cuando no haya subtítulos locales</label>
    <TextField label="Clave API de OpenSubtitles" type="password" autoComplete="off" value={key} onChange={e => setKey(e.target.value)} hint={configured ? 'Ya hay una clave guardada. Déjala vacía para conservarla.' : 'Se guarda solo en el servidor y no vuelve a mostrarse.'} />
    <TextField label="Usuario (opcional)" autoComplete="off" value={username} onChange={e => setUsername(e.target.value)} hint="Añade tu cuenta si la necesita tu plan de descarga." />
    <TextField label="Contraseña" type="password" autoComplete="new-password" value={password} onChange={e => setPassword(e.target.value)} hint="Déjala vacía para conservar la de la misma cuenta." />
    <TextField label="Idiomas de búsqueda" value={languages} onChange={e => setLanguages(e.target.value)} hint="Códigos separados por comas, por ejemplo es,en,fr. Vacío: todos los idiomas que devuelva el proveedor." />
    <div className="flex gap-2"><Button type="submit" disabled={busy || (!configured && !key.trim())}>Comprobar y guardar</Button>
      {configured && <Button type="button" variant="ghost" disabled={busy} onClick={() => { setBusy(true); void api('DELETE', path).then(load).then(() => { setError(false); setMessage('Configuración de OpenSubtitles eliminada.'); }).catch(() => { setError(true); setMessage('No se pudo quitar la configuración.'); }).finally(() => setBusy(false)); }}>Quitar configuración</Button>}
    </div>
    <p className="text-xs text-text-subtle">La búsqueda no descarga subtítulos ni consume una descarga hasta que eliges una pista. La sincronización depende de la edición del vídeo; el menú muestra la versión publicada.</p>
  </form></Card>;
}
