// SPDX-License-Identifier: AGPL-3.0-or-later

// licenses.txt next to the built page (decision S8): the license of every npm package
// whose code ends up in the bundle, found from the bundle itself (no list to keep in
// step), plus the icon path data. The page links to it from "About"; nothing loads it.
// A bundled package without a license text stops the build: its notice must ship.

import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import type { Plugin } from 'vite';

export interface PackageLicense {
  name: string;
  version: string;
  license: string;
  text: string;
}

const PACKAGE = /[\\/]node_modules[\\/](?:\.pnpm[\\/][^\\/]+[\\/]node_modules[\\/])?((?:@[^\\/]+[\\/])?[^\\/]+)[\\/]/;
/** LICENSE, LICENCE, COPYING, also with a suffix (LICENSE-MIT, LICENSE.md), and NOTICE. */
const LICENSE_FILE = /^(licen[cs]e|copying)([-.].*)?$/i;
const NOTICE_FILE = /^notice([-.].*)?$/i;
const RULE = '='.repeat(72);

/** packageOf names the npm package a bundled module comes from, or null for own code. */
export function packageOf(moduleId: string): { name: string; root: string } | null {
  const match = PACKAGE.exec(moduleId);
  if (!match || match.index === undefined) return null;
  const name = match[1]!.replace(/\\/g, '/');
  return { name, root: moduleId.slice(0, match.index + match[0].length - 1) };
}

/** licenseName reads the license field, also in the old object and array forms. */
export function licenseName(field: unknown): string {
  const one = (x: unknown) => (typeof x === 'string' ? x : x && typeof x === 'object' && 'type' in x ? String((x as { type: unknown }).type) : '');
  if (Array.isArray(field)) return field.map(one).filter(Boolean).join(' OR ') || 'see text';
  return one(field) || 'see text';
}

/** readPackage reads a package's name, version, license and its license and notice texts. */
export function readPackage(root: string): PackageLicense {
  const manifest = join(root, 'package.json');
  if (!existsSync(manifest)) throw new Error(`licenses: no package.json in ${root}`);
  let pkg: { name?: string; version?: string; license?: unknown; licenses?: unknown };
  try {
    pkg = JSON.parse(readFileSync(manifest, 'utf8')) as typeof pkg;
  } catch (err) {
    throw new Error(`licenses: unreadable package.json in ${root}`, { cause: err });
  }
  const files = readdirSync(root).sort();
  const texts = [...files.filter((f) => LICENSE_FILE.test(f)), ...files.filter((f) => NOTICE_FILE.test(f))].map((f) => readFileSync(join(root, f), 'utf8').trim());
  return { name: pkg.name ?? root, version: pkg.version ?? '?', license: licenseName(pkg.license ?? pkg.licenses), text: texts.join('\n\n') };
}

const byName = (a: PackageLicense, b: PackageLicense) => (a.name < b.name ? -1 : a.name > b.name ? 1 : a.version < b.version ? -1 : 1);

/** licenseText writes the file: one block per package, sorted by name (the same on every machine), then the extras. */
export function licenseText(packages: readonly PackageLicense[], extras: readonly string[]): string {
  const blocks = [...packages].sort(byName).map((p) => `${p.name} ${p.version} (${p.license})\n\n${p.text}`.trim());
  return `${['Home-Mandate includes the following third-party code.', ...blocks, ...extras.map((e) => e.trim())].join(`\n\n${RULE}\n\n`)}\n`;
}

/**
 * licenses is the Vite plugin. extraFiles are added as they are (e.g. the icons' license);
 * generated are packages whose generated code is bundled from outside node_modules (the
 * Paraglide runtime in src/lib/paraglide).
 */
export function licenses(extraFiles: readonly string[], generated: readonly string[] = []): Plugin {
  let root = process.cwd();
  return {
    name: 'home-mandate-licenses',
    apply: 'build',
    configResolved(config) {
      root = config.root;
    },
    generateBundle(_, bundle) {
      // By directory: two versions of one package are two entries.
      const roots = new Set<string>(generated.map((name) => join(root, 'node_modules', name)));
      for (const chunk of Object.values(bundle)) {
        if (chunk.type !== 'chunk') continue;
        for (const id of chunk.moduleIds) {
          const found = packageOf(id);
          if (found) roots.add(found.root);
        }
      }
      const packages = [...roots].map(readPackage);
      const missing = packages.filter((p) => p.text === '').map((p) => p.name);
      if (missing.length > 0) this.error(`licenses: no license text for ${missing.join(', ')}`);
      const extras = extraFiles.map((f) => readFileSync(f, 'utf8'));
      this.emitFile({ type: 'asset', fileName: 'licenses.txt', source: licenseText(packages, extras) });
    },
  };
}
