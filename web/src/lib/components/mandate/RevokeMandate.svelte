<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Revoking only the mandate (#123): the agent stays admitted, every request of it is denied
  until a new mandate exists. A destructive confirmation with the focus on Cancel, like
  revoking an agent. The same action on the mandates page and on a mandate's page.
-->
<script lang="ts">
  import type { ApiClient } from '../../api/client.ts';
  import { m } from '../../i18n.ts';
  import { MARK } from '../../ui/sentence.ts';
  import { toasts } from '../../ui/toasts.ts';
  import { cleanUntrusted } from '../../untrusted.ts';
  import Button from '../Button.svelte';
  import RemoveDialog from '../RemoveDialog.svelte';

  interface Props {
    api: ApiClient;
    id: string;
    name: string;
    /** Called after the revocation. A failed one keeps the dialog; revoking again is harmless. */
    onrevoked: () => void;
  }

  let { api, id, name, onrevoked }: Props = $props();

  let open = $state(false);
  let busy = $state(false);
  let error = $state('');
  let trigger: HTMLButtonElement | undefined = $state();

  async function revoke() {
    if (busy) return;
    busy = true;
    error = '';
    try {
      await api.revokeMandate(id);
      open = false;
      toasts.show({ kind: 'success', text: m.mandate_revoked_toast() });
      onrevoked();
    } catch {
      error = m.mandate_revoke_failed();
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

<Button variant="danger" bind:element={trigger} aria-label={m.mandate_revoke_named({ mandate: cleanUntrusted(name) })} onclick={() => (open = true)}>{m.mandate_revoke_button()}</Button>
<RemoveDialog
  {open}
  title={m.mandate_revoke_title({ mandate: MARK })}
  {name}
  body={[m.mandate_revoke_body(), m.mandate_revoke_final()]}
  confirm={m.mandate_revoke_button()}
  {busy}
  {error}
  onclose={close}
  onconfirm={() => void revoke()}
/>
