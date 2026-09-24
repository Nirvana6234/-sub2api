import type { BuildConfig } from "@/config/build";

declare global {
  interface Window {
    __PAW_CONFIG__?: BuildConfig;
  }
}

export {};
