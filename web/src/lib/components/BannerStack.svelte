<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Banners under the header, in the order of design README section 5: emergency stop ›
  audit chain broken › clock behind the audit log › Home Assistant unreachable › TLS missing; then the lost live
  connection (README section 7), shown only after 5 s. They leave when the cause is gone.
-->
<script lang="ts">
  import type { SystemStatus } from '../api/types.ts';
  import { formatDate, formatNumber, formatTime } from '../format.ts';
  import { m } from '../i18n.ts';
  import Banner from './Banner.svelte';

  /** README section 7: the connection banner shows after 5 s without the stream. */
  const CONNECTION_GRACE_MS = 5000;

  interface Props {
    system: SystemStatus;
    /** Browser time the live stream was lost; null while connected. */
    downSince: number | null;
    /** Current browser time; ticks so the 5 s grace can pass. */
    now: number;
    locale: string;
    timeZone: string;
    onliftestop: () => void;
    onchain: (seq: number) => void;
  }

  let { system, downSince, now, locale, timeZone, onliftestop, onchain }: Props = $props();

  const ctx = $derived({ locale, timeZone });
  /** A time from the server; an unreadable one shows nothing instead of breaking the frame. */
  function at(iso: string | null): string {
    const date = iso ? new Date(iso) : null;
    return date && !Number.isNaN(date.getTime()) ? formatTime(date, ctx) : '';
  }
  const brokenAt = $derived(system.chain.valid ? null : system.chain.broken_at_seq);
  /** The server warns from 14 days before the end of validity; so does the UI. */
  const TLS_WARNING_MS = 14 * 24 * 3_600_000;
  const tlsUntil = $derived(system.tls.present && system.tls.valid_until ? new Date(system.tls.valid_until) : null);
  const tlsLeft = $derived(tlsUntil !== null && !Number.isNaN(tlsUntil.getTime()) ? tlsUntil.getTime() - now : null);
  const tlsExpired = $derived(tlsLeft !== null && tlsLeft <= 0);
  const tlsExpiring = $derived(tlsLeft !== null && tlsLeft > 0 && tlsLeft < TLS_WARNING_MS);
  const tlsMissing = $derived(system.mode === 'container' ? m.set_tls_missing_container() : m.set_tls_missing());
  const connectionLost = $derived(downSince !== null && now - downSince >= CONNECTION_GRACE_MS);
</script>

<div class="stack">
  {#if system.emergency_stop.active}
    <Banner
      flush
      kind="estop"
      title={m.banner_estop_title()}
      body={m.banner_estop_body({ time: at(system.emergency_stop.since) })}
      action={{ label: m.banner_estop_action(), onclick: onliftestop }}
    />
  {/if}
  {#if brokenAt !== null}
    <Banner
      flush
      kind="critical"
      title={m.banner_chain_title({ number: formatNumber(brokenAt, ctx) })}
      body={m.banner_chain_body()}
      action={{ label: m.banner_chain_action(), onclick: () => onchain(brokenAt) }}
    />
  {/if}
  {#if system.directory.store_failing_since !== null || system.directory.overflow}
    <Banner
      flush
      kind={system.directory.overflow ? 'critical' : 'warning'}
      title={m.banner_directory_title()}
      body={system.directory.overflow ? m.banner_directory_overflow() : m.banner_directory_body({ time: at(system.directory.store_failing_since) })}
    />
  {/if}
  {#if system.directory.renames_last_hour > system.directory.rename_flood_threshold}
    <Banner flush kind="info" title={m.banner_rename_flood_title()} body={m.banner_rename_flood_body({ count: system.directory.renames_last_hour })} />
  {/if}
  {#if system.clock_behind}
    <Banner flush kind="critical" title={m.banner_clock_title()} body={m.banner_clock_body()} />
  {/if}
  {#if !system.ha.connected}
    <Banner flush kind="warning" title={m.banner_ha_title()} body={m.banner_ha_body({ time: at(system.ha.since) })} />
  {/if}
  {#if !system.tls.present && !system.tls.proxy}
    <Banner flush kind="warning" title={m.banner_tls_title()} body={tlsMissing} />
  {/if}
  {#if system.tls.present && system.tls.renewal_failed}
    <Banner flush kind="warning" title={m.banner_tls_renewal_title()} body={m.banner_tls_renewal_body()} />
  {/if}
  {#if tlsExpired && tlsUntil}
    <Banner flush kind="critical" title={m.banner_tls_expired_title()} body={m.banner_tls_expired_body({ date: formatDate(tlsUntil, ctx) })} />
  {/if}
  {#if tlsExpiring && tlsUntil}
    <Banner flush kind="warning" title={m.banner_tls_expiring_title()} body={m.banner_tls_expiring_body({ date: formatDate(tlsUntil, ctx) })} />
  {/if}
  {#if connectionLost && downSince !== null}
    <Banner flush kind="warning" title={m.banner_conn_title()} body={m.banner_conn_body({ time: formatTime(new Date(downSince), ctx) })} />
  {/if}
</div>

<style>
  .stack {
    display: flex;
    flex-direction: column;
  }
</style>
