<script lang="ts">
  import { contact } from '../lib/contact.ts';
</script>

<h1>Terms of use</h1>

<p>By using this service you agree to these terms. If you do not agree, do not use it.</p>

<h2>The service</h2>
<ul>
  <li>
    FileBunny lets you share encrypted files and messages. It is offered free of charge and as is, without any warranty.
    It can be changed, limited or stopped at any time.
  </li>
  <li>
    Files are kept until they expire (at most 7 days) or are downloaded as often as allowed, and may be deleted earlier.
    There are no backups. Keep your own copy of anything that matters.
  </li>
  <li>
    Nobody can recover a lost password or private key, and nobody can decrypt a file for you. Keep the delete link if
    you may want to remove a file yourself.
  </li>
</ul>

<h2>What you may not do</h2>
<ul>
  <li>Share anything that is illegal where you or the recipient are, or in the Netherlands.</li>
  <li>Share sexual material involving minors, in any form.</li>
  <li>Spread malware, phishing or spam, or use the e-mail function to harass anyone.</li>
  <li>Share material that infringes someone else's rights.</li>
  <li>Disrupt the service: get around its limits, overload it, or attack it.</li>
</ul>
<p>You are responsible for what you share and with whom.</p>

<h2>Limits</h2>
<p>
  The service limits sizes, expiry, downloads, mails and how much one visitor can do in an hour or a day, and asks your
  browser for a small proof of work before uploads, new upload links and mails. Limits may change without notice.
</p>

<h2>Liability</h2>
<p>
  To the extent the law allows, the operator is not liable for any damage from using the service, from its
  unavailability, or from the loss or disclosure of files.
</p>

<h2>Reports</h2>
<p>
  The operator cannot see what is inside a file. Reports about illegal content can be sent to
  {#if contact.abuse}<a href="mailto:{contact.abuse}">{contact.abuse}</a>{:else}the address on the <a href="#/contact">contact page</a>{/if}.
  Everything stored expires within 7 days.
</p>

<h2>Changes</h2>
<p>These terms can change; the version on this page applies. This service is operated from the Netherlands, and Dutch law applies.</p>
