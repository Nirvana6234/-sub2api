package handler

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestShouldTryOpenAIAutoGroupAfterTerminalFailover(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		want       bool
	}{
		{name: "upstream forbidden", statusCode: http.StatusForbidden, want: true},
		{name: "upstream service unavailable", statusCode: http.StatusServiceUnavailable, want: true},
		{name: "client request error", statusCode: http.StatusBadRequest, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &service.UpstreamFailoverError{
				StatusCode:        tt.statusCode,
				NextAccountAction: service.NextAccountStop,
			}
			require.Equal(t, tt.want, shouldTryOpenAIAutoGroupAfterTerminalFailover(err))
		})
	}
}
