<script lang="ts">
  // Creating an upload link: the owner's public key, then the limits. The
  // public key (with its user IDs) is stored on the server; the private
  // key never leaves this browser.
  import { createInbox, ApiError, siteBase, type InboxCreated } from '../lib/api.ts';
  import { tick } from 'svelte';
  import { readPublicKeys, type KeyInfo, type NewKeyPair } from '../lib/crypto.ts';
  import type { Recipient } from '../lib/keylookup.ts';
  import { manageLink, uploadLink } from '../lib/route.ts';
  import { formatBytes, formatDate } from '../lib/format.ts';
  import { storageLabel, storages } from '../lib/storage.ts';
  import CopyField from '../components/CopyField.svelte';
  import ErrorMessage from '../components/ErrorMessage.svelte';
  import Info from '../components/Info.svelte';
  import KeyGen from '../components/KeyGen.svelte';
  import KeyPicker from '../components/KeyPicker.svelte';
  import StorageStatus from '../components/StorageStatus.svelte';

  const expiryOptions = [
    { value: '24h', label: '1 day' },
    { value: '168h', label: '7 days' },
    { value: '720h', label: '30 days' },
  ];
  const uploadOptions = [1, 5, 10, 50, 100];

  let keySource = $state<'new' | 'existing'>('new');
  // Raw: OpenPGP key objects must not be wrapped in proxies.
  let existing = $state.raw<Recipient[]>([]);
  let generated = $state.raw<KeyInfo | null>(null);
  let generatedArmored = '';

  let expires = $state('168h');
  let maxUploads = $state(10);
  // Where uploads to the link are kept; always through FileBunny, so the
  // limits of the link work and the service sees neither side.
  let storageName = $state('local');
  let statusOpen = $state(false);
  let busy = $state(false);
  let error = $state('');
  let missingKey = $state(false);
  let result = $state.raw<{ inbox: InboxCreated; fingerprint: string } | null>(null);

  const key = $derived(keySource === 'new' ? generated : (existing[0]?.info ?? null));

  // Once there is a key, the tabs fold away to the chosen key.
  function changeKey() {
    generated = null;
    generatedArmored = '';
    existing = [];
  }

  // Tabs: arrow keys move between them, as for any tab list.
  const sources = ['new', 'existing'] as const;
  async function tabKey(e: KeyboardEvent) {
    if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return;
    e.preventDefault();
    keySource = keySource === 'new' ? 'existing' : 'new';
    await tick();
    document.getElementById(`sourcetab-${keySource}`)?.focus();
  }

  function days(seconds: number): string {
    const d = Math.round(seconds / 86400);
    return d === 1 ? '1 day' : `${d} days`;
  }

  function useGenerated(pair: NewKeyPair) {
    generated = pair.info;
    generatedArmored = pair.publicKey;
  }

  async function create() {
    if (busy) return;
    // The button stays enabled and says what is missing.
    if (!key) {
      missingKey = true;
      document.getElementById(`sourcetab-${keySource}`)?.focus();
      return;
    }
    busy = true;
    error = '';
    try {
      // Send exactly the one key, armored, whatever form it came in.
      const armored = keySource === 'new' ? generatedArmored : key.key.armor();
      const [check] = await readPublicKeys(armored);
      if (!check || check.fingerprint !== key.fingerprint) throw new Error('key changed');
      const storage = storageName === 'local' ? undefined : storageName;
      const inbox = await createInbox(armored, { expires, maxUploads, storage });
      result = { inbox, fingerprint: key.fingerprint };
    } catch (err) {
      error =
        err instanceof ApiError && err.status === 503 && storageName !== 'local'
          ? `${storageLabel(storageName)} is not available right now. Try again later, or keep uploads on FileBunny.`
          : err instanceof ApiError && err.status === 503
          ? 'This server has too many open upload links right now. Try again later.'
          : err instanceof ApiError
            ? `Creating the upload link failed: ${err.message}`
            : 'Creating the upload link failed. Check your connection and try again.';
    } finally {
      busy = false;
    }
  }
</script>

{#if result}
  <section>
    <h1>Your upload link is ready</h1>
    <CopyField label="Upload link: give this to the people who send you files" value={uploadLink(siteBase(), result.inbox.id, result.fingerprint)} />
    <p class="note">
      Open until {formatDate(result.inbox.expiresAt)}, for at most {result.inbox.maxUploads}
      {result.inbox.maxUploads === 1 ? 'upload' : 'uploads'}. Senders can upload up to
      {formatBytes(result.inbox.maxSize)} in total; each upload is deleted {days(result.inbox.keepSeconds)} after it
      arrives. Everything sent is encrypted to your key <code>{result.fingerprint}</code>.
    </p>
    {#if result.inbox.storage}
      <p class="note">
        Uploads are kept at {storageLabel(result.inbox.storage)} through FileBunny; {storageLabel(result.inbox.storage)}
        only sees this server.
      </p>
    {/if}
    <CopyField label="Management link: keep this to yourself" value={manageLink(siteBase(), result.inbox.id, result.inbox.manageToken)} />
    <p class="warning">
      Save the management link now. It is the only way to see, download and delete what you receive, and it cannot be
      shown again.
    </p>
    <p><a href={`#/m/${result.inbox.id}/${result.inbox.manageToken}`}>Open the management page</a></p>
  </section>
{:else}
  <h1>Receive files</h1>
  <p>
    Create a link that others use to send you files or messages. They are encrypted in the sender's browser with your
    public key: only you can open them.
  </p>

  <fieldset disabled={busy}>
    <legend>1. Your key</legend>
    {#if key}
      <section class="recipients" aria-labelledby="your-key">
        <h3 id="your-key">Your key</h3>
        <div class="key chosen">
          {#each key.userIDs as uid (uid)}<div class="uid">{uid}</div>{/each}
          <code class="fingerprint">{key.fingerprint}</code>
          {#if keySource === 'new'}<div class="note">New key pair; you saved the private key.</div>{/if}
        </div>
        <button type="button" onclick={changeKey}>Change key</button>
      </section>
    {:else}
      <div class="tabs" role="tablist" aria-label="Which key">
        {#each sources as t (t)}
          <button
            type="button"
            role="tab"
            id={`sourcetab-${t}`}
            aria-selected={keySource === t}
            aria-controls={`sourcepanel-${t}`}
            tabindex={keySource === t ? 0 : -1}
            onclick={() => (keySource = t)}
            onkeydown={tabKey}
          >
            {t === 'new' ? 'Create a new key pair' : 'Use my existing public key'}
          </button>
        {/each}
      </div>
      <div class="tabpanel" role="tabpanel" id={`sourcepanel-${keySource}`} aria-labelledby={`sourcetab-${keySource}`}>
        {#if keySource === 'new'}
          <KeyGen onCreated={useGenerated} continueLabel="Use this key for the upload link" />
        {:else}
          <KeyPicker bind:recipients={existing} single title="Your public key" />
        {/if}
      </div>
    {/if}
    <p class="note">
      FileBunny stores your public key with this upload link, so senders get it automatically.
      <Info label="What FileBunny sees">
        A public key contains the name and e-mail address in its user IDs, so FileBunny sees whose upload link this is. A new key pair without name or address avoids that. The server never sees your private key or what
        is sent to you.
      </Info>
    </p>
  </fieldset>

  <fieldset disabled={busy}>
    <legend>2. How long and how often?</legend>
    <div class="row">
      <label class="field">
        <span>Link open for</span>
        <select bind:value={expires}>
          {#each expiryOptions as o (o.value)}<option value={o.value}>{o.label}</option>{/each}
        </select>
      </label>
      <label class="field">
        <span>Number of uploads</span>
        <select bind:value={maxUploads}>
          {#each uploadOptions as n (n)}<option value={n}>{n === 1 ? '1 upload' : `${n} uploads`}</option>{/each}
        </select>
      </label>
    </div>
    <p class="note">You see how much senders can upload, and how long each upload is kept, once the link is created.</p>
  </fieldset>

  <fieldset disabled={busy}>
    <legend>3. Where are uploads kept?</legend>
    <label class="field">
      <span>Stored at</span>
      <select bind:value={storageName}>
        {#each storages as o (o.name)}<option value={o.name}>{o.label}</option>{/each}
      </select>
    </label>
    {#if storageName !== 'local'}
      <p class="note">
        Sent through FileBunny: {storageLabel(storageName)} sees this server, not the senders or you, and only gets
        encrypted data. You fetch the files through FileBunny.
      </p>
    {/if}
    <details class="more" bind:open={statusOpen}>
      <summary>Storage status</summary>
      {#if statusOpen}<StorageStatus />{/if}
    </details>
  </fieldset>

  <ErrorMessage error={missingKey && !key ? 'Choose your key first: create a new key pair or use one you have.' : error} />
  <button type="button" class="primary" disabled={busy} onclick={create}>
    {busy ? 'Creating…' : 'Create upload link'}
  </button>
{/if}
