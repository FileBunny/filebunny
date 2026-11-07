<script lang="ts">
  // How FileBunny works and what it protects against: the threat model of
  // docs/architecture.md in plain words.
</script>

<h1>How FileBunny works</h1>

<p>
  FileBunny encrypts a file or message in your browser with OpenPGP, the standard also used by GnuPG and many mail
  programs. Only the encrypted result is uploaded. The recipient downloads it and decrypts it in their own browser, or
  with <code>gpg</code>.
</p>
<ul>
  <li><strong>With a password:</strong> anyone who has the link and the password can open it. Send the password separately from the link, for example by phone.</li>
  <li><strong>With public keys:</strong> only the holders of the matching private keys can open it.</li>
  <li>
    <strong>Upload links:</strong> you create a link with your public key; whatever people send through it is encrypted
    to you, so only you can read it.
  </li>
</ul>
<p>
  Files are deleted when they expire (at most 7 days) or after their last allowed download. There are no accounts.
</p>

<h2>What it protects against</h2>
<ul>
  <li><strong>A curious or compromised server:</strong> it only stores encrypted data and never sees passwords, keys, file names or content.</li>
  <li><strong>Leaked links:</strong> a link never contains the password or a key.</li>
  <li>
    <strong>Tracking:</strong> no cookies, no trackers, no scripts or fonts from elsewhere. IP addresses are not stored or
    logged; limits against abuse use a hash of your IP address that changes every day and is only kept in memory.
  </li>
</ul>

<h2>What it does not protect against</h2>
<ul>
  <li>
    <strong>Altered code from the server.</strong> The code that encrypts comes from the server you load it from. A
    compromised server could send code that reads what you type. The downloadable single HTML file and reproducible
    builds make that harder to hide, not impossible.
  </li>
  <li>
    <strong>Guessing passwords:</strong> anyone with the link to a password-protected file can try passwords offline, as
    often as they like. Short or common passwords will be found. Use Generate, or a long passphrase.
  </li>
  <li>
    <strong>Future quantum computers:</strong> the public-key encryption in OpenPGP today could be broken by a large
    quantum computer. Someone who keeps a copy of a file encrypted to a public key might then open it years from now.
    Password-protected files do not rely on public-key encryption, but they are only as strong as their password.
  </li>
  <li>
    <strong>Metadata:</strong> the server sees the size of the encrypted data and when it is uploaded and downloaded.
    Your network and the hosting provider can see that you connect.
  </li>
  <li>
    <strong>The wrong public key:</strong> a key found online may not belong to the person you think. Compare the
    fingerprint with them through another channel.
  </li>
  <li>
    <strong>External storage you choose:</strong> a file stored directly at an external service of your choice is outside FileBunny's
    control, and that service sees your IP address.
  </li>
  <li><strong>Infected devices</strong> of sender or recipient, and recipients who pass the link and password on.</li>
</ul>

<h2>Abuse limits</h2>
<p>
  Before an upload, a new upload link or a mail, your browser solves a small puzzle (proof of work). It takes a fraction
  of a second, longer on older phones, and longer for someone who uploads a lot. It replaces captchas: nothing is tracked.
</p>

<p class="note">
  FileBunny is free software (AGPL-3.0): <a href="https://github.com/FileBunny/filebunny" rel="noopener noreferrer">source code and documentation</a>.
</p>
