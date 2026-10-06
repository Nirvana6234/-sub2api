package sign

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHandoffMarkerBindsNodeMethodPathAndTime(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	now := time.Unix(1_700_000_000, 0)
	h := SignHandoff(key, 7, "post", "/v1/live", now)

	id, err := VerifyHandoff(key, h, "POST", "/v1/live", now.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, int64(7), id)

	for name, tc := range map[string]struct {
		key          []byte
		header       string
		method, path string
		at           time.Time
	}{
		"other path":   {key, h, "POST", "/v1/responses", now},
		"other method": {key, h, "GET", "/v1/live", now},
		"other key":    {[]byte("ffffffffffffffffffffffffffffffff"), h, "POST", "/v1/live", now},
		"expired":      {key, h, "POST", "/v1/live", now.Add(HandoffValidity + time.Second)},
		"from future":  {key, h, "POST", "/v1/live", now.Add(-HandoffValidity - time.Second)},
		"empty":        {key, "", "POST", "/v1/live", now},
		"garbage":      {key, "v1.x.y.z", "POST", "/v1/live", now},
		"no key":       {nil, h, "POST", "/v1/live", now},
		"other node":   {key, "v1.8." + h[len("v1.7."):], "POST", "/v1/live", now},
	} {
		_, err := VerifyHandoff(tc.key, tc.header, tc.method, tc.path, tc.at)
		require.ErrorIs(t, err, ErrBadHandoff, name)
	}
}
