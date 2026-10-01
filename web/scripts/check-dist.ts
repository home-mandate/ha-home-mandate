// SPDX-License-Identifier: AGPL-3.0-or-later

// Checks the production build (docs/TESTING.md section 4, UI): no references to other
// hosts, only relative asset paths (Ingress serves the UI under a per-installation
// path), and nothing inline that a CSP without 'unsafe-inline' would block.
// Run after `vite build` with: node scripts/check-dist.ts

import { readFileSync, readdirSync } from 'node:fs';
import { join, relative } from 'node:path';

/** Files that are not text; everything else is scanned. */
const BINARY = /\.(woff2?|png|jpe?g|gif|webp|avif|ico|wasm)$/i;

/** Documentation links in error messages and XML namespace URIs; they load nothing. */
const ALLOWED_ORIGINS: { origin: string; path: string }[] = [
  { origin: 'https://svelte.dev', path: '/e/' },
  { origin: 'https://paraglidejs.com', path: '/errors' },
  { origin: 'http://www.w3.org', path: '/' },
];
/** Base URLs the Paraglide runtime passes to new URL() for parsing; they load nothing. */
const ALLOWED_URLS = new Set(['http://fallback.com/', 'http://example.com/']);

const ABSOLUTE_URL = /\b(?:https?|wss?|ftp):\/\/[^\s"'`<>)\\]+/gi;
const PROTOCOL_RELATIVE = /(?<![\w:/\\])\/\/(?:[a-z0-9-]+\.)+[a-z]{2,}(?::\d+)?(?:\/[^\s"'`<>)\\]*)?/gi;

export function isAllowedUrl(raw: string): boolean {
  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    return false;
  }
  if (ALLOWED_URLS.has(url.href)) return true;
  // new URL() resolves "..", so a doc-link prefix cannot be used to reach other paths.
  return ALLOWED_ORIGINS.some((a) => url.origin === a.origin && url.pathname.startsWith(a.path));
}

export function checkFile(name: string, content: string): string[] {
  const problems: string[] = [];
  for (const [url] of content.matchAll(ABSOLUTE_URL)) {
    if (!isAllowedUrl(url)) problems.push(`${name}: external reference ${url}`);
  }
  for (const [url] of content.matchAll(PROTOCOL_RELATIVE)) {
    problems.push(`${name}: external reference ${url}`);
  }
  if (/\.(html|css|svg)$/i.test(name)) {
    for (const [, scheme] of content.matchAll(/\b(javascript|data|blob):/gi)) {
      problems.push(`${name}: ${scheme!.toLowerCase()}: URL`);
    }
  }
  if (name.endsWith('.html')) problems.push(...checkHtml(name, content));
  return problems;
}

function checkHtml(name: string, html: string): string[] {
  const problems: string[] = [];
  for (const [, attr, value] of html.matchAll(/\s(src|href)\s*=\s*["']?([^"'\s>]*)/gi)) {
    if (!value!.startsWith('./') && !value!.startsWith('#')) {
      problems.push(`${name}: ${attr} "${value}" is not relative to the page`);
    }
  }
  for (const [, body] of html.matchAll(/<script\b[^>]*>([\s\S]*?)<\/script>/gi)) {
    if (body!.trim() !== '') problems.push(`${name}: inline <script>`);
  }
  if (/<style[\s>]/i.test(html)) problems.push(`${name}: inline <style>`);
  if (/\sstyle\s*=/i.test(html)) problems.push(`${name}: style attribute`);
  if (/\son[a-z]+\s*=/i.test(html)) problems.push(`${name}: inline event handler`);
  if (/<base[\s>]/i.test(html)) problems.push(`${name}: <base> element`);
  return problems;
}

export function run(dist: string): string[] {
  return readdirSync(dist, { recursive: true, withFileTypes: true })
    .filter((e) => e.isFile() && !BINARY.test(e.name))
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
