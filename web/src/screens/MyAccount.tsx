// «Mi cuenta» (cuentas principales) y «Mi perfil» (miembros de un hogar). Fase 5 de replica/PLAN_HOGARES_Y_PIN.md:
// PIN de 4 dígitos con código de recuperación, cuándo se vuelve a pedir, historial guardado aparte tras un
// restablecimiento y «Crear mi hogar» si el administrador lo permite.
import { useCallback, useEffect, useState, type ReactNode } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { api, ApiError } from '../lib/api';
import { useSession } from '../lib/session';
import { Banner, Button, Card, TextField } from '../components/ui';
import { NewPinPad, PinPad, RecoverPin, RecoveryCodeNotice } from '../components/pinPad';

interface Seal {
  id: string;
  sealed_at: string;
  expires_at: string;
  items: number;
}

export const LOCK_OPTIONS: { minutes: number; label: string; hint: string }[] = [
  { minutes: 0, label: 'Siempre', hint: 'Al elegir el perfil, al abrir Velora y al volver tras 2 minutos fuera.' },
  { minutes: 15, label: 'Tras 15 minutos sin uso', hint: '' },
  { minutes: 60, label: 'Tras 1 hora sin uso', hint: 'Opción predeterminada.' },
  { minutes: 240, label: 'Tras 4 horas sin uso', hint: '' },
];

const day = (s: string) => new Date(s).toLocaleDateString('es-ES', { dateStyle: 'long' });

type Flow = null | 'set' | 'change' | 'remove' | 'code' | 'forgot';

export function MyAccount() {
  const { user, refresh, setUser } = useSession();
  const navigate = useNavigate();
  const [flow, setFlow] = useState<Flow>(null);
  const [current, setCurrent] = useState<{ current_pin?: string; password?: string } | null>(null);
  const [usePassword, setUsePassword] = useState(false);
  const [password, setPassword] = useState('');
  const [code, setCode] = useState('');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);
  const [seals, setSeals] = useState<Seal[]>([]);
  const [oldCode, setOldCode] = useState('');
  const [confirmHousehold, setConfirmHousehold] = useState(false);

  const loadSeals = useCallback(async () => {
    const r = await api<{ seals: Seal[] }>('GET', '/api/v1/me/history/seals');
    setSeals(r.seals);
  }, []);
  useEffect(() => {
    void loadSeals().catch(() => undefined);
  }, [loadSeals]);

  if (!user) return null;
  const member = !!user.household_id && !user.household_owner;
  const title = member ? 'Mi perfil' : 'Mi cuenta';

  function reset(message = '') {
    setFlow(null);
    setCurrent(null);
    setUsePassword(false);
    setPassword('');
    setError('');
    setNotice(message);
  }
  async function run<T>(action: () => Promise<T>): Promise<T | undefined> {
    setBusy(true);
    setError('');
    try {
      return await action();
    } catch (e) {
      setError(e instanceof ApiError && e.code === 'credencial_incorrecta' ? 'PIN o contraseña incorrectos.' : e instanceof Error ? e.message : 'No se ha podido guardar.');
      setCurrent(null);
      return undefined;
    } finally {
      setBusy(false);
    }
  }

  // Código recién generado: se enseña una sola vez, a pantalla completa.
  if (code)
    return (
      <main className="flex min-h-full items-center justify-center p-6">
        <RecoveryCodeNotice
          code={code}
          name={user.display_name}
          onDone={() => {
            setCode('');
            reset('PIN guardado. Tu código de recuperación anterior, si lo había, ya no vale.');
          }}
        />
      </main>
    );

  // Pasos de los cambios de PIN: primero se comprueba el PIN actual (o la contraseña), luego el nuevo.
  if (flow) {
    const cancel = (
      <Button type="button" variant="ghost" onClick={() => reset()}>
        Cancelar
      </Button>
    );
    let body: ReactNode;
    if (flow === 'forgot')
      body = (
        <RecoverPin
          profile={user}
          onCancel={() => reset()}
          onRecovered={(r) => {
            setUser(r.user);
            setCode(r.recovery_code);
          }}
        />
      );
    else if (flow !== 'set' && !current)
      body = usePassword ? (
        <form
          className="mx-auto flex w-full max-w-sm flex-col gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            setCurrent({ password });
          }}
        >
          <h2 className="text-lg">Confirma con tu contraseña</h2>
          <TextField label="Contraseña de tu cuenta" type="password" autoComplete="current-password" autoFocus value={password} onChange={(e) => setPassword(e.target.value)} />
          {error && <Banner tone="error">{error}</Banner>}
          <Button disabled={!password}>Continuar</Button>
          {cancel}
        </form>
      ) : (
        <PinPad
          title="Escribe tu PIN actual"
          busy={busy}
          error={error}
          allowLegacy
          onComplete={async (pin) => {
            setError('');
            if (flow === 'change') {
              setCurrent({ current_pin: pin });
              return;
            }
            if (flow === 'remove') {
              const ok = await run(() => api('PUT', '/api/v1/me/pin', { current_pin: pin, pin: '' }).then(() => true));
              if (!ok) return false;
              await refresh();
              reset('PIN quitado. Tu perfil ya no está protegido.');
              return;
            }
            const r = await run(() => api<{ recovery_code: string }>('POST', '/api/v1/me/pin/recovery-code', { current_pin: pin }));
            if (r) setCode(r.recovery_code);
            else return false;
          }}
        >
          {flow === 'change' && !user.passwordless && (
            <Button type="button" variant="ghost" onClick={() => setUsePassword(true)}>
              He olvidado mi PIN: usar mi contraseña
            </Button>
          )}
          {flow === 'change' && (
            <Button type="button" variant="ghost" onClick={() => setFlow('forgot')}>
              He olvidado mi PIN: usar mi código de recuperación
            </Button>
          )}
          {cancel}
        </PinPad>
      );
    else if (flow === 'remove' && current?.password !== undefined)
      body = (
        <div className="flex flex-col items-center gap-3">
          <p>¿Quitar el PIN de tu perfil?</p>
          <Button
            disabled={busy}
            onClick={async () => {
              const ok = await run(() => api('PUT', '/api/v1/me/pin', { password: current.password, pin: '' }).then(() => true));
              if (ok) {
                await refresh();
                reset('PIN quitado. Tu perfil ya no está protegido.');
              }
            }}
          >
            Quitar el PIN
          </Button>
          {error && <Banner tone="error">{error}</Banner>}
          {cancel}
        </div>
      );
    else
      body = (
        <NewPinPad
          title={flow === 'set' ? 'Elige tu PIN' : 'PIN nuevo'}
          busy={busy}
          error={error}
          onDone={async (pin) => {
            const r = await run(() => api<{ recovery_code: string }>('PUT', '/api/v1/me/pin', { ...(current ?? {}), pin }));
            if (r) {
              await refresh();
              setCode(r.recovery_code);
            } else if (flow !== 'set') setCurrent(null);
          }}
        >
          {cancel}
        </NewPinPad>
      );
    return <main className="flex min-h-full flex-col items-center justify-center gap-6 p-6">{body}</main>;
  }

  return (
    <main className="mx-auto flex w-full max-w-3xl flex-col gap-5 p-6">
      <h1 className="text-xl">{title}</h1>
      <p className="text-sm text-text-muted">
        {user.display_name}
        {!user.passwordless && <> · usuario <strong>{user.username}</strong></>}
        {member && ' · miembro de un hogar'}
        {user.household_owner && ' · propietario de un hogar'}
      </p>
      {error && <Banner tone="error">{error}</Banner>}
      {notice && <Banner tone="info">{notice}</Banner>}

      <Card>
        <h2 className="mb-2 text-lg">PIN del perfil</h2>
        <p className="mb-4 text-sm text-text-muted">
          Un PIN de 4 dígitos protege tu historial, tus avances y tus listas frente a quien use tu dispositivo o tu hogar. Al
          activarlo verás un código de recuperación: con él eliges un PIN nuevo si lo olvidas, sin pedírselo a nadie.
          {user.household_owner && ' Como propietario, tu PIN protege también la gestión del hogar.'}
        </p>
        {user.has_pin ? (
          <div className="flex flex-wrap gap-3">
            <Button onClick={() => setFlow('change')}>Cambiar PIN</Button>
            <Button variant="secondary" onClick={() => setFlow('code')}>
              Generar un código de recuperación nuevo
            </Button>
            <Button variant="ghost" onClick={() => setFlow('remove')}>
              Quitar el PIN
            </Button>
          </div>
        ) : (
          <Button onClick={() => setFlow('set')}>Activar PIN</Button>
        )}
      </Card>

      {user.has_pin && (
        <Card>
          <h2 className="mb-2 text-lg">Cuándo se vuelve a pedir el PIN</h2>
          <p className="mb-4 text-sm text-text-muted">
            Cuenta el uso real (mando, teclas, toques). Nunca se pide mientras reproduces algo, aunque esté en pausa. Cada
            dispositivo se desbloquea por separado, y al cambiar a otro perfil el tuyo se bloquea en ese dispositivo.
          </p>
          <fieldset className="flex flex-col gap-2">
            <legend className="sr-only">Cuándo se vuelve a pedir el PIN</legend>
            {LOCK_OPTIONS.map((o) => (
              <label key={o.minutes} className="flex items-start gap-3 rounded-md p-2 hover:bg-surface-raised">
                <input
                  type="radio"
                  name="pin-lock"
                  className="mt-1 h-4 w-4 accent-[var(--color-accent)]"
                  checked={user.pin_lock_minutes === o.minutes}
                  disabled={busy}
                  onChange={() =>
                    void run(async () => {
                      await api('PUT', '/api/v1/me/pin-lock', { minutes: o.minutes });
                      await refresh();
                      setNotice('Guardado.');
                    })
                  }
                />
                <span>
                  {o.label}
                  {o.hint && <span className="block text-xs text-text-muted">{o.hint}</span>}
                </span>
              </label>
            ))}
          </fieldset>
        </Card>
      )}

      {seals.length > 0 && (
        <Card>
          <h2 className="mb-2 text-lg">Tu historial anterior está guardado aparte</h2>
          <p className="mb-4 text-sm text-text-muted">
            Alguien restableció el PIN de tu perfil sin tu código, así que tu historial y tus avances se guardaron aparte, donde
            nadie puede verlos. Solo vuelven con el código de recuperación que tenías <strong>antes</strong> del restablecimiento.
            Si no lo recuperas, se elimina en la fecha indicada.
          </p>
          <ul className="mb-4 flex flex-col gap-1 text-sm">
            {seals.map((s) => (
              <li key={s.id}>
                Guardado el {day(s.sealed_at)} · {s.items} {s.items === 1 ? 'título' : 'títulos'} · se elimina el {day(s.expires_at)}
              </li>
            ))}
          </ul>
          <form
            className="flex flex-col gap-3"
            onSubmit={(e) => {
              e.preventDefault();
              void run(async () => {
                const r = await api<{ items: number }>('POST', '/api/v1/me/history/unseal', { recovery_code: oldCode });
                setOldCode('');
                await loadSeals();
                setNotice(`Historial recuperado: ${r.items} ${r.items === 1 ? 'título' : 'títulos'}. Donde habías visto algo después, se conserva el avance más reciente.`);
              });
            }}
          >
            <TextField label="Código de recuperación anterior" autoComplete="off" placeholder="XXXX-XXXX" value={oldCode} onChange={(e) => setOldCode(e.target.value.toUpperCase())} />
            <div>
              <Button disabled={busy || oldCode.replace(/[^0-9A-Z]/g, '').length !== 8}>Recuperar mi historial</Button>
            </div>
          </form>
        </Card>
      )}

      {!user.household_id && user.can_create_household && (
        <Card>
          <h2 className="mb-2 text-lg">Crear mi hogar</h2>
          <p className="mb-4 text-sm text-text-muted">
            Un hogar reúne los perfiles de tu familia: cada uno con su historial, sus avances y, si quiere, su PIN. Tú serás el
            propietario: podrás añadir, renombrar y eliminar miembros y vincular dispositivos. Ver bibliotecas y reproducir
            sigue igual que ahora.
          </p>
          {confirmHousehold ? (
            <div className="flex flex-wrap gap-3">
              <Button
                disabled={busy}
                onClick={() =>
                  void run(async () => {
                    await api('POST', '/api/v1/me/household');
                    await refresh();
                    navigate('/hogar');
                  })
                }
              >
                Sí, crear mi hogar
              </Button>
              <Button variant="ghost" onClick={() => setConfirmHousehold(false)}>
                Cancelar
              </Button>
            </div>
          ) : (
            <Button onClick={() => setConfirmHousehold(true)}>Crear mi hogar</Button>
          )}
        </Card>
      )}

      {member && (
        <p className="text-sm text-text-muted">
          Si olvidas tu PIN y no tienes el código, el propietario del hogar puede restablecerlo. Tu historial quedará guardado
          aparte durante 30 días y solo tu código anterior podrá devolverlo: ni el propietario ni el administrador pueden verlo.
        </p>
      )}
      <div className="flex flex-wrap gap-4 text-sm">
        {user.household_id && <Link to="/hogar" className="text-accent">Mi hogar</Link>}
        {!user.passwordless && <Link to="/cuenta/seguridad" className="text-accent">Contraseña y códigos de acceso</Link>}
        <Link to="/" className="text-accent">Volver a Velora</Link>
      </div>
    </main>
  );
}

