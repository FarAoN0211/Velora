// VELORA · preset de Tailwind que mapea los tokens (tokens.css) a clases semánticas.
// Los componentes usan solo roles (bg-surface, text-muted, ring-focus…), nunca hex sueltos.
// Borrador de diseño: se moverá al proyecto web en la etapa E1 (replica-build).
const v = (name) => `var(--${name})`;

const colorRoles = [
  'bg', 'surface', 'surface-raised', 'surface-overlay', 'border', 'border-input',
  'text', 'text-muted', 'text-subtle', 'accent', 'accent-strong', 'on-accent', 'focus-ring',
  'danger', 'on-danger', 'success', 'warning', 'info',
  'status-verified', 'status-planned', 'status-unavailable', 'status-error', 'status-remote',
  'progress-track', 'progress-fill', 'scrim',
];

const typeSteps = ['xs', 'sm', 'base', 'lg', 'xl', 'display'];

module.exports = {
  darkMode: ['selector', '[data-theme="dark"]'],
  theme: {
    colors: {
      transparent: 'transparent',
      current: 'currentColor',
      ...Object.fromEntries(colorRoles.map((r) => [r, v(`color-${r}`)])),
    },
    fontFamily: { sans: v('font-sans'), mono: v('font-mono') },
    fontSize: Object.fromEntries(
      typeSteps.map((s) => [s, [v(`type-${s}-size`), { lineHeight: v(`type-${s}-line`), fontWeight: v(`type-${s}-weight`) }]]),
    ),
    // spacing: se usa la escala por defecto de Tailwind (múltiplos de 4 px), que coincide con tokens.space
    // (4, 8, 12, 16, 24, 32, 48, 64, 96 = p-1, p-2, p-3, p-4, p-6, p-8, p-12, p-16, p-24). Corregido en E0.
    borderRadius: { none: '0', sm: v('radius-sm'), md: v('radius-md'), lg: v('radius-lg'), xl: v('radius-xl'), full: v('radius-pill') },
    boxShadow: { none: 'none', card: v('shadow-card'), pop: v('shadow-pop'), 'focus-tv': v('shadow-focus-tv') },
    screens: { tablet: '600px', desktop: '1024px', wide: '1440px' },
    extend: {
      transitionDuration: { fast: v('motion-fast'), base: v('motion-base'), slow: v('motion-slow') },
      transitionTimingFunction: { velora: v('motion-ease') },
      scale: { focus: v('focus-scale') },
      maxWidth: { content: v('layout-max-content') },
      width: { sidebar: v('layout-sidebar'), 'sidebar-collapsed': v('layout-sidebar-collapsed') },
      height: { header: v('layout-header'), 'bottom-nav': v('layout-bottom-nav') },
      minHeight: { touch: v('touch-target-min') },
      minWidth: { touch: v('touch-target-min') },
      padding: { 'safe-x': v('safe-x'), 'safe-y': v('safe-y') },
      aspectRatio: { poster: '2 / 3', backdrop: '16 / 9', square: '1 / 1' },
      ringColor: { focus: v('color-focus-ring') },
    },
  },
};
