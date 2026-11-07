<script lang="ts">
  import { copyText } from '../lib/format.ts';

  // primary: the one thing to do next (the share link), in the gradient.
  let { label, value, primary = false }: { label: string; value: string; primary?: boolean } = $props();
  let copied = $state(false);

  async function copy() {
    copied = await copyText(value);
    if (copied) setTimeout(() => (copied = false), 2000);
  }
</script>

<label class="field">
  <span>{label}</span>
  <span class="row">
    <input type="text" readonly {value} onfocus={(e) => e.currentTarget.select()} />
    <button type="button" class:primary onclick={copy}>{copied ? 'Copied' : 'Copy'}</button>
  </span>
</label>
