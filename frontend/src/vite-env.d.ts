/// <reference types="svelte" />
/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_FILEBUNNY_API?: string;
  /** The release tag (vX.Y.Z) this build comes from; lib/release.ts. */
  readonly VITE_FILEBUNNY_VERSION?: string;
  /** Contact addresses of this instance (lib/contact.ts). */
  readonly VITE_FILEBUNNY_CONTACT?: string;
  readonly VITE_FILEBUNNY_ABUSE?: string;
  readonly VITE_FILEBUNNY_SECURITY?: string;
}
