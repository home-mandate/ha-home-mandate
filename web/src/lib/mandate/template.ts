// SPDX-License-Identifier: AGPL-3.0-or-later

// Templates as the UI names them (docs/ARCHITECTURE.md section 6): a base template by its
// title and description in the UI language, the household's own by the name its author
// gave it. Names of new templates are checked as the server does: a pattern, and the
// prefix "hm-" belongs to the base templates.

import type { ApiClient } from '../api/client.ts';
import type { Language, LocalizedText, MandateDraft, Template, TemplateSummary } from '../api/types.ts';
import { getLocale } from '../paraglide/runtime.js';
import { cleanUntrusted } from '../untrusted.ts';

/** An unsaved edit of a template, kept while the app is open (as for mandates). */
export interface UnsavedTemplate {
  /** The template the edit started from; null for a new one. */
  stored: Template | null;
  origin: MandateDraft;
  draft: MandateDraft;
}

/** Key of the new template's edit in AppState.unsavedTemplates: no template name looks like it. */
export const NEW_TEMPLATE_KEY = '_new';

/** internal/api templateNamePattern. */
export const TEMPLATE_NAME = /^[a-z0-9][a-z0-9-]{0,31}$/;
export const TEMPLATE_NAME_MAX = 32;
/** Names starting with this are reserved for base templates. */
export const RESERVED_PREFIX = 'hm-';

type Named = Pick<TemplateSummary, 'name' | 'builtin' | 'title'>;

/** The text in the UI language, else in English, else empty. */
function localized(text: LocalizedText): string {
  const lang = getLocale() as Language;
  return cleanUntrusted(text[lang] ?? text.en ?? '');
}

/** templateTitle shows a base template by its title, any other by its own name. */
export function templateTitle(t: Named): string {
  return (t.builtin && localized(t.title)) || cleanUntrusted(t.name);
}

/** templateDescription is a base template's description; empty for the household's own. */
export function templateDescription(t: Pick<TemplateSummary, 'builtin' | 'description'>): string {
  return t.builtin ? localized(t.description) : '';
}

/** titleOf finds a template's title by name, or shows the name if the template is not known. */
export function titleOf(name: string, templates: readonly Named[]): string {
  const t = templates.find((x) => x.name === name);
  return t ? templateTitle(t) : cleanUntrusted(name);
}

/**
 * cautiousChoice is the template to preselect when an agent is admitted (decision G1): the
 * first that grants nothing, else the first base template (the most cautious comes first),
 * else the first; "" without templates. Never by name.
 */
export function cautiousChoice(templates: readonly Pick<Template, 'name' | 'builtin' | 'draft'>[]): string {
  const pick = templates.find((t) => t.draft.rules.length === 0) ?? templates.find((t) => t.builtin) ?? templates[0];
  return pick?.name ?? '';
}

export type NameProblem = 'format' | 'reserved' | 'taken';

/** nameProblem checks the name of a new template; null when the server would take it. */
export function nameProblem(name: string, existing: readonly Pick<TemplateSummary, 'name'>[]): NameProblem | null {
  if (name.startsWith(RESERVED_PREFIX)) return 'reserved';
  if (!TEMPLATE_NAME.test(name)) return 'format';
  return existing.some((t) => t.name === name) ? 'taken' : null;
}

/**
 * offeredTemplates loads the templates a mandate can be made from, with their rules: every
 * template but hidden base templates, which the server refuses. One that cannot be read
 * (removed meanwhile) is left out; a failing list fails.
 */
export async function offeredTemplates(api: Pick<ApiClient, 'templates' | 'template'>): Promise<Template[]> {
  const list = (await api.templates()).filter((t) => !t.hidden);
  const loaded = await Promise.all(list.map((t) => api.template(t.name).catch(() => null)));
  return loaded.filter((t): t is Template => t !== null && !t.hidden);
}
