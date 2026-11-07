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
    link is revoked or has expired and is empty. A generated key without name or e-mail address avoids this.
  </li>
  <li>
    <strong>Mailed links:</strong> the recipient's e-mail address is used for that one mail and not stored. To count mails and to
    respect "no more mail" requests, the server keeps a keyed hash of the address (for counting, until the next day).
  </li>
</ul>

<h2>What the server does not store</h2>
<ul>
  <li>
    <strong>IP addresses:</strong> not in logs, not in the database. To limit abuse, the server keeps a hash of your
    IP address in memory only; the key of that hash changes every day and is never stored, so the hashes cannot be traced
    back or linked across days.
  </li>
  <li>File names, file types and content: these are inside the encryption.</li>
  <li>Passwords, private keys and passphrases: they never leave your browser.</li>
</ul>

<h2>Other services, only when you use them</h2>
<p>
  Some optional features work with services outside FileBunny, such as keyservers, external storage and mail relays.
  Which services this server works with can change over time. Before anything is sent, the page tells you where it goes
  and who can see what.
</p>
<ul>
  <li>
    <strong>From your browser:</strong> when your browser talks to such a service itself, for example to look up or
    publish a public key, or to store a file there directly, that service sees your IP address and what you send it: the
    e-mail address you look up or publish, or the encrypted file.
  </li>
  <li>
    <strong>Through this server:</strong> when this server talks to a service for you, for example to fetch a key from
    the recipient's domain or to store a file elsewhere "through FileBunny", the service sees only this server. This
    server does not store or log what it passes on.
  </li>
  <li>
    <strong>E-mail:</strong> a mailed link goes through a mail relay, which sees the recipient's e-mail address and the mail, and
    keeps its own logs. The recipient's mail provider sees it too, as with any mail.
  </li>
  <li>
    These services keep their own logs under their own terms. They never get your password, your private key or the
    content of a file.
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
