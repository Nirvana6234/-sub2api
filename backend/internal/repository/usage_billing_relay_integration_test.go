//go:build integration

package repository

import "testing"

func TestUsageBillingRelaySettlement(t *testing.T) {
	runUsageBillingRelayContract(t, integrationDB)
}
