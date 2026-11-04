<script lang="ts">
  // The link at the bottom of every mail. Nothing happens on opening it:
  // mail scanners open links too. The token is a keyed hash of the address;
  // the address itself is not in it and FileBunny does not keep it.
  import { ApiError, setMailBlock } from '../lib/api.ts';
  import ErrorMessage from '../components/ErrorMessage.svelte';

  let { token }: { token: string } = $props();

  let step = $state<'ask' | 'blocked' | 'allowed'>('ask');
  let busy = $state(false);
  let error = $state('');

  async function set(block: boolean) {
    busy = true;
    error = '';
    try {
      await setMailBlock(token, block);
      step = block ? 'blocked' : 'allowed';
    } catch (err) {
      error = err instanceof ApiError && err.status === 404 ? 'This link is not valid.' : 'That did not work. Try again later.';
    } finally {
      busy = false;
    }
  }
</script>

<h1>E-mail from FileBunny</h1>

{#if step === 'blocked'}
  <p>FileBunny will not send e-mail to your address any more.</p>
  <button type="button" disabled={busy} onclick={() => set(false)}>Allow e-mail again</button>
{:else if step === 'allowed'}
  <p>Your address can get links from FileBunny again.</p>
{:else}
  <p>
    FileBunny mailed a link to your address because it was entered when the file was shared. Here you can stop all
    e-mail from FileBunny to your address.
  </p>
  <button type="button" class="primary" disabled={busy} onclick={() => set(true)}>Stop e-mail to my address</button>
{/if}
<ErrorMessage {error} />
<p class="note">FileBunny keeps no list of addresses: only a code derived from your address, to recognize it.</p>
