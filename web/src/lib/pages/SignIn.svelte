<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- Direct mode without a session: sign in through Home Assistant, and why the last attempt failed. -->
<script lang="ts">
  import type { SignInError } from '../app/signin.ts';
  import FullPageState from '../components/FullPageState.svelte';
  import { m } from '../i18n.ts';

  interface Props {
    error: SignInError | null;
    /** The request whose page was opened (issue #53): the sign-in ends there. */
    request?: string | null;
  }

  let { error, request = null }: Props = $props();

  const REASONS: Record<SignInError, () => string> = {
    denied: () => m.signin_error_denied(),
    failed: () => m.signin_error_failed(),
    busy: () => m.signin_error_busy(),
  };
  const note = $derived(error ? REASONS[error]() : undefined);
</script>

<!-- Relative to the page, under the UI's own prefix: /ui/signin, or /ui/requests/<id>,
     which signs in and returns to the request (the router only gives a well-formed ID). -->
<FullPageState
  icon="lock"
  badge="person"
  title={m.signin_title()}
  body={m.signin_body()}
  {note}
  cta={{ href: request ? `requests/${request}` : 'signin', label: m.signin_action(), icon: 'arrow' }}
/>
