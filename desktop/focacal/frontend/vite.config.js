import {defineConfig} from 'vite'
import react from '@vitejs/plugin-react'

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/auth': 'http://localhost:5233',
      '/health': 'http://localhost:5233',
      '/events': 'http://localhost:5233',
      '/conflicts': 'http://localhost:5233',
      '/sources': 'http://localhost:5233',
      '/destinations': 'http://localhost:5233',
      '/config': 'http://localhost:5233',
    },
  },
})
