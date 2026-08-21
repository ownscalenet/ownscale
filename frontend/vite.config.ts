import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'node:path'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    // Build straight into the Go module's embed directory so `go build`
    // (via go:embed) picks up the latest frontend build. See ../Makefile.
    outDir: path.resolve(import.meta.dirname, '../internal/webui/dist'),
    emptyOutDir: true,
  },
  server: {
    proxy: {
      // Dev-server convenience: forward API/auth calls to the Go backend
      // running on :8080 so `npm run dev` works against a live backend.
      '/api': 'http://localhost:8080',
      '/auth': 'http://localhost:8080',
      '/logout': 'http://localhost:8080',
    },
  },
})
