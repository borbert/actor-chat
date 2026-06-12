import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5174,
    // Allow importing convex/_generated from the repo root.
    fs: { allow: [".."] },
  },
});
