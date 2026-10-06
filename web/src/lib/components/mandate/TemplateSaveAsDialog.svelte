<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Saving the edit as a new template: asks for its name and checks it as the server does
  (pattern; "hm-" belongs to base templates; not taken). A template that allows critical
  actions without approval says so and the button becomes the separate confirmation (U9).
  The safe button keeps editing; the name field gets the focus.
-->
<script lang="ts">
  import { tick, untrack } from 'svelte';
  import type { TemplateSummary } from '../../api/types.ts';
  import { m } from '../../i18n.ts';
  import { nameProblem, TEMPLATE_NAME_MAX, type NameProblem } from '../../mandate/template.ts';
  import Button from '../Button.svelte';
  import Dialog from '../Dialog.svelte';
  import Icon from '../Icon.svelte';
  import TextField from '../TextField.svelte';

  interface Props {
    open: boolean;
    /** Templates that exist; a new one cannot take their names. */
    existing: readonly Pick<TemplateSummary, 'name'>[];
    /** The template stores critical actions without approval: the separate confirmation. */
    critical: boolean;
    busy: boolean;
    /** What the server said about the name; null if nothing. */
    refused: NameProblem | null;
    /** Error of the last attempt that is not about the name, already worded. */
    error: string;
    /** The name was changed: the server's last answer no longer applies. */
    onedit: () => void;
    onclose: () => void;
    onconfirm: (name: string) => void;
  }

  let { open, existing, critical, busy, refused, error, onedit, onclose, onconfirm }: Props = $props();

  const id = $props.id();
  const TEXTS: Record<NameProblem, () => string> = {
    format: () => m.template_name_format({ max: TEMPLATE_NAME_MAX }),
    reserved: () => m.template_name_reserved(),
    taken: () => m.template_name_taken(),
  };

  let name = $state('');
  let checked = $state(false);
  let field: HTMLInputElement | undefined = $state();

  $effect(() => {
    if (!open) return;
    untrack(() => {
      name = '';
      checked = false;
    });
  });

  const problem = $derived(checked ? nameProblem(name.trim(), existing) : null);
  // The server's answer counts for the name it was given, until the name is changed.
  const shown = $derived(problem ?? refused);
  const nameError = $derived(shown ? TEXTS[shown]() : '');
  const confirmLabel = $derived(critical ? `${m.critical_confirm_action()} · ${m.template_save_as_confirm()}` : m.template_save_as_confirm());

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    checked = true;
    if (nameProblem(name.trim(), existing) !== null) {
      await tick();
      field?.focus();
      return;
    }
    if (!busy) onconfirm(name.trim());
  }
</script>

<Dialog {open} labelledby="{id}-title" describedby={critical ? `${id}-body ${id}-flag` : `${id}-body`} destructive={critical} initial={field ?? null} {onclose}>
  <form onsubmit={submit} novalidate>
    <h2 id="{id}-title">{m.template_save_as_title()}</h2>
    <p id="{id}-body">{m.template_save_as_body()}</p>
    {#if critical}
      <div id="{id}-flag" class="flag"><Icon name="critical" /><span>{m.save_critical_flag()}</span></div>
    {/if}
    <TextField
      label={m.template_name_label()}
      bind:value={name}
      bind:element={field}
      help={m.template_name_help()}
      error={nameError}
      maxlength={TEMPLATE_NAME_MAX}
      autocomplete="off"
      autocapitalize="none"
      spellcheck={false}
      mono
      oninput={() => {
        checked = false;
        onedit();
      }}
    />
    <p class="error" role="alert">{#if error}<Icon name="warning" size={16} />{error}{/if}</p>
    <div class="actions">
      <Button size="lg" onclick={onclose}>{m.save_back()}</Button>
      <Button type="submit" variant={critical ? 'danger' : 'primary'} size="lg" {busy}>{confirmLabel}</Button>
    </div>
  </form>
</Dialog>

<style>
  form {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-4);
  }
  h2 {
    margin: 0;
    font-size: var(--hm-font-size-xl);
    font-weight: var(--hm-font-weight-semibold);
  }
  p {
    margin: 0;
    font-size: 15px;
    color: var(--hm-color-text-muted);
    text-wrap: pretty;
  }
  .flag {
    display: flex;
    gap: var(--hm-space-2);
    padding: 10px var(--hm-space-3);
    border-radius: var(--hm-radius-md);
    font-size: 15px;
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-danger-fg);
    background: var(--hm-color-danger-bg);
    border: var(--hm-border-width) solid var(--hm-color-danger-border);
  }
  .error {
    display: flex;
    gap: 6px;
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-danger-fg);
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    justify-content: flex-end;
    gap: var(--hm-space-3);
  }
</style>
