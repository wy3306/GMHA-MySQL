import { defineConfig } from 'vite'

export default defineConfig({
  base: '/',
  define: {
    __VUE_OPTIONS_API__: true,
    __VUE_PROD_DEVTOOLS__: false,
    __VUE_PROD_HYDRATION_MISMATCH_DETAILS__: false
  },
  build: { outDir: 'dist', emptyOutDir: true },
  server: {
    host: '127.0.0.1',
    port: 5173,
    strictPort: true,
    proxy: {
      '/api': process.env.GMHA_API_TARGET || 'http://127.0.0.1:8080',
      '/ws': { target: process.env.GMHA_API_TARGET || 'ws://127.0.0.1:8080', ws: true }
    }
  }
})
