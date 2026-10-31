<script lang="ts">
  // Decrypts ciphertext that is already in this page, with a password or a
  // private key, and shows or saves the result. The ciphertext stays here,
  // so a wrong password can be retried without fetching it again.
  import { onDestroy } from 'svelte';
  import { decrypt, KeyError, readPrivateKey, type Decrypted } from '../lib/crypto.ts';
  import { copyText, formatBytes } from '../lib/format.ts';
  import ErrorMessage from './ErrorMessage.svelte';

  let { ciphertext, opensWith }: { ciphertext: Uint8Array; opensWith: { password: boolean; key: boolean } } = $props();

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

  onDestroy(() => {
    if (fileUrlObject) URL.revokeObjectURL(fileUrlObject);
  });

  async function pickKeyFile(e: Event & { currentTarget: HTMLInputElement }) {
    const f = e.currentTarget.files?.[0];
    keyFile = f ? new Uint8Array(await f.arrayBuffer()) : null;
  }

  async function open(e: SubmitEvent) {
    e.preventDefault();
    if (busy) return;
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

{#if result}
  {#if result.kind === 'text'}
    <pre class="message">{result.text}</pre>
    <button type="button" onclick={copyResult}>{copied ? 'Copied' : 'Copy text'}</button>
  {:else}
    <p>
      <a class="button primary" href={fileUrlObject} download={result.name}>Save {result.name}</a>
      <span class="note">{formatBytes(result.data.length)}</span>
    </p>
  {/if}
  <p class="note">Decrypted in this browser. Nothing you entered was sent anywhere.</p>
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
