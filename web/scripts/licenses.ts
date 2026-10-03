// SPDX-License-Identifier: AGPL-3.0-or-later

// licenses.txt next to the built page (decision S8): the license of every npm package
// whose code ends up in the bundle, found from the bundle itself (no list to keep in
// step), plus the icon path data. The page links to it from "About"; nothing loads it.

import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import type { Plugin } from 'vite';

export interface PackageLicense {
  name: string;
  version: string;
  license: string;
  text: string;
}

const PACKAGE = /[\\/]node_modules[\\/](?:\.pnpm[\\/][^\\/]+[\\/]node_modules[\\/])?((?:@[^\\/]+[\\/])?[^\\/]+)[\\/]/;
const LICENSE_FILE = /^(licen[cs]e|copying)(\.(md|txt))?$/i;
const RULE = '='.repeat(72);

/** packageOf names the npm package a bundled module comes from, or null for own code. */
export function packageOf(moduleId: string): { name: string; root: string } | null {
  const match = PACKAGE.exec(moduleId);
  if (!match || match.index === undefined) return null;
  const name = match[1]!.replace(/\\/g, '/');
  return { name, root: moduleId.slice(0, match.index + match[0].length - 1) };
}

function readPackage(root: string): PackageLicense | null {
  const manifest = join(root, 'package.json');
  if (!existsSync(manifest)) return null;
  const pkg = JSON.parse(readFileSync(manifest, 'utf8')) as { name: string; version: string; license?: string };
  const file = readdirSync(root).find((f) => LICENSE_FILE.test(f));
  return { name: pkg.name, version: pkg.version, license: pkg.license ?? 'see text', text: file ? readFileSync(join(root, file), 'utf8').trim() : '' };
}

/** licenseText writes the file: one block per package, sorted by name, then the extras. */
export function licenseText(packages: readonly PackageLicense[], extras: readonly string[]): string {
  const blocks = [...packages]
    .sort((a, b) => a.name.localeCompare(b.name))
    .map((p) => `${p.name} ${p.version} (${p.license})\n\n${p.text}`.trim());
  return `${['Home-Mandate includes the following third-party code.', ...blocks, ...extras.map((e) => e.trim())].join(`\n\n${RULE}\n\n`)}\n`;
}

/**
 * licenses is the Vite plugin. extraFiles are added as they are (e.g. the icons' license);
 * generated are packages whose generated code is bundled from outside node_modules (the
 * Paraglide runtime in src/lib/paraglide).
 */
export function licenses(extraFiles: readonly string[], generated: readonly string[] = []): Plugin {
  return {
    name: 'home-mandate-licenses',
    apply: 'build',
    generateBundle(_, bundle) {
      const roots = new Map<string, string>(generated.map((name) => [name, join(process.cwd(), 'node_modules', name)]));
      for (const chunk of Object.values(bundle)) {
        if (chunk.type !== 'chunk') continue;
        for (const id of chunk.moduleIds) {
          const found = packageOf(id);
          if (found && !roots.has(found.name)) roots.set(found.name, found.root);
        }
      }
      const packages = [...roots.values()].map(readPackage).filter((p): p is PackageLicense => p !== null);
      const extras = extraFiles.map((f) => readFileSync(f, 'utf8'));
      this.emitFile({ type: 'asset', fileName: 'licenses.txt', source: licenseText(packages, extras) });
    },
  };
}

export const ICONS_LICENSE = join(dirname(new URL(import.meta.url).pathname), '..', 'src', 'lib', 'icons', 'LICENSE-mdi.txt');
