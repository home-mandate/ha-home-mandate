<script lang="ts">
  import { m } from './lib/paraglide/messages.js';
  import { getLocale } from './lib/paraglide/runtime.js';
  import { parseHash, type Route } from './lib/router.ts';
  import { formatDate } from './lib/format.ts';

  // Release date of v0.1; the household time zone comes from Home Assistant later.
  const release = new Date('2026-10-31T12:00:00Z');
  const timeZone = 'Europe/Berlin';

  let route: Route = $state(parseHash(window.location.hash));

  function onHashChange() {
    route = parseHash(window.location.hash);
  }
</script>

<svelte:window onhashchange={onHashChange} />

<header>
  <strong>{m.app_title()}</strong>
  <nav>
    <a href="#/">{m.nav_overview()}</a>
  </nav>
</header>

<main>
  {#if route.name === 'home'}
    <h1>{m.home_heading()}</h1>
    <p>{m.home_intro()}</p>
    <p>{m.home_release({ date: formatDate(release, { locale: getLocale(), timeZone }) })}</p>
  {:else}
    <h1>{m.not_found_heading()}</h1>
    <p><a href="#/">{m.not_found_back()}</a></p>
  {/if}
</main>
