import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

export default defineConfig({
  plugins: [svelte()],
  build: {
    target: 'es2022',
    // No inline scripts or styles, so a strict CSP stays possible.
    assetsInlineLimit: 0,
    modulePreload: { polyfill: false },
  },
  server: {
    // During development the Go backend runs on :8080.
    proxy: { '/api': 'http://127.0.0.1:8080' },
  },
});
