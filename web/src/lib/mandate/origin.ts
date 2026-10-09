// SPDX-License-Identifier: AGPL-3.0-or-later

// Where the rules of a mandate came from (#16): the template they were last taken from,
// per version template or edit, and the proposal to rename a mandate that still carries
// the name of the template it came from. Display only: the server keeps the origin and
// never evaluates it.

import type { MandateVersion, RulesFrom, TemplateSummary } from '../api/types.ts';
import { formatDateTime, type FormatContext } from '../format.ts';
import { m } from '../i18n.ts';
import { titleOf } from './template.ts';

type Named = Pick<TemplateSummary, 'name' | 'builtin' | 'title'>;

/** A template's title isolated from the text around it: its name may be in any script. */
const titled = (name: string, templates: readonly Named[]) => `\u2068${titleOf(name, templates)}\u2069`;

/** rulesFromText says from which template and when the rules were last taken; "" if unknown. */
export function rulesFromText(from: RulesFrom | null, templates: readonly Named[], ctx: FormatContext): string {
  if (from === null) return '';
  const values = { template: titled(from.template, templates), date: formatDateTime(new Date(from.at), ctx) };
  return from.edited_since ? m.origin_rules_from_edited(values) : m.origin_rules_from(values);
}

/** versionOriginText says where the rules of one version came from; "" for versions of before. */
export function versionOriginText(version: MandateVersion, templates: readonly Named[]): string {
  if (version.origin === 'edit') return m.origin_version_edit();
  if (version.origin === 'template' && version.template !== null) return m.origin_version_template({ template: titled(version.template, templates) });
  return '';
}

/** Every name a template goes by: its name and, for a base template, its titles. */
function namesOf(t: Pick<TemplateSummary, 'name' | 'title'>): string[] {
  return [t.name, ...Object.values(t.title).filter((title): title is string => typeof title === 'string')];
}

/**
 * renameProposal is the name to propose when a template is applied: the agent's name,
 * while the mandate still carries the name of the template its rules came from (any of
 * them for a mandate of before origins were kept). null when the name was chosen by a
 * human or already fits.
 */
export function renameProposal(
  current: string,
  agentName: string,
  from: RulesFrom | null,
  templates: readonly Pick<TemplateSummary, 'name' | 'title'>[],
): string | null {
  const proposed = agentName.trim();
  if (proposed === '' || proposed === current) return null;
  let names: string[];
  if (from === null) names = templates.flatMap(namesOf);
  else {
    const previous = templates.find((t) => t.name === from.template);
    names = previous ? namesOf(previous) : [from.template];
  }
  return names.includes(current) ? proposed : null;
}
