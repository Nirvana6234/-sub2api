"use client";

import { useEffect } from "react";
import { getClientConfig } from "@/config/client";

export function PawPwaRegister() {
  useEffect(() => {
    if (process.env.NODE_ENV !== "production") return;
    if (!("serviceWorker" in navigator)) return;
    const mountPath = getClientConfig().mountPath;
    void navigator.serviceWorker
      .register(`${mountPath}/service-worker.js`)
      .catch(() => {
      // PWA support is optional in embedded desktop shells.
      });
  }, []);

  return null;
}
