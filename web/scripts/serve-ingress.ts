// SPDX-License-Identifier: AGPL-3.0-or-later

// Serves dist/ the way Home Assistant Ingress does: under a per-installation path, with
// the Content Security Policy the gateway will send (internal/webui, week 4).
// Usage: INGRESS_PATH=/api/hassio_ingress/<token>/ PORT=4173 node scripts/serve-ingress.ts

import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { extname, join, normalize, sep } from 'node:path';

export const CSP =
  "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; font-src 'self'; " +
  "connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'";

const TYPES: Record<string, string> = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.svg': 'image/svg+xml',
  '.woff2': 'font/woff2',
};

/** normalizePrefix requires an absolute path and makes it end with a slash. */
export function normalizePrefix(prefix: string): string {
  if (!prefix.startsWith('/') || prefix.startsWith('//') || prefix.includes('..')) {
    throw new Error(`invalid INGRESS_PATH ${JSON.stringify(prefix)}`);
  }
  return prefix.endsWith('/') ? prefix : `${prefix}/`;
}

export type Resolution = { status: 200; file: string } | { status: 400 | 404 | 405 };

/** resolve maps a request to a file in dist, or to an error status. */
export function resolve(method: string, target: string, prefix: string, dist: string): Resolution {
  if (method !== 'GET' && method !== 'HEAD') return { status: 405 };
  const path = target.split(/[?#]/, 1)[0] ?? '';
  // Origin-form only: "/path". "//host/…" and absolute-form targets are rejected.
  if (!path.startsWith('/') || path.startsWith('//')) return { status: 400 };
  if (!path.startsWith(prefix)) return { status: 404 };
  const file = normalize(join(dist, path.slice(prefix.length) || 'index.html'));
  if (!file.startsWith(dist + sep)) return { status: 404 };
  return { status: 200, file };
}

export function contentType(file: string): string {
  return TYPES[extname(file)] ?? 'application/octet-stream';
}

if (import.meta.main) {
  const dist = join(process.cwd(), 'dist');
  const prefix = normalizePrefix(process.env.INGRESS_PATH ?? '/');
  const port = Number(process.env.PORT ?? 4173);
  createServer((req, res) => {
    const r = resolve(req.method ?? '', req.url ?? '', prefix, dist);
    if (r.status !== 200) {
      res.writeHead(r.status).end();
      return;
    }
    readFile(r.file).then(
      (body) => {
        res.writeHead(200, {
          'Content-Type': contentType(r.file),
          'Content-Security-Policy': CSP,
          'X-Content-Type-Options': 'nosniff',
          'Referrer-Policy': 'no-referrer',
          'Cache-Control': 'no-store',
        });
        res.end(req.method === 'HEAD' ? undefined : body);
      },
      () => res.writeHead(404).end(),
    );
  }).listen(port, '127.0.0.1', () => console.log(`serving ${dist} at http://127.0.0.1:${port}${prefix}`));
}
