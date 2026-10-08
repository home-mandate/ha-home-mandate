// SPDX-License-Identifier: AGPL-3.0-or-later

// Hash routing: Home Assistant Ingress serves the UI under a per-installation path, so the
// router must not depend on the base path (docs/ARCHITECTURE.md section 12). Routes of the
// design handoff (README section 1); anything else is not_found. Identifiers in the hash
// are encoded path segments; client IDs are often URLs.

import { TEMPLATE_NAME } from './mandate/template.ts';

export type SettingsSection = 'approvers' | 'critical' | 'defaults' | 'ha' | 'mcp' | 'retention' | 'estop' | 'about';

export type Route =
  | { name: 'overview' }
  /** add: the choice of ways to connect an agent is open on arrival; every entry point to connect one leads here. */
  | { name: 'agents'; add?: true }
  | { name: 'pair' }
  | { name: 'connect' }
  | { name: 'agent'; id: string }
  | { name: 'mandates' }
  | { name: 'mandate'; id: string }
  | { name: 'mandate_versions'; id: string }
  | { name: 'templates' }
  /** name: null for a new template. */
  | { name: 'template'; template: string | null }
  | { name: 'audit'; query: Record<string, string[]> }
  | { name: 'audit_entry'; seq: number }
  | { name: 'requests' }
  | { name: 'settings'; section: SettingsSection | null }
  | { name: 'not_found' };

export type Section = 'overview' | 'agents' | 'mandates' | 'audit' | 'settings';

const SECTIONS: readonly SettingsSection[] = ['approvers', 'critical', 'defaults', 'ha', 'mcp', 'retention', 'estop', 'about'];
const MANDATE_ID = /^[A-Za-z0-9_-]{4,64}$/;
/** The new template has a segment no template name can take. */
const NEW_TEMPLATE = '_new';
const SEQ = /^[1-9]\d{0,14}$/;
const NOT_FOUND: Route = { name: 'not_found' };

function decode(segment: string): string | null {
  try {
    const value = decodeURIComponent(segment);
    return value === '' ? null : value;
  } catch {
    return null;
  }
}

function parseQuery(query: string): Record<string, string[]> {
  // No prototype: keys such as __proto__ or constructor are plain keys (security review S5).
  const out: Record<string, string[]> = Object.create(null);
  for (const [key, value] of new URLSearchParams(query)) out[key] = [...(Object.hasOwn(out, key) ? (out[key] ?? []) : []), value];
  return out;
}

/** parseHash maps a location hash such as "#/mandates/m-1" to a route. */
export function parseHash(hash: string): Route {
  const [path = '', query = ''] = (hash.replace(/^#/, '') || '/').split('?', 2);
  if (!path.startsWith('/')) return NOT_FOUND;
  const parts = path.split('/').slice(1);
  const [first = '', second, third, ...rest] = parts;
  if (rest.length > 0) return NOT_FOUND;
  if (third !== undefined) {
    // An agent lives under "id/": its ID may equal a page name such as "pair" (security review S4).
    if (first === 'agents' && second === 'id') {
      const id = decode(third);
      return id === null ? NOT_FOUND : { name: 'agent', id };
    }
    const versions = first === 'mandates' && third === 'versions' && second !== undefined && MANDATE_ID.test(second);
    return versions ? { name: 'mandate_versions', id: second } : NOT_FOUND;
  }
  if (second === undefined) {
    switch (first) {
      case '':
        return { name: 'overview' };
      case 'agents':
        return new URLSearchParams(query).has('add') ? { name: 'agents', add: true } : { name: 'agents' };
      case 'mandates':
        return { name: 'mandates' };
      case 'templates':
        return { name: 'templates' };
      case 'audit':
        return { name: 'audit', query: parseQuery(query) };
      case 'settings':
        return { name: 'settings', section: null };
      default:
        return NOT_FOUND;
    }
  }
  switch (first) {
    case 'agents':
      if (second === 'pair') return { name: 'pair' };
      if (second === 'browser') return { name: 'connect' };
      return NOT_FOUND;
    case 'mandates':
      return MANDATE_ID.test(second) ? { name: 'mandate', id: second } : NOT_FOUND;
    case 'templates':
      if (second === NEW_TEMPLATE) return { name: 'template', template: null };
      return TEMPLATE_NAME.test(second) ? { name: 'template', template: second } : NOT_FOUND;
    case 'audit':
      if (second === 'requests') return { name: 'requests' };
      return SEQ.test(second) ? { name: 'audit_entry', seq: Number(second) } : NOT_FOUND;
    case 'settings':
      return SECTIONS.includes(second as SettingsSection) ? { name: 'settings', section: second as SettingsSection } : NOT_FOUND;
    default:
      return NOT_FOUND;
  }
}

/** href returns the hash of a route; parseHash(href(r)) gives r back. */
export function href(route: Route): string {
  switch (route.name) {
    case 'overview':
    case 'not_found':
      return '#/';
    case 'agents':
      return route.add ? '#/agents?add' : '#/agents';
    case 'pair':
      return '#/agents/pair';
    case 'connect':
      return '#/agents/browser';
    case 'agent':
      return `#/agents/id/${encodeURIComponent(route.id)}`;
    case 'mandates':
      return '#/mandates';
    case 'mandate':
      return `#/mandates/${encodeURIComponent(route.id)}`;
    case 'mandate_versions':
      return `#/mandates/${encodeURIComponent(route.id)}/versions`;
    case 'templates':
      return '#/templates';
    case 'template':
      return `#/templates/${route.template === null ? NEW_TEMPLATE : encodeURIComponent(route.template)}`;
    case 'audit': {
      const params = new URLSearchParams();
      for (const [key, values] of Object.entries(route.query)) for (const v of values) params.append(key, v);
      const q = params.toString();
      return q ? `#/audit?${q}` : '#/audit';
    }
    case 'audit_entry':
      return `#/audit/${route.seq}`;
    case 'requests':
      return '#/audit/requests';
    case 'settings':
      return route.section ? `#/settings/${route.section}` : '#/settings';
  }
}

/** sectionOf names the navigation tab a route belongs to. */
export function sectionOf(route: Route): Section | null {
  switch (route.name) {
    case 'overview':
      return 'overview';
    case 'agents':
    case 'pair':
    case 'connect':
    case 'agent':
      return 'agents';
    case 'mandates':
    case 'mandate':
    case 'mandate_versions':
    case 'templates':
    case 'template':
      return 'mandates';
    case 'audit':
    case 'audit_entry':
    case 'requests':
      return 'audit';
    case 'settings':
      return 'settings';
    case 'not_found':
      return null;
  }
}
