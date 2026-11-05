// Which release this page is, and where to check it (docs/verify.md).
// The version is set at build time from the release tag; other builds say
// "dev".

const v = (import.meta.env?.VITE_FILEBUNNY_VERSION ?? '').trim();

/** vX.Y.Z (or vX.Y.Z-rc.N) for a release build, "dev" otherwise. */
export const version = /^v\d+\.\d+\.\d+(-rc\.\d+)?$/.test(v) ? v : 'dev';

const repo = 'https://github.com/FileBunny/filebunny';

/** How to check that this site or a download matches a release. */
export const verifyUrl = `${repo}/blob/main/docs/verify.md`;

/** The signed releases, including the single HTML file. */
export const releasesUrl = version === 'dev' ? `${repo}/releases` : `${repo}/releases/tag/${version}`;
