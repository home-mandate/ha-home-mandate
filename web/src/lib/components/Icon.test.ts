// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, render } from '@testing-library/svelte';
import Icon from './Icon.svelte';
import { ICON_PATHS } from '../icons/paths.ts';

afterEach(cleanup);

const svg = (c: HTMLElement) => c.querySelector('svg') as SVGSVGElement;

describe('Icon', () => {
  it('draws the MDI path, decorative by default', () => {
    const { container } = render(Icon, { name: 'allow' });
    const s = svg(container);
    expect(s.getAttribute('viewBox')).toBe('0 0 24 24');
    expect(s.getAttribute('aria-hidden')).toBe('true');
    expect(s.getAttribute('focusable')).toBe('false');
    expect(s.querySelector('path')?.getAttribute('d')).toBe(ICON_PATHS.allow);
    expect(s.querySelector('path')?.getAttribute('fill')).toBe('currentColor');
    expect(s.getAttribute('width')).toBe('20');
  });

  it('takes the sizes 16, 20 and 32', () => {
    for (const size of [16, 20, 32] as const) {
      const { container } = render(Icon, { name: 'power', size });
      expect(svg(container).getAttribute('height')).toBe(String(size));
      cleanup();
    }
  });

  it('draws "default" as its own dashed circle with a minus', () => {
    const { container } = render(Icon, { name: 'default' });
    const s = svg(container);
    expect(s.querySelector('circle')?.getAttribute('stroke-dasharray')).toBe('2.4 2.6');
    expect(s.querySelector('path')?.getAttribute('d')).toBe('M8 12h8');
    expect(s.getAttribute('stroke')).toBe('currentColor');
  });

  it('marks direction icons for mirroring in right-to-left layouts', () => {
    for (const name of ['chevron', 'arrow', 'back'] as const) {
      const { container } = render(Icon, { name });
      expect(svg(container).hasAttribute('data-rtl-mirror')).toBe(true);
      cleanup();
    }
    const { container } = render(Icon, { name: 'check' });
    expect(svg(container).hasAttribute('data-rtl-mirror')).toBe(false);
  });

  it('becomes an image with a name when labelled', () => {
    const { container } = render(Icon, { name: 'warning', label: 'Warnung' });
    const s = svg(container);
    expect(s.getAttribute('role')).toBe('img');
    expect(s.getAttribute('aria-label')).toBe('Warnung');
    expect(s.hasAttribute('aria-hidden')).toBe(false);
  });
});
