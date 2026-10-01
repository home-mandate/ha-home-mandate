// SPDX-License-Identifier: AGPL-3.0-or-later

// Checks the production build (docs/TESTING.md section 4, UI): no references to external
// hosts and nothing inline that a CSP without 'unsafe-inline' would block.
// Run after `vite build` with: node scripts/check-dist.ts

import { readFileSync, readdirSync } from 'node:fs';
import { join, relative } from 'node:path';

/** Documentation links in error messages and XML namespaces; they load nothing. */
const ALLOWED_URL_PREFIXES = ['https://svelte.dev/e/', 'https://paraglidejs.com/errors', 'http://www.w3.org/'];
/** Base URLs the Paraglide runtime passes to new URL() for parsing; they load nothing. */
const ALLOWED_URLS = new Set(['http://fallback.com', 'http://example.com']);

export function checkFile(name: string, content: string): string[] {
  const problems: string[] = [];
  for (const match of content.matchAll(/(?:\bhttps?:)?\/\/[a-z0-9-]+(?:\.[a-z0-9-]+)*\.[a-z]{2,}[^\s"'`)]*/gi)) {
    const url = match[0];
    if (!ALLOWED_URLS.has(url) && !ALLOWED_URL_PREFIXES.some((p) => url.startsWith(p))) {
      problems.push(`${name}: external reference ${url}`);
    }
  }
  if (name.endsWith('.html')) {
    if (/<script(?![^>]*\bsrc=)[^>]*>/i.test(content)) problems.push(`${name}: inline <script>`);
    if (/<style[\s>]/i.test(content)) problems.push(`${name}: inline <style>`);
    if (/\sstyle=/i.test(content)) problems.push(`${name}: style attribute`);
    if (/\son[a-z]+=/i.test(content)) problems.push(`${name}: inline event handler`);
  }
  return problems;
}

export function run(dist: string): string[] {
  return readdirSync(dist, { recursive: true, withFileTypes: true })
    .filter((e) => e.isFile() && /\.(html|js|css|svg|json)$/.test(e.name))
    .flatMap((e) => {
      const path = join(e.parentPath, e.name);
      return checkFile(relative(dist, path), readFileSync(path, 'utf8'));
    });
}

if (import.meta.main) {
  const problems = run(join(process.cwd(), 'dist'));
  for (const p of problems) console.error(p);
  if (problems.length > 0) process.exit(1);
  console.log('dist: ok');
}
