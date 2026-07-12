import { defineConfig, type Plugin } from 'vite';

// ghostty-web embeds a base64 WASM fallback even when the caller supplies a
// WASM URL. Let Vite emit that fallback as the same external asset instead.
const embeddedGhosttyWasmPattern = /new URL\("data:application\/wasm;base64,[^"]+", self\.location\)/;

const externalizeGhosttyWasm = (): Plugin => ({
  name: 'externalize-ghostty-wasm',
  enforce: 'pre',
  transform(code, id) {
    if (!id.includes('/node_modules/ghostty-web/dist/ghostty-web.js')) {
      return null;
    }

    if (!embeddedGhosttyWasmPattern.test(code)) {
      this.error('ghostty-web no longer contains the expected embedded WASM fallback');
    }

    return {
      code: code.replace(
        embeddedGhosttyWasmPattern,
        'new URL("./ghostty-vt.wasm", import.meta.url)',
      ),
      map: null,
    };
  },
});

export default defineConfig({
  base: '/',
  plugins: [externalizeGhosttyWasm()],
  build: {
    outDir: '../assets/dist',
    emptyOutDir: true,
    target: 'es2022',
  },
});
