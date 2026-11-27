<script lang="ts">
  import { onMount } from 'svelte';
  import { ApiError, deleteFile, offeredStorage } from '../lib/api.ts';
  import { storages } from '../lib/storage.ts';
  import ErrorMessage from '../components/ErrorMessage.svelte';

  let { id, token }: { id: string; token: string } = $props();

  // The token stays in this page only: take it out of the address bar, so
  // it does not linger in this tab's history entry, session restore or a
  // shared screen. (The browser's history list may still have the visit.)
  onMount(() => history.replaceState(null, '', `${location.pathname}${location.search}#/`));

  // Services that cannot delete a file before they expire it themselves.
  // This page does not know where the file is, so it names each one this
  // server offers (all of them if it does not say; none until it answers).
  let offered = $state<ReadonlySet<string> | null | undefined>(undefined);
  onMount(() => {
    offeredStorage().then((o) => (offered = o));
  });
  const keeping = $derived(
    storages.filter((s) => s.keepsHours && offered !== undefined && (offered === null || offered.has(s.name))),
  );

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
  <p>Deleted: the share link no longer works.</p>
  <p><a href="#/">Share something new</a></p>
{:else}
  <p>This deletes the encrypted file right away. The share link stops working.</p>
  {#each keeping as s (s.name)}
    <p class="note">
      If it was stored at {s.short}, which cannot delete files, FileBunny stops handing it out right away, but the
      encrypted copy stays at {s.short} until {s.keepsHours} hours after the upload; only this server knows where.
    </p>
  {/each}
  <p class="note">The delete link was removed from the address bar; keep your own copy if you need it later.</p>
  <ErrorMessage {error} />
  <button type="button" class="danger" onclick={remove} disabled={status === 'busy'}>Delete now</button>
{/if}
