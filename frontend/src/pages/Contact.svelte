<script lang="ts">
  import { siteBase } from '../lib/api.ts';
  import { contact, securityReporting } from '../lib/contact.ts';
</script>

<h1>Contact</h1>

{#if contact.general}
  <p>General questions: <a href="mailto:{contact.general}">{contact.general}</a></p>
{:else}
  <p>This instance has not published a contact address yet.</p>
{/if}

<h2>Reporting illegal content</h2>
<p>
  The operator cannot see what is inside a file: everything is encrypted before it is uploaded, and the server never gets
  the password or key. Files expire within 7 days.
  {#if contact.abuse}
    To report a link, mail <a href="mailto:{contact.abuse}">{contact.abuse}</a> with the link and why you report it.
  {/if}
</p>
<p>
  Material showing the sexual abuse of children: report it to the police or, in the Netherlands, to
  <a href="https://www.offlimits.nl/" rel="noopener noreferrer">Offlimits</a> (Meldpunt Kinderporno). Do not
  send the material itself to anyone, including us.
</p>

<h2>Security</h2>
<p>
  Found a vulnerability? Please report it privately:
  {#if contact.security}
    <a href="mailto:{contact.security}">{contact.security}</a>
    (see <a href="{siteBase()}/.well-known/security.txt">security.txt</a> for the encryption key), or
  {/if}
  through <a href={securityReporting} rel="noopener noreferrer">GitHub's private vulnerability reporting</a>.
</p>
