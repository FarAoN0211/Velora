// Teclado de PIN (fase 5 de replica/PLAN_HOGARES_Y_PIN.md): cuatro casillas y un teclado numérico en pantalla que se
// maneja con el mando (cruceta y Aceptar), con las teclas numéricas del mando o del teclado y con el dedo. Al teclear
// el cuarto dígito se envía solo. Los PIN antiguos de 5 a 8 dígitos se escriben con «Mi PIN tiene más de 4 dígitos».
import { useEffect, useRef, useState, type ReactNode } from 'react';
import { Delete } from 'lucide-react';
import { api, type PinFailures, type User } from '../lib/api';
import { isTv } from '../lib/tv';
import { Banner, Button, TextField } from './ui';

interface PinPadProps {
  title: string;
  hint?: ReactNode;
  busy?: boolean;
  error?: string;
  /** Se llama con el PIN completo; si devuelve false (PIN incorrecto) se vacían las casillas. */
  onComplete: (pin: string) => void | boolean | Promise<void | boolean>;
  /** Permite PIN antiguos de hasta 8 dígitos (solo al comprobar un PIN que ya existe). */
  allowLegacy?: boolean;
  /** Botones o enlaces bajo el teclado («He olvidado mi PIN», «Cancelar»…). */
  children?: ReactNode;
  autoFocus?: boolean;
}

export function PinPad({ title, hint, busy, error, onComplete, allowLegacy, children, autoFocus = true }: PinPadProps) {
  // Los dígitos viven también en una referencia: las teclas pueden llegar más deprisa de lo que se repinta la pantalla.
  const [digits, setShown] = useState('');
  const value = useRef('');
  const setDigits = (v: string) => {
    value.current = v;
    setShown(v);
  };
  const [legacy, setLegacy] = useState(false);
  const max = legacy ? 8 : 4;
  const first = useRef<HTMLButtonElement>(null);
  const sent = useRef(false);

  async function submit(pin: string) {
    if (sent.current) return;
    sent.current = true;
    try {
      const ok = await onComplete(pin);
      if (ok === false) setDigits('');
    } finally {
      sent.current = false;
    }
  }

  function press(d: string) {
    if (busy || sent.current || value.current.length >= max) return;
    const next = value.current + d;
    setDigits(next);
    if (!legacy && next.length === 4) void submit(next);
  }
  const erase = () => setDigits(value.current.slice(0, -1));

  // Con un error nuevo (PIN incorrecto) se vacían las casillas para volver a intentarlo.
  useEffect(() => {
    if (error) setDigits('');
  }, [error]); // eslint-disable-line react-hooks/exhaustive-deps

  // Teclas numéricas del mando o del teclado, en cualquier parte de la pantalla.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement | null;
      if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA')) return;
      if (/^[0-9]$/.test(e.key)) {
        e.preventDefault();
        press(e.key);
      } else if (e.key === 'Backspace' || e.key === 'Delete') {
        e.preventDefault();
        erase();
      } else if (e.key === 'Enter' && legacy && value.current.length >= 4) {
        e.preventDefault();
        void submit(value.current);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  });

  useEffect(() => {
    if (autoFocus) first.current?.focus();
  }, [autoFocus]);

  const key = 'flex h-14 items-center justify-center rounded-lg bg-surface-raised text-xl font-semibold text-text hover:bg-surface-overlay focus-visible:outline-none focus-visible:ring-4 focus-visible:ring-focus disabled:opacity-50 tv:h-16 tv:text-2xl';
  return (
    <div className="mx-auto flex w-full max-w-xs flex-col items-center gap-4 tv:max-w-sm">
      <h2 className="text-center text-lg tv:text-2xl">{title}</h2>
      {hint && <div className="text-center text-sm text-text-muted">{hint}</div>}
      <div className="flex gap-3" aria-label={`${digits.length} de ${legacy ? 'hasta 8' : 4} dígitos escritos`} role="status">
        {Array.from({ length: legacy ? Math.max(4, digits.length + (digits.length < 8 ? 1 : 0)) : 4 }, (_, i) => (
          <span
            key={i}
            aria-hidden
            className={`flex h-12 w-10 items-center justify-center rounded-md border-2 text-2xl tv:h-14 tv:w-12 ${i < digits.length ? 'border-accent' : 'border-border-input'}`}
          >
            {i < digits.length ? '•' : ''}
          </span>
        ))}
      </div>
      {error && <Banner tone="error">{error}</Banner>}
      <div className="grid w-full grid-cols-3 gap-2">
        {['1', '2', '3', '4', '5', '6', '7', '8', '9'].map((d, i) => (
          <button key={d} ref={i === 0 ? first : undefined} type="button" className={key} disabled={busy} onClick={() => press(d)}>
            {d}
          </button>
        ))}
        <button type="button" className={key} disabled={busy || !digits} onClick={erase} aria-label="Borrar el último dígito">
          <Delete aria-hidden className="h-6 w-6" />
        </button>
        <button type="button" className={key} disabled={busy} onClick={() => press('0')}>
          0
        </button>
        {legacy ? (
          <button type="button" className={key + ' text-base'} disabled={busy || digits.length < 4} onClick={() => void submit(digits)}>
            Abrir
          </button>
        ) : (
          <span aria-hidden />
        )}
      </div>
      {allowLegacy && !legacy && (
        <button type="button" className="text-xs text-text-muted underline" onClick={() => setLegacy(true)}>
          Mi PIN tiene más de 4 dígitos
        </button>
      )}
      {children && <div className="flex flex-col items-center gap-2">{children}</div>}
    </div>
  );
}

/** Pide un PIN nuevo dos veces (escribirlo y repetirlo). */
export function NewPinPad({ title, busy, error, onDone, children }: { title: string; busy?: boolean; error?: string; onDone: (pin: string) => void; children?: ReactNode }) {
  const [first, setFirst] = useState('');
  const [mismatch, setMismatch] = useState('');
  if (!first)
    return (
      <PinPad key="nuevo" title={title} hint="Elige 4 dígitos." busy={busy} error={error || mismatch} onComplete={(p) => {setMismatch('');setFirst(p);}}>
        {children}
      </PinPad>
    );
  return (
    <PinPad
      key="repetir"
      title="Repite el PIN nuevo"
      busy={busy}
      onComplete={(p) => {
        if (p !== first) {
          setFirst('');
          setMismatch('Los PIN no coinciden. Vuelve a empezar.');
          return false;
        }
        onDone(p);
      }}
    >
      {children}
    </PinPad>
  );
}

/** El código de recuperación del PIN, que solo se enseña una vez. */
export function RecoveryCodeNotice({ code, name, onDone }: { code: string; name?: string; onDone: () => void }) {
  return (
    <div className="mx-auto flex w-full max-w-md flex-col items-center gap-4 text-center">
      <h2 className="text-lg tv:text-2xl">Tu código de recuperación</h2>
      <p className="select-text rounded-lg bg-surface-raised px-6 py-4 font-mono text-3xl tracking-widest tv:text-4xl">{code}</p>
      <p className="text-sm text-text-muted">
        Apúntalo y guárdalo fuera de Velora. <strong>Solo se muestra ahora.</strong> Sirve para elegir un PIN nuevo si lo olvidas
        y para recuperar tu historial si alguien restablece tu PIN. Si generas otro, este deja de valer.
      </p>
      <div className="flex flex-wrap justify-center gap-3">
        {!isTv && (
          <Button
            type="button"
            variant="secondary"
            onClick={() => {
              const url = URL.createObjectURL(new Blob([`Velora · código de recuperación del PIN${name ? ` de ${name}` : ''}\n\n${code}\n`], { type: 'text/plain' }));
              const a = document.createElement('a');
              a.href = url;
              a.download = 'velora-codigo-pin.txt';
              a.click();
              setTimeout(() => URL.revokeObjectURL(url), 1000);
            }}
          >
            Guardar en un archivo
          </Button>
        )}
        <Button type="button" autoFocus onClick={onDone}>
          Ya lo he guardado
        </Button>
      </div>
    </div>
  );
}

export interface RecoverResult {
  user: User;
  csrf_token: string;
  recovery_code: string;
  pin_failures?: PinFailures;
}

/** «He olvidado mi PIN»: código de recuperación → PIN nuevo. Se conserva todo y se entra en el perfil. */
export function RecoverPin({ profile, onRecovered, onCancel }: { profile: { id: string; display_name: string }; onRecovered: (r: RecoverResult) => void; onCancel: () => void }) {
  const [code, setCode] = useState('');
  const [step, setStep] = useState<'code' | 'pin'>('code');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  if (step === 'code')
    return (
      <form
        className="mx-auto flex w-full max-w-sm flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          setError('');
          setStep('pin');
        }}
      >
        <h2 className="text-center text-lg tv:text-2xl">He olvidado el PIN de {profile.display_name}</h2>
        <TextField
          label="Código de recuperación"
          autoFocus
          autoComplete="off"
          autoCapitalize="characters"
          placeholder="XXXX-XXXX"
          maxLength={12}
          value={code}
          onChange={(e) => setCode(e.target.value.toUpperCase())}
          hint="El que se mostró al activar el PIN. Si no lo tienes, pide al propietario del hogar que restablezca tu PIN: tu historial quedará guardado aparte y podrás recuperarlo con este código."
        />
        {error && <Banner tone="error">{error}</Banner>}
        <Button disabled={code.replace(/[^0-9A-Z]/g, '').length !== 8}>Continuar</Button>
        <Button type="button" variant="ghost" onClick={onCancel}>
          Cancelar
        </Button>
      </form>
    );
  return (
    <NewPinPad
      title={`PIN nuevo de ${profile.display_name}`}
      busy={busy}
      onDone={async (pin) => {
        setBusy(true);
        try {
          onRecovered(await api<RecoverResult>('POST', '/api/v1/auth/pin/recover', { user_id: profile.id, recovery_code: code, pin }));
        } catch (e) {
          setError(e instanceof Error ? e.message : 'No se ha podido recuperar el PIN.');
          setStep('code');
        } finally {
          setBusy(false);
        }
      }}
    >
      <Button type="button" variant="ghost" onClick={onCancel}>
        Cancelar
      </Button>
    </NewPinPad>
  );
}

/** Aviso de intentos fallidos de PIN desde la anterior visita. */
export function pinFailuresText(n: PinFailures): string {
  const when = new Date(n.last_at).toLocaleString('es-ES', { dateStyle: 'medium', timeStyle: 'short' });
  return n.count === 1
    ? `Desde tu última visita ha habido 1 intento fallido de abrir tu perfil (${when}).`
    : `Desde tu última visita ha habido ${n.count} intentos fallidos de abrir tu perfil (el último, ${when}).`;
}
