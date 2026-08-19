import { beforeEach, describe, expect, it, vi } from "vitest";

const useAppStore = vi.fn();

vi.mock("@/stores/app", () => ({
  useAppStore,
}));

describe("FeatureFlags.playground", () => {
  beforeEach(() => {
    useAppStore.mockReset();
    vi.resetModules();
  });

  it("fails closed when public settings are missing", async () => {
    useAppStore.mockReturnValue({ cachedPublicSettings: undefined });
    const { FeatureFlags, isFeatureFlagEnabled } = await import("../featureFlags");

    expect(isFeatureFlagEnabled(FeatureFlags.playground)).toBe(false);
  });

  it("resolves true only when playground_enabled is explicitly true", async () => {
    useAppStore.mockReturnValue({
      cachedPublicSettings: { playground_enabled: true },
    });
    const { FeatureFlags, isFeatureFlagEnabled } = await import("../featureFlags");

    expect(isFeatureFlagEnabled(FeatureFlags.playground)).toBe(true);
  });

  it("stays disabled when playground_enabled is explicitly false", async () => {
    useAppStore.mockReturnValue({
      cachedPublicSettings: { playground_enabled: false },
    });
    const { FeatureFlags, isFeatureFlagEnabled } = await import("../featureFlags");

    expect(isFeatureFlagEnabled(FeatureFlags.playground)).toBe(false);
  });
});
