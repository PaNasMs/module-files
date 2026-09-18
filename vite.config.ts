import { defineConfig } from 'vite'
import { resolve } from 'node:path'
const id = 'files'
const shared: Record<string, string> = {
  react: 'OstojaOSSDK.react',
  'react/jsx-runtime': 'OstojaOSSDK.jsx',
  '@tanstack/react-query': 'OstojaOSSDK.query',
  '@radix-ui/react-dialog': 'OstojaOSSDK.dialog',
  'react-router-dom': 'OstojaOSSDK.router',
}
for (const name of [
  'ui',
  'operations',
  'removable',
  'runtime',
  'completion',
  'layout',
  'client',
  'i18n',
  'navigation',
])
  shared['@ostojaos/' + name] = 'OstojaOSSDK.' + name
export default defineConfig({
  resolve: {
    alias: {
      '@mdi/js': resolve('node_modules/@mdi/js/mdi.js'),
      '@xterm/xterm': resolve('node_modules/@xterm/xterm'),
      '@xterm/addon-fit': resolve('node_modules/@xterm/addon-fit'),
    },
  },
  build: {
    outDir: resolve('dist/ui'),
    emptyOutDir: true,
    lib: {
      entry: resolve('frontend/' + id + '.tsx'),
      name: 'OstojaOSModule_' + id.replaceAll('-', '_'),
      formats: ['iife'],
      fileName: () => 'index.js',
      cssFileName: 'index',
    },
    rollupOptions: { external: Object.keys(shared), output: { globals: shared } },
  },
})
