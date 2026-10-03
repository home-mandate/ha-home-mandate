// SPDX-License-Identifier: AGPL-3.0-or-later
/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** "1" in the dev server and the mock/pseudo test builds; never in the release build. */
  readonly VITE_MOCK?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}

/** Test builds only (VITE_MOCK): Playwright sets the options and drives the mock through hmMock. */
interface Window {
  hmMock?: import('./lib/api/mock.ts').MockControls;
  hmMockOptions?: import('./lib/api/mock.ts').MockOptions;
}
