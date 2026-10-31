<script lang="ts">
  // The owner's view of an upload link: what arrived, opening it with the
  // private key (which stays in this page), deleting, revoking. Decrypted
  // content is shown as text or offered as a file, never rendered as HTML.
  import { onDestroy, onMount } from 'svelte';
  import type { PrivateKey } from 'openpgp';
  import {
    ApiError,
    deleteInboxFile,
    fetchInboxFile,
    inboxInfo,
    inboxStatus,
    revokeInbox,
    siteBase,
    type InboxFile,
    type InboxStatus,
  } from '../lib/api.ts';
  import { decrypt, KeyError, readPrivateKey, readPublicKeys, type Decrypted } from '../lib/crypto.ts';
  import { uploadLink } from '../lib/route.ts';
  import { copyText, formatBytes, formatDate } from '../lib/format.ts';
  import CopyField from '../components/CopyField.svelte';
  import ErrorMessage from '../components/ErrorMessage.svelte';

  let { id, token }: { id: string; token: string } = $props();

  let status = $state.raw<InboxStatus | null>(null);
  let link = $state('');
  let error = $state('');
  let gone = $state(false);
  let revoked = $state(false);
  let confirmRevoke = $state(false);
  let confirmDelete = $state('');

  // The private key, unlocked once for all files.
  let keyInput = $state('');
  let keyFile: Uint8Array | null = null;
  let passphrase = $state('');
  let showPassphrase = $state(false);
  let privateKey = $state.raw<PrivateKey | null>(null);
  let keyError = $state('');

  type Opened = { result: Decrypted; url: string };
  let opened = $state.raw<Record<string, Opened>>({});
  let busyFile = $state('');
  let fileError = $state<Record<string, string>>({});
  let copied = $state('');

  onMount(refresh);
  onDestroy(() => {
    for (const o of Object.values(opened)) if (o.url) URL.revokeObjectURL(o.url);
  });

  async function refresh() {
    error = '';
    try {
      status = await inboxStatus(id, token);
    } catch (err) {
      if (err instanceof ApiError && (err.status === 404 || err.status === 403)) gone = true;
      else error = 'Could not reach the server. Try again later.';
      return;
    }
    // The upload link again, if the link is still open (needs the key's fingerprint).
    if (!link) {
      const info = await inboxInfo(id).catch(() => null);
      const keys = info ? await readPublicKeys(info.publicKey).catch(() => []) : [];
      if (keys.length === 1) link = uploadLink(siteBase(), id, keys[0]!.fingerprint);
    }
  }

  async function pickKeyFile(e: Event & { currentTarget: HTMLInputElement }) {
    const input = e.currentTarget;
    const f = input.files?.[0];
    keyFile = f ? new Uint8Array(await f.arrayBuffer()) : null;
  }

  async function unlock() {
    keyError = '';
    const source = keyInput.trim() || keyFile;
    if (!source) {
      keyError = 'Paste your private key or choose its file.';
      return;
    }
    try {
      privateKey = await readPrivateKey(source, passphrase);
      passphrase = '';
    } catch (err) {
      const reason = err instanceof KeyError ? err.reason : 'invalid';
      keyError =
        reason === 'passphrase-needed'
          ? 'This private key is protected by a passphrase. Enter it below.'
          : reason === 'wrong-passphrase'
            ? 'The passphrase is not correct for this key.'
            : 'This is not a valid OpenPGP private key.';
    }
  }

  async function open(f: InboxFile) {
    if (!privateKey) return;
    busyFile = f.id;
    fileError = { ...fileError, [f.id]: '' };
    try {
      const ct = await fetchInboxFile(id, token, f.id);
      const result = await decrypt(ct, { privateKeys: [privateKey] });
      const url =
        result.kind === 'file'
          ? URL.createObjectURL(new Blob([result.data as BlobPart], { type: 'application/octet-stream' }))
          : '';
      opened = { ...opened, [f.id]: { result, url } };
    } catch (err) {
      fileError = {
        ...fileError,
        [f.id]:
          err instanceof ApiError
            ? 'Download failed; it may have expired.'
            : 'Could not decrypt this with your key. It may have been encrypted to another key, or be damaged.',
      };
    } finally {
      busyFile = '';
    }
  }

  async function saveEncrypted(f: InboxFile) {
    busyFile = f.id;
    try {
      const ct = await fetchInboxFile(id, token, f.id);
      const url = URL.createObjectURL(new Blob([ct as BlobPart], { type: 'application/octet-stream' }));
      const a = document.createElement('a');
      a.href = url;
      a.download = `filebunny-${f.receivedAt.toISOString().slice(0, 10)}-${f.id.slice(0, 6)}.gpg`;
      a.click();
      setTimeout(() => URL.revokeObjectURL(url), 10_000);
    } catch {
      fileError = { ...fileError, [f.id]: 'Download failed; it may have expired.' };
    } finally {
      busyFile = '';
    }
  }

  async function remove(f: InboxFile) {
    confirmDelete = '';
    try {
      await deleteInboxFile(id, token, f.id);
      const { [f.id]: o, ...rest } = opened;
      if (o?.url) URL.revokeObjectURL(o.url);
      opened = rest;
      await refresh();
    } catch {
      fileError = { ...fileError, [f.id]: 'Deleting failed. Try again.' };
    }
  }

  async function revoke() {
    try {
      await revokeInbox(id, token);
      revoked = true;
    } catch {
      error = 'Revoking failed. Try again.';
      confirmRevoke = false;
    }
  }

  async function copy(t: string, key: string) {
    if (await copyText(t)) {
      copied = key;
      setTimeout(() => (copied = ''), 2000);
    }
  }
</script>

<h1>Your upload link</h1>

{#if revoked}
  <p class="ok">The upload link is revoked and everything it received is deleted.</p>
{:else if gone}
  <p>This upload link does not exist any more, or this management link is wrong.</p>
{:else if !status}
  {#if !error}<p>Loading…</p>{/if}
{:else}
  <p>
    {#if status.open}
      <span class="ok">Open</span> until {formatDate(status.expiresAt)}.
    {:else if status.expiresAt > new Date()}
      <span class="notice">Full</span>: no uploads or space left.
    {:else}
      <span class="notice">Closed</span> since {formatDate(status.expiresAt)}; received files stay until they expire.
    {/if}
    {status.uploads} of {status.maxUploads} uploads used, {formatBytes(status.bytes)} of {formatBytes(status.maxBytes)}.
  </p>
  {#if link && status.open}
    <CopyField label="Upload link to share" value={link} />
  {/if}

  <section class="recipients" aria-labelledby="received-title">
    <h3 id="received-title">Received ({status.files.length})</h3>
    {#if status.files.length === 0}
      <p class="note">Nothing yet. <button type="button" class="small" onclick={refresh}>Refresh</button></p>
    {:else}
      {#if !privateKey}
        <p class="note">Unlock your private key below to open what you received, or save it encrypted for gpg.</p>
      {/if}
      {#each status.files as f (f.id)}
        {@const o = opened[f.id]}
        <div class="key chosen">
          <div class="uid">Received {formatDate(f.receivedAt)}</div>
          <div class="note">{formatBytes(f.size)} encrypted, kept until {formatDate(f.expiresAt)}</div>
          {#if o?.result.kind === 'text'}
            <pre class="message">{o.result.text}</pre>
            <button type="button" class="small" onclick={() => o.result.kind === 'text' && copy(o.result.text, f.id)}>
              {copied === f.id ? 'Copied' : 'Copy text'}
            </button>
          {:else if o?.result.kind === 'file'}
            <p><a class="button primary small" href={o.url} download={o.result.name}>Save {o.result.name}</a></p>
          {/if}
          <div class="row actions">
            {#if privateKey && !o}
              <button type="button" class="small primary" disabled={busyFile === f.id} onclick={() => open(f)}>
                {busyFile === f.id ? 'Opening…' : 'Open'}
              </button>
            {/if}
            <button type="button" class="small" disabled={busyFile === f.id} onclick={() => saveEncrypted(f)}>
              Save encrypted (.gpg)
            </button>
            {#if confirmDelete === f.id}
              <button type="button" class="small danger" onclick={() => remove(f)}>Yes, delete</button>
              <button type="button" class="small" onclick={() => (confirmDelete = '')}>Cancel</button>
            {:else}
              <button type="button" class="small" onclick={() => (confirmDelete = f.id)}>Delete</button>
            {/if}
          </div>
          <ErrorMessage error={fileError[f.id] ?? ''} />
        </div>
      {/each}
      <button type="button" onclick={refresh}>Refresh</button>
    {/if}
  </section>

  {#if status.files.length > 0 && !privateKey}
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
        <span>Passphrase of the key</span>
        <span class="row">
          <input type={showPassphrase ? 'text' : 'password'} bind:value={passphrase} autocomplete="off" />
          <button type="button" onclick={() => (showPassphrase = !showPassphrase)}>{showPassphrase ? 'Hide' : 'Show'}</button>
        </span>
      </label>
      <ErrorMessage error={keyError} />
      <button type="button" class="primary" onclick={unlock}>Unlock</button>
    </fieldset>
  {:else if privateKey}
    <p class="note">
      Private key unlocked in this page. <button type="button" class="small" onclick={() => (privateKey = null)}>Forget it</button>
    </p>
  {/if}

  <details class="more">
    <summary>With gpg instead</summary>
    <p class="note">Save a file encrypted (.gpg) and run:</p>
    <pre>gpg --decrypt filebunny-….gpg > decrypted</pre>
  </details>

  <h3>Revoke</h3>
  <p class="note">Stops all uploads and deletes everything this link received. This cannot be undone.</p>
  {#if confirmRevoke}
    <p class="row">
      <button type="button" class="danger" onclick={revoke}>Yes, revoke and delete everything</button>
      <button type="button" onclick={() => (confirmRevoke = false)}>Cancel</button>
    </p>
  {:else}
    <button type="button" class="danger" onclick={() => (confirmRevoke = true)}>Revoke upload link…</button>
  {/if}
{/if}
<ErrorMessage {error} />
