import { getBuildConfig, type BuildConfig } from "./build";

export function getClientConfig(): BuildConfig {
  if (typeof window !== "undefined" && window.__PAW_CONFIG__) {
    return window.__PAW_CONFIG__;
  }
  return getBuildConfig();
}
