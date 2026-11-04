<script lang="ts">
  import { contact } from '../lib/contact.ts';
</script>

<h1>Terms of use</h1>

<p>By using this service you agree to these terms. If you do not agree, do not use it.</p>

<h2>1. The service</h2>
<ul>
  <li>
    FileBunny lets you share encrypted files and messages. It is offered free of charge, on an "as is" and "as
    available" basis.
  </li>
  <li>
    Files are kept for a limited time. They expire automatically after at most 7 days, or when their download limit is
    reached. They may be removed earlier, for example for legal reasons.
  </li>
  <li>There are no backups. Keep your own copy of anything that matters.</li>
  <li>
    Encryption and decryption happen in your browser. The server stores only encrypted data and never has the password
    or private key needed to decrypt it.
  </li>
  <li>
    Lost passwords and private keys cannot be recovered, and nobody can decrypt, restore or retrieve a file for you. Keep
    your download, delete and management links safe.
  </li>
  <li>Some optional features use other services. Their own terms apply to what you send them.</li>
</ul>

<h2>2. Acceptable use</h2>
<p>You may not use this service to:</p>
<ul>
  <li>Share content that is illegal under Dutch law or other applicable law.</li>
  <li>Share, distribute or facilitate access to child sexual abuse material or any sexual content involving minors.</li>
  <li>Distribute malware, viruses, phishing material or other harmful software.</li>
  <li>Send spam, unsolicited or harassing messages with the e-mail function.</li>
  <li>Infringe copyrights, trademarks, privacy rights or other rights of others.</li>
  <li>Threaten, harass, exploit or otherwise harm other people.</li>
  <li>
    Get around technical limits, abuse resources, overload the infrastructure or interfere with the security or
    availability of the service.
  </li>
</ul>
<p>You are solely responsible for what you upload, the links you share and the people you share them with.</p>

<h2>3. Limits</h2>
<p>
  To keep the service available to everyone, it limits file sizes, storage time, downloads, e-mails and the number of
  actions one visitor can take in a given period. Before uploads, new upload links and e-mails, your browser is asked
  for a small proof of work. Limits are enforced automatically and may change without notice. Requests over a limit
  are refused.
</p>

<h2>4. Illegal content and abuse reports</h2>
<p>
  The operator cannot see what is inside encrypted files and does not inspect or moderate their contents. Suspected
  illegal content, infringements, abuse or security issues can be reported to
  {#if contact.abuse}<a href="mailto:{contact.abuse}">{contact.abuse}</a>{:else}the address on the <a href="#/contact">contact page</a>{/if}.
  Include the link or enough detail to identify the file, and explain why you are reporting it.
</p>
<p>
  The operator cannot judge encrypted content. Reports are read, and the operator may remove a file or act where the
  law requires it. Everything stored expires within 7 days.
</p>

<h2>5. Privacy and security</h2>
<p>
  What the service stores, and what it does not, is described on the <a href="#/privacy">privacy page</a>. No
  encryption or internet service can guarantee absolute security. You are responsible for protecting your passwords,
  private keys and links.
</p>

<h2>6. Availability and liability</h2>
<p>
  There is no guarantee of availability, reliability, security or suitability for any purpose. The operator does not
  guarantee that files stay available until they expire, that downloads always succeed or that the service runs without
  interruption.
</p>
<p>
  To the maximum extent permitted by law, the operator is not liable for damage from using or being unable to use the
  service, including data loss, interruptions, unauthorized access or disclosure of information. Nothing in these terms
  excludes or limits liability where Dutch or European law does not allow that.
</p>

<h2>7. Changes and termination</h2>
<p>
  The operator may change, restrict, suspend or stop the service, or any part of it, at any time. These terms may be
  updated; the version on this page applies. Continued use after an update means you accept the new terms, to the
  extent the law allows.
</p>

<h2>8. Applicable law</h2>
<p>
  The service is operated from the Netherlands. These terms are governed by Dutch law, without prejudice to mandatory
  law that protects you.
</p>
