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

const dist = join(process.cwd(), 'dist');
const prefix = process.env.INGRESS_PATH ?? '/';
const port = Number(process.env.PORT ?? 4173);

createServer(async (req, res) => {
  const url = new URL(req.url ?? '/', 'http://localhost');
  if (!url.pathname.startsWith(prefix)) {
    res.writeHead(404).end();
    return;
  }
  const rel = url.pathname.slice(prefix.length) || 'index.html';
  const file = normalize(join(dist, rel));
  if (!file.startsWith(dist + sep)) {
    res.writeHead(404).end();
    return;
  }
  try {
    const body = await readFile(file);
    res.writeHead(200, {
      'Content-Type': TYPES[extname(file)] ?? 'application/octet-stream',
      'Content-Security-Policy': CSP,
      'X-Content-Type-Options': 'nosniff',
    });
    res.end(body);
  } catch {
    res.writeHead(404).end();
  }
}).listen(port, '127.0.0.1', () => console.log(`serving ${dist} at http://127.0.0.1:${port}${prefix}`));
