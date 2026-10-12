<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Home Assistant connection (design README 6.11 section 3; decisions D7, S6): how
  Home-Mandate is connected (as an app by the Supervisor, as a container with the token of
  its own user), status, version and that user; no address and no token. The household's
  time zone, units and country as Home Assistant reports them: the country decides the
  first day of the week (#43). Why it needs admin rights, with the fixed command list from
  the server, in the page.
-->
<script lang="ts">
  import type { Household, SystemStatus } from '../../api/types.ts';
  import { countryName, formatDateTime, isCountryCode, type FormatContext } from '../../format.ts';
  import { m } from '../../i18n.ts';
  import { cleanUntrusted } from '../../untrusted.ts';
  import StatusPill from '../StatusPill.svelte';

  interface Props {
    ha: SystemStatus['ha'];
    mode: SystemStatus['mode'];
    /** From the session; null before it is known. */
    household: Household | null;
    ctx: FormatContext;
  }

  let { ha, mode, household, ctx }: Props = $props();

  const modeText = $derived(mode === 'container' ? m.set_ha_mode_container() : m.set_ha_mode_app());
  const since = $derived(ha.since ? formatDateTime(new Date(ha.since), ctx) : null);
  const units = $derived(
    household
      ? new Intl.ListFormat(ctx.locale, { style: 'short', type: 'unit' }).format(Object.values(household.unit_system).filter(Boolean).map((u) => cleanUntrusted(u)))
      : '',
  );
  const code = $derived(household && isCountryCode(household.country) ? household.country : null);
  const country = $derived(code ? m.set_ha_country_value({ name: countryName(code, ctx.locale) ?? code, code }) : m.set_ha_country_unknown());
</script>

<p class="desc">{modeText}</p>
<dl>
  <dt>{m.overview_ha_label()}</dt>
  <dd>
    {#if ha.connected}
      <StatusPill tone="positive">{m.status_connected()}</StatusPill>
      {#if since}<span class="muted">{m.set_ha_since({ time: since })}</span>{/if}
    {:else}
      <StatusPill tone="danger" shape="square">{m.status_disconnected()}</StatusPill>
      {#if since}<span class="muted">{m.set_ha_down_since({ time: since })}</span>{/if}
    {/if}
  </dd>
  <dt>{m.set_ha_version()}</dt>
  <dd class="mono">{ha.version ?? '—'}</dd>
  <dt>{m.set_ha_user()}</dt>
  <dd><bdi>{ha.user_name ? cleanUntrusted(ha.user_name) : '—'}</bdi></dd>
  {#if household}
    <dt>{m.set_ha_time_zone()}</dt>
    <dd><span dir="ltr">{cleanUntrusted(household.time_zone)}</span></dd>
    <dt>{m.set_ha_units()}</dt>
    <dd><span dir="ltr">{units || '—'}</span></dd>
    <dt>{m.set_ha_country()}</dt>
    <dd>{country}</dd>
  {/if}
</dl>
{#if household}<p class="muted">{m.set_ha_household_note()}</p>{/if}

<div class="why">
  <h3>{m.set_ha_why_title()}</h3>
  <p>{m.set_ha_why_body()}</p>
  <details>
    <summary>{m.set_ha_why_list()}</summary>
    <ul role="list">
      {#each ha.commands as command (command)}<li><span dir="ltr">{command}</span></li>{/each}
    </ul>
  </details>
</div>

<style>
  .desc,
  .muted {
    margin: 0;
    color: var(--hm-color-text-muted);
  }
  dl {
    display: grid;
    grid-template-columns: fit-content(45%) minmax(0, 1fr);
    gap: var(--hm-space-3) var(--hm-space-4);
    margin: 0;
  }
  dt {
    color: var(--hm-color-text-muted);
    overflow-wrap: anywhere;
  }
  dd {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--hm-space-2);
    margin: 0;
  }
  .mono {
    font-family: var(--hm-font-mono);
  }
  .why {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-2);
    padding: var(--hm-space-4);
    border-radius: var(--hm-radius-md);
    background: var(--hm-color-info-bg);
  }
  h3 {
    margin: 0;
    font-size: var(--hm-font-size-md);
    font-weight: var(--hm-font-weight-semibold);
  }
  .why p {
    margin: 0;
  }
  summary {
    display: flex;
    align-items: center;
    min-block-size: var(--hm-size-touch);
    cursor: pointer;
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-accent-text);
  }
  ul {
    margin: 0;
    padding-inline-start: var(--hm-space-5);
    font-family: var(--hm-font-mono);
    font-size: var(--hm-font-size-sm);
  }
</style>
