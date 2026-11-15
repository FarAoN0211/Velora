// Identificadores de presencia/dispositivo; nunca se usan como credenciales.
let sequence = 0;
export function newClientId(): string {
  const bytes = new Uint8Array(16);
  try {
    if (typeof globalThis.crypto?.randomUUID === 'function') return globalThis.crypto.randomUUID();
    if (typeof globalThis.crypto?.getRandomValues === 'function') globalThis.crypto.getRandomValues(bytes);
    else throw new Error('Sin generador del navegador');
  } catch {
    // Compatibilidad con HTTP y WebView antiguos, sin promesas criptográficas.
    for (let i = 0; i < bytes.length; i++) bytes[i] = Math.floor(Math.random() * 256);
    const n = Date.now() + ++sequence;
    for (let i = 0; i < 6; i++) bytes[i] ^= Math.floor(n / 2 ** (8 * i)) & 255;
  }
  bytes[6] = (bytes[6] & 15) | 64;
  bytes[8] = (bytes[8] & 63) | 128;
  const hex = Array.from(bytes, b => b.toString(16).padStart(2, '0')).join('');
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}
