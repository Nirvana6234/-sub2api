package nodegw

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/stretchr/testify/require"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func TestNegativeKeyCacheExpiresAndIsInvalidatedByHash(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	c := newNegativeKeyCache(0, clock.now)
	rej := &relayv1.SelectRejection{Status: 401}
	h := keyHash("sk-x")
	require.Len(t, h, 64, "the key is the SHA-256 hex the master's auth cache uses")

	require.Nil(t, c.get(h, false))
	c.put(h, false, rej)
	require.Same(t, rej, c.get(h, false))
	require.Nil(t, c.get(h, true), "Google and non-Google entries are separate")

	clock.t = clock.t.Add(DefaultKeyNegativeTTL - time.Second)
	require.Same(t, rej, c.get(h, false))
	clock.t = clock.t.Add(2 * time.Second)
	require.Nil(t, c.get(h, false), "expires after the TTL")

	c.put(h, false, rej)
	c.put(h, true, rej)
	c.put(keyHash("sk-y"), false, rej)
	c.invalidate([]string{h})
	require.Nil(t, c.get(h, false))
	require.Nil(t, c.get(h, true))
	require.NotNil(t, c.get(keyHash("sk-y"), false), "only the pushed hashes are cleared")
	c.clear()
	require.Nil(t, c.get(keyHash("sk-y"), false))
}

func TestNegativeKeyCacheDisabledAndBounded(t *testing.T) {
	off := newNegativeKeyCache(-1, nil)
	off.put("h", false, &relayv1.SelectRejection{})
	require.Nil(t, off.get("h", false), "a negative TTL turns the cache off")

	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	c := newNegativeKeyCache(time.Minute, clock.now)
	for i := 0; i < negativeKeyCacheMax; i++ {
		c.entries[negativeKey{hash: string(rune(i)) + "x"}] = negativeEntry{expires: clock.t.Add(time.Hour)}
	}
	c.put("fresh", false, &relayv1.SelectRejection{})
	require.LessOrEqual(t, len(c.entries), negativeKeyCacheMax, "a full cache is cleared instead of growing")
	require.NotNil(t, c.get("fresh", false))
}
