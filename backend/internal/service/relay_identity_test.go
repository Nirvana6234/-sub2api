//go:build unit

package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// 指纹只读 FingerprintHeaderNames 里的请求头：从节点只带这几个给主节点，结果与拿全部请求头算的一样。
func TestFingerprintReadsOnlyTheListedHeaders(t *testing.T) {
	all := http.Header{}
	all.Set("User-Agent", "claude-cli/2.1.300 (external, cli)")
	all.Set("X-Stainless-Lang", "js")
	all.Set("X-Stainless-Package-Version", "0.99.0")
	all.Set("X-Stainless-OS", "Linux")
	all.Set("X-Stainless-Arch", "x64")
	all.Set("X-Stainless-Runtime", "node")
	all.Set("X-Stainless-Runtime-Version", "v24.0.0")
	all.Set("X-Stainless-Retry-Count", "3")
	all.Set("Anthropic-Beta", "oauth-2025-04-20")
	all.Set("Authorization", "Bearer x")

	listed := http.Header{}
	for _, name := range FingerprintHeaderNames {
		if v := all.Get(name); v != "" {
			listed.Set(name, v)
		}
	}
	s := &IdentityService{}
	require.Equal(t, s.createFingerprintFromHeaders(all), s.createFingerprintFromHeaders(listed))

	merged, mergedListed := &Fingerprint{UserAgent: "claude-cli/2.1.0"}, &Fingerprint{UserAgent: "claude-cli/2.1.0"}
	mergeHeadersIntoFingerprint(merged, all)
	mergeHeadersIntoFingerprint(mergedListed, listed)
	require.Equal(t, merged, mergedListed)
}
