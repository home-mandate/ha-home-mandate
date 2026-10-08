// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, describe, expect, it } from 'vitest';
import { dock, DOCK_ID, portal } from './portal.ts';

afterEach(() => document.body.replaceChildren());

describe('portal', () => {
  it('moves an element to the body and removes it again', () => {
    const host = document.createElement('div');
    const node = document.createElement('p');
    host.append(node);
    document.body.append(host);
    const action = portal(node);
    expect(node.parentElement).toBe(document.body);
    action.destroy();
    expect(node.isConnected).toBe(false);
  });
});

describe('dock', () => {
  it('moves an element into the dock below the page and removes it again', () => {
    const target = document.createElement('div');
    target.id = DOCK_ID;
    const page = document.createElement('div');
    const node = document.createElement('section');
    page.append(node);
    document.body.append(page, target);
    const action = dock(node);
    expect(node.parentElement).toBe(target);
    action.destroy();
    expect(node.isConnected).toBe(false);
  });

  it('leaves the element in place without a dock', () => {
    const page = document.createElement('div');
    const node = document.createElement('section');
    page.append(node);
    document.body.append(page);
    dock(node);
    expect(node.parentElement).toBe(page);
  });
});
