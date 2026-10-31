<script lang="ts">
  import { parseRoute } from './lib/route.ts';
  import Send from './pages/Send.svelte';
  import Download from './pages/Download.svelte';
  import Delete from './pages/Delete.svelte';
  import Receive from './pages/Receive.svelte';
  import KeyPage from './pages/KeyPage.svelte';
  import InboxUpload from './pages/InboxUpload.svelte';
  import InboxManage from './pages/InboxManage.svelte';
  import External from './pages/External.svelte';
  import Status from './pages/Status.svelte';

  let route = $state(parseRoute(location.hash));
  const tab = $derived(route.page === 'inbox-manage' ? 'receive' : route.page);

  $effect(() => {
    const update = () => (route = parseRoute(location.hash));
    window.addEventListener('hashchange', update);
    return () => window.removeEventListener('hashchange', update);
  });
</script>

<header>
  <a href="#/" class="brand">FileBunny</a>
  <span class="tagline">Encrypt in your browser with OpenPGP, share anonymously.</span>
  <!-- Someone sending to an upload link has no task to choose; the
       management page belongs to Receive. -->
  {#if route.page !== 'inbox-upload'}
    <nav aria-label="Main">
      <a href="#/" aria-current={tab === 'send' ? 'page' : undefined}>Send</a>
      <a href="#/receive" aria-current={tab === 'receive' ? 'page' : undefined}>Receive</a>
      <a href="#/key" aria-current={tab === 'key' ? 'page' : undefined}>Create a key</a>
    </nav>
  {/if}
</header>

<main>
  {#if route.page === 'send'}
    <Send />
  {:else if route.page === 'download'}
    {#key route.id}
      <Download id={route.id} />
    {/key}
  {:else if route.page === 'delete'}
    {#key route.id}
      <Delete id={route.id} token={route.token} />
    {/key}
  {:else if route.page === 'receive'}
    <Receive />
  {:else if route.page === 'key'}
    <KeyPage />
  {:else if route.page === 'inbox-upload'}
    {#key `${route.id}/${route.fingerprint}`}
      <InboxUpload id={route.id} fingerprint={route.fingerprint} />
    {/key}
  {:else if route.page === 'inbox-manage'}
    {#key route.id}
      <InboxManage id={route.id} token={route.token} />
    {/key}
  {:else if route.page === 'external'}
    {#key route.bin}
      <External bin={route.bin} />
    {/key}
  {:else if route.page === 'status'}
    <Status />
  {:else}
    <h1>Page not found</h1>
    <p>Check the link, or <a href="#/">share something new</a>.</p>
  {/if}
</main>

<footer>
  Everything is encrypted in this browser before it is uploaded. The server only stores ciphertext and no IP addresses.
</footer>
