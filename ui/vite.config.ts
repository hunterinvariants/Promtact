import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The build output lands in ../web/dist, which the Go server embeds, so the
// product still ships as a single binary.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "../web/dist",
    emptyOutDir: true,
  },
  server: {
    proxy: {
      "/api": "http://127.0.0.1:8080",
    },
  },
});
