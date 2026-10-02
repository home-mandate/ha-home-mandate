// SPDX-License-Identifier: AGPL-3.0-or-later
/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** "1" in the dev server and the mock/pseudo test builds; never in the release build. */
  readonly VITE_MOCK?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
