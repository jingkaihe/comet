import { defineConfig } from 'vite';

export default defineConfig({
  base: '/',
  build: {
    outDir: '../assets/dist',
    emptyOutDir: true,
    target: 'es2022',
  },
});
