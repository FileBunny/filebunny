import { defineConfig, type Plugin } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// `vite build --mode single` builds one self-contained HTML file
// (dist-single/filebunny.html) that can be saved and opened from disk. It
// talks to the API at VITE_FILEBUNNY_API (see .env.single).
export default defineConfig(({ mode }) => {
  const single = mode === 'single';
  return {
    plugins: [svelte(), single && singleFile()],
    build: {
      target: 'es2022',
      outDir: single ? 'dist-single' : 'dist',
      // No inline scripts or styles, so a strict CSP stays possible.
      assetsInlineLimit: 0,
      modulePreload: { polyfill: false },
    },
    server: {
      // During development the Go backend runs on :8080.
      proxy: { '/api': 'http://127.0.0.1:8080' },
    },
  };
});

/**
 * Inlines the one script and the one stylesheet into the HTML and adds a
 * CSP that allows exactly those (by hash) and connections to the API only.
 * Fails the build if anything else would be left as a separate file.
 */
function singleFile(): Plugin {
  let api = '';
  const sha256 = async (s: string) => {
    const digest = new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(s)));
    return `'sha256-${btoa(String.fromCharCode(...digest))}'`;
  };
  return {
    name: 'filebunny-single-file',
    enforce: 'post',
    configResolved(config) {
      api = String(config.env.VITE_FILEBUNNY_API ?? '');
      // http only for local testing against `make run`.
      if (!/^(https:\/\/[^/]+|http:\/\/(localhost|127\.0\.0\.1)(:\d+)?)$/.test(api)) {
        throw new Error(`VITE_FILEBUNNY_API must be an https origin without a path, got "${api}"`);
      }
    },
    async generateBundle(_, bundle) {
      const html = bundle['index.html'];
      if (html?.type !== 'asset') throw new Error('single file: index.html not found');
      let page = String(html.source);
      let script = '';
      let style = '';

      page = page.replace(/<script type="module" crossorigin src="\/([^"]+)"><\/script>\s*/, (_m, name: string) => {
        const chunk = bundle[name];
        if (chunk?.type !== 'chunk') throw new Error(`single file: script ${name} not found`);
        script = chunk.code.replace(/<\/(script)/gi, '<\\/$1');
        delete bundle[name];
        return '';
      });
      page = page.replace(/<link rel="stylesheet" crossorigin href="\/([^"]+)">\s*/, (_m, name: string) => {
        const asset = bundle[name];
        if (asset?.type !== 'asset') throw new Error(`single file: stylesheet ${name} not found`);
        style = String(asset.source);
        if (/<\/style/i.test(style)) throw new Error('single file: stylesheet contains </style');
        delete bundle[name];
        return '';
      });
      if (!script) throw new Error('single file: no script found in index.html');

      const csp = [
        "default-src 'none'",
        `script-src ${await sha256(script)}`,
        `style-src ${await sha256(style)}`,
        `connect-src ${api}`,
        "img-src 'self' data:",
        "base-uri 'none'",
        "form-action 'none'",
      ].join('; ');
      // Replacement functions, not strings: the code contains "$&" and the like.
      page = page
        .replace('<meta charset="utf-8" />', () => `<meta charset="utf-8" />\n    <meta http-equiv="Content-Security-Policy" content="${csp}" />`)
        .replace('</head>', () => `  <style>${style}</style>\n  </head>`)
        .replace('</body>', () => `  <script type="module">${script}</script>\n  </body>`);

      const left = Object.keys(bundle).filter((name) => name !== 'index.html');
      if (left.length > 0) throw new Error(`single file: unexpected extra files ${left.join(', ')}`);
      delete bundle['index.html'];
      this.emitFile({ type: 'asset', fileName: 'filebunny.html', source: page });
    },
  };
}
