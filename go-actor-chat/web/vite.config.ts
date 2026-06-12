import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    // Allow importing convex/_generated from the repo root.
    fs: { allow: [".."] },
  },
});
