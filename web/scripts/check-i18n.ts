// SPDX-License-Identifier: AGPL-3.0-or-later

// i18n checks for every commit (docs/TESTING.md section 5): same keys in every catalog,
// no orphaned or unknown keys, same placeholders, and no hard-coded visible text in
// Svelte markup. Run with: node scripts/check-i18n.ts

import { readFileSync, readdirSync } from 'node:fs';
import { join, relative } from 'node:path';
import { parse } from 'svelte/compiler';

export type Catalog = Record<string, unknown>;

/** Attributes whose static values are shown to users or read by assistive technology. */
const VISIBLE_ATTRIBUTES = new Set(['title', 'alt', 'placeholder', 'aria-label', 'aria-description', 'aria-placeholder', 'label']);
const LETTER = /\p{L}/u;

/** compareKeys reports keys missing from a catalog compared to the union of all. */
export function compareKeys(catalogs: Record<string, Catalog>): string[] {
  const all = new Set(Object.values(catalogs).flatMap((c) => messageKeys(c)));
  const problems: string[] = [];
  for (const [locale, catalog] of Object.entries(catalogs)) {
    const keys = new Set(messageKeys(catalog));
    for (const key of [...all].sort()) {
      if (!keys.has(key)) problems.push(`${locale}: missing key "${key}"`);
    }
  }
  return problems;
}

/** comparePlaceholders reports keys whose placeholders differ between catalogs. */
export function comparePlaceholders(catalogs: Record<string, Catalog>): string[] {
  const entries = Object.entries(catalogs);
  const [first, ...rest] = entries;
  if (!first) return [];
  const problems: string[] = [];
  for (const key of messageKeys(first[1]).sort()) {
    const want = placeholders(first[1][key]);
    for (const [locale, catalog] of rest) {
      if (!(key in catalog)) continue;
      const got = placeholders(catalog[key]);
      if (got.join(',') !== want.join(',')) {
        problems.push(`${locale}: "${key}" has placeholders {${got.join('}, {')}}, ${first[0]} has {${want.join('}, {')}}`);
      }
    }
  }
  return problems;
}

/** compareUsage reports catalog keys never used in the sources and used keys that do not exist. */
export function compareUsage(catalog: Catalog, sources: Record<string, string>): string[] {
  const keys = new Set(messageKeys(catalog));
  const used = new Map<string, string>();
  for (const [file, source] of Object.entries(sources)) {
    for (const key of usedKeys(source)) used.set(key, file);
  }
  const problems: string[] = [];
  for (const key of [...keys].sort()) {
    if (!used.has(key)) problems.push(`orphaned key "${key}"`);
  }
  for (const [key, file] of used) {
    if (!keys.has(key)) problems.push(`${file}: unknown key "${key}"`);
  }
  return problems;
}

/** usedKeys finds m.key(…) and m["key"](…) calls. */
export function usedKeys(source: string): string[] {
  const keys: string[] = [];
  for (const match of source.matchAll(/\bm\.([A-Za-z_$][\w$]*)\s*\(|\bm\[\s*['"]([^'"]+)['"]\s*\]/g)) {
    keys.push((match[1] ?? match[2]) as string);
  }
  return keys;
}

/** findHardcodedText reports visible text in Svelte markup that is not from a catalog. */
export function findHardcodedText(source: string, file: string): string[] {
  const ast = parse(source, { modern: true, filename: file });
  const problems: string[] = [];
  const visit = (node: unknown): void => {
    if (!node || typeof node !== 'object') return;
    if (Array.isArray(node)) {
      node.forEach(visit);
      return;
    }
    const n = node as Record<string, unknown>;
    if (n.type === 'Text' && typeof n.data === 'string' && LETTER.test(n.data)) {
      problems.push(`${file}: hard-coded text "${n.data.trim()}"`);
    }
    if (n.type === 'Attribute') {
      // Only visible attributes count; src, class, href and the like are not text.
      if (typeof n.name !== 'string' || !VISIBLE_ATTRIBUTES.has(n.name)) return;
      const values = Array.isArray(n.value) ? n.value : [];
      for (const v of values as Record<string, unknown>[]) {
        if (v.type === 'Text' && typeof v.data === 'string' && LETTER.test(v.data)) {
          problems.push(`${file}: hard-coded ${n.name} "${v.data.trim()}"`);
        }
      }
      return;
    }
    for (const [key, value] of Object.entries(n)) {
      if (key !== 'parent' && key !== 'metadata') visit(value);
    }
  };
  visit(ast.fragment);
  return problems;
}

function messageKeys(catalog: Catalog): string[] {
  return Object.keys(catalog).filter((k) => k !== '$schema');
}

function placeholders(message: unknown): string[] {
  const names = new Set<string>();
  const walk = (v: unknown): void => {
    if (typeof v === 'string') {
      for (const match of v.matchAll(/\{([A-Za-z_][\w]*)\}/g)) names.add(match[1] as string);
    } else if (v && typeof v === 'object') {
      Object.values(v).forEach(walk);
    }
  };
  walk(message);
  return [...names].sort();
}

function listFiles(dir: string, skip: string): string[] {
  return readdirSync(dir, { recursive: true, withFileTypes: true })
    .filter((e) => e.isFile())
    .map((e) => join(e.parentPath, e.name))
    .filter((p) => !p.startsWith(skip));
}

export function run(root: string): string[] {
  const settings = JSON.parse(readFileSync(join(root, 'project.inlang/settings.json'), 'utf8')) as {
    locales: string[];
    baseLocale: string;
  };
  const catalogs: Record<string, Catalog> = {};
  for (const locale of settings.locales) {
    catalogs[locale] = JSON.parse(readFileSync(join(root, 'messages', `${locale}.json`), 'utf8')) as Catalog;
  }
  const files = listFiles(join(root, 'src'), join(root, 'src/lib/paraglide')).filter(
    (f) => /\.(svelte|ts)$/.test(f) && !f.endsWith('.test.ts'),
  );
  const sources = Object.fromEntries(files.map((f) => [relative(root, f), readFileSync(f, 'utf8')]));
  return [
    ...compareKeys(catalogs),
    ...comparePlaceholders(catalogs),
    ...compareUsage(catalogs[settings.baseLocale] ?? {}, sources),
    ...Object.entries(sources)
      .filter(([f]) => f.endsWith('.svelte'))
      .flatMap(([f, s]) => findHardcodedText(s, f)),
  ];
}

if (import.meta.main) {
  const problems = run(process.cwd());
  for (const p of problems) console.error(p);
  if (problems.length > 0) process.exit(1);
  console.log('i18n: ok');
}
