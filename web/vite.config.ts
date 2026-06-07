import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

const CONTROL = process.env.CONTROL_URL || "http://127.0.0.1:8088";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    port: 5173,
    proxy: {
      "/api": { target: CONTROL, changeOrigin: true },
    },
  },
});
