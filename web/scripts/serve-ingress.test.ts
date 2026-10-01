// SPDX-License-Identifier: AGPL-3.0-or-later

import { join, sep } from 'node:path';
import { describe, expect, it } from 'vitest';
import { contentType, normalizePrefix, resolve } from './serve-ingress.ts';

const dist = `${sep}srv${sep}dist`;
const prefix = '/api/hassio_ingress/tok/';

describe('resolve', () => {
  it.each([
    ['GET', '/api/hassio_ingress/tok/', join(dist, 'index.html')],
    ['GET', '/api/hassio_ingress/tok/?x=1#y', join(dist, 'index.html')],
    ['HEAD', '/api/hassio_ingress/tok/assets/a.js', join(dist, 'assets', 'a.js')],
  ])('%s %s → file', (method, target, file) => {
    expect(resolve(method, target, prefix, dist)).toEqual({ status: 200, file });
  });

  it.each([
    ['POST', '/api/hassio_ingress/tok/', 405],
    ['GET', 'http://[', 400],
    ['GET', 'http://evil/api/hassio_ingress/tok/', 400],
    ['GET', '//foo/api/hassio_ingress/tok/index.html', 400],
    ['GET', '', 400],
    ['GET', '/index.html', 404],
    ['GET', '/api/hassio_ingress/tokindex.html', 404],
    ['GET', '/api/hassio_ingress/tok/../../etc/passwd', 404],
    ['GET', '/api/hassio_ingress/tok/..', 404],
  ])('%s %s → %i', (method, target, status) => {
    expect(resolve(method, target, prefix, dist)).toEqual({ status });
  });
});

describe('normalizePrefix', () => {
  it('adds a trailing slash', () => {
    expect(normalizePrefix('/api/x')).toBe('/api/x/');
    expect(normalizePrefix('/')).toBe('/');
  });
  it.each(['api/x', '//host/x', '/api/../x'])('rejects %s', (p) => {
    expect(() => normalizePrefix(p)).toThrow();
  });
});

describe('contentType', () => {
  it('maps known extensions and falls back to octet-stream', () => {
    expect(contentType('a.js')).toBe('text/javascript; charset=utf-8');
    expect(contentType('a.bin')).toBe('application/octet-stream');
  });
});
