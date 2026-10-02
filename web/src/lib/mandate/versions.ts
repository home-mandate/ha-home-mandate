// SPDX-License-Identifier: AGPL-3.0-or-later

// Versions of a mandate: the server lists them newest first; a version's number is its
// position counted from the oldest ("v1"). A digest is a hash of the content, so two
// versions can share one (a restored version has the digest of the one it restores):
// versions are told apart by their number, never by their digest.

import type { MandateDetail, MandateDocument, MandateDraft, MandateVersion } from '../api/types.ts';

const SHORT_LENGTH = 8;

/** shortDigest returns the first characters of a digest without its "sha256:" prefix. */
export function shortDigest(digest: string): string {
  return digest.replace(/^sha256:/, '').slice(0, SHORT_LENGTH);
}

/** numberAt returns the number of the version at an index of a list that is newest first. */
export function numberAt(versions: readonly MandateVersion[], index: number): number {
  return versions.length - index;
}

/** versionAt finds a version by its number; undefined if the list has no such version. */
export function versionAt(versions: readonly MandateVersion[], number: number): MandateVersion | undefined {
  return Number.isInteger(number) && number >= 1 ? versions[versions.length - number] : undefined;
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
