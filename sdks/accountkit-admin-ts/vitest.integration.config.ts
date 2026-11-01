import { defineConfig } from "vitest/config";
export default defineConfig({
  test: {
    environment: "node",
    include: ["test/integration/**/*.test.ts"],
    testTimeout: 60000,
    hookTimeout: 15000,
    fileParallelism: false,
  },
});
