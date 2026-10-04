// SPDX-License-Identifier: AGPL-3.0-or-later

// Test helper: children for components under test, without a .svelte harness.
import { createRawSnippet } from 'svelte';

export const text = (value: string) => createRawSnippet(() => ({ render: () => `<span>${value}</span>` }));
export const html = (markup: string) => createRawSnippet(() => ({ render: () => markup }));
