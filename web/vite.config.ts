import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "../internal/web/dist",
    emptyOutDir: true,
  },
  server: {
    proxy: {
      "/api": "http://127.0.0.1:8080",
      "/lang": "http://127.0.0.1:8080",
      "/auth": "http://127.0.0.1:8080",
      "/oauth": "http://127.0.0.1:8080",
      "/.well-known": "http://127.0.0.1:8080",
    },
  },
});
