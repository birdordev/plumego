import react from '@vitejs/plugin-react'
import { defineConfig, loadEnv } from 'vite'

export default defineConfig(({ mode }) => {
  const { CLOUD_MANAGER_DEV_API } = loadEnv(mode, process.cwd(), 'CLOUD_MANAGER_')
  const proxy = CLOUD_MANAGER_DEV_API
    ? Object.fromEntries(['/v1', '/console/login', '/console/logout', '/console/session'].map((path) =>
        [path, { target: CLOUD_MANAGER_DEV_API }]))
    : undefined

  return {
    base: '/console/',
    plugins: [react()],
    server: { host: 'localhost', proxy },
    build: {
      outDir: '_embedded',
      license: { fileName: 'third-party-licenses.md' },
      cssCodeSplit: false,
      rolldownOptions: {
        output: {
          entryFileNames: 'app.js',
          chunkFileNames: 'assets/[name]-[hash].js',
          assetFileNames: 'styles[extname]',
          codeSplitting: {
            groups: [
              { name: 'react-runtime', test: /[\\/]node_modules[\\/](?:react|react-dom|scheduler)[\\/]/ },
              { name: 'zod', test: /[\\/]node_modules[\\/]zod[\\/]/ },
            ],
          },
        },
      },
    },
  }
})
