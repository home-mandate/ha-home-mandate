// SPDX-License-Identifier: AGPL-3.0-or-later

// What the entries of audit/v1 say (SPEC-v0 sections 9.1 and 11.7): why a sign-in was
// rejected, the key an agent was bound to, tokens withdrawn, certificates of the public
// name, the state of their monitoring, settings and exports. Values from outside (names and
// issuers of certificates, settings) are cleaned; an unknown code is shown cleaned too.

import type { AuditEntry, AuthReason, ConfigSetting, CTMonitoring } from '../api/types.ts';
import { m } from '../i18n.ts';
import { cleanUntrusted, isolate } from '../untrusted.ts';

/** How much of a key or digest a sentence shows; the detail shows it whole. */
const SHORT = 12;

const AUTH: Record<AuthReason, () => string> = {
  unknown: () => m.auth_reason_unknown(),
  expired: () => m.auth_reason_expired(),
  withdrawn: () => m.auth_reason_withdrawn(),
  revoked: () => m.auth_reason_revoked(),
  audience: () => m.auth_reason_audience(),
  client: () => m.auth_reason_client(),
  proof: () => m.auth_reason_proof(),
  replay: () => m.auth_reason_replay(),
  unbound_refused: () => m.auth_reason_unbound_refused(),
  second_key: () => m.auth_reason_second_key(),
  not_approver: () => m.auth_reason_not_approver(),
  other: () => m.auth_reason_other(),
};

const CAUSES: Record<NonNullable<AuditEntry['credentials']>['cause'], () => string> = {
  emergency_stop: () => m.credentials_cause_emergency_stop(),
  public_origin: () => m.credentials_cause_public_origin(),
  user: () => m.credentials_cause_user(),
  refresh_reuse: () => m.credentials_cause_refresh_reuse(),
  bound: () => m.credentials_cause_bound(),
};

const CERT: Record<NonNullable<NonNullable<AuditEntry['certificate']>['status']>, () => string> = {
  alarm: () => m.cert_status_alarm(),
  not_yet_valid: () => m.cert_status_not_yet_valid(),
  past_exposure: () => m.cert_status_past_exposure(),
};

const CT: Record<CTMonitoring, () => string> = {
  ok: () => m.ct_status_ok(),
  limited: () => m.ct_status_limited(),
  unavailable: () => m.ct_status_unavailable(),
};

const SETTINGS: Record<ConfigSetting, () => string> = {
  timezone: () => m.setting_timezone(),
  clock_check: () => m.setting_clock_check(),
  checkpoint_key: () => m.setting_checkpoint_key(),
  public_origin: () => m.setting_public_origin(),
  remote_access: () => m.setting_remote_access(),
  unbound_tokens: () => m.setting_unbound_tokens(),
  server_key: () => m.setting_server_key(),
  trusted_proxy: () => m.setting_trusted_proxy(),
};

/**
 * known looks a code up; a code the UI does not know is shown cleaned. An entry comes from
 * a log anyone with the data directory could have written: a code that is no string is shown
 * as text, never trusted to be one.
 */
function known(texts: Readonly<Record<string, () => string>>, code: unknown): string {
  const text = String(code);
  return Object.hasOwn(texts, text) ? texts[text]!() : cleanUntrusted(text);
}

/** short is the beginning of a key or digest, enough to tell it apart in a sentence. */
export function short(value: unknown): string {
  const clean = cleanUntrusted(typeof value === 'string' ? value : '');
  return clean.length > SHORT ? `${clean.slice(0, SHORT)}…` : clean;
}

/** names are the names of a certificate, cleaned and isolated; nothing if they are no list. */
function names(c: NonNullable<AuditEntry['certificate']>): string {
  const list: unknown = c.names;
  if (!Array.isArray(list)) return '';
  return list.filter((n): n is string => typeof n === 'string').map((n) => isolate(cleanUntrusted(n))).join(', ');
}

/** certValidity is the validity of a certificate, or null if a time cannot be read. */
export function certValidity(c: NonNullable<AuditEntry['certificate']>): { from: Date; to: Date } | null {
  const from = new Date(String(c.not_before));
  const to = new Date(String(c.not_after));
  return Number.isNaN(from.getTime()) || Number.isNaN(to.getTime()) ? null : { from, to };
}

/** ctText says a state of Certificate Transparency monitoring. */
export function ctText(status: CTMonitoring): string {
  return known(CT, status);
}

/** v1Text is the sentence of an entry of audit/v1; empty for every other entry. */
export function v1Text(entry: AuditEntry): string {
  switch (entry.event) {
    case 'auth.rejected':
      return entry.auth ? known(AUTH, entry.auth.reason) : '';
    case 'agent.bound': {
      const key = short(entry.binding?.jkt);
      return key ? m.v1_key({ key }) : '';
    }
    case 'credentials.withdrawn': {
      const c = entry.credentials;
      if (!c) return '';
      const cause = known(CAUSES, c.cause);
      return c.scope === 'all' ? m.credentials_all({ cause }) : m.credentials_agent({ cause });
    }
    case 'cert.unexpected': {
      const c = entry.certificate;
      if (!c) return '';
      return c.status === undefined ? names(c) : m.cert_unexpected_text({ names: names(c), status: known(CERT, c.status) });
    }
    case 'cert.acknowledged': {
      const id = short(entry.certificate?.digest ?? entry.certificate?.public_key);
      return id ? m.v1_certificate({ digest: id }) : '';
    }
    case 'ct.status':
      return entry.ct ? ctText(entry.ct.status) : '';
    case 'config.changed':
      return entry.config ? configText(entry.config) : '';
    case 'log.exported':
      return entry.exported ? m.log_exported_range({ from: entry.exported.from_seq, to: entry.exported.to_seq }) : m.log_exported_all();
    default:
      return '';
  }
}

function configText(c: NonNullable<AuditEntry['config']>): string {
  const setting = known(SETTINGS, c.setting);
  const value = isolate(cleanUntrusted(String(c.value ?? '')));
  const previous = isolate(cleanUntrusted(String(c.previous_value ?? '')));
  if (c.value === undefined) return m.config_removed_text({ setting, previous });
  if (c.previous_value === undefined) return m.config_set_text({ setting, value });
  return m.config_changed_text({ setting, previous, value });
}
