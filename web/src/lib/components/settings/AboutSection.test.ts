// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { cleanup, render, screen } from '@testing-library/svelte';
import AboutSection from './AboutSection.svelte';
import { setLocale } from '../../paraglide/runtime.js';

beforeEach(() => setLocale('en', { reload: false }));
afterEach(cleanup);

describe('AboutSection', () => {
  it('shows the logo at 72 px with version, commit and license', () => {
    const { container } = render(AboutSection, { version: '0.1.0', commit: 'abc1234' });
    const logo = container.querySelector('img.light');
    expect(logo?.getAttribute('src')).toBe('./hm-logo.svg');
    expect(logo?.getAttribute('height')).toBe('72');
    expect(screen.getByText('0.1.0')).toBeTruthy();
    expect(screen.getByText('abc1234')).toBeTruthy();
    expect(screen.getByText(/AGPL-3\.0-or-later/)).toBeTruthy();
  });

  it('says that name and logo are not covered by the license', () => {
    render(AboutSection, { version: '0.1.0', commit: 'abc1234' });
    expect(screen.getByText('The name and the logo are not covered by the license. “Home-Mandate” has been filed as a trademark.')).toBeTruthy();
    cleanup();
    setLocale('de', { reload: false });
    render(AboutSection, { version: '0.1.0', commit: 'abc1234' });
    expect(screen.getByText('Name und Logo sind von der Lizenz ausgenommen. „Home-Mandate“ ist als Marke angemeldet.')).toBeTruthy();
  });
});
