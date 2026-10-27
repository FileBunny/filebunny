<script lang="ts">
  import { copyText } from '../lib/format.ts';

  let { label, value }: { label: string; value: string } = $props();
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
    <button type="button" onclick={copy}>{copied ? 'Copied' : 'Copy'}</button>
  </span>
</label>
