import { defineConfig } from "vite";
import solid from "vite-plugin-solid";
import { fileURLToPath, URL } from "node:url";

// In development the Go server is reached through the proxy so that cookies
// and WebSockets behave exactly as in production (same origin).
const apiTarget = process.env.VITE_API_TARGET ?? "http://localhost:8080";

export default defineConfig({
  plugins: [solid()],
  // The bundle version shown in bug reports; the image build passes the
  // same VERSION the Go binary gets.
  define: {
    __APP_VERSION__: JSON.stringify(process.env.APP_VERSION ?? "dev"),
  },
  resolve: {
    alias: {
      "~": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  server: {
    // The end-to-end runner reaches this server by its compose service name.
    allowedHosts: process.env.VITE_ALLOWED_HOSTS ? process.env.VITE_ALLOWED_HOSTS.split(",") : undefined,
    proxy: {
      "/api": { target: apiTarget, changeOrigin: false, ws: true },
      "/media": { target: apiTarget, changeOrigin: false },
      "/healthz": { target: apiTarget, changeOrigin: false },
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    sourcemap: false,
  },
});
