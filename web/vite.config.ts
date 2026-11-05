import { defineConfig } from 'vite';

export default defineConfig({
  base: '/app/',
  server: {
    port: 5173,
    proxy: { '/api/v1': { target: 'http://127.0.0.1:9090', changeOrigin: true } },
  },
  build: { outDir: 'dist', emptyOutDir: true },
});
