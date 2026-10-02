<script lang="ts">
  import { m } from './lib/i18n.ts';
  import { parseHash, type Route } from './lib/router.ts';

  // Interim shell until the frame of the design (header, banners, emergency stop) follows.
  let route: Route = $state(parseHash(window.location.hash));

  function onHashChange() {
    route = parseHash(window.location.hash);
  }
</script>

<svelte:window onhashchange={onHashChange} />

<header>
  <strong>{m.app_name()}</strong>
  <nav aria-label={m.nav_label()}>
    <a href="#/">{m.nav_overview()}</a>
  </nav>
</header>

<main>
  {#if route.name === 'home'}
    <h1>{m.overview_title()}</h1>
  {:else}
    <h1>{m.notfound_title()}</h1>
    <p>{m.notfound_body()}</p>
    <p><a href="#/">{m.notfound_back()}</a></p>
  {/if}
</main>
