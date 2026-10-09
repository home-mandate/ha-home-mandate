<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  "Remove all revoked" (issue #21): removes every revoked agent with its mandates and every
  other revoked mandate from the lists, after a confirmation, e.g. to clean up after an
  emergency stop or many test agents. The same action on the agents and the mandates page.
-->
<script lang="ts">
  import type { ApiClient } from '../api/client.ts';
  import { m } from '../i18n.ts';
  import { toasts } from '../ui/toasts.ts';
  import Button from './Button.svelte';
  import RemoveDialog from './RemoveDialog.svelte';

  interface Props {
    api: ApiClient;
    /** Called after the removal, also when the answer was lost (the lists reload). */
    onremoved: () => void;
  }

  let { api, onremoved }: Props = $props();

  let open = $state(false);
  let busy = $state(false);
  let error = $state('');
  let trigger: HTMLButtonElement | undefined = $state();

  async function remove() {
    if (busy) return;
    busy = true;
    error = '';
    try {
      const count = await api.removeRevoked();
      open = false;
      toasts.show({ kind: 'success', text: m.remove_revoked_done({ agents: count.agents, mandates: count.mandates }) });
      onremoved();
    } catch {
      error = m.remove_failed();
      onremoved();
    } finally {
      busy = false;
    }
  }

  function close() {
    if (busy) return;
    open = false;
    error = '';
    trigger?.focus();
  }
</script>

<Button bind:element={trigger} onclick={() => (open = true)}>{m.remove_revoked()}</Button>
<RemoveDialog
  {open}
  title={m.remove_revoked_title()}
  body={[m.remove_revoked_body(), m.remove_keeps()]}
  confirm={m.remove_button()}
  {busy}
  {error}
  onclose={close}
  onconfirm={() => void remove()}
/>
