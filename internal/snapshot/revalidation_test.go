package snapshot

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/fsnotify/fsnotify"
)

func TestSnapshotRevalidationKeepsCacheFresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		files := &Files{}
		files.unhealthy.Store(true)
		source, cache := files.Revisions()
		time.Sleep(2 * unhealthyRevalidationInterval)
		nextSource, nextCache := files.Revisions()
		if source != nextSource || cache == nextCache {
			t.Fatalf("source/cache roles mixed: %q/%q => %q/%q", source, cache, nextSource, nextCache)
		}
		files.Invalidate()
		actualSource, actualCache := files.Revisions()
		if actualSource == nextSource || actualCache == nextCache {
			t.Fatal("explicit invalidation did not change both revisions")
		}
	})
}

func TestSnapshotRevalidationPreservesEventsAndFilter(t *testing.T) {
	files := &Files{filter: func(path string) bool { return path == "plugin.json" }}
	files.unhealthy.Store(true)
	before := files.Revision()
	files.event(fsnotify.Event{Name: "ignored.log", Op: fsnotify.Write})
	if files.Revision() != before {
		t.Fatal("filtered runtime output changed source identity")
	}
	files.event(fsnotify.Event{Name: "plugin.json", Op: fsnotify.Write})
	if files.Revision() == before {
		t.Fatal("real source event was ignored in unhealthy state")
	}
}

func TestSnapshotRevalidationHealthyAndNil(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		files := &Files{}
		source, cache := files.Revisions()
		time.Sleep(time.Second)
		nextSource, nextCache := files.Revisions()
		if source != cache || source != nextSource || cache != nextCache {
			t.Fatal("healthy cache changed without an event")
		}
		files.unhealthy.Store(true)
		if files.Revision() == source {
			t.Fatal("watcher health transition must invalidate prior healthy snapshots")
		}
		var absent *Files
		source, cache = absent.Revisions()
		if source != "none" || cache != "none" || absent.Revision() != "none" {
			t.Fatal("nil tracker changed its contract")
		}
	})
}
