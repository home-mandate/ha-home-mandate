<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Basics of a mandate (design README 6.5): display name, the agent it belongs to, validity
  as calendar days in the household time zone and the rate limit. The agent cannot be
  changed: a mandate belongs to one agent (decision D3). A template has only the rate
  limit here: name, agent and validity come with the admission.
-->
<script lang="ts">
  import { untrack } from 'svelte';
  import type { MandateDraft } from '../../api/types.ts';
  import { m } from '../../i18n.ts';
  import { dateInZone, expiresAfter, startOfDay, validUntilDate } from '../../mandate/dates.ts';
  import { withExpires, withLimit, withValidFrom } from '../../mandate/edit.ts';
  import { NAME_MAX, type FieldProblem, type Part } from '../../mandate/problems.ts';
  import AgentName from '../AgentName.svelte';
  import Icon from '../Icon.svelte';
  import TextField from '../TextField.svelte';

  interface Props {
    name: string;
    draft: MandateDraft;
    /** The mandate's agent; none for a template. */
    agent?: { client_id: string; display_name: string };
    timeZone: string;
    /** Problems of the settings that may be shown now. */
    problems: readonly FieldProblem[];
    disabled: boolean;
    onname: (name: string) => void;
    onchange: (draft: MandateDraft) => void;
    /** A field was left. */
    ontouch: (part: Part) => void;
  }

  let { name, draft, agent = undefined, timeZone, problems, disabled, onname, onchange, ontouch }: Props = $props();

  const id = $props.id();
  const WHOLE = /^\d{1,6}$/;
  const problem = (part: Part) => problems.find((p) => p.part === part)?.text ?? '';

  const NAME: Part = 'name';
  const FROM: Part = 'valid_from';
  const UNTIL: Part = 'expires';
  const LIMIT: Part = 'limit';
  const nameError = $derived(problem(NAME));
  const fromError = $derived(problem(FROM));
  const untilError = $derived(problem(UNTIL));
  const limitError = $derived(problem(LIMIT));
  const from = $derived(dateInZone(draft.valid_from, timeZone));
  const until = $derived(validUntilDate(draft.expires, timeZone));

  // What is typed stays as typed; it only follows the draft when that changed elsewhere.
  let rate: string | number | null = $state('');
  const typedRate = () => (WHOLE.test(String(rate ?? '')) ? Number(rate) : NaN);
  $effect(() => {
    const limit = draft.limits.max_actions_per_hour;
    untrack(() => {
      if (!Object.is(typedRate(), limit)) rate = Number.isNaN(limit) ? '' : String(limit);
    });
  });

  const value = (event: Event) => (event.currentTarget as HTMLInputElement).value;

  function setFrom(event: Event) {
    // An emptied or impossible date makes the draft invalid; the field says so.
    onchange(withValidFrom(draft, startOfDay(value(event), timeZone) ?? ''));
  }

  function setUntil(event: Event) {
    const date = value(event);
    onchange(withExpires(draft, date === '' ? null : (expiresAfter(date, timeZone) ?? '')));
  }
</script>

<div class="grid">
  {#if agent}
    <TextField label={m.editor_name()} value={name} error={nameError} maxlength={NAME_MAX} {disabled} oninput={(e) => onname(value(e))} onblur={() => ontouch(NAME)} />
    <div class="agent">
      <span id="{id}-agent" class="label">{m.editor_agent()}</span>
      <span class="value" aria-labelledby="{id}-agent" role="group"><AgentName name={agent.display_name} client={agent.client_id} /></span>
      <span class="help">{m.editor_agent_help()}</span>
    </div>
    <TextField type="date" label={m.editor_valid_from()} value={from} error={fromError} {disabled} oninput={setFrom} onblur={() => ontouch(FROM)} />
    <TextField
      type="date"
      label={m.editor_valid_until()}
      value={until}
      error={untilError}
      help={m.editor_valid_until_help()}
      {disabled}
      oninput={setUntil}
      onblur={() => ontouch(UNTIL)}
    />
  {/if}
  <div class="rate">
    <label for="{id}-rate">{m.editor_rate()}</label>
    <span class="row">
      <input
        id="{id}-rate"
        type="number"
        inputmode="numeric"
        min="1"
        bind:value={rate}
        {disabled}
        aria-invalid={limitError ? 'true' : undefined}
        aria-describedby="{id}-unit {id}-rate-help"
        oninput={() => onchange(withLimit(draft, typedRate()))}
        onblur={() => ontouch(LIMIT)}
      />
      <span id="{id}-unit" class="unit">{m.editor_rate_unit()}</span>
    </span>
    <span id="{id}-rate-help" class="help" class:error={limitError}>
      {#if limitError}<Icon name="warning" size={16} />{limitError}{:else if !agent}{m.template_basics_note()}{/if}
    </span>
  </div>
</div>

<style>
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 200px), 1fr));
    gap: var(--hm-space-3) var(--hm-space-4);
    align-items: start;
  }
  .agent,
  .rate {
    display: flex;
    flex-direction: column;
    gap: 6px;
    min-inline-size: 0;
  }
  .label,
  label {
    font-size: var(--hm-font-size-sm);
    font-weight: var(--hm-font-weight-medium);
  }
  .value {
    display: flex;
    align-items: center;
    min-block-size: var(--hm-size-touch);
    font-size: var(--hm-font-size-md);
  }
  .row {
    display: flex;
    align-items: center;
    gap: var(--hm-space-2);
  }
  input {
    inline-size: 96px;
    box-sizing: border-box;
    min-block-size: var(--hm-size-touch);
    padding-block: 0;
    padding-inline: var(--hm-space-3);
    border-radius: var(--hm-radius-md);
    font: inherit;
    font-size: var(--hm-font-size-md);
    color: var(--hm-color-text);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-border-strong);
  }
  input[aria-invalid='true'] {
    border-color: var(--hm-color-danger-fg);
    /* A shadow, not an outline: the outline stays free for the focus ring. */
    box-shadow: 0 0 0 1px var(--hm-color-danger-fg);
  }
  input:disabled {
    color: var(--hm-color-text-disabled);
    background: var(--hm-color-surface-sunken);
    border-color: var(--hm-color-border);
  }
  .unit {
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-text-muted);
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
</style>
