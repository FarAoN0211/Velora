import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// En desarrollo, /api se redirige al servidor de la red interna de velora-dev (sin puertos publicados).
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: { '/api': process.env.VELORA_API_URL ?? 'http://server:8640' },
  },
  build: {
    outDir: 'dist',
    sourcemap: false,
  },
});
