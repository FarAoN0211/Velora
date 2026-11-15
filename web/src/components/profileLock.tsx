// Pantalla de perfil bloqueado (fase 5 de replica/PLAN_HOGARES_Y_PIN.md). La decide el servidor (/me «locked» o una
// respuesta 423): aquí solo se escribe el PIN, se recupera con el código o se cambia de perfil.
import { useState, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { Lock } from 'lucide-react';
import { api, ApiError, type PinFailures, type User } from '../lib/api';
import { useSession } from '../lib/session';
import { Button } from './ui';
import { PinPad, RecoverPin, RecoveryCodeNotice, pinFailuresText } from './pinPad';

export function ProfileLock() {
  const { user, setUser, setPinNotice, logout } = useSession();
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [mode, setMode] = useState<'pin' | 'recover'>('pin');
  const [newCode, setNewCode] = useState<{ code: string; user: User } | null>(null);
  if (!user) return null;

  if (newCode)
    return (
      <LockFrame>
        <RecoveryCodeNotice code={newCode.code} name={newCode.user.display_name} onDone={() => setUser(newCode.user)} />
      </LockFrame>
    );
  if (mode === 'recover')
    return (
      <LockFrame>
        <RecoverPin
          profile={user}
          onCancel={() => setMode('pin')}
          onRecovered={(r) => {
            if (r.pin_failures) setPinNotice(r.pin_failures);
            setNewCode({ code: r.recovery_code, user: r.user });
          }}
        />
      </LockFrame>
    );
  return (
    <LockFrame>
      <span aria-hidden className="flex h-14 w-14 items-center justify-center rounded-full bg-surface-raised">
        <Lock className="h-7 w-7" />
      </span>
      <PinPad
        title={`Perfil de ${user.display_name} bloqueado`}
        hint="Escribe tu PIN para seguir."
        busy={busy}
        error={error}
        allowLegacy
        onComplete={async (pin) => {
          setBusy(true);
          setError('');
          try {
            const r = await api<{ user: User; pin_failures?: PinFailures }>('POST', '/api/v1/me/unlock', { pin });
            if (r.pin_failures) setPinNotice(r.pin_failures);
            setUser({ ...r.user, locked: false });
            return true;
          } catch (e) {
            setError(e instanceof ApiError && e.code === 'credencial_incorrecta' ? 'PIN incorrecto.' : e instanceof Error ? e.message : 'No se ha podido desbloquear.');
            return false;
          } finally {
            setBusy(false);
          }
        }}
      >
        <Button type="button" variant="ghost" onClick={() => setMode('recover')}>
          He olvidado mi PIN
        </Button>
        <Link to="/perfiles" className="rounded-md p-2 text-sm text-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus">
          Cambiar de perfil
        </Link>
        <Button type="button" variant="ghost" size="sm" onClick={() => void logout()}>
          Cerrar sesión
        </Button>
      </PinPad>
    </LockFrame>
  );
}

function LockFrame({ children }: { children: ReactNode }) {
  return <main className="flex min-h-screen flex-col items-center justify-center gap-6 p-6">{children}</main>;
}

/** El aviso de intentos fallidos, una vez, al entrar en el perfil. */
export function PinFailuresNotice() {
  const { pinNotice, setPinNotice, user } = useSession();
  if (!pinNotice || !user || user.locked) return null;
  return (
    <div role="alert" className="fixed inset-x-4 bottom-4 z-50 mx-auto flex max-w-lg flex-col gap-3 rounded-lg border border-danger bg-surface-raised p-4 shadow-card">
      <p className="text-sm">{pinFailuresText(pinNotice)} Si no has sido tú, cambia tu PIN en «Mi cuenta».</p>
      <div className="flex gap-3">
        <Button size="sm" autoFocus onClick={() => setPinNotice(null)}>
          Entendido
        </Button>
        <Link to="/cuenta" onClick={() => setPinNotice(null)} className="self-center text-sm text-accent">
          Ir a Mi cuenta
        </Link>
      </div>
    </div>
  );
}
