import { QuickLogin } from '../components/quickLogin';
// S02 · Perfiles y acceso (web). El PIN y el emparejamiento por código de las TV llegan en E3.
import { useEffect, useRef, useState, type FormEvent } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { ArrowLeft, Lock } from 'lucide-react';
import { api, ApiError, type Profile, type User } from '../lib/api';
import { useSession } from '../lib/session';
import { Banner, Button, Card, Loading, TextField, initials } from '../components/ui';

export function Login({returnTo='/'}:{returnTo?:string}) {
  const { info, setUser } = useSession();
  const navigate = useNavigate();
  const [profiles, setProfiles] = useState<Profile[] | null>(null);
  const [chosen, setChosen] = useState<Profile | null>(null);
  const [quick,setQuick]=useState(!returnTo.startsWith('/vincular'));
  const [manual, setManual] = useState(false);
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [slow, setSlow] = useState(false);
  const passwordRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    const t = setTimeout(() => setSlow(true), 8000);
    api<{ profiles: Profile[] }>('GET', '/api/v1/auth/profiles')
      .then((r) => setProfiles(r.profiles))
      .catch(() => setProfiles([]))
      .finally(() => clearTimeout(t));
    return () => clearTimeout(t);
  }, []);

  async function submit(ev: FormEvent) {
    ev.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const body = chosen ? { user_id: chosen.id, password } : { username: username.trim(), password };
      const r = await api<{ user: User }>('POST', '/api/v1/auth/login', body);
      setUser(r.user);
      navigate(returnTo, { replace: true });
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'No se ha podido iniciar sesión.');
      setPassword('');
      passwordRef.current?.focus();
    } finally {
      setBusy(false);
    }
  }

  const showForm = chosen !== null || manual || (profiles !== null && profiles.length === 0);

  return (
    <main className="mx-auto flex min-h-full max-w-3xl flex-col justify-center gap-8 p-6">
      <h1 className="text-center text-xl">{showForm ? 'Iniciar sesión' : `¿Quién está viendo ${info?.name ?? 'VELORA'}?`}</h1>

      {quick ? <><QuickLogin/><Button variant="ghost" onClick={()=>setQuick(false)}>Entrar con usuario y contraseña</Button></> : <>
      {profiles === null && <Loading slow={slow} />}

      {!showForm && profiles && profiles.length > 0 && (
        <>
          <ul className="flex flex-wrap justify-center gap-6">
            {profiles.map((p) => (
              <li key={p.id}>
                <button
                  onClick={() => setChosen(p)}
                  className="group flex w-32 flex-col items-center gap-3 rounded-lg p-2 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus"
                >
                  <span
                    aria-hidden
                    className="flex h-24 w-24 items-center justify-center rounded-lg bg-surface-raised text-xl text-text transition-transform duration-fast group-hover:scale-105 motion-reduce:transform-none"
                  >
                    {initials(p.display_name)}
                  </span>
                  <span className="flex items-center gap-1 text-sm text-text">
                    {p.display_name}
                    {p.has_pin && <Lock aria-label="con PIN" className="h-3.5 w-3.5 text-text-muted" />}
                  </span>
                </button>
              </li>
            ))}
          </ul>
          <div className="text-center">
            <Button variant="ghost" onClick={() => setManual(true)}>
              Iniciar sesión manualmente
            </Button>
          </div>
        </>
      )}

      {showForm && (
        <Card className="mx-auto w-full max-w-sm">
          <form onSubmit={submit} className="flex flex-col gap-4">
            {chosen ? (
              <p className="text-center text-base">
                Hola, <strong>{chosen.display_name}</strong>
              </p>
            ) : (
              <TextField label="Usuario" value={username} autoComplete="username" onChange={(e) => setUsername(e.target.value)} autoFocus />
            )}
            <TextField
              ref={passwordRef}
              label="Contraseña"
              type="password"
              value={password}
              autoComplete="current-password"
              onChange={(e) => setPassword(e.target.value)}
              autoFocus={chosen !== null}
            />
            {error && <Banner tone="error">{error}</Banner>}
            <Button type="submit" loading={busy} disabled={!password || (!chosen && !username.trim())}>
              Entrar
            </Button>
            {(chosen || manual) && profiles && profiles.length > 0 && (
              <Button
                type="button"
                variant="ghost"
                onClick={() => {
                  setChosen(null);
                  setManual(false);
                  setError(null);
                  setPassword('');
                }}
              >
                <ArrowLeft aria-hidden className="h-4 w-4" /> Volver a los perfiles
              </Button>
            )}
          </form>
        </Card>
      )}
      <div className="flex flex-wrap justify-center gap-4"><Button variant="ghost" onClick={()=>setQuick(true)}>Entrar con QR o código</Button><Link to="/recuperar" className="self-center text-sm text-accent">He olvidado mi contraseña</Link></div>
      </>}
    </main>
  );
}
