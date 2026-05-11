import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/api": "http://localhost:8080",
      "/healthz": "http://localhost:8080",
    },
  },
  build: {
    outDir: "dist",
    // Do not wipe dist/.gitkeep — it pins the directory so `go:embed` can
    // find at least one file on a fresh clone before `npm run build` runs.
    emptyOutDir: false,
    sourcemap: false,
  },
});
