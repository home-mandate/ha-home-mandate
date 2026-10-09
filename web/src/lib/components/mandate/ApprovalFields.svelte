<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Approval settings: the timeout as number and unit, and who gets the push notification.
  Used for the mandate's default and for a rule's own settings. In a template, people may
  include the approvers placeholder (named by the page), shown with a group mark. Errors show once the field
  was left or a save was tried, never while typing.
-->
<script lang="ts">
  import type { Approval } from '../../api/types.ts';
  import { m } from '../../i18n.ts';
  import { isPlaceholder } from '../../mandate/placeholder.ts';
  import { cleanUntrusted } from '../../untrusted.ts';
  import Icon from '../Icon.svelte';
  import TimeoutField from '../TimeoutField.svelte';

  interface Props {
    approval: Approval;
    /** People who can approve: Home Assistant user id and name. */
    people: readonly { id: string; name: string }[];
    timeoutError?: string;
    approversError?: string;
    /** The installation's upper limit for the timeout in seconds; null while unknown. */
    maxTimeout?: number | null;
    disabled?: boolean;
    onchange: (approval: Approval) => void;
    /** A field was left or a person was switched. */
    ontouch?: (part: 'timeout' | 'approvers') => void;
  }

  let { approval, people, timeoutError = '', approversError = '', maxTimeout = null, disabled = false, onchange, ontouch }: Props = $props();

  const id = $props.id();
  const MAX_INITIALS = 2;

  function toggle(person: string) {
    const approvers = approval.approvers.includes(person) ? approval.approvers.filter((a) => a !== person) : [...approval.approvers, person];
    onchange({ ...approval, approvers });
    ontouch?.('approvers');
  }

  function initials(name: string): string {
    return name
      .split(/\s+/u)
      .slice(0, MAX_INITIALS)
      .map((word) => [...word][0]?.toLocaleUpperCase() ?? '')
      .join('');
  }
</script>

<div class="fields">
  <TimeoutField
    timeout={approval.timeout}
    error={timeoutError}
    max={maxTimeout}
    {disabled}
    onchange={(timeout) => onchange({ ...approval, timeout })}
    ontouch={() => ontouch?.('timeout')}
  />

  <fieldset aria-describedby="{id}-approvers-help">
    <legend>{m.editor_approvers()}</legend>
    <div class="people">
      {#each people as person (person.id)}
        {@const on = approval.approvers.includes(person.id)}
        <button type="button" class="person" aria-pressed={on} aria-disabled={disabled ? 'true' : undefined} onclick={() => !disabled && toggle(person.id)}>
          <span class="avatar" aria-hidden="true">
            {#if isPlaceholder(person.id)}<Icon name="person" size={16} />{:else}{initials(cleanUntrusted(person.name))}{/if}
          </span>
          <bdi>{cleanUntrusted(person.name)}</bdi>
          {#if on}<Icon name="check" size={16} />{/if}
        </button>
      {/each}
    </div>
    <span id="{id}-approvers-help" class="help" class:error={approversError}>
      {#if approversError}<Icon name="warning" size={16} />{approversError}{:else}{m.editor_approvers_help()}{/if}
    </span>
  </fieldset>
</div>

<style>
  .fields {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 240px), 1fr));
    gap: var(--hm-space-4);
  }
  fieldset {
    display: flex;
    flex-direction: column;
    gap: 6px;
    min-inline-size: 0;
    margin: 0;
    padding: 0;
    border: none;
  }
  legend {
    padding: 0;
    font-size: var(--hm-font-size-sm);
    font-weight: var(--hm-font-weight-medium);
  }
  legend {
    padding-block-end: 6px;
  }
  .people {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }
  .person {
    display: inline-flex;
    align-items: center;
    gap: var(--hm-space-2);
    min-block-size: var(--hm-size-touch);
    padding-inline: 6px var(--hm-space-3);
    border-radius: var(--hm-radius-pill);
    font: inherit;
    font-size: 15px;
    font-weight: var(--hm-font-weight-medium);
    text-align: start;
    color: var(--hm-color-text);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-border-strong);
    cursor: pointer;
  }
  .person[aria-pressed='true'] {
    font-weight: var(--hm-font-weight-semibold);
    background: var(--hm-color-accent-subtle);
    border: 2px solid var(--hm-color-accent);
  }
  .person[aria-disabled='true'] {
    color: var(--hm-color-text-disabled);
    cursor: not-allowed;
  }
  .avatar {
    display: flex;
    align-items: center;
    justify-content: center;
    inline-size: 30px;
    block-size: 30px;
    border-radius: var(--hm-radius-pill);
    font-size: 12px;
    font-weight: var(--hm-font-weight-semibold);
    background: var(--hm-color-surface-sunken);
    color: var(--hm-color-text-muted);
  }
  .person[aria-pressed='true'] .avatar {
    background: var(--hm-color-accent);
    color: var(--hm-color-text-on-accent);
  }
  .help {
    display: flex;
    gap: 6px;
    font-size: var(--hm-font-size-xs);
    color: var(--hm-color-text-subtle);
  }
  .error {
    color: var(--hm-color-danger-fg);
  }
  @media (forced-colors: active) {
    .person[aria-pressed='true'] {
      border-color: Highlight;
    }
  }
</style>
