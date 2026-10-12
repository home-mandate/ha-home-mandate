// SPDX-License-Identifier: AGPL-3.0-or-later

// The household's week as the UI shows it: Home Assistant's country decides the first day
// (#43). AppState sets the country from the session; every list of weekdays (the picker,
// the rule summaries) reads the order here, so it follows a renewed session too.

import type { Weekday } from '../api/types.ts';
import { weekdayOrder } from './labels.ts';

let country: string | null = $state(null);

/** setHouseholdCountry takes over the household's country; null when it is unknown. */
export function setHouseholdCountry(value: string | null): void {
  country = value;
}

/** householdWeek is the household's week in the UI language, starting on its first day. */
export function householdWeek(language: string): readonly Weekday[] {
  return weekdayOrder(language, country);
}
