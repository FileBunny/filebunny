<script lang="ts">
  // "E-mail the link" on the result page of Send. Shown only when the
  // server mails (it is asked after the upload, not on page load). The
  // server sends a fixed text with the link; the address is not stored.
  import { onMount } from 'svelte';
  import { ApiError, mailAvailable, mailLink } from '../lib/api.ts';
  import ErrorMessage from './ErrorMessage.svelte';

  let { id, deleteToken }: { id: string; deleteToken: string } = $props();

  let available = $state(false);
  let email = $state('');
  let busy = $state(false);
  let error = $state('');
  let sent = $state<string[]>([]);

  onMount(() => {
    mailAvailable().then((a) => (available = a));
  });

  async function send(e: SubmitEvent) {
    e.preventDefault();
    if (busy) return;
    const to = email.trim();
    if (!to) {
      error = 'Enter an e-mail address first.';
      return;
    }
    busy = true;
    error = '';
    try {
      await mailLink(id, deleteToken, to);
      sent = [...sent, to];
      email = '';
    } catch (err) {
      error = err instanceof ApiError ? `Not sent: ${err.message}.` : 'Not sent. Check your connection and try again.';
    } finally {
      busy = false;
    }
  }
</script>

{#if available}
  <form class="mail-link" onsubmit={send}>
    <label class="field">
      <span>E-mail the link</span>
      <span class="row">
        <input type="email" bind:value={email} autocomplete="off" placeholder="name@example.org" maxlength="254" />
        <button type="submit" disabled={busy}>{busy ? 'Sending…' : 'Send'}</button>
      </span>
    </label>
    <p class="note">
      FileBunny mails the link in a fixed text, never the password or the delete link. The address is used for this
      mail only and not stored.
    </p>
    {#each sent as to, i (i)}
      <p class="ok">Sent to {to}</p>
    {/each}
    {#if sent.length > 0}
      <p class="note">Ask the recipient to check their spam folder: mail from FileBunny may land there.</p>
    {/if}
    <ErrorMessage {error} />
  </form>
{/if}
