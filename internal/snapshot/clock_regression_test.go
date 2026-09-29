package snapshot

import (
	"testing"
	"testing/synctest"
	"time"
)

// This test intentionally uses only the pre-fix API so CI can run the exact
// same assertion against the unmodified parent commit.
func TestUnhealthySourceRevisionIgnoresClock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		files := &Files{}
		files.unhealthy.Store(true)
		before := files.Revision()
		time.Sleep(time.Second)
		if after := files.Revision(); after != before {
			t.Fatalf("wall clock changed the source revision: before=%q after=%q", before, after)
		}
	})
}
