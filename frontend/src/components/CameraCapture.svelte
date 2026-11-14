<script lang="ts" module>
  export type Photo = { blob: Blob; width: number; height: number };
</script>

<script lang="ts">
  // Taking a photo with the camera, for the send page and upload links.
  // The camera only starts on a click and stops as soon as the photo is
  // taken, when this is closed or the page is left. The photo is a JPEG
  // drawn on a canvas (so without EXIF: no location, device or time) and
  // only kept in memory: no object URL, no browser storage. Clearing it
  // empties the canvas and drops the Blob; when the browser frees that
  // memory is up to the browser.
  import { onDestroy, onMount } from 'svelte';
  import {
    cameraConstraints,
    cameraErrorText,
    cameraUnavailableText,
    jpegQuality,
    flipTarget,
    mirrorPreview,
    resolutions,
    type Camera,
    type Resolution,
  } from '../lib/camera.ts';
  import { formatBytes } from '../lib/format.ts';
  import ErrorMessage from './ErrorMessage.svelte';

  let { id, photo = $bindable(null) }: { id: string; photo: Photo | null } = $props();

  let resolution = $state<Resolution>('medium');
  let stream = $state.raw<MediaStream | null>(null);
  let starting = $state(false);
  let error = $state('');
  let deviceIds = $state<string[]>([]);
  let deviceId = $state<string | undefined>(undefined);
  let facing = $state<string | undefined>(undefined);
  // Set when a flip found no camera on the other side.
  let cannotFlip = $state(false);
  let notice = $state('');
  let mirror = $state(false);
  let live = $state({ width: 0, height: 0 });
  let video = $state<HTMLVideoElement>();
  let canvas = $state<HTMLCanvasElement>();
  // Between drawing the photo and getting its JPEG.
  let capturing = $state(false);
  // Each start gets a number, so a slow camera that answers after a newer
  // start (or after closing) is stopped instead of shown.
  let attempt = 0;

  const unavailable = cameraUnavailableText(
    typeof window !== 'undefined' && window.isSecureContext,
    typeof navigator !== 'undefined' && typeof navigator.mediaDevices?.getUserMedia === 'function',
  );
  const flipTo = $derived(cannotFlip ? null : flipTarget(facing, deviceIds, deviceId));

  function stop() {
    attempt++;
    stream?.getTracks().forEach((t) => t.stop());
    stream = null;
    starting = false;
    if (video) video.srcObject = null;
  }

  function clearPhoto() {
    if (canvas) {
      // A zero-sized canvas releases its pixels.
      canvas.width = 0;
      canvas.height = 0;
    }
    photo = null;
  }

  /** Starts a camera; returns the error name if it failed, else ''. */
  async function start(camera: Camera = { deviceId }): Promise<string> {
    stop();
    const mine = ++attempt;
    starting = true;
    error = '';
    notice = '';
    try {
      const s = await navigator.mediaDevices.getUserMedia(cameraConstraints(resolution, camera));
      if (mine !== attempt) {
        s.getTracks().forEach((t) => t.stop());
        return '';
      }
      const settings = s.getVideoTracks()[0]?.getSettings() ?? {};
      stream = s;
      deviceId = settings.deviceId;
      facing = settings.facingMode;
      mirror = mirrorPreview(settings.facingMode);
      live = { width: settings.width ?? 0, height: settings.height ?? 0 };
      if (video) video.srcObject = s;
      // Labels and IDs are only complete once access was granted.
      const devices = await navigator.mediaDevices.enumerateDevices();
      if (mine === attempt) deviceIds = devices.filter((d) => d.kind === 'videoinput').map((d) => d.deviceId);
      return '';
    } catch (err) {
      const name = err instanceof DOMException ? err.name : 'Error';
      if (mine === attempt) error = cameraErrorText(name);
      return name;
    } finally {
      if (mine === attempt) starting = false;
    }
  }

  async function flip() {
    if (!flipTo) return;
    const back = deviceId;
    const failed = await start(flipTo);
    if (failed === 'OverconstrainedError' || failed === 'NotFoundError') {
      // No camera on the other side after all: back to the one we had.
      cannotFlip = true;
      if ((await start({ deviceId: back })) === '') notice = 'There is no other camera to switch to.';
    }
  }

  function take() {
    if (!video || !canvas || !stream || video.videoWidth === 0) return;
    const width = video.videoWidth;
    const height = video.videoHeight;
    capturing = true;
    canvas.width = width;
    canvas.height = height;
    canvas.getContext('2d')?.drawImage(video, 0, 0, width, height);
    // The camera is not needed any more.
    stop();
    canvas.toBlob(
      (blob) => {
        capturing = false;
        if (blob) photo = { blob, width, height };
        else {
          clearPhoto();
          error = 'The photo could not be made. Try again.';
        }
      },
      'image/jpeg',
      jpegQuality,
    );
  }

  function retake() {
    clearPhoto();
    void start();
  }

  function changeResolution() {
    if (stream) void start();
  }

  // Cleared by the page (after sending, or "Send something else"): empty
  // the canvas too.
  $effect(() => {
    if (photo === null && !capturing && canvas && canvas.width > 0) {
      canvas.width = 0;
      canvas.height = 0;
    }
  });

  // Closing the tab, navigating away, or the page going into the
  // back/forward cache: camera off, photo gone.
  function leave() {
    stop();
    clearPhoto();
  }
  onMount(() => {
    window.addEventListener('pagehide', leave);
    return () => window.removeEventListener('pagehide', leave);
  });
  onDestroy(leave);
</script>

<div class="camera">
  {#if unavailable}
    <p class="notice">{unavailable}</p>
  {:else}
    <div class="camera-view" hidden={!stream && !photo && !capturing}>
      <!-- muted and playsinline: iOS only shows a live preview inline with both. -->
      <video bind:this={video} class:mirror hidden={!stream} autoplay muted playsinline aria-label="Camera preview"></video>
      <canvas bind:this={canvas} hidden={!photo && !capturing} aria-label="The photo you took"></canvas>
    </div>

    {#if photo}
      <p class="note" role="status">Photo taken: {photo.width}×{photo.height}, {formatBytes(photo.blob.size)}.</p>
    {:else if stream}
      <p class="note" role="status">Camera on{live.width ? `, ${live.width}×${live.height}` : ''}.</p>
    {/if}

    {#if stream}
      <div class="choice" role="radiogroup" aria-label="Resolution">
        {#each resolutions as r (r.value)}
          <label><input type="radio" bind:group={resolution} value={r.value} onchange={changeResolution} /> {r.label}</label>
        {/each}
      </div>
    {/if}

    <div class="camera-buttons">
      {#if photo}
        <button type="button" {id} onclick={retake}>Retake</button>
      {:else if stream}
        <!-- Short labels, so the three fit on one line on a phone; the
             accessible names contain the visible words. -->
        <button type="button" {id} class="primary" onclick={take}>Take photo</button>
        {#if flipTo}
          <button type="button" onclick={flip} aria-label={flipTo.facing ? 'Flip camera (front and back)' : 'Flip to the next camera'}
            >Flip</button
          >
        {/if}
        <button type="button" onclick={stop} aria-label="Stop camera">Stop</button>
      {:else}
        <button type="button" {id} onclick={() => start()} disabled={starting}>
          {starting ? 'Starting the camera…' : 'Start camera'}
        </button>
      {/if}
    </div>
    {#if notice}<p class="note" role="status">{notice}</p>{/if}
    <ErrorMessage {error} />
    <p class="note">The photo stays in this browser until it is encrypted. It is a JPEG without location or device data.</p>
  {/if}
</div>
