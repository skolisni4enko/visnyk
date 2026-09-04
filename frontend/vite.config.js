import { defineConfig } from 'vite';
export default defineConfig({
  server: { port: 34115, strictPort: true },
  build: { outDir: 'dist' }
});
