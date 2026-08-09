import {defineConfig, type Plugin} from 'vite'
import react from '@vitejs/plugin-react'
import {execFile} from 'node:child_process'
import {promisify} from 'node:util'

const run = promisify(execFile)

function simWasm(): Plugin {
  return {
    name: 'sim-wasm',
    configureServer(server) {
      server.watcher.add('../sim')
      server.watcher.on('change', async (file) => {
        if (!file.endsWith('.go')) return
        try {
          await run('go', ['build', '-o', 'public/main.wasm', '../sim/wasm'], {
            env: {...process.env, GOOS: 'js', GOARCH: 'wasm'},
          })
          server.config.logger.info('sim rebuilt')
          server.hot.send({type: 'full-reload'})
        } catch (err) {
          server.config.logger.error(`sim build failed\n${String(err)}`)
        }
      })
    },
    handleHotUpdate({file, server}) {
      if (file.endsWith('utils/wasm.ts')) {
        server.hot.send({type: 'full-reload'})
        return []
      }
    },
  }
}

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), simWasm()],
})