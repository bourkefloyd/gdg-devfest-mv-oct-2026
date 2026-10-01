import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 4317,
    strictPort: true,
    proxy: {
      "/v1": {
        target: "http://127.0.0.1:8787",
        changeOrigin: true,
      },
      "/healthz": "http://127.0.0.1:8787",
      "/readyz": "http://127.0.0.1:8787",
    },
  },
});
