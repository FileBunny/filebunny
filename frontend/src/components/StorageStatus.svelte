<script lang="ts">
  // Which external storage this server offers, and whether it works now.
  // Fetches when it is shown: the pages mount it only on request, so
  // nothing is fetched just by opening a page.
  import { onMount } from 'svelte';
  import { storageStatus, type StorageStatus } from '../lib/api.ts';
  import { formatDate } from '../lib/format.ts';
  import { storageByName } from '../lib/storage.ts';
  import ErrorMessage from './ErrorMessage.svelte';

  let list = $state<StorageStatus[] | null>(null);
  let error = $state('');

  onMount(() => {
    storageStatus().then(
      (l) => (list = l),
      () => (error = 'Could not reach the server. Try again later.'),
    );
  });
</script>

{#if list === null && !error}
  <p>Loading…</p>
{:else if list}
  <ul class="status">
    <li class="ok"><span class="mark" aria-hidden="true">✓</span>FileBunny (this server): available</li>
    {#each list as s (s.name)}
      <li class={s.available ? 'ok' : 'error'}><span class="mark" aria-hidden="true">{s.available ? '✓' : '✗'}</span>{storageByName(s.name)?.label ?? s.name}, through FileBunny: {s.available ? 'available' : 'not available right now'}
        {#if s.checkedAt}<span class="note">(checked {formatDate(s.checkedAt)})</span>{/if}
      </li>
    {/each}
  </ul>
  {#if list.length === 0}
    <p class="note">This server does not offer external storage.</p>
  {/if}
{/if}
<ErrorMessage {error} />
<p class="note">Uploading directly from your browser does not depend on this server, only on the service being up.</p>
