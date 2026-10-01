// SPDX-License-Identifier: AGPL-3.0-or-later

// i18n checks for every commit (docs/TESTING.md section 5): same keys in every catalog,
// no orphaned or unknown keys, same placeholders, no hard-coded visible text in Svelte
// markup, and only logical CSS properties. Run with: node scripts/check-i18n.ts
//
// Limits: string literals in <script> blocks and .ts files are not checked; text built
// there must come from a catalog by review.

import { readFileSync, readdirSync } from 'node:fs';
import { join, relative } from 'node:path';
import { parse } from 'svelte/compiler';

export type Catalog = Record<string, unknown>;

/** Element attributes whose values are shown to users or read by assistive technology. */
const VISIBLE_ATTRIBUTES = new Set([
  'title',
  'alt',
  'placeholder',
  'label',
  'content',
  'aria-label',
  'aria-description',
  'aria-placeholder',
  'aria-roledescription',
  'aria-valuetext',
]);
/** Component props that conventionally carry text. */
const TEXT_PROPS = new Set(['text', 'label', 'heading', 'title', 'description', 'message', 'caption', 'placeholder']);
const BUTTON_INPUT = /^(button|submit|reset)$/i;
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
  for (const match of source.matchAll(/(?<![\w$.])m\.([A-Za-z_$][\w$]*)\s*\(|(?<![\w$.])m\[\s*['"]([^'"]+)['"]\s*\]/g)) {
    keys.push((match[1] ?? match[2]) as string);
  }
  return keys;
}

type Node = Record<string, unknown>;

const isNode = (v: unknown): v is Node => !!v && typeof v === 'object' && !Array.isArray(v);

/** isMessageCall matches m.key(…); its arguments are interpolated values, not text. */
function isMessageCall(n: Node): boolean {
  const callee = n.callee;
  return (
    n.type === 'CallExpression' &&
    isNode(callee) &&
    callee.type === 'MemberExpression' &&
    isNode(callee.object) &&
    callee.object.type === 'Identifier' &&
    callee.object.name === 'm'
  );
}

/** stringsIn returns string literals and template text with letters in an expression. */
function stringsIn(node: unknown): string[] {
  if (Array.isArray(node)) return node.flatMap(stringsIn);
  if (!isNode(node) || isMessageCall(node)) return [];
  const found: string[] = [];
  if (node.type === 'Literal' && typeof node.value === 'string' && LETTER.test(node.value)) found.push(node.value);
  if (node.type === 'TemplateElement' && isNode(node.value)) {
    const cooked = node.value.cooked;
    if (typeof cooked === 'string' && LETTER.test(cooked)) found.push(cooked);
  }
  for (const [key, value] of Object.entries(node)) {
    if (key !== 'parent' && key !== 'metadata' && key !== 'loc') found.push(...stringsIn(value));
  }
  return found;
}

function staticAttribute(element: Node, name: string): string | undefined {
  const attrs = Array.isArray(element.attributes) ? (element.attributes as Node[]) : [];
  const attr = attrs.find((a) => a.type === 'Attribute' && a.name === name);
  const values = attr && Array.isArray(attr.value) ? (attr.value as Node[]) : [];
  return values.length === 1 && values[0]?.type === 'Text' ? String(values[0].data) : undefined;
}

function isTextAttribute(element: Node, name: string): boolean {
  if (VISIBLE_ATTRIBUTES.has(name)) return true;
  if (element.type === 'Component' && TEXT_PROPS.has(name)) return true;
  return name === 'value' && element.name === 'input' && BUTTON_INPUT.test(staticAttribute(element, 'type') ?? '');
}

/** attributeTexts returns hard-coded text in an attribute value, static or as expression. */
function attributeTexts(attr: Node): string[] {
  const values = Array.isArray(attr.value) ? (attr.value as Node[]) : isNode(attr.value) ? [attr.value] : [];
  return values.flatMap((v) => {
    if (v.type === 'Text' && typeof v.data === 'string' && LETTER.test(v.data)) return [v.data];
    if (v.type === 'ExpressionTag') return stringsIn(v.expression);
    return [];
  });
}

/**
 * findHardcodedText reports visible text in Svelte markup that is not from a catalog:
 * text nodes, string literals in {…}, {@const} and {@render}, and text attributes on
 * elements and components. Conditions, classes and other attributes are not text.
 */
export function findHardcodedText(source: string, file: string): string[] {
  const ast = parse(source, { modern: true, filename: file });
  const problems: string[] = [];
  const report = (what: string, texts: string[]) => {
    for (const t of texts) problems.push(`${file}: hard-coded ${what} "${t.trim()}"`);
  };
  const visit = (node: unknown): void => {
    if (Array.isArray(node)) {
      node.forEach(visit);
      return;
    }
    if (!isNode(node)) return;
    switch (node.type) {
      case 'Text':
        if (typeof node.data === 'string' && LETTER.test(node.data)) report('text', [node.data]);
        return;
      case 'ExpressionTag':
      case 'RenderTag':
        report('text', stringsIn(node.expression));
        return;
      case 'ConstTag':
        report('text', stringsIn(node.declaration));
        return;
    }
    if (Array.isArray(node.attributes)) {
      for (const attr of node.attributes as Node[]) {
        if (attr.type === 'Attribute' && typeof attr.name === 'string' && isTextAttribute(node, attr.name)) {
          report(attr.name, attributeTexts(attr));
        }
      }
    }
    for (const [key, value] of Object.entries(node)) {
      if (key !== 'attributes' && key !== 'parent' && key !== 'metadata') visit(value);
    }
  };
  visit(ast.fragment);
  return problems;
}

const PHYSICAL_CSS = [
  /(?:^|[\s;{])((?:margin|padding|border)-(?:left|right)(?:-[a-z]+)?)\s*:/g,
  /(?:^|[\s;{])((?:left|right))\s*:/g,
  /(?:^|[\s;{])((?:text-align|float|clear)\s*:\s*(?:left|right))\b/g,
];

/**
 * findPhysicalCss reports physical direction properties; only logical ones (inline/block)
 * work for right-to-left languages (docs/ARCHITECTURE.md section 12).
 */
export function findPhysicalCss(css: string, file: string): string[] {
  const withoutComments = css.replace(/\/\*[\s\S]*?\*\//g, '');
  return PHYSICAL_CSS.flatMap((re) =>
    [...withoutComments.matchAll(re)].map((m) => `${file}: physical CSS "${m[1]}", use a logical property`),
  );
}

function cssOf(file: string, source: string): string {
  if (file.endsWith('.css')) return source;
  return [...source.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)].map((m) => m[1]).join('\n');
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
    (f) => /\.(svelte|ts|css)$/.test(f) && !f.endsWith('.test.ts'),
  );
  const sources = Object.fromEntries(files.map((f) => [relative(root, f), readFileSync(f, 'utf8')]));
  return [
    ...compareKeys(catalogs),
    ...comparePlaceholders(catalogs),
    ...compareUsage(catalogs[settings.baseLocale] ?? {}, sources),
    ...Object.entries(sources)
      .filter(([f]) => f.endsWith('.svelte'))
      .flatMap(([f, s]) => findHardcodedText(s, f)),
    ...Object.entries(sources).flatMap(([f, s]) => findPhysicalCss(cssOf(f, s), f)),
  ];
}

if (import.meta.main) {
  const problems = run(process.cwd());
  for (const p of problems) console.error(p);
  if (problems.length > 0) process.exit(1);
  console.log('i18n: ok');
}
