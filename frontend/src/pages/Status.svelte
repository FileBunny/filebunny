<script lang="ts">
  // Which external storage this server offers, and whether it works now.
  import { onMount } from 'svelte';
  import { storageStatus, type StorageStatus } from '../lib/api.ts';
  import { formatDate } from '../lib/format.ts';
  import ErrorMessage from '../components/ErrorMessage.svelte';

  const names: Record<string, string> = { filebin: 'Filebin (filebin.net), through FileBunny' };

  let list = $state<StorageStatus[] | null>(null);
  let error = $state('');

  onMount(() => {
    storageStatus().then(
      (l) => (list = l),
      () => (error = 'Could not reach the server. Try again later.'),
    );
  });
</script>

<h1>Storage status</h1>

{#if list === null && !error}
  <p>Loading…</p>
{:else if list}
  <ul class="status">
    <li class="ok">FileBunny (this server): available</li>
    {#each list as s (s.name)}
      <li class={s.available ? 'ok' : 'error'}>
        {names[s.name] ?? s.name}: {s.available ? 'available' : 'not available right now'}
        {#if s.checkedAt}<span class="note">(checked {formatDate(s.checkedAt)})</span>{/if}
      </li>
    {/each}
  </ul>
  {#if list.length === 0}
    <p class="note">This server does not offer external storage.</p>
  {/if}
{/if}
<ErrorMessage {error} />
<p class="note">
  Uploading directly from your browser to Filebin does not depend on this server; it only needs filebin.net to be up.
</p>
