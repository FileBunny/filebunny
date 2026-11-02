<script lang="ts">
  import { contact } from '../lib/contact.ts';
</script>

<h1>Privacy</h1>

<p>
  FileBunny is built to know as little as possible about you. There are no accounts, no cookies, no trackers and no
  analytics, and nothing is loaded from other sites unless you choose a feature that needs it (listed below).
</p>

<h2>What the server stores</h2>
<ul>
  <li>
    <strong>Encrypted data</strong> you upload, until it expires (at most 7 days) or after its last allowed download. The
    server cannot decrypt it: it never gets the password or a private key.
  </li>
  <li>
    <strong>Details of each file:</strong> its size, when it was uploaded and expires, the download limit and count, and a
    hash of the delete link's token.
  </li>
  <li>
    <strong>Upload links:</strong> the public key you register, including the names and e-mail addresses in it, until the
    link is revoked or has expired and is empty. A generated key without name or address avoids this.
  </li>
  <li>
    <strong>Mailed links:</strong> the recipient's address is used for that one mail and not stored. To count mails and to
    respect "no more mail" requests, the server keeps a keyed hash of the address (for counting, until the next day).
  </li>
</ul>

<h2>What the server does not store</h2>
<ul>
  <li>
    <strong>IP addresses:</strong> not in logs, not in the database. To limit abuse, the server keeps a hash of your
    address in memory only; the key of that hash changes every day and is never stored, so the hashes cannot be traced
    back or linked across days.
  </li>
  <li>File names, file types and content: these are inside the encryption.</li>
  <li>Passwords, private keys and passphrases: they never leave your browser.</li>
</ul>

<h2>Other parties, only when you use them</h2>
<ul>
  <li>
    <strong>keys.openpgp.org:</strong> looking up someone's key by e-mail, or publishing your own, goes from your browser
    to keys.openpgp.org, which sees your IP address and the address looked up or published.
  </li>
  <li>
    <strong>Web Key Directory:</strong> if you switch this on, this server fetches the key from the recipient's domain. It
    sees the address looked up but does not store or log it; the domain sees only this server.
  </li>
  <li>
    <strong>Filebin (filebin.net):</strong> stored "through FileBunny", Filebin only sees this server. Stored "directly",
    your browser talks to Filebin, which sees your IP address.
  </li>
  <li>
    <strong>E-mail:</strong> a mailed link goes through a mail relay, which sees the recipient's address and the mail, and
    keeps its own logs. The recipient's mail provider sees it too, as with any mail.
  </li>
  <li>The hosting provider of this server can see that connections are made, as with any website.</li>
</ul>

<h2>Your rights</h2>
<p>
  Under the GDPR you can ask what is stored about you and have it removed. Because FileBunny stores nothing that
  identifies you, it usually cannot find data about you. You can delete your own files with the delete link, revoke an
  upload link with its management link, and stop mail to your address with the link at the bottom of any mail.
  {#if contact.general}Questions: <a href="mailto:{contact.general}">{contact.general}</a>.{/if}
</p>
