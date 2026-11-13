<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Certificates (issue #66): the Certificate Transparency monitoring of the public name. Its
  state, the certificates Home-Mandate did not obtain itself, newest first, and "This is
  mine", which an approver confirms like a critical request (on a phone that asks for
  unlocking, or here with the right to critical requests). Names, issuer and serial come
  from a public log: untrusted text, cleaned and isolated.
-->
<script lang="ts">
  import { ApiError } from '../../api/client.ts';
  import type { CTCertificate, CTReason, CTStatus } from '../../api/types.ts';
  import type { AppState } from '../../app/state.svelte.ts';
  import { formatDate, formatDateTime, type FormatContext } from '../../format.ts';
  import { m } from '../../i18n.ts';
  import { cleanUntrusted, isolate } from '../../untrusted.ts';
  import { toasts } from '../../ui/toasts.ts';
  import Banner from '../Banner.svelte';
  import Button from '../Button.svelte';
  import StatusPill from '../StatusPill.svelte';

  interface Props {
    app: AppState;
    ct: CTStatus;
    ctx: FormatContext;
  }

  let { app, ct, ctx }: Props = $props();

  /** The certificate whose request or answer is under way. */
  let busy: string | null = $state(null);
  const me = $derived(app.session?.user.id ?? null);
  const now = $derived(app.system ? new Date(app.system.server_time) : new Date());

  const at = (iso: string | null) => (iso ? formatDateTime(new Date(iso), ctx) : '');
  const day = (iso: string) => formatDate(new Date(iso), ctx);

  /** Why the monitoring does not run; an unknown reason reads as a name that is not public. */
  function notApplicable(reason: CTReason | null): string {
    switch (reason) {
      case 'no_public_url':
        return m.ct_na_no_public_url();
      case 'no_tls':
        return m.ct_na_no_tls();
      case 'ip_address':
        return m.ct_na_ip_address();
      default:
        return m.ct_na_not_public_name();
    }
  }
  const SOURCE_NAMES: Record<string, string> = { certspotter: 'Cert Spotter', crtsh: 'crt.sh' };

  function resultText(c: CTCertificate): string {
    if (c.result === 'past-exposure') return m.ct_result_past();
    return new Date(c.not_before) > now ? m.ct_result_not_yet() : m.ct_result_alarm();
  }

  async function mine(c: CTCertificate) {
    if (busy) return;
    busy = c.digest;
    try {
      await app.api.acknowledgeCertificate(c.digest);
      toasts.show({ kind: 'success', text: m.ct_requested() });
    } catch (e) {
      toasts.show({ kind: 'error', text: e instanceof ApiError && e.code === 'no_approvers' ? m.ct_no_approvers() : m.ct_failed() });
    } finally {
      busy = null;
    }
  }

  async function answer(c: CTCertificate, approve: boolean) {
    if (busy || !c.pending) return;
    busy = c.digest;
    try {
      await app.api.answerCertificate(c.pending.id, approve);
      toasts.show({ kind: 'success', text: approve ? m.ct_confirmed() : m.ct_declined() });
    } catch {
      toasts.show({ kind: 'error', text: m.ct_failed() });
    } finally {
      busy = null;
    }
  }
</script>

<p class="desc">{m.ct_intro()}</p>

{#if ct.state === 'off'}
  <StatusPill tone="neutral" wrap>{m.ct_off()}</StatusPill>
{:else if ct.state === 'not_applicable'}
  <StatusPill tone="neutral" wrap>{notApplicable(ct.reason)}</StatusPill>
{:else if ct.state === 'unreadable'}
  <Banner kind="warning" body={m.ct_unreadable()} />
{:else}
  {#if ct.unavailable}
    <Banner kind="critical" body={m.ct_unavailable({ time: at(ct.last_success ?? ct.since) })} />
  {:else}
    <StatusPill tone="positive" wrap
      >{ct.last_success
        ? m.ct_watching({ name: ct.name ?? '', time: at(ct.last_success) })
        : m.ct_not_checked_yet({ name: ct.name ?? '' })}</StatusPill
    >
  {/if}
  {#if !ct.own_key_known}
    <Banner kind="warning" body={m.ct_no_own_key()} />
  {:else if ct.key_error}
    <p class="note">{m.ct_key_error()}</p>
  {/if}

  {#if ct.certificates.length === 0}
    <p class="desc">{m.ct_none_found()}</p>
  {:else}
    <ul class="certs" role="list">
      {#each ct.certificates as c (c.digest)}
        <li class="cert" class:alarm={c.result === 'alarm'}>
          {#if c.acknowledged}
            <StatusPill tone="positive" wrap
              >{m.ct_acknowledged({
                person: isolate(c.acknowledged.by_name ?? c.acknowledged.by),
                via: c.acknowledged.via === 'push' ? m.ct_via_push() : m.ct_via_ui(),
                time: at(c.acknowledged.at),
              })}</StatusPill
            >
          {:else}
            <StatusPill tone={c.result === 'alarm' ? 'danger' : 'warning'} wrap>{resultText(c)}</StatusPill>
          {/if}
          <dl>
            <dt>{m.ct_names()}</dt>
            <dd>{cleanUntrusted(c.names.join(', '))}</dd>
            <dt>{m.ct_issuer()}</dt>
            <dd>{cleanUntrusted(c.issuer)}</dd>
            <dt>{m.ct_validity()}</dt>
            <dd>{m.ct_validity_range({ from: day(c.not_before), to: day(c.not_after) })}</dd>
            <dt>{m.ct_serial()}</dt>
            <dd class="code">{cleanUntrusted(c.serial, 128)}</dd>
            <dt>{m.ct_digest()}</dt>
            <dd class="code">{c.digest}</dd>
          </dl>
          {#if c.result !== 'none'}
            {#if c.pending}
              <p class="note" role="status">
                {m.ct_pending({ time: at(c.pending.expires), person: isolate(c.pending.requested_by_name ?? c.pending.requested_by) })}
              </p>
              {#if me !== null && c.pending.answerers.includes(me)}
                <div class="actions">
                  <Button disabled={busy !== null} onclick={() => answer(c, false)}>{m.ct_confirm_no()}</Button>
                  <Button variant="primary" busy={busy === c.digest} disabled={busy !== null && busy !== c.digest} onclick={() => answer(c, true)}
                    >{m.ct_confirm_yes()}</Button
                  >
                </div>
              {/if}
            {:else}
              <div class="actions">
                <Button busy={busy === c.digest} disabled={busy !== null && busy !== c.digest} onclick={() => mine(c)}>{m.ct_mine()}</Button>
              </div>
              <p class="note">{m.ct_mine_hint()}</p>
            {/if}
          {/if}
        </li>
      {/each}
    </ul>
  {/if}

  <div class="sources">
    <span class="label">{m.ct_sources()}</span>
    <ul role="list">
      {#each ct.sources as s (s.name)}
        {@const source = SOURCE_NAMES[s.name] ?? s.name}
        {@const failedLast = s.failure !== null && s.last_failure !== null && (s.last_success === null || s.last_failure > s.last_success)}
        <li>
          {s.last_success ? m.ct_source_ok({ source, time: at(s.last_success) }) : m.ct_source_never({ source })}{#if failedLast}
            · {m.ct_source_failed({ time: at(s.last_failure) })}{/if}
        </li>
      {/each}
    </ul>
  </div>
{/if}

<style>
  .desc {
    margin: 0;
    color: var(--hm-color-text-muted);
  }
  .note {
    margin: 0;
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-text-muted);
  }
  .certs {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-3);
    margin: 0;
    padding: 0;
    list-style: none;
  }
  .cert {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: var(--hm-space-3);
    padding: var(--hm-space-4);
    border-radius: var(--hm-radius-md);
    border: var(--hm-border-width) solid var(--hm-color-border-subtle);
    min-inline-size: 0;
  }
  .cert.alarm {
    border-color: var(--hm-color-danger-border);
    background: var(--hm-color-danger-bg);
  }
  dl {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    gap: var(--hm-space-1) var(--hm-space-3);
    margin: 0;
    inline-size: 100%;
  }
  @media (min-width: 768px) {
    dl {
      grid-template-columns: max-content minmax(0, 1fr);
    }
  }
  dt {
    font-size: var(--hm-font-size-sm);
    font-weight: var(--hm-font-weight-medium);
  }
  dd {
    margin: 0;
    overflow-wrap: anywhere;
  }
  .code {
    font-family: var(--hm-font-mono);
    font-size: var(--hm-font-size-sm);
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hm-space-3);
  }
  .sources {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-1);
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-text-muted);
  }
  .sources ul {
    margin: 0;
    padding: 0;
    list-style: none;
  }
  .label {
    font-weight: var(--hm-font-weight-medium);
    color: var(--hm-color-text);
  }
  @media (forced-colors: active) {
    .cert.alarm {
      border-color: Mark;
    }
  }
</style>
