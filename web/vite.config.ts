// SPDX-License-Identifier: AGPL-3.0-or-later
/// <reference types="vitest/config" />

import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

export default defineConfig({
  // Relative paths: Home Assistant Ingress serves the UI under a per-installation path.
  base: './',
  plugins: [svelte()],
  build: {
    target: 'es2024',
    // No data: URIs and no inline polyfill, so the CSP can stay at 'self'.
    assetsInlineLimit: 0,
    modulePreload: { polyfill: false },
  },
  resolve: process.env.VITEST ? { conditions: ['browser'] } : undefined,
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.ts', 'scripts/**/*.test.ts'],
    // A machine time zone far from any household, to catch formatting in the wrong zone.
    env: { TZ: 'Pacific/Kiritimati' },
    coverage: {
      provider: 'v8',
      include: ['src/lib/**/*.ts', 'scripts/**/*.ts'],
      exclude: ['src/lib/paraglide/**', '**/*.test.ts'],
      thresholds: { lines: 85, statements: 85, functions: 85, branches: 85 },
    },
  },
});
