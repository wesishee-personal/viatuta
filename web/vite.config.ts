import { defineConfig, loadEnv } from 'vite';
import react from '@vitejs/plugin-react';

// The dev server proxies the API so the browser sees one origin. That keeps
// CORS out of the development loop entirely and means VITE_API_BASE only
// matters for a deployed build.
const API_TARGET = process.env.VITE_API_TARGET ?? 'http://localhost:8080';

export default defineConfig(({ mode }) => {
  // Everything prefixed VITE_ is inlined into the bundle at build time, so a
  // secret token here would be published, not configured. Fail the build
  // rather than emit one — a dist/ that has already been written is a leak
  // whether or not anyone deploys it.
  const env = loadEnv(mode, process.cwd(), 'VITE_');
  const token = env['VITE_MAPBOX_TOKEN'] ?? '';
  if (token.startsWith('sk.')) {
    throw new Error(
      'VITE_MAPBOX_TOKEN is a SECRET Mapbox token (sk.). It would be compiled into the ' +
        'public JavaScript bundle. Revoke it at https://account.mapbox.com/access-tokens/ ' +
        'and use a public pk. token instead.',
    );
  }

  return {
    plugins: [react()],
    server: {
      port: 5173,
      proxy: {
        '/v1': API_TARGET,
        '/healthz': API_TARGET,
        '/readyz': API_TARGET,
      },
    },
  };
});
