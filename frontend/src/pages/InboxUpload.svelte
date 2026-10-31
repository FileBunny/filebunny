<script lang="ts">
  // Sending to an upload link. The key comes from the server, but the link
  // carries its fingerprint: if they differ, nothing is encrypted or sent,
  // so a server (or database) that swapped the key is caught.
  import { onMount } from 'svelte';
  import { ApiError, inboxInfo, uploadToInbox, type InboxInfo } from '../lib/api.ts';
  import { encrypt, readPublicKeys, type KeyInfo } from '../lib/crypto.ts';
  import { linkFingerprint } from '../lib/route.ts';
  import { formatBytes, formatDate } from '../lib/format.ts';
  import ErrorMessage from '../components/ErrorMessage.svelte';
  import Info from '../components/Info.svelte';

  let { id, fingerprint }: { id: string; fingerprint: string } = $props();

  let info = $state.raw<InboxInfo | null | undefined>(undefined); // undefined: loading
  let key = $state.raw<KeyInfo | null>(null);
  let keyProblem = $state('');

  let what = $state<'text' | 'file'>('text');
  let text = $state('');
  let file = $state<File | null>(null);
  let busy = $state(false);
  let sent = $state(0);
  let error = $state('');
  let done = $state<{ what: string; size: number } | null>(null);
  let controller: AbortController | null = null;

  onMount(load);

  async function load() {
    try {
      info = await inboxInfo(id);
    } catch {
      error = 'Could not reach the server. Try again later.';
      return;
    }
    if (!info) return;
    try {
      const keys = await readPublicKeys(info.publicKey);
      const match = keys.length === 1 && linkFingerprint(keys[0]!.fingerprint) === fingerprint ? keys[0]! : null;
      if (!match) {
        keyProblem =
          'The key the server sent does not match this link. Do not send anything, and tell the person who gave you the link.';
      } else if (!match.canEncrypt) {
        keyProblem = 'The key of this upload link can no longer be used (expired or revoked).';
      } else {
        key = match;
      }
    } catch {
      keyProblem = 'The server sent something that is not a valid public key. Do not send anything.';
    }
  }

  const size = $derived(what === 'file' ? (file?.size ?? 0) : new TextEncoder().encode(text).length);
  // OpenPGP adds a little; the server checks the exact ciphertext size.
  const tooLarge = $derived(info ? size > info.maxFileSize - 1024 : false);
  const ready = $derived(key !== null && (what === 'text' ? text.length > 0 : file !== null) && !tooLarge);

  function pickFile(e: Event & { currentTarget: HTMLInputElement }) {
    file = e.currentTarget.files?.[0] ?? null;
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (!ready || busy || !key) return;
    busy = true;
    error = '';
    sent = 0;
    controller = new AbortController();
    try {
      const plaintext =
        what === 'text' ? { kind: 'text' as const, text } : { kind: 'file' as const, name: file!.name, data: file!.stream() };
      const ciphertext = await encrypt(plaintext, { publicKeys: [key.key] });
      const stored = await uploadToInbox(id, ciphertext, (n) => (sent = n), controller.signal);
      done = { what: what === 'file' ? file!.name : 'Your message', size: stored };
    } catch (err) {
      if (controller.signal.aborted) error = 'Upload cancelled.';
      else if (err instanceof ApiError && err.status === 409) error = 'This upload link is full.';
      else if (err instanceof ApiError && err.status === 404) error = 'This upload link has expired or was revoked.';
      else if (err instanceof ApiError) error = `Upload failed: ${err.message}`;
      else error = 'Encryption or upload failed. Check your connection and try again.';
    } finally {
      busy = false;
      controller = null;
    }
  }

  async function again() {
    done = null;
    text = '';
    file = null;
    info = await inboxInfo(id).catch(() => info);
  }
</script>

<h1>Send to an upload link</h1>

{#if info === undefined && !error}
  <p>Loading…</p>
{:else if info === null}
  <p>This upload link does not exist, has expired or was revoked.</p>
{:else if keyProblem}
  <p class="warning" role="alert">{keyProblem}</p>
{:else if done}
  <p class="ok" role="status">
    {done.what} was encrypted and uploaded ({formatBytes(done.size)}). Only the holder of the private key of
    {key?.userIDs[0] ?? 'this upload link'} can open it.
  </p>
  <p class="note">FileBunny does not notify the recipient; they see it when they check their upload link.</p>
  <button type="button" onclick={again}>Send something else</button>
{:else if info && key}
  <section class="recipients" aria-labelledby="inbox-recipient">
    <h3 id="inbox-recipient">Encrypted for</h3>
    <div class="key chosen">
      {#each key.userIDs as uid (uid)}<div class="uid">{uid}</div>{/each}
      <code class="fingerprint">{key.fingerprint}</code>
    </div>
    <p class="note">
      The key matches the fingerprint in this link.
      <Info label="About this check">
        The link you received contains the key's fingerprint. This page computed the fingerprint of the key the server
        sent and compared the two: a changed database or a swapped key is caught, and nothing is sent. If you got the
        link through a channel you trust, the key belongs to whoever gave it to you. The check runs in code from this
        server, so it cannot protect against a fully compromised server; the signed single HTML file (later) can.
      </Info>
    </p>
  </section>

  {#if info.uploadsLeft === 0 || info.maxFileSize === 0}
    <p class="notice">This upload link is full.</p>
  {:else}
    <p class="note">
      Open until {formatDate(info.expiresAt)}; {info.uploadsLeft} {info.uploadsLeft === 1 ? 'upload' : 'uploads'} and
      {formatBytes(info.maxFileSize)} left.
    </p>
    <form onsubmit={submit}>
      <fieldset disabled={busy}>
        <legend>What do you want to send?</legend>
        <div class="choice">
          <label><input type="radio" bind:group={what} value="text" /> Text</label>
          <label><input type="radio" bind:group={what} value="file" /> File</label>
        </div>
        {#if what === 'text'}
          <label class="field">
            <span>Message</span>
            <textarea bind:value={text} rows="8" spellcheck="false"></textarea>
          </label>
        {:else}
          <label class="field">
            <span>File (max {formatBytes(info.maxFileSize)})</span>
            <input type="file" onchange={pickFile} />
          </label>
          {#if file}<p class="note">{file.name}, {formatBytes(file.size)}</p>{/if}
        {/if}
        {#if tooLarge}<p class="error">Too large for what this link has left.</p>{/if}
      </fieldset>
      <ErrorMessage {error} />
      {#if busy}
        <progress max={size} value={Math.min(sent, size)}></progress>
        <button type="button" onclick={() => controller?.abort()}>Cancel</button>
      {:else}
        <button type="submit" class="primary" disabled={!ready}>Encrypt and send</button>
      {/if}
      <p class="note">Encrypted in this browser before it is sent. The server only gets ciphertext.</p>
    </form>
  {/if}
{/if}
<ErrorMessage error={info === undefined ? error : ''} />
