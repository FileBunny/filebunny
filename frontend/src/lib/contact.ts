// The contact addresses of this instance, set at build time
// (VITE_FILEBUNNY_CONTACT, _ABUSE, _SECURITY in frontend/.env); abuse and
// security fall back to the general address. Empty when not published.

const env = import.meta.env ?? {};

/** A plain address (name@domain), or empty if the value is not one. */
function address(v: string | undefined): string {
  const a = (v ?? '').trim();
  return /^[^\s@<>"]+@[^\s@<>"]+\.[^\s@<>"]+$/.test(a) ? a : '';
}

const general = address(env.VITE_FILEBUNNY_CONTACT);

export const contact = {
  general,
  abuse: address(env.VITE_FILEBUNNY_ABUSE) || general,
  security: address(env.VITE_FILEBUNNY_SECURITY) || general,
};

/** Where security issues of the software go, whatever the instance. */
export const securityReporting = 'https://github.com/FileBunny/filebunny/security/advisories/new';
