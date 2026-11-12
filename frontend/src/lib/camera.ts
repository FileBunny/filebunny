// Helpers for taking a photo in the browser (components/CameraCapture).
// The photo is drawn on a canvas and encoded as JPEG there, so it has no
// EXIF data: no location, no device, no time. It never leaves the page
// unencrypted and is not written to any browser storage.

/**
 * The file name inside the OpenPGP message, e.g. "photo-04821.jpg": a
 * random number, so a second photo does not overwrite the first in the
 * recipient's downloads. Neutral: no date or device.
 */
export function photoName(): string {
  // Rejection sampling: 100000 does not divide 2^32, so plain modulo would
  // be (very slightly) biased.
  const limit = Math.floor(2 ** 32 / 100_000) * 100_000;
  const n = new Uint32Array(1);
  do crypto.getRandomValues(n);
  while (n[0]! >= limit);
  return `photo-${String(n[0]! % 100_000).padStart(5, '0')}.jpg`;
}

/** JPEG quality for canvas.toBlob. */
export const jpegQuality = 0.92;

export type Resolution = 'small' | 'medium' | 'largest';

/**
 * What we ask the camera for. Cameras only have a few fixed modes and
 * pick the closest; "largest" asks for more than most deliver.
 */
export const resolutions: { value: Resolution; label: string; width: number; height: number }[] = [
  { value: 'small', label: 'Small', width: 1280, height: 720 },
  { value: 'medium', label: 'Medium', width: 1920, height: 1080 },
  { value: 'largest', label: 'Largest', width: 4096, height: 2160 },
];

export type Facing = 'environment' | 'user';

/**
 * The constraints for getUserMedia: a chosen camera by ID, or else a
 * facing mode (the back camera by default, on phones). Video only.
 */
export function cameraConstraints(resolution: Resolution, camera: { deviceId?: string; facing?: Facing }): MediaStreamConstraints {
  const r = resolutions.find((o) => o.value === resolution) ?? resolutions[1]!;
  const video: MediaTrackConstraints = { width: { ideal: r.width }, height: { ideal: r.height } };
  if (camera.deviceId) video.deviceId = { exact: camera.deviceId };
  else video.facingMode = { ideal: camera.facing ?? 'environment' };
  return { video, audio: false };
}

/** The camera after the current one, round robin; undefined if there is no other. */
export function nextCamera(deviceIds: string[], current: string | undefined): string | undefined {
  const ids = deviceIds.filter((id) => id !== '');
  if (ids.length < 2) return undefined;
  const i = current === undefined ? -1 : ids.indexOf(current);
  return ids[(i + 1) % ids.length];
}

/**
 * Mirror the preview of a camera that faces you, as a mirror would; laptop
 * cameras often report no facing mode. The photo itself is never mirrored.
 */
export function mirrorPreview(facingMode: string | undefined): boolean {
  return facingMode !== 'environment';
}

/** A short message for a failed getUserMedia, by DOMException name. */
export function cameraErrorText(name: string | undefined): string {
  switch (name) {
    case 'NotAllowedError':
    case 'SecurityError':
      return 'The camera is blocked. Allow it for this site in your browser, or choose File.';
    case 'NotFoundError':
    case 'OverconstrainedError':
      return 'No camera found. Choose File to share a photo you already have.';
    case 'NotReadableError':
    case 'AbortError':
      return 'The camera could not be started. Close other apps that use it and try again.';
    default:
      return 'The camera could not be started. Choose File instead.';
  }
}

/** Why the camera cannot be used here at all, or '' if it can. */
export function cameraUnavailableText(secureContext: boolean, hasGetUserMedia: boolean): string {
  if (!secureContext) return 'The camera only works over HTTPS. Choose File instead.';
  if (!hasGetUserMedia) return 'This browser cannot use the camera here. Choose File instead.';
  return '';
}

/** The summary of step 1, e.g. "Photo, 1920×1080, 412 KiB". */
export function photoSummary(width: number, height: number, size: string): string {
  return `Photo, ${width}×${height}, ${size}`;
}
