// Los valores salen de tailwind.preset.cjs (copia de replica/design; `npm run tokens:check` comprueba que no divergen).
const plugin = require('tailwindcss/plugin');

module.exports = {
  presets: [require('./tailwind.preset.cjs')],
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  // `tv:` solo se aplica en la app de TV (lib/tv.ts), mida lo que mida la pantalla.
  plugins: [plugin(({ addVariant }) => addVariant('tv', 'html.tv &'))],
};
