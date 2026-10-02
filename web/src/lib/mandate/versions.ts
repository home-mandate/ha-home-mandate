// SPDX-License-Identifier: AGPL-3.0-or-later

// Versions of a mandate: the server lists them newest first; a version's number is its
// number, counted by the server from the oldest ("v1"). A digest is a hash of the content,
// so two versions can share one (a restored version has the digest of the one it
// restores): versions are told apart by their number, never by their digest.

import type { MandateDetail, MandateDocument, MandateDraft, MandateVersion } from '../api/types.ts';

const SHORT_LENGTH = 8;

/** shortDigest returns the first characters of a digest without its "sha256:" prefix. */
export function shortDigest(digest: string): string {
  return digest.replace(/^sha256:/, '').slice(0, SHORT_LENGTH);
}

/** currentNumber returns the number of the current version, the first of the list; 0 without versions. */
export function currentNumber(versions: readonly MandateVersion[]): number {
  return versions[0]?.number ?? 0;
}

/** versionAt finds a version by its number; undefined if the list has no such version. */
export function versionAt(versions: readonly MandateVersion[], number: number): MandateVersion | undefined {
  return versions.find((v) => v.number === number);
}

/** draftOf returns the part of a stored version that a human edits. */
export function draftOf(document: MandateDocument): MandateDraft {
  return {
    rules: document.rules,
    approval: document.approval,
    limits: document.limits,
    valid_from: document.valid_from,
    ...(document.expires === undefined ? {} : { expires: document.expires }),
  };
}

/**
 * An edit that is not saved yet. It survives a look at the versions or the audit log (the
 * editor keeps it in AppState while the page is open) and is gone with a reload, where
 * the browser asks first.
 */
export interface UnsavedMandate {
  /** The version the edit started from. */
  stored: MandateDetail;
  name: string;
  draft: MandateDraft;
}

/**
 * restoredDraft is what restoring an earlier version stores: its rules, approval settings
 * and rate limit. The validity (valid from, valid until) stays as it is now, as does the
 * name: restoring is meant to bring back the rules of that time, not a period that may
 * be over or end later than intended. Applying a template behaves the same.
 */
export function restoredDraft(current: MandateDraft, earlier: MandateDraft): MandateDraft {
  return { ...current, rules: earlier.rules, approval: earlier.approval, limits: earlier.limits };
}
