// SPDX-License-Identifier: AGPL-3.0-or-later

// Runs around every Vitest test: the household's country is app-wide state (set from the
// session), so no test inherits the week of a household another test started (#43).
import { afterEach } from 'vitest';
import { setHouseholdCountry } from '../mandate/week.svelte.ts';

afterEach(() => setHouseholdCountry(null));
