// Subtítulos en el reproductor (E1.2, B3). El servidor los entrega como WebVTT con tiempos absolutos de la película; la
// web los dibuja ella misma sobre el vídeo, así siguen sincronizados en Optimizado (donde cada salto abre un flujo que
// empieza en 0) y tras cambiar de audio. El texto se muestra siempre como texto: nunca se interpreta HTML.

export interface SubTrack {
  id: string;
  label?: string;
  type?: string;
  hearing_impaired?: boolean;
  provider?: string;
  language?: string;
  title?: string;
  codec: string;
  forced: boolean;
  default: boolean;
  external: boolean;
  supported: boolean;
}

export interface Cue {
  start: number;
  end: number;
  text: string;
}

function seconds(t: string): number {
  const parts = t.trim().replace(',', '.').split(':').map(Number);
  return parts.reduce((acc, p) => acc * 60 + p, 0);
}

export function parseVTT(vtt: string): Cue[] {
  const cues: Cue[] = [];
  for (const block of vtt.replace(/\r/g, '').split(/\n{2,}/)) {
    const lines = block.split('\n');
    const i = lines.findIndex((l) => l.includes('-->'));
    if (i < 0) continue;
    const [a, b] = lines[i].split('-->');
    const start = seconds(a);
    const end = seconds(b.trim().split(/\s+/)[0]);
    const text = lines
      .slice(i + 1)
      .join('\n')
      .replace(/<[^>]*>/g, '')
      .replace(/&lt;/g, '<')
      .replace(/&gt;/g, '>')
      .replace(/&amp;/g, '&')
      .trim();
    if (Number.isFinite(start) && Number.isFinite(end) && text) cues.push({ start, end, text });
  }
  return cues.sort((x, y) => x.start - y.start);
}

/** Las frases activas en el segundo t (búsqueda lineal acotada: las listas son de unos miles de frases). */
export function activeCues(cues: Cue[], t: number): string[] {
  const out: string[] = [];
  for (const c of cues) {
    if (c.start > t) break;
    if (t < c.end) out.push(c.text);
  }
  return out;
}

export function channelsLabel(ch?: number | null): string {
  if (!ch) return '';
  return ({ 1: 'mono', 2: 'estéreo', 6: '5.1', 8: '7.1' } as Record<number, string>)[ch] ?? `${ch} canales`;
}

/** Spanish forced subtitles are selected initially; explicit user choices still win. */
export function defaultSubtitle(tracks: SubTrack[], native = false): string {
  const candidates = tracks.filter(t => t.forced && /^(es|spa)([-_]|$)/i.test(t.language ?? '') && (native || t.supported));
  return (candidates.find(t => t.default) ?? candidates[0])?.id ?? '';
}
