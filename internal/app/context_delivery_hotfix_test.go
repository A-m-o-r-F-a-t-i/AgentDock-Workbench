package app

import (
	"testing"
	"time"
)

func TestContextCompleteDeliveryIsClaimedOncePerSharedBuild(t *testing.T) {
	snapshots := &contextSnapshots{deliveries: map[uint64]time.Time{}}
	now := time.Now()
	if !snapshots.claimCompleteDelivery(42, now) {
		t.Fatal("first waiter did not claim the shared Context delivery")
	}
	if snapshots.claimCompleteDelivery(42, now.Add(time.Second)) {
		t.Fatal("duplicate waiter was allowed to create a second full Context sequence")
	}
	if !snapshots.claimCompleteDelivery(42, now.Add(contextDeliveryClaimTTL+time.Second)) {
		t.Fatal("expired delivery claim was not released")
	}
}
