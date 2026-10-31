<script lang="ts">
  // Generates a key pair in this browser. The private key is shown only as
  // a file to save; the user must confirm they saved it (and know the
  // passphrase) before going on, because nobody can recover it.
  import { onDestroy } from 'svelte';
  import { generateKeyPair, type NewKeyPair } from '../lib/crypto.ts';
  import { publishKey, type PublishStatus } from '../lib/keylookup.ts';
  import CopyField from './CopyField.svelte';
  import ErrorMessage from './ErrorMessage.svelte';
  import Info from './Info.svelte';
  import SecretField from './SecretField.svelte';

  let { onCreated, continueLabel = 'Continue' }: { onCreated?: (pair: NewKeyPair) => void; continueLabel?: string } =
    $props();

  let name = $state('');
  let email = $state('');
  let passphrase = $state('');
  let busy = $state(false);
  let error = $state('');

  let pair = $state.raw<NewKeyPair | null>(null);
  let privateUrl = $state('');
  let publicUrl = $state('');
  let savedPrivate = $state(false);
  let confirmed = $state(false);
  let continueError = $state('');

  let publishing = $state(false);
  let published = $state.raw<PublishStatus[] | null>(null);
  let publishError = $state('');

  const fileBase = $derived(
    (name.trim() || email.trim() || 'filebunny').replace(/[^A-Za-z0-9._@-]+/g, '-').replace(/^-+|-+$/g, '') ||
      'filebunny',
  );

  onDestroy(revokeUrls);

  function revokeUrls() {
    if (privateUrl) URL.revokeObjectURL(privateUrl);
    if (publicUrl) URL.revokeObjectURL(publicUrl);
  }

  async function create() {
    error = '';
    if (passphrase.length < 8) {
      error = 'Choose a passphrase of at least 8 characters, or generate one.';
      return;
    }
    if (email.trim() !== '' && !/^[^\s@<>]+@[^\s@<>]+\.[^\s@<>]+$/.test(email.trim())) {
      error = 'This does not look like an e-mail address. Leave it empty if you do not want one in the key.';
      return;
    }
    busy = true;
    try {
      pair = await generateKeyPair({ name, email, passphrase });
      revokeUrls();
      privateUrl = URL.createObjectURL(new Blob([pair.privateKey], { type: 'application/pgp-keys' }));
      publicUrl = URL.createObjectURL(new Blob([pair.publicKey], { type: 'application/pgp-keys' }));
    } catch {
      error = 'Generating the key pair failed.';
    } finally {
      busy = false;
    }
  }

  function startOver() {
    revokeUrls();
    pair = null;
    privateUrl = publicUrl = '';
    savedPrivate = confirmed = false;
    continueError = '';
    published = null;
    publishError = '';
  }

  async function publish() {
    if (!pair) return;
    publishing = true;
    publishError = '';
    try {
      published = await publishKey(pair.publicKey);
    } catch (err) {
      publishError = err instanceof Error ? err.message : 'Publishing failed.';
    } finally {
      publishing = false;
    }
  }

  function savedIt() {
    savedPrivate = true;
    continueError = '';
  }

  // The button stays enabled and says what is missing: a greyed-out
  // button does not tell you why.
  function continueWith() {
    if (!pair) return;
    if (!savedPrivate) {
      continueError = 'Save the private key first.';
      document.getElementById('save-private')?.focus();
    } else if (!confirmed) {
      continueError = 'Tick the box to confirm you saved the private key.';
      document.getElementById('confirm-saved')?.focus();
    } else {
      continueError = '';
      onCreated?.(pair);
    }
  }

  const publishText: Record<PublishStatus['status'], string> = {
    published: 'published',
    pending: 'check your mailbox: keys.openpgp.org sent a verification link',
    unpublished: 'not published',
    revoked: 'revoked',
  };
</script>

{#if !pair}
  <div class="keygen">
    <label class="field">
      <span>Name (optional)</span>
      <input type="text" bind:value={name} autocomplete="off" />
    </label>
    <label class="field">
      <span>E-mail address (optional)</span>
      <input type="email" bind:value={email} autocomplete="off" spellcheck="false" />
    </label>
    <p class="note">
      Anyone who gets your public key sees the name and address; leave both empty for an anonymous key.
      <Info label="Name and e-mail in the key">
        Without them the key only says "FileBunny key". An e-mail address lets others find your key on a keyserver, if
        you choose to publish it.
      </Info>
    </p>
    <SecretField label="Passphrase for the private key" bind:value={passphrase} />
    <p class="notice">There is no way to recover a lost passphrase or private key. Write the passphrase down.</p>
    <ErrorMessage {error} />
    <button type="button" class="primary" onclick={create} disabled={busy}>{busy ? 'Creating…' : 'Create key pair'}</button>
  </div>
{:else}
  <div class="keygen">
    <div class="key chosen">
      {#each pair.info.userIDs as uid (uid)}<div class="uid">{uid}</div>{/each}
      <code class="fingerprint">{pair.info.fingerprint}</code>
    </div>

    <h3>Save your private key</h3>
    <p class="warning">
      Your private key and its passphrase are the only way to open what is sent to you. FileBunny keeps no copy: if you
      lose either, nobody can open those files.
    </p>
    <p class="row">
      <a id="save-private" class="button primary" href={privateUrl} download={`${fileBase}-private-key.asc`} onclick={savedIt}>
        Save private key (.asc)
      </a>
      <a class="button" href={publicUrl} download={`${fileBase}-public-key.asc`}>Save public key (.asc)</a>
    </p>
    <label class="checkbox">
      <input id="confirm-saved" type="checkbox" bind:checked={confirmed} onchange={() => (continueError = '')} />
      I saved the private key in a safe place and I know its passphrase.
    </label>

    {#if pair.info.userIDs.some((u) => u.includes('@'))}
      <h3>Publish it (optional)</h3>
      <p class="note">
        So others can find your key by e-mail address.
        <Info label="About publishing">
          Your browser sends the public key directly to keys.openpgp.org, which then mails a verification link to the
          address. Only after you click it is the address findable. keys.openpgp.org sees your IP address. You can remove
          the key there later.
        </Info>
      </p>
      {#if published}
        <ul class="sources">
          {#each published as p (p.email)}
            <li class={p.status === 'published' ? 'ok' : 'notice'}>{p.email}: {publishText[p.status]}</li>
          {/each}
        </ul>
      {:else}
        <button type="button" onclick={publish} disabled={publishing}>
          {publishing ? 'Publishing…' : 'Publish on keys.openpgp.org'}
        </button>
      {/if}
      <ErrorMessage error={publishError} />
    {/if}

    <details class="more">
      <summary>Show the public key as text</summary>
      <CopyField label="Public key" value={pair.publicKey} />
    </details>

    <p class="row">
      {#if onCreated}
        <button type="button" class="primary" onclick={continueWith}>{continueLabel}</button>
      {/if}
      <button type="button" onclick={startOver}>Start over</button>
    </p>
    <ErrorMessage error={continueError} />
  </div>
{/if}
