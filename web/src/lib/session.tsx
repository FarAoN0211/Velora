import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from 'react';
import { api, ApiError, onProfileLocked, type PinFailures, type SystemInfo, type User } from './api';
import { invalidateMenu } from './catalog';

interface SessionState {
  info: SystemInfo | null;
  user: User | null;
  loading: boolean;
  error: string | null;
  refresh: () => Promise<void>;
  setUser: (u: User | null) => void;
  logout: () => Promise<void>;
  /** Aviso de intentos fallidos de PIN que se enseña al entrar en el perfil (una vez). */
  pinNotice: PinFailures | null;
  setPinNotice: (n: PinFailures | null) => void;
}

const SessionContext = createContext<SessionState | null>(null);

// Cada cuánto se avisa como mucho de una interacción real (teclas, mando, toques, clics) y cada cuánto se comprueba,
// con Velora a la vista, si el servidor ha bloqueado el perfil (fase 4 de replica/PLAN_HOGARES_Y_PIN.md).
const INTERACTION_EVERY_MS = 60_000;
const CHECK_EVERY_MS = 60_000;

export function SessionProvider({ children }: { children: ReactNode }) {
  const [info, setInfo] = useState<SystemInfo | null>(null);
  const [user, setUserState] = useState<User | null>(null);
  const [pinNotice, setPinNotice] = useState<PinFailures | null>(null);
  // Al cambiar de usuario se descarta el menú de bibliotecas del anterior.
  const setUser = useCallback((u: User | null) => {
    invalidateMenu();
    setUserState(u);
  }, []);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const i = await api<SystemInfo>('GET', '/api/v1/system/info');
      setInfo(i);
      if (!i.setup_required) {
        try {
          const me = await api<{ user: User }>('GET', '/api/v1/me');
          setUser(me.user);
        } catch (e) {
          if (e instanceof ApiError && e.status === 401) setUser(null);
          else throw e;
        }
      }
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'No se ha podido cargar VELORA.');
    } finally {
      setLoading(false);
    }
  }, [setUser]);

  const logout = useCallback(async () => {
    await api('POST', '/api/v1/auth/logout');
    setPinNotice(null);
    setUser(null);
  }, [setUser]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  // Cualquier 423 «perfil_bloqueado» marca el perfil como bloqueado: la pantalla de PIN tapa lo privado.
  // La app Android avisa con «velora:bloqueado» si su reproductor propio se ha topado con el bloqueo.
  useEffect(() => {
    const lock = () => setUserState((u) => (u && !u.locked ? { ...u, locked: true } : u));
    window.addEventListener('velora:bloqueado', lock);
    const off = onProfileLocked(lock);
    return () => {
      window.removeEventListener('velora:bloqueado', lock);
      off();
    };
  }, []);

  // Señal de uso real. Solo importa a un perfil con PIN y sin bloquear; las consultas automáticas no cuentan.
  const watched = user?.has_pin && !user.locked ? user : null;
  const lastInteraction = useRef(0);
  useEffect(() => {
    if (!watched) return;
    const always = watched.pin_lock_minutes === 0;
    const interaction = () => {
      const now = Date.now();
      if (now - lastInteraction.current < INTERACTION_EVERY_MS) return;
      lastInteraction.current = now;
      void api('POST', '/api/v1/me/activity', { interaction: true }).catch(() => undefined);
    };
    // Con Velora a la vista: con «Siempre» cuenta como uso; con las demás opciones solo se mira si ya está bloqueado.
    const check = () => {
      if (document.hidden) return;
      const req = always ? api('POST', '/api/v1/me/activity', { interaction: false }) : api<{ user: User }>('GET', '/api/v1/me');
      void req
        .then((r) => {
          if (r && (r as { user?: User }).user?.locked) setUserState((u) => (u ? { ...u, locked: true } : u));
        })
        .catch(() => undefined);
    };
    const visible = () => {
      if (!document.hidden) check();
    };
    const events = ['keydown', 'pointerdown', 'touchstart', 'wheel'] as const;
    events.forEach((e) => window.addEventListener(e, interaction, { passive: true, capture: true }));
    document.addEventListener('visibilitychange', visible);
    window.addEventListener('velora:volver', visible); // vuelta del reproductor propio de la app Android
    const timer = window.setInterval(check, CHECK_EVERY_MS);
    return () => {
      events.forEach((e) => window.removeEventListener(e, interaction, { capture: true }));
      document.removeEventListener('visibilitychange', visible);
      window.removeEventListener('velora:volver', visible);
      window.clearInterval(timer);
    };
  }, [watched?.id, watched?.pin_lock_minutes]); // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <SessionContext.Provider value={{ info, user, loading, error, refresh, setUser, logout, pinNotice, setPinNotice }}>
      {children}
    </SessionContext.Provider>
  );
}

export function useSession(): SessionState {
  const ctx = useContext(SessionContext);
  if (!ctx) throw new Error('useSession fuera de SessionProvider');
  return ctx;
}
