<script lang="ts">
  import { onMount } from 'svelte';
  import { ApiError, download, fileInfo, fileUrl, type FileInfo } from '../lib/api.ts';
  import { messageInfo } from '../lib/crypto.ts';
  import { formatBytes, formatDate } from '../lib/format.ts';
  import Decrypt from '../components/Decrypt.svelte';
  import ErrorMessage from '../components/ErrorMessage.svelte';

  let { id }: { id: string } = $props();

  let info = $state<FileInfo | null | undefined>(undefined); // undefined: loading
  let ciphertext = $state.raw<Uint8Array | null>(null);
  let opensWith = $state({ password: false, key: false });
  let received = $state(0);
  let busy = $state(false);
  let error = $state('');

  onMount(() => {
    fileInfo(id).then(
      (i) => (info = i),
      () => (error = 'Could not reach the server. Try again later.'),
    );
  });

  async function fetchCiphertext() {
    busy = true;
    error = '';
    try {
      const data = await download(id, (n) => (received = n));
      opensWith = await messageInfo(data);
      ciphertext = data;
    } catch (err) {
      ciphertext = null;
      error =
        err instanceof ApiError && err.status === 404
          ? 'This file no longer exists: it expired, was deleted or has no downloads left.'
          : err instanceof ApiError && err.status >= 500
            ? 'The server could not deliver the file right now. Try again later.'
            : 'The download failed or this is not an OpenPGP message.';
    } finally {
      busy = false;
    }
  }
</script>

<h1>Encrypted file</h1>

{#if info === undefined && !error}
  <p>Loading…</p>
{:else if info === null && !ciphertext}
  <p>This file does not exist, has expired, or has no downloads left.</p>
  <p><a href="#/">Share something new</a></p>
{:else if !ciphertext}
  {#if info}
    <p>
      {formatBytes(info.size)} of encrypted data, available until {formatDate(info.expiresAt)}.
      {#if info.downloadsLeft === 1}
        <strong>This is the last download: afterwards the file is deleted.</strong>
      {:else if info.downloadsLeft !== null}
        {info.downloadsLeft} downloads left.
      {/if}
    </p>
    <p class="note">The file is downloaded first and then decrypted in this browser.</p>
    {#if busy}
      <progress max={info.size} value={received}></progress>
    {:else}
      <button type="button" class="primary" onclick={fetchCiphertext}>Download</button>
    {/if}
  {/if}
  <ErrorMessage {error} />

  {#if info}
    <details>
      <summary>Download and decrypt with gpg instead</summary>
      <p class="note">This also uses up a download.</p>
      <pre>curl -fo message.gpg {fileUrl(id)}
gpg --decrypt message.gpg > decrypted</pre>
      <p class="note">
        <code>gpg --list-packets message.gpg</code> shows the original file name (<code>name=…</code>) after you
        enter the password or key.
      </p>
    </details>
  {/if}
{:else}
  <Decrypt {ciphertext} {opensWith} />
{/if}
