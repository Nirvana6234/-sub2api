"use client";

import { useEffect } from "react";
import { getClientConfig } from "@/config/client";

export function PawPwaRegister() {
  useEffect(() => {
    if (!("serviceWorker" in navigator)) return;

    // A production PWA worker can remain registered on the same local origin
    // while developing. Remove it so stale cached chunks cannot mask dev files.
    if (process.env.NODE_ENV !== "production") {
      void navigator.serviceWorker.getRegistrations().then((registrations) =>
        Promise.all(registrations.map((registration) => registration.unregister())),
      );
      void caches
        .keys()
        .then((keys) =>
          Promise.all(
            keys
              .filter((key) => key.startsWith("paw-shell-"))
              .map((key) => caches.delete(key)),
          ),
        );
      return;
    }

    const mountPath = getClientConfig().mountPath;
    void navigator.serviceWorker
      .register(`${mountPath}/service-worker.js`)
      .catch(() => {
      // PWA support is optional in embedded desktop shells.
      });
  }, []);

  return null;
}
