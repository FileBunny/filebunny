<script lang="ts" module>
  export type Recording = { blob: Blob; ext: string; seconds: number };
</script>

<script lang="ts">
  // Recording audio with the microphone, for the send page and upload
  // links. The microphone only starts on a click and stops as soon as the
  // recording stops (by the button, at the limit, or when this is closed
  // or the page is left). The recording is only kept in memory: no object
  // URL, no browser storage. Playing it back decodes it with the Web Audio
  // API and drops the decoded sound when playing ends. When the browser
  // frees that memory is up to the browser.
  import { onDestroy, onMount } from 'svelte';
  import {
    audioBitsPerSecond,
    audioConstraints,
    formatDuration,
    level,
    maxSeconds,
    micErrorText,
    micUnavailableText,
    extension,
    pickFormat,
  } from '../lib/audio.ts';
  import { formatBytes } from '../lib/format.ts';
  import ErrorMessage from './ErrorMessage.svelte';

  let {
    id,
    recording = $bindable(null),
    active = $bindable(false),
  }: { id: string; recording: Recording | null; active?: boolean } = $props();

  let stream = $state.raw<MediaStream | null>(null);
  let recorder: MediaRecorder | null = null;
  let chunks: Blob[] = [];
  let starting = $state(false);
  let error = $state('');
  let notice = $state('');
  let elapsed = $state(0);
  let loudness = $state(0);
  let playing = $state(false);
  let decoding = $state(false);
  // Each start gets a number, so a slow microphone that answers after a
  // newer start (or after closing) is stopped instead of used.
  let attempt = 0;
  let began = 0;
  let timer: ReturnType<typeof setInterval> | undefined;
  let frame = 0;
  let meter: AudioContext | null = null;
  let player: AudioContext | null = null;

  const unavailable = micUnavailableText(
    typeof window !== 'undefined' && window.isSecureContext,
    typeof navigator !== 'undefined' && typeof navigator.mediaDevices?.getUserMedia === 'function',
    typeof MediaRecorder === 'function',
  );

  $effect(() => {
    active = stream !== null || starting;
  });

  /** Microphone off, timer and level meter off. */
  function release() {
    attempt++;
    clearInterval(timer);
    cancelAnimationFrame(frame);
    void meter?.close().catch(() => {});
    meter = null;
    stream?.getTracks().forEach((t) => t.stop());
    stream = null;
    starting = false;
    loudness = 0;
  }

  /** Stops recording; keep = false throws what was recorded away. */
  function stop(keep = true) {
    const r = recorder;
    recorder = null;
    if (r && r.state !== 'inactive') {
      if (keep) {
        const seconds = Math.min(maxSeconds, (performance.now() - began) / 1000);
        // The last chunk comes just before onstop.
        r.onstop = () => finish(r.mimeType, seconds);
      } else {
        r.onstop = null;
        r.ondataavailable = null;
        chunks = [];
      }
      r.stop();
    }
    release();
  }

  function finish(mimeType: string, seconds: number) {
    const parts = chunks;
    chunks = [];
    if (parts.length === 0 || seconds < 0.5) {
      error = 'Nothing was recorded. Try again.';
      return;
    }
    const type = mimeType || parts[0]!.type;
    recording = { blob: new Blob(parts, { type }), ext: extension(type), seconds };
  }

  function stopPlaying() {
    void player?.close().catch(() => {});
    player = null;
    playing = false;
    decoding = false;
  }

  function clearRecording() {
    stopPlaying();
    chunks = [];
    recording = null;
  }

  async function start() {
    stop(false);
    clearRecording();
    const mine = ++attempt;
    starting = true;
    error = '';
    notice = '';
    elapsed = 0;
    const format = pickFormat((t) => MediaRecorder.isTypeSupported(t));
    let s: MediaStream;
    try {
      s = await navigator.mediaDevices.getUserMedia(audioConstraints());
    } catch (err) {
      if (mine === attempt) {
        error = micErrorText(err instanceof DOMException ? err.name : 'Error');
        starting = false;
      }
      return;
    }
    if (mine !== attempt) {
      s.getTracks().forEach((t) => t.stop());
      return;
    }
    let r: MediaRecorder;
    try {
      r = new MediaRecorder(s, { audioBitsPerSecond, ...(format ? { mimeType: format.mimeType } : {}) });
    } catch {
      s.getTracks().forEach((t) => t.stop());
      starting = false;
      error = 'This browser cannot record audio here. Choose File instead.';
      return;
    }
    r.ondataavailable = (e) => {
      if (e.data.size > 0) chunks.push(e.data);
    };
    r.onerror = () => {
      stop();
      notice = 'The recording stopped because of an error. What was recorded so far is kept.';
    };
    // The microphone went away (unplugged, or taken by the system): keep
    // what there is.
    s.getAudioTracks()[0]?.addEventListener('ended', () => {
      if (recorder === r) {
        stop();
        notice = 'The microphone stopped. What was recorded so far is kept.';
      }
    });
    stream = s;
    recorder = r;
    starting = false;
    // A chunk every second, so an interruption keeps what came before.
    r.start(1000);
    began = performance.now();
    timer = setInterval(() => {
      elapsed = (performance.now() - began) / 1000;
      if (elapsed >= maxSeconds) {
        stop();
        notice = `The recording stopped at the limit of ${formatDuration(maxSeconds)}.`;
      }
    }, 250);
    showLevel(s);
  }

  // A level meter, so you see that the microphone hears you.
  function showLevel(s: MediaStream) {
    try {
      meter = new AudioContext();
      // Created after an await, so it may start suspended (iOS).
      void meter.resume().catch(() => {});
      const analyser = meter.createAnalyser();
      analyser.fftSize = 512;
      meter.createMediaStreamSource(s).connect(analyser);
      const samples = new Uint8Array(analyser.fftSize);
      const draw = () => {
        if (!meter) return;
        analyser.getByteTimeDomainData(samples);
        loudness = level(samples);
        frame = requestAnimationFrame(draw);
      };
      draw();
    } catch {
      // No meter; recording works without it.
      meter = null;
    }
  }

  async function play() {
    if (!recording || playing) return;
    error = '';
    decoding = true;
    playing = true;
    const ctx = new AudioContext();
    player = ctx;
    try {
      const sound = await ctx.decodeAudioData(await recording.blob.arrayBuffer());
      if (player !== ctx) return;
      const source = ctx.createBufferSource();
      source.buffer = sound;
      source.connect(ctx.destination);
      source.onended = () => {
        if (player === ctx) stopPlaying();
      };
      decoding = false;
      // Created on the click, but resumed after an await (iOS).
      await ctx.resume().catch(() => {});
      source.start();
    } catch {
      if (player === ctx) {
        stopPlaying();
        error = 'This browser cannot play the recording back. It is sent as recorded.';
      }
    }
  }

  // Cleared by the page (after sending, or "Send something else"): stop
  // playing too.
  $effect(() => {
    if (recording === null && player) stopPlaying();
  });

  // Closing the tab, navigating away, or the page going into the
  // back/forward cache: microphone off, recording gone.
  function leave() {
    stop(false);
    clearRecording();
  }
  onMount(() => {
    window.addEventListener('pagehide', leave);
    return () => window.removeEventListener('pagehide', leave);
  });
  onDestroy(leave);
</script>

<div class="recorder">
  {#if unavailable}
    <p class="notice">{unavailable}</p>
  {:else}
    {#if stream}
      <p class="note recording" role="status">
        <span class="dot" aria-hidden="true"></span> Recording: {formatDuration(elapsed)} of {formatDuration(maxSeconds)}
      </p>
      <meter class="level" min="0" max="1" value={loudness} aria-label="Microphone level"></meter>
    {:else if recording}
      <p class="note" role="status">Recorded: {formatDuration(recording.seconds)}, {formatBytes(recording.blob.size)}.</p>
    {/if}

    <div class="camera-buttons">
      {#if stream}
        <button type="button" {id} class="primary" onclick={() => stop()}>Stop recording</button>
      {:else if recording}
        {#if playing}
          <button type="button" onclick={stopPlaying}>{decoding ? 'Preparing…' : 'Stop playing'}</button>
        {:else}
          <button type="button" {id} onclick={play}>Play</button>
        {/if}
        <button type="button" onclick={start}>Record again</button>
      {:else}
        <button type="button" {id} onclick={start} disabled={starting}>
          {starting ? 'Starting the microphone…' : 'Start recording'}
        </button>
      {/if}
    </div>
    {#if notice}<p class="note" role="status">{notice}</p>{/if}
    <ErrorMessage {error} />
    <p class="note">
      At most {formatDuration(maxSeconds)}. The recording stays in this browser until it is encrypted.
    </p>
  {/if}
</div>
