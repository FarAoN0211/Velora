// Insignias técnicas de la ficha (docs/insignias/hoja-referencia-insignias.webp): fondo oscuro, borde de color por
// categoría y texto en negrita, dibujadas solo con CSS (styles/base.css, «.vb»). Los datos los calcula el servidor del
// análisis de ffprobe (campo tech de cada archivo): lo que no se conoce no llega y no se dibuja. Una sola fila; si no
// cabe (móvil), se desplaza en horizontal sin partir la fila. No reciben el foco: en TV el mando pasa de largo.
import type { TechBadge } from '../lib/catalog';

/** Texto principal y secundario de cada insignia conocida; las desconocidas usan su label tal cual. */
const PARTS: Record<string, Record<string, [string, string?]>> = {
  resolution: { sd: ['SD', '480p'], hd: ['HD', '720p'], fhd: ['FULL HD', '1080p'], qhd: ['QHD', '1440p'], '4k': ['4K', 'UHD'], '8k': ['8K', 'UHD'] },
  hdr: { sdr: ['SDR'], hdr10: ['HDR10'], hdr10plus: ['HDR10+'], dovi: ['DOLBY', 'VISION'], hlg: ['HLG'] },
  video: { h264: ['H.264', 'AVC'], hevc: ['H.265', 'HEVC'], av1: ['AV1'], vp9: ['VP', '9'], mpeg2: ['MPEG-2'], vvc: ['VVC', 'H.266'] },
  audio: {
    ac3: ['DOLBY', 'DIGITAL'],
    eac3: ['DOLBY', 'DIGITAL PLUS'],
    truehd: ['DOLBY', 'TRUEHD'],
    atmos: ['DOLBY', 'ATMOS'],
    dts: ['dts'],
    dtshd_ma: ['dts', 'HD MA'],
    dtsx: ['dts', ':X'],
    aac: ['AAC'],
    flac: ['FLAC'],
    pcm: ['PCM'],
  },
  channels: { mono: ['MONO'], '2.0': ['ESTÉREO', '2.0'], '5.1': ['5.1'], '7.1': ['7.1'], '7.1.4': ['7.1.4'] },
  imax: { imax: ['IMAX'], imax_enhanced: ['IMAX', 'ENHANCED'] },
};

/** Puntos de altavoz de la hoja (canales): uno por canal principal, en una fila. */
const DOTS: Record<string, number> = { mono: 1, '2.0': 2, '5.1': 5, '7.1': 7, '7.1.4': 7 };

function parts(b: TechBadge): [string, string?] {
  const known = PARTS[b.category]?.[b.id];
  if (known) return known;
  if (b.category === 'fps') return [b.id, 'FPS'];
  if (b.category === 'bitdepth') return [b.id, 'BIT'];
  return [b.label];
}

export function TechBadges({ badges, size = 'md', className = '' }: { badges?: TechBadge[] | null; size?: 'sm' | 'md'; className?: string }) {
  if (!badges?.length) return null;
  return (
    <span
      role="img"
      data-tv-omitir
      aria-label={`Información técnica: ${badges.map((b) => b.label).join(', ')}`}
      className={`vb-row ${size === 'sm' ? 'vb-sm' : ''} ${className}`}
    >
      {badges.map((b) => {
        const [main, sub] = parts(b);
        return (
          <span key={`${b.category}:${b.id}`} className="vb" data-c={b.category} data-id={b.id} title={b.label}>
            {b.category === 'hdr' && b.id === 'hdr10plus' && <span className="vb-bars" />}
            {(b.category === 'fps' || b.category === 'bitdepth') && <span className={b.category === 'fps' ? 'vb-film' : 'vb-layers'} />}
            {b.category === 'channels' && DOTS[b.id] && (
              <span className="vb-dots">
                {Array.from({ length: Math.min(DOTS[b.id], 5) }, (_, i) => (
                  <i key={i} />
                ))}
              </span>
            )}
            <b className={main === 'DOLBY' ? 'vb-main vb-brand' : 'vb-main'}>{main}</b>
            {sub && <span className="vb-sub">{sub}</span>}
          </span>
        );
      })}
    </span>
  );
}
