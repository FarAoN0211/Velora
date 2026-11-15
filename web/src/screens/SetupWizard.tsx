import { QuickLogin } from '../components/quickLogin';
import { isTv } from '../lib/tv';
// S01 · Asistente inicial (replica/design/screens.md). Solo existe mientras no hay administrador.
import { useState, type FormEvent } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { Check } from 'lucide-react';
import { api, ApiError, type User } from '../lib/api';
import { useSession } from '../lib/session';
import { Banner, Button, Card, TextField } from '../components/ui';

const STEPS = ['Idioma', 'Servidor', 'Administrador', 'Bibliotecas', 'Resumen'] as const;

export function SetupWizard() {
  const { refresh, setUser } = useSession();
  const navigate = useNavigate();
  const [params]=useSearchParams();
  const [phone,setPhone]=useState(false);
  const [step, setStep] = useState(0);
  const [setupCode, setSetupCode] = useState('');
  const [serverName, setServerName] = useState('VELORA');
  const [username, setUsername] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [password, setPassword] = useState('');
  const [password2, setPassword2] = useState('');
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  function validateAdmin(): boolean {
    const e: Record<string, string> = {};
    if (setupCode.replace(/[-\s]/g, '').length !== 12) e.setupCode = 'El código tiene 12 caracteres (por ejemplo ABCD-EFGH-JKLM).';
    if (!/^[A-Za-z0-9._-]{3,32}$/.test(username)) e.username = 'Entre 3 y 32 caracteres: letras, números, punto, guion o guion bajo.';
    else if (['admin', 'root'].includes(username.toLowerCase())) e.username = 'Por seguridad, elige otro nombre que no sea «admin» ni «root».';
    if (!displayName.trim()) e.displayName = 'Escribe el nombre que se verá en los perfiles.';
    if ([...password].length < 10) e.password = 'Usa al menos 10 caracteres.';
    if (password2 !== password) e.password2 = 'Las contraseñas no coinciden.';
    setErrors(e);
    return Object.keys(e).length === 0;
  }

  function next(ev: FormEvent) {
    ev.preventDefault();
    if (step === 1 && !serverName.trim()) {
      setErrors({ serverName: 'Pon un nombre al servidor.' });
      return;
    }
    if (step === 2 && !validateAdmin()) return;
    setErrors({});
    setStep((s) => Math.min(s + 1, STEPS.length - 1));
  }

  async function finish() {
    setBusy(true);
    setSubmitError(null);
    try {
      const res = await api<{ user: User }>('POST', '/api/v1/setup', {
        setup_code: setupCode,
        server_name: serverName.trim(),
        language: 'es-ES',
        username,
        display_name: displayName.trim(),
        password,
      });
      setUser(res.user);
      await refresh();
      navigate(/^\d{6}$/.test(params.get('codigo')??'')?`/vincular?codigo=${params.get('codigo')}`:'/cuenta/seguridad', { replace: true });
    } catch (e) {
      setSubmitError(e instanceof ApiError ? e.message : 'No se ha podido completar la configuración.');
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="mx-auto flex min-h-full max-w-xl flex-col justify-center gap-6 p-6">
      {isTv&&<><Button variant="secondary" onClick={()=>setPhone(p=>!p)}>{phone?'Configurar con el mando':'Configurar desde el móvil con QR'}</Button>{phone&&<QuickLogin setup/>}</>}
      <header>
        <p className="text-sm font-semibold text-accent">Primer arranque</p>
        <h1 className="text-xl">Vamos a preparar tu servidor</h1>
      </header>

      <ol className="flex flex-wrap gap-2" aria-label="Pasos del asistente">
        {STEPS.map((label, i) => (
          <li
            key={label}
            aria-current={i === step ? 'step' : undefined}
            className={`flex items-center gap-1 rounded-full px-3 py-1 text-xs ${
              i === step ? 'bg-accent text-on-accent' : i < step ? 'bg-surface-raised text-text' : 'bg-surface text-text-subtle'
            }`}
          >
            {i < step && <Check aria-hidden className="h-3 w-3" />}
            {i + 1}. {label}
          </li>
        ))}
      </ol>

      <Card>
        <form onSubmit={next} className="flex flex-col gap-4" noValidate>
          {step === 0 && (
            <>
              <h2 className="text-lg">Idioma</h2>
              <fieldset className="flex flex-col gap-2">
                <legend className="sr-only">Idioma del servidor</legend>
                <label className="flex items-center gap-2">
                  <input type="radio" name="lang" defaultChecked /> Español (España)
                </label>
                <p className="text-sm text-text-muted">Catalán, euskera, gallego e inglés llegarán más adelante.</p>
              </fieldset>
            </>
          )}
          {step === 1 && (
            <>
              <h2 className="text-lg">Nombre del servidor</h2>
              <TextField
                label="Nombre"
                value={serverName}
                maxLength={60}
                onChange={(e) => setServerName(e.target.value)}
                hint="Así aparecerá en las aplicaciones de tu casa."
                error={errors.serverName}
                autoFocus
              />
            </>
          )}
          {step === 2 && (
            <>
              <h2 className="text-lg">Cuenta de administración</h2>
              <p className="text-sm text-text-muted">
                Es una cuenta local de este servidor: no se crea en ningún servicio externo.
              </p>
              <TextField
                label="Código de instalación"
                value={setupCode}
                autoComplete="off"
                spellCheck={false}
                onChange={(e) => setSetupCode(e.target.value.toUpperCase())}
                hint="Aparece en la consola del servidor al arrancar y en /config/secrets/setup-code."
                error={errors.setupCode}
                autoFocus
              />
              <TextField label="Usuario" value={username} autoComplete="username" onChange={(e) => setUsername(e.target.value)} error={errors.username} />
              <TextField label="Nombre visible" value={displayName} maxLength={60} onChange={(e) => setDisplayName(e.target.value)} error={errors.displayName} />
              <TextField label="Contraseña" type="password" value={password} autoComplete="new-password" onChange={(e) => setPassword(e.target.value)} hint="Al menos 10 caracteres." error={errors.password} />
              <TextField label="Repite la contraseña" type="password" value={password2} autoComplete="new-password" onChange={(e) => setPassword2(e.target.value)} error={errors.password2} />
            </>
          )}
          {step === 3 && (
            <>
              <h2 className="text-lg">Bibliotecas</h2>
              <Banner>
                Podrás añadir tus carpetas de películas, series y música en cuanto esté disponible la gestión de bibliotecas (etapa E1).
                Los archivos se leerán siempre en modo de solo lectura.
              </Banner>
            </>
          )}
          {step === 4 && (
            <>
              <h2 className="text-lg">Resumen</h2>
              <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
                <dt className="text-text-muted">Idioma</dt>
                <dd>Español (España)</dd>
                <dt className="text-text-muted">Servidor</dt>
                <dd className="break-all">{serverName}</dd>
                <dt className="text-text-muted">Administrador</dt>
                <dd className="break-all">
                  {displayName} ({username})
                </dd>
              </dl>
              {submitError && <Banner tone="error">{submitError}</Banner>}
            </>
          )}

          <div className="mt-2 flex justify-between gap-2">
            <Button type="button" variant="ghost" onClick={() => setStep((s) => Math.max(0, s - 1))} disabled={step === 0 || busy}>
              Atrás
            </Button>
            {step < STEPS.length - 1 ? (
              <Button type="submit">Continuar</Button>
            ) : (
              <Button type="button" onClick={finish} loading={busy}>
                Crear y entrar
              </Button>
            )}
          </div>
        </form>
      </Card>
    </main>
  );
}
