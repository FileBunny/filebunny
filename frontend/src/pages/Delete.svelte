<script lang="ts">
  import { onMount } from 'svelte';
  import { ApiError, deleteFile } from '../lib/api.ts';
  import ErrorMessage from '../components/ErrorMessage.svelte';

  let { id, token }: { id: string; token: string } = $props();

  // The token stays in this page only: take it out of the address bar, so
  // it does not linger in this tab's history entry, session restore or a
  // shared screen. (The browser's history list may still have the visit.)
  onMount(() => history.replaceState(null, '', `${location.pathname}${location.search}#/`));

  let status = $state<'ready' | 'busy' | 'deleted'>('ready');
  let error = $state('');

  async function remove() {
    status = 'busy';
    error = '';
    try {
      await deleteFile(id, token);
      status = 'deleted';
    } catch (err) {
      status = 'ready';
      if (err instanceof ApiError && err.status === 404) error = 'This file no longer exists.';
      else if (err instanceof ApiError && err.status === 403) error = 'This delete link is not valid for this file.';
      else error = 'Deleting failed. Try again later.';
    }
  }
</script>

<h1>Delete file</h1>
{#if status === 'deleted'}
  <p>The file has been deleted from the server.</p>
  <p><a href="#/">Share something new</a></p>
{:else}
  <p>This deletes the encrypted file right away. The share link stops working.</p>
  <p class="note">The delete link was removed from the address bar; keep your own copy if you need it later.</p>
  <ErrorMessage {error} />
  <button type="button" class="danger" onclick={remove} disabled={status === 'busy'}>Delete now</button>
{/if}
