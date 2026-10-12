import { defineConfig, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import fs from 'node:fs'
import path from 'node:path'

// wasmPath is where the browser SSH page loads the Tailscale WebAssembly
// client from. The production build copies the module's precompressed
// copies there (see the Makefile's "frontend" target), so the dev server
// has to serve the uncompressed one itself.
const wasmPath = '/assets/main.wasm'

function serveWasmInDev(): Plugin {
  return {
    name: 'ownscale-serve-tailscale-wasm',
    apply: 'serve',
    configureServer(server) {
      server.middlewares.use(wasmPath, (_req, res) => {
        res.setHeader('Content-Type', 'application/wasm')
        fs.createReadStream(
          path.resolve(import.meta.dirname, 'node_modules/@tailscale/connect/main.wasm'),
        ).pipe(res)
      })
    },
  }
}

// xtermRulesOnly returns the rules of css whose selector mentions xterm.
// @tailscale/connect's stylesheet also ships Tailwind's preflight and
// utilities, which would restyle the whole console once the SSH page
// loads it.
function xtermRulesOnly(css: string): string {
  let out = ''
  let depth = 0
  let start = 0
  for (let i = 0; i < css.length; i++) {
    if (css[i] === '{') {
      depth++
    } else if (css[i] === '}') {
      depth--
      if (depth === 0) {
        const rule = css.slice(start, i + 1)
        if (rule.slice(0, rule.indexOf('{')).includes('xterm')) out += rule
        start = i + 1
      }
    }
  }
  return out
}

function stripTailscaleBaseCSS(): Plugin {
  return {
    name: 'ownscale-xterm-css-only',
    enforce: 'pre',
    transform(code, id) {
      if (!id.includes('@tailscale/connect/pkg.css')) return
      return { code: xtermRulesOnly(code), map: null }
    },
  }
}

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss(), serveWasmInDev(), stripTailscaleBaseCSS()],
  build: {
    // Build straight into the Go module's embed directory so `go build`
    // (via go:embed) picks up the latest frontend build. See ../Makefile.
    outDir: path.resolve(import.meta.dirname, '../webui/dist'),
    emptyOutDir: true,
    rollupOptions: {
      output: {
        entryFileNames: 'assets/index.js',
        chunkFileNames: 'assets/[name].js',
        assetFileNames: 'assets/index.[ext]',
      },
    },
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
