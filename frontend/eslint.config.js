import js from "@eslint/js";
import tseslint from "typescript-eslint";

export default [
  {
    ignores: ["dist", "node_modules", "playwright-report", "rspack.config.cjs", "test-results"],
  },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    files: ["src/**/*.{ts,tsx}"],
    languageOptions: {
      globals: {
        clearTimeout: "readonly",
        console: "readonly",
        document: "readonly",
        EventSource: "readonly",
        fetch: "readonly",
        FormData: "readonly",
        HTMLInputElement: "readonly",
        HTMLTextAreaElement: "readonly",
        setTimeout: "readonly",
        URL: "readonly",
        window: "readonly",
        URLSearchParams: "readonly",
      },
    },
    rules: {
      "@typescript-eslint/no-explicit-any": "error",
    },
  },
  {
    files: ["e2e/**/*.ts", "playwright.config.ts"],
    languageOptions: {
      globals: {
        Event: "readonly",
        EventSource: "readonly",
        EventTarget: "readonly",
        MessageEvent: "readonly",
        process: "readonly",
        setTimeout: "readonly",
        URL: "readonly",
        window: "readonly",
      },
    },
  },
];
