package dashboard

import (
	"testing"

	"transithub/backend/internal/modules/upstream"
)

func TestMapPlatformErrorPreservesAuthenticationFailureClass(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "non-admin", err: &upstream.RequestError{MessageKey: upstream.ErrorAdminRequired}, want: ErrorAdminOnly},
		{name: "rejected token", err: &upstream.RequestError{MessageKey: upstream.ErrorAuth}, want: ErrorAuthRejected},
		{name: "interactive login", err: &upstream.RequestError{MessageKey: upstream.ErrorInteractiveLoginRequired}, want: ErrorInteractiveLoginRequired},
		{name: "upstream request", err: &upstream.RequestError{MessageKey: upstream.ErrorRequest}, want: ErrorUpstreamRequest},
		{name: "invalid response", err: &upstream.RequestError{MessageKey: upstream.ErrorInvalidResponse}, want: ErrorInvalidResponse},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := mapPlatformError(test.err).Error(); got != test.want {
				t.Fatalf("mapPlatformError() = %q, want %q", got, test.want)
			}
		})
	}
}
