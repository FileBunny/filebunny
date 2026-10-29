<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { ApiError, download, fileInfo, fileUrl, type FileInfo } from '../lib/api.ts';
  import { decrypt, KeyError, messageInfo, readPrivateKey, type Decrypted } from '../lib/crypto.ts';
  import { copyText, formatBytes, formatDate } from '../lib/format.ts';
  import ErrorMessage from '../components/ErrorMessage.svelte';

  let { id }: { id: string } = $props();

  let info = $state<FileInfo | null | undefined>(undefined); // undefined: loading
  let ciphertext = $state<Uint8Array | null>(null);
  let opensWith = $state({ password: false, key: false });
  let received = $state(0);
  let busy = $state(false);
  let error = $state('');

  let password = $state('');
  let showPassword = $state(false);
  let keyInput = $state('');
  let keyFile = $state<Uint8Array | null>(null);
  let passphrase = $state('');
  let showPassphrase = $state(false);

  let result = $state<Decrypted | null>(null);
  let fileUrlObject = $state('');
  let copied = $state(false);

  onMount(() => {
    fileInfo(id).then(
      (i) => (info = i),
      () => (error = 'Could not reach the server. Try again later.'),
    );
  });

  onDestroy(() => {
    if (fileUrlObject) URL.revokeObjectURL(fileUrlObject);
  });

  async function fetchCiphertext() {
    busy = true;
    error = '';
    try {
      ciphertext = await download(id, (n) => (received = n));
      opensWith = await messageInfo(ciphertext);
    } catch (err) {
      ciphertext = null;
      error =
        err instanceof ApiError && err.status === 404
          ? 'This file no longer exists: it expired, was deleted or has no downloads left.'
          : 'The download failed or this is not an OpenPGP message.';
    } finally {
      busy = false;
    }
  }

  async function pickKeyFile(e: Event & { currentTarget: HTMLInputElement }) {
    const f = e.currentTarget.files?.[0];
    keyFile = f ? new Uint8Array(await f.arrayBuffer()) : null;
  }

  // The ciphertext stays in this page, so a wrong password can be retried
  // without using up another download.
  async function open(e: SubmitEvent) {
    e.preventDefault();
    if (!ciphertext || busy) return;
    busy = true;
    error = '';
    try {
      let secret;
      if (password) {
        secret = { password };
      } else {
        const keySource = keyInput.trim() || keyFile;
        if (!keySource) throw new UserError('Enter the password or load your private key.');
        const key = await readPrivateKey(keySource, passphrase).catch((err: unknown) => {
          throw new UserError(keyErrorMessage(err));
        });
        secret = { privateKeys: [key] };
      }
      result = await decrypt(ciphertext, secret);
      if (result.kind === 'file') {
        fileUrlObject = URL.createObjectURL(new Blob([result.data as BlobPart], { type: 'application/octet-stream' }));
      }
    } catch (err) {
      error = err instanceof UserError ? err.message : 'Decryption failed: wrong password or key, or the data is damaged.';
    } finally {
      busy = false;
    }
  }

  class UserError extends Error {}

  function keyErrorMessage(err: unknown): string {
    switch (err instanceof KeyError ? err.reason : 'invalid') {
      case 'passphrase-needed':
        return 'This private key is protected by a passphrase. Enter it below.';
      case 'wrong-passphrase':
        return 'The passphrase is not correct for this key.';
      default:
        return 'This is not a valid OpenPGP private key.';
    }
  }

  async function copyResult() {
    if (result?.kind === 'text') copied = await copyText(result.text);
  }
</script>

<h1>Encrypted file</h1>

{#if info === undefined && !error}
  <p>Loading…</p>
{:else if info === null && !ciphertext}
  <p>This file does not exist, has expired, or has no downloads left.</p>
  <p><a href="#/">Share something new</a></p>
{:else if result}
  {#if result.kind === 'text'}
    <pre class="message">{result.text}</pre>
    <button type="button" onclick={copyResult}>{copied ? 'Copied' : 'Copy text'}</button>
  {:else}
    <p>
      <a class="button primary" href={fileUrlObject} download={result.name}>Save {result.name}</a>
      <span class="note">{formatBytes(result.data.length)}</span>
    </p>
  {/if}
  <p class="note">Decrypted in this browser. Nothing you entered was sent to the server.</p>
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
  <form onsubmit={open}>
    {#if opensWith.password}
      <label class="field">
        <span>Password</span>
        <span class="row">
          <input type={showPassword ? 'text' : 'password'} bind:value={password} autocomplete="off" />
          <button type="button" onclick={() => (showPassword = !showPassword)}>{showPassword ? 'Hide' : 'Show'}</button>
        </span>
      </label>
    {/if}
    {#if opensWith.key}
      <fieldset>
        <legend>Your private key</legend>
        <p class="note">The key stays in this browser and is never uploaded.</p>
        <label class="field">
          <span>Private key, ASCII-armored</span>
          <textarea bind:value={keyInput} rows="4" spellcheck="false" placeholder="-----BEGIN PGP PRIVATE KEY BLOCK-----"></textarea>
        </label>
        <label class="field">
          <span>…or load a key file</span>
          <input type="file" onchange={pickKeyFile} />
        </label>
        <label class="field">
          <span>Passphrase of the key (if any)</span>
          <span class="row">
            <input type={showPassphrase ? 'text' : 'password'} bind:value={passphrase} autocomplete="off" />
            <button type="button" onclick={() => (showPassphrase = !showPassphrase)}>{showPassphrase ? 'Hide' : 'Show'}</button>
          </span>
        </label>
      </fieldset>
    {/if}
    <ErrorMessage {error} />
    <button type="submit" class="primary" disabled={busy}>Decrypt</button>
  </form>
{/if}
