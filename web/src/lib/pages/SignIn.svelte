<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- Direct mode without a session: sign in through Home Assistant, and why the last attempt failed. -->
<script lang="ts">
  import type { SignInError } from '../app/signin.ts';
  import FullPageState from '../components/FullPageState.svelte';
  import { m } from '../i18n.ts';

  interface Props {
    error: SignInError | null;
  }

  let { error }: Props = $props();

  const REASONS: Record<SignInError, () => string> = {
    denied: () => m.signin_error_denied(),
    failed: () => m.signin_error_failed(),
    busy: () => m.signin_error_busy(),
  };
  const note = $derived(error ? REASONS[error]() : undefined);
</script>

<!-- "signin" is relative to the page: the UI's own prefix, e.g. /ui/signin. -->
<FullPageState
  icon="lock"
  badge="person"
  title={m.signin_title()}
  body={m.signin_body()}
  {note}
  cta={{ href: 'signin', label: m.signin_action(), icon: 'arrow' }}
/>
