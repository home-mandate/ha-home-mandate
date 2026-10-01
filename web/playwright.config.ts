// SPDX-License-Identifier: AGPL-3.0-or-later

import { randomBytes } from 'node:crypto';
import { defineConfig, devices } from '@playwright/test';

// A random Ingress path per run checks relative asset paths and hash routing.
const ingressPath = process.env.INGRESS_PATH ?? `/api/hassio_ingress/${randomBytes(16).toString('hex')}/`;
process.env.INGRESS_PATH = ingressPath;
const port = 4173;

export default defineConfig({
  testDir: './e2e',
  forbidOnly: !!process.env.CI,
  retries: 0,
  reporter: process.env.CI ? 'github' : 'list',
  use: {
    baseURL: `http://127.0.0.1:${port}${ingressPath}`,
    trace: 'retain-on-failure',
  },
  projects: [
    { name: 'de', use: { ...devices['Desktop Chrome'], locale: 'de-DE', timezoneId: 'America/New_York' } },
    { name: 'en', use: { ...devices['Desktop Chrome'], locale: 'en-US', timezoneId: 'Asia/Tokyo' } },
  ],
  webServer: {
    command: 'node scripts/serve-ingress.ts',
    url: `http://127.0.0.1:${port}${ingressPath}`,
    env: { INGRESS_PATH: ingressPath, PORT: String(port) },
    reuseExistingServer: false,
  },
});
