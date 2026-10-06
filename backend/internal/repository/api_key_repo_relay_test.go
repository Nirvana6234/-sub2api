//go:build integration

package repository

import "testing"

func TestAPIKeyRepoRelayNodeAssignment(t *testing.T) {
	tx := testEntTx(t)
	runAPIKeyRelayContract(t, tx.Client())
}
