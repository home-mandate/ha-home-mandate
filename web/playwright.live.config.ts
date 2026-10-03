// SPDX-License-Identifier: AGPL-3.0-or-later

// Playwright against the real gateway behind the Ingress stand-in of the E2E environment
// (e2e/ui_test.go starts everything and sets HM_LIVE_*; make e2e-ui). Not part of
// `pnpm e2e`, which runs against the static mock build.
import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: './e2e-live',
  forbidOnly: !!process.env.CI,
  retries: 0,
  workers: 1,
  reporter: process.env.CI ? 'github' : 'list',
  use: { trace: 'retain-on-failure', reducedMotion: 'reduce' },
  projects: [
    { name: 'de', use: { ...devices['Desktop Chrome'], locale: 'de-DE', timezoneId: 'America/New_York' } },
    { name: 'en', use: { ...devices['Desktop Chrome'], locale: 'en-US', timezoneId: 'Asia/Tokyo' } },
  ],
});
