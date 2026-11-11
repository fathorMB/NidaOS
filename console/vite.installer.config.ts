/// <reference types="vitest/config" />
import react from '@vitejs/plugin-react';
import { resolve } from 'node:path';
import { defineConfig } from 'vite';

// The installer page (milestone M-07, decision D32). It is a second entry
// of the console project: it shares the design tokens and the parts. The
// build goes into nida-installer: core/internal/installerui/dist.
// For "npx vite --config vite.installer.config.ts", NIDA_INSTALLER is the
// address of a nida-installer, for example http://localhost:8090.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../core/internal/installerui/dist',
    emptyOutDir: false,
    rollupOptions: { input: resolve(__dirname, 'installer.html') },
  },
  server: {
    open: '/installer.html',
    proxy: { '/api': { target: process.env.NIDA_INSTALLER ?? 'http://localhost:8090' } },
  },
});
