<script lang="ts">
  // A file stored directly at Filebin (docs/storage/filebin.md). FileBunny's
  // server knows nothing about it: the browser fetches it from Filebin, or,
  // when Filebin wants to check the browser first, the visitor opens it in a
  // tab and loads the saved file here.
  import { messageInfo } from '../lib/crypto.ts';
  import { formatBytes } from '../lib/format.ts';
  import { filebinFetch, filebinFileUrl, filebinKeepsDays } from '../lib/filebin.ts';
  import Decrypt from '../components/Decrypt.svelte';
  import ErrorMessage from '../components/ErrorMessage.svelte';

  let { bin }: { bin: string } = $props();

  let ciphertext = $state.raw<Uint8Array | null>(null);
  let opensWith = $state({ password: false, key: false });
  let step = $state<'start' | 'busy' | 'tab' | 'gone'>('start');
  let received = $state(0);
  let error = $state('');
  let dragging = $state(false);

  const url = $derived(filebinFileUrl(bin));

  async function fetchCiphertext() {
    step = 'busy';
    error = '';
    try {
      const got = await filebinFetch(bin, (n) => (received = n));
      if (got.kind === 'data') await use(got.data, 'start');
      else step = got.kind;
    } catch {
      step = 'tab';
      error = 'The download from Filebin failed. Open it in a new tab instead.';
    }
  }

  /** Takes ciphertext from Filebin or from a saved file. */
  async function use(data: Uint8Array, back: typeof step) {
    try {
      opensWith = await messageInfo(data);
      ciphertext = data;
    } catch {
      step = back;
      error = 'This is not an OpenPGP message. Load the file you saved from Filebin (data.bin).';
    }
  }

  async function load(f: File | undefined) {
    if (!f) return;
    error = '';
    await use(new Uint8Array(await f.arrayBuffer()), 'tab');
  }

  function dragOver(e: DragEvent) {
    e.preventDefault();
    dragging = true;
  }

  async function drop(e: DragEvent) {
    e.preventDefault();
    dragging = false;
    await load(e.dataTransfer?.files[0]);
  }
</script>

<h1>Encrypted file at Filebin</h1>

{#if ciphertext}
  <Decrypt {ciphertext} {opensWith} />
{:else if step === 'gone'}
  <p>This file no longer exists at Filebin: it expired or was deleted.</p>
  <p><a href="#/">Share something new</a></p>
{:else}
  <p>
    This file is stored at Filebin (filebin.net), not at FileBunny. Your browser downloads it from Filebin directly, so
    Filebin sees your IP address. It is decrypted in this browser afterwards.
  </p>
  <p class="note">Filebin keeps files for about {filebinKeepsDays} days.</p>

  {#if step === 'start'}
    <button type="button" class="primary" onclick={fetchCiphertext}>Download from Filebin</button>
  {:else if step === 'busy'}
    <progress></progress>
    <p class="note">{formatBytes(received)} received</p>
  {:else}
    <p>Filebin wants to check your browser first, which only works on its own page.</p>
    <ol>
      <li>
        <a href={url} target="_blank" rel="noopener noreferrer">Open the file at Filebin</a> in a new tab and save it
        (data.bin).
      </li>
      <li>Load the saved file here:</li>
    </ol>
    <div
      class="dropzone"
      class:dragging
      role="group"
      aria-label="Drop the saved file"
      ondragover={dragOver}
      ondragleave={() => (dragging = false)}
      ondrop={drop}
    >
      <label class="field">
        <span>Drop the saved file here, or choose it</span>
        <input type="file" onchange={(e) => load(e.currentTarget.files?.[0])} />
      </label>
    </div>
  {/if}
  <ErrorMessage {error} />

  <details>
    <summary>Download and decrypt with gpg instead</summary>
    <pre>curl -fLo message.gpg {url}
gpg --decrypt message.gpg > decrypted</pre>
  </details>
{/if}
