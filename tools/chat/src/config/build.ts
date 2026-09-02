export interface BuildConfig {
  buildMode: "standalone" | "export";
  isApp: boolean;
  pawServiceUrl: string;
  mountPath: string;
}

export function getBuildConfig(): BuildConfig {
  const buildMode = (process.env.BUILD_MODE as BuildConfig["buildMode"] | undefined) ?? "standalone";
  const mountPath = (process.env.PAW_MOUNT_PATH ?? "")
    .trim()
    .replace(/^\/+|\/+$/g, "");
  return {
    buildMode,
    isApp: !!process.env.BUILD_APP,
    pawServiceUrl: process.env.PAW_SERVICE_URL ?? "",
    mountPath: mountPath ? `/${mountPath}` : "",
  };
}
