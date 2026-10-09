import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { existsSync } from "node:fs";
import { fileURLToPath } from "node:url";

// Public exports include these exact shared presentation files.
const shared = (publicPath: string, sourcePath: string) => {
  const path = fileURLToPath(new URL(publicPath, import.meta.url));
  return existsSync(path) ? path : fileURLToPath(new URL(sourcePath, import.meta.url));
};
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    dedupe: ["react", "react-dom"],
    alias: {
      "@shared/mark": shared("./src/shared/mark.tsx", "../src/components/anvil/mark.tsx"),
      "@shared/styles": shared("./src/shared/styles.css", "../src/styles.css"),
    },
  },
  server: { proxy: { "/api": "http://127.0.0.1:8081" } },
});
