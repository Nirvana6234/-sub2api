// Run with `npm test` (node --test; Node strips the types itself).

import assert from "node:assert/strict";
import { test } from "node:test";

import {
  MAIN_SITE_EXPIRES_AT_KEY,
  MAIN_SITE_TOKEN_KEY,
  extractPairCode,
  mainSiteAccessToken,
  normalizePairCode,
} from "./handoffCore.ts";

function store(values: Record<string, string>) {
  return { getItem: (key: string) => (key in values ? values[key] : null) };
}

test("pair code: six digits, spaces ignored", () => {
  assert.equal(normalizePairCode("123456"), "123456");
  assert.equal(normalizePairCode(" 123 456 "), "123456");
  assert.equal(normalizePairCode("12345"), null);
  assert.equal(normalizePairCode("12a456"), null);
  assert.equal(normalizePairCode(null), null);
});

test("extract: code taken and removed from the address, other params kept", () => {
  assert.deepEqual(extractPairCode("https://gongfeiai.com/paw/?pair=123456&x=1#top"), {
    code: "123456",
    cleanedUrl: "/paw/?x=1#top",
  });
  assert.deepEqual(extractPairCode("https://gongfeiai.com/paw/?pair=abc"), { code: null, cleanedUrl: "/paw/" });
  assert.equal(extractPairCode("https://gongfeiai.com/paw/"), null);
});

test("main-site token: adopted with its expiry, refused near expiry", () => {
  const now = 1_000_000;
  assert.deepEqual(
    mainSiteAccessToken(store({ [MAIN_SITE_TOKEN_KEY]: "jwt", [MAIN_SITE_EXPIRES_AT_KEY]: String(now + 3_600_000) }), now),
    { accessToken: "jwt", expiresAt: now + 3_600_000 },
  );
  assert.equal(
    mainSiteAccessToken(store({ [MAIN_SITE_TOKEN_KEY]: "jwt", [MAIN_SITE_EXPIRES_AT_KEY]: String(now + 10_000) }), now),
    null,
  );
  assert.deepEqual(mainSiteAccessToken(store({ [MAIN_SITE_TOKEN_KEY]: "jwt" }), now), { accessToken: "jwt" });
  assert.equal(mainSiteAccessToken(store({}), now), null);
});
