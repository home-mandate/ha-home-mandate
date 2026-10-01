// SPDX-License-Identifier: AGPL-3.0-or-later

// Hash routing: Home Assistant Ingress serves the UI under a per-installation path,
// so the router must not depend on the base path (docs/ARCHITECTURE.md section 12).

export type Route = { name: 'home' } | { name: 'not_found' };

/** parseHash maps a location hash such as "#/" to a route. */
export function parseHash(hash: string): Route {
  const path = hash.replace(/^#/, '') || '/';
  switch (path) {
    case '/':
      return { name: 'home' };
    default:
      return { name: 'not_found' };
  }
}
