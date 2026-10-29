<script lang="ts">
  import { parseRoute } from './lib/route.ts';
  import Send from './pages/Send.svelte';
  import Download from './pages/Download.svelte';
  import Delete from './pages/Delete.svelte';
  import Receive from './pages/Receive.svelte';
  import KeyPage from './pages/KeyPage.svelte';
  import InboxUpload from './pages/InboxUpload.svelte';
  import InboxManage from './pages/InboxManage.svelte';

  let route = $state(parseRoute(location.hash));

  $effect(() => {
    const update = () => (route = parseRoute(location.hash));
    window.addEventListener('hashchange', update);
    return () => window.removeEventListener('hashchange', update);
  });
</script>

<header>
  <a href="#/" class="brand">FileBunny</a>
  <span class="tagline">Encrypt in your browser with OpenPGP, share anonymously.</span>
  <nav aria-label="Main">
    <a href="#/" aria-current={route.page === 'send' ? 'page' : undefined}>Send</a>
    <a href="#/receive" aria-current={route.page === 'receive' ? 'page' : undefined}>Receive</a>
    <a href="#/key" aria-current={route.page === 'key' ? 'page' : undefined}>Create a key</a>
  </nav>
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
  {:else}
    <h1>Page not found</h1>
    <p>Check the link, or <a href="#/">share something new</a>.</p>
  {/if}
</main>

<footer>
  Everything is encrypted in this browser before it is uploaded. The server only stores ciphertext and no IP addresses.
</footer>
