/// <reference types="vitest/config" />
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

// The build goes into nida-core (decision D29): core/internal/console/dist.
// For "npm run dev", NIDA_SERVER is the address of a nida-core with the API,
// for example http://localhost:8470.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../core/internal/console/dist',
    emptyOutDir: false,
    sourcemap: false,
    chunkSizeWarningLimit: 800,
  },
  server: {
    proxy: { '/api': { target: process.env.NIDA_SERVER ?? 'http://localhost:8470' } },
  },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.{ts,tsx}'],
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
  },
});
