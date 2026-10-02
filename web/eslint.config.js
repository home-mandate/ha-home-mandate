// SPDX-License-Identifier: AGPL-3.0-or-later

import { defineConfig } from 'eslint/config';
import js from '@eslint/js';
import ts from 'typescript-eslint';
import svelte from 'eslint-plugin-svelte';
import globals from 'globals';

export default defineConfig(
  { ignores: ['dist/', 'dist-pseudo/', 'coverage/', 'src/lib/paraglide/', 'playwright-report/', 'test-results/'] },
  js.configs.recommended,
  ts.configs.recommended,
  svelte.configs.recommended,
  { files: ['src/**'], languageOptions: { globals: globals.browser } },
  { files: ['scripts/**', 'e2e/**', '*.config.*'], languageOptions: { globals: globals.node } },
  {
    files: ['**/*.svelte', '**/*.svelte.ts'],
    languageOptions: { parserOptions: { parser: ts.parser } },
  },
  {
    rules: {
      // Svelte escapes output; raw HTML would bypass that (docs/ARCHITECTURE.md section 12).
      'svelte/no-at-html-tags': 'error',
      'no-eval': 'error',
      'no-implied-eval': 'error',
      'no-new-func': 'error',
    },
  },
);
