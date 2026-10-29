<script lang="ts">
  // Creating an upload link: the owner's public key, then the limits. The
  // public key (with its user IDs) is stored on the server; the private
  // key never leaves this browser.
  import { createInbox, ApiError, siteBase, type InboxCreated } from '../lib/api.ts';
  import { readPublicKeys, type KeyInfo, type NewKeyPair } from '../lib/crypto.ts';
  import type { Recipient } from '../lib/keylookup.ts';
  import { manageLink, uploadLink } from '../lib/route.ts';
  import { formatDate } from '../lib/format.ts';
  import CopyField from '../components/CopyField.svelte';
  import ErrorMessage from '../components/ErrorMessage.svelte';
  import Info from '../components/Info.svelte';
  import KeyGen from '../components/KeyGen.svelte';
  import KeyPicker from '../components/KeyPicker.svelte';

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
  let busy = $state(false);
  let error = $state('');
  let result = $state.raw<{ inbox: InboxCreated; fingerprint: string } | null>(null);

  const key = $derived(keySource === 'new' ? generated : (existing[0]?.info ?? null));

  function useGenerated(pair: NewKeyPair) {
    generated = pair.info;
    generatedArmored = pair.publicKey;
  }

  async function create() {
    if (!key || busy) return;
    busy = true;
    error = '';
    try {
      // Send exactly the one key, armored, whatever form it came in.
      const armored = keySource === 'new' ? generatedArmored : key.key.armor();
      const [check] = await readPublicKeys(armored);
      if (!check || check.fingerprint !== key.fingerprint) throw new Error('key changed');
      const inbox = await createInbox(armored, { expires, maxUploads });
      result = { inbox, fingerprint: key.fingerprint };
    } catch (err) {
      error =
        err instanceof ApiError && err.status === 503
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
      {result.inbox.maxUploads === 1 ? 'upload' : 'uploads'}. Everything sent is encrypted to your key
      <code>{result.fingerprint}</code>.
    </p>
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
    <div class="choice">
      <label><input type="radio" bind:group={keySource} value="new" /> Create a new key pair</label>
      <label><input type="radio" bind:group={keySource} value="existing" /> Use my existing public key</label>
    </div>
    {#if keySource === 'new'}
      {#if generated}
        <div class="key chosen">
          {#each generated.userIDs as uid (uid)}<div class="uid">{uid}</div>{/each}
          <code class="fingerprint">{generated.fingerprint}</code>
          <div class="note">New key pair, private key saved by you.</div>
        </div>
        <button type="button" onclick={() => (generated = null)}>Create another key pair</button>
      {:else}
        <KeyGen onCreated={useGenerated} continueLabel="Use this key for the upload link" />
      {/if}
    {:else}
      <KeyPicker bind:recipients={existing} single title="Your public key" />
    {/if}
    <p class="note">
      The server stores your public key, so senders can fetch it.
      <Info label="What the server sees">
        A public key contains the name and e-mail address in its user IDs, so {new URL(siteBase()).host} sees whose upload
        link this is. A new key pair without name or address avoids that. The server never sees your private key or what
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
    <p class="note">At most 100 MB in total. Each upload is kept for 7 days after it arrives.</p>
  </fieldset>

  <ErrorMessage {error} />
  <button type="button" class="primary" disabled={!key || busy} onclick={create}>
    {busy ? 'Creating…' : 'Create upload link'}
  </button>
{/if}
