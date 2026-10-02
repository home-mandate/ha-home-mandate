// SPDX-License-Identifier: AGPL-3.0-or-later

// Display names for the identifiers of a mandate. Identifiers stay English on the wire
// (allow, unlock, light); the UI shows catalog texts. Where the design names an action
// differently from the specification, the specification name stays and only the label
// differs (decision D10). Anything unknown is shown as it is, without hidden characters.

import type { Weekday } from '../api/types.ts';
import type { CellDecision } from '../engine/analysis.ts';
import { m } from '../i18n.ts';
import { cleanUntrusted } from '../untrusted.ts';

type Texts = Readonly<Record<string, () => string>>;

const MAX_IDENTIFIER = 64;

const ACTIONS: Texts = {
  read: () => m.action_read(),
  turn_on: () => m.action_turn_on(),
  turn_off: () => m.action_turn_off(),
  set: () => m.action_set(),
  set_temperature: () => m.action_set_temperature(),
  set_mode: () => m.action_set_mode(),
  open: () => m.action_open(),
  close: () => m.action_close(),
  stop: () => m.action_stop(),
  set_position: () => m.action_set_position(),
  lock: () => m.action_lock(),
  unlock: () => m.action_unlock(),
  arm: () => m.action_arm(),
  disarm: () => m.action_disarm(),
  snapshot: () => m.action_snapshot(),
  play: () => m.action_play(),
  pause: () => m.action_pause(),
  set_volume: () => m.action_volume(),
  activate: () => m.action_activate(),
  run: () => m.action_run(),
};

/** Labels that depend on the category, keyed "category.action". */
const ACTIONS_OF_CATEGORY: Texts = {
  'light.set': () => m.action_adjust(),
  'media.turn_on': () => m.action_media_on(),
  'media.turn_off': () => m.action_media_off(),
};

const CATEGORIES: Texts = {
  all: () => m.cat_all(),
  light: () => m.cat_light(),
  switch: () => m.cat_switch(),
  climate: () => m.cat_climate(),
  cover: () => m.cat_cover(),
  gate: () => m.cat_gate(),
  lock: () => m.cat_lock(),
  alarm: () => m.cat_alarm(),
  camera: () => m.cat_camera(),
  media: () => m.cat_media(),
  sensor: () => m.cat_sensor(),
  scene: () => m.cat_scene(),
  script: () => m.cat_script(),
  other: () => m.cat_other(),
};

const DECISIONS: Readonly<Record<CellDecision, () => string>> = {
  allow: () => m.decision_allow(),
  ask: () => m.decision_ask(),
  deny: () => m.decision_deny(),
  default: () => m.decision_default(),
};

const lookup = (texts: Texts, key: string) => (Object.hasOwn(texts, key) ? texts[key] : undefined);

/** actionLabel names an action of a rule or a request; "*" is "all actions". */
export function actionLabel(category: string | undefined, action: string): string {
  if (action === '*') return m.rule_all_actions();
  const text = lookup(ACTIONS_OF_CATEGORY, `${category}.${action}`) ?? lookup(ACTIONS, action);
  return text ? text() : cleanUntrusted(action, MAX_IDENTIFIER);
}

/** categoryLabel names a category; "all" stands for a rule without one. */
export function categoryLabel(category: string): string {
  const text = lookup(CATEGORIES, category);
  return text ? text() : cleanUntrusted(category, MAX_IDENTIFIER);
}

export function decisionLabel(decision: CellDecision): string {
  return DECISIONS[decision]();
}

export const WEEKDAYS: readonly Weekday[] = ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun'];

/** 2024-01-01 was a Monday. */
const A_MONDAY = Date.UTC(2024, 0, 1);
const DAY_MS = 86_400_000;

/** weekdayNames returns the short weekday names of a locale, from Intl. */
export function weekdayNames(locale: string): Record<Weekday, string> {
  const format = new Intl.DateTimeFormat(locale, { weekday: 'short', timeZone: 'UTC' });
  return Object.fromEntries(WEEKDAYS.map((day, i) => [day, format.format(A_MONDAY + i * DAY_MS)])) as Record<Weekday, string>;
}
