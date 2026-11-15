import { newClientId } from './clientId';
// Cliente de la API de VELORA para la web: sesión por cookie HttpOnly + token anti-CSRF (architecture.md §8).

export interface User {
 household_id: string|null;
 household_owner: boolean;
 passwordless: boolean;
  id: string;
  username: string;
  display_name: string;
  is_admin: boolean;
  has_pin: boolean;
  can_play: boolean;
  can_download: boolean;
  can_delete: boolean;
  can_remote: boolean;
  lock_profile: boolean;
  /** Permiso del administrador para crear (y ampliar) su propio hogar. */
  can_create_household: boolean;
  /** Cuándo se vuelve a pedir el PIN: 0 = siempre; 15, 60 (predeterminado) o 240 minutos sin uso. */
  pin_lock_minutes: number;
  /** El servidor ha bloqueado el perfil en este dispositivo: hay que escribir su PIN. */
  locked: boolean;
}

/** Intentos fallidos de PIN o código desde la anterior visita al perfil. */
export interface PinFailures {
  count: number;
  last_at: string;
}

export interface SystemInfo {
  name: string;
  version: string;
  language: string;
  setup_required: boolean;
}

export interface Profile {
  id: string;
  display_name: string;
  has_pin: boolean;
}

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
  }
}

// Avisos de «perfil bloqueado» (423): cualquier pantalla que choque con el bloqueo hace aparecer la de PIN.
const lockListeners = new Set<() => void>();
export function onProfileLocked(cb: () => void): () => void {
  lockListeners.add(cb);
  return () => {
    lockListeners.delete(cb);
  };
}

const DEVICE_KEY = 'velora.device-id';
let memoryDeviceId: string | undefined;

export function deviceId(): string {
  try {
    let id = localStorage.getItem(DEVICE_KEY);
    if (!id) {
      id = newClientId();
      localStorage.setItem(DEVICE_KEY, id);
    }
    return id;
  } catch {
    return memoryDeviceId ??= newClientId();
  }
}

export function csrfToken(): string {
  const m = document.cookie.match(/(?:^|;\s*)velora_csrf=([^;]+)/);
  return m ? decodeURIComponent(m[1]) : '';
}

// signal: la petición se corta si la acción que la pidió deja de valer (otro salto, otro título, salir); entonces se
// lanza ApiError con código «cancelada».
export async function api<T>(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  const form = body instanceof FormData;
  const headers: Record<string, string> = {
    'X-Velora-Client': 'web',
    'X-Velora-Device-Id': deviceId(),
    'X-Velora-Device-Name': 'Navegador',
  };
  if (body !== undefined && !form) headers['Content-Type'] = 'application/json'; // con FormData lo pone el navegador
  if (method !== 'GET') headers['X-CSRF-Token'] = csrfToken();

  let res: Response;
  try {
    res = await fetch(path, {
      method,
      headers,
      credentials: 'same-origin',
      body: body === undefined ? undefined : form ? (body as FormData) : JSON.stringify(body),
      signal,
    });
  } catch {
    if (signal?.aborted) throw new ApiError(0, 'cancelada', 'Petición cancelada.');
    throw new ApiError(0, 'red', 'No se puede contactar con el servidor. Comprueba la conexión.');
  }
  if (res.status === 204) return undefined as T;
  const data = await res.json().catch(() => null);
  if (signal?.aborted) throw new ApiError(0, 'cancelada', 'Petición cancelada.');
  if (!res.ok) {
    const err = data?.error;
    if (res.status === 423 && err?.code === 'perfil_bloqueado') lockListeners.forEach((cb) => cb());
    throw new ApiError(res.status, err?.code ?? 'desconocido', err?.message ?? 'Ha ocurrido un error inesperado.');
  }
  return data as T;
}
