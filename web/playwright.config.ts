// SPDX-License-Identifier: AGPL-3.0-or-later

import { randomBytes } from 'node:crypto';
import { defineConfig, devices } from '@playwright/test';

// A random Ingress path per run checks relative asset paths and hash routing.
const ingressPath = process.env.INGRESS_PATH ?? `/api/hassio_ingress/${randomBytes(16).toString('hex')}/`;
process.env.INGRESS_PATH = ingressPath;
const port = 4173;
const pseudoPort = 4174;
const at = (p: number) => `http://127.0.0.1:${p}${ingressPath}`;
// The screen sweep (step 6: themes, rtl, widths, axe), the colour scheme switch and the
// unsaved changes of the editors (issue #20) run in every language; the flows only in de/en.
const sweep = /(sweep|hostile|theme|unsaved|savebar)\.spec\.ts$/;

export default defineConfig({
  testDir: './e2e',
  forbidOnly: !!process.env.CI,
  retries: 0,
  reporter: process.env.CI ? 'github' : 'list',
  use: {
    baseURL: at(port),
    trace: 'retain-on-failure',
    // Countdowns, toasts and transitions settle at once.
    reducedMotion: 'reduce',
  },
  projects: [
    { name: 'de', use: { ...devices['Desktop Chrome'], locale: 'de-DE', timezoneId: 'America/New_York' } },
    { name: 'en', use: { ...devices['Desktop Chrome'], locale: 'en-US', timezoneId: 'Asia/Tokyo' } },
    // Pseudo-localized build: accented and about 40 % longer texts find clipping and hard-coded strings.
    {
      name: 'pseudo',
      testMatch: sweep,
      use: { ...devices['Desktop Chrome'], locale: 'de-DE', timezoneId: 'Europe/Berlin', baseURL: at(pseudoPort) },
    },
  ],
  // The UI needs data: the static test builds run against the mock client.
  webServer: [
    {
      command: 'node scripts/serve-ingress.ts',
      url: at(port),
      env: { INGRESS_PATH: ingressPath, PORT: String(port), DIST: 'dist-mock' },
      // Locally a running server can be reused (same INGRESS_PATH); it reads dist on every request.
      reuseExistingServer: !process.env.CI,
    },
    {
      command: 'node scripts/serve-ingress.ts',
      url: at(pseudoPort),
      env: { INGRESS_PATH: ingressPath, PORT: String(pseudoPort), DIST: 'dist-pseudo' },
      reuseExistingServer: !process.env.CI,
    },
  ],
});
