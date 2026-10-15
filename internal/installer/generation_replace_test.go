package installer

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestReplaceGenerationDirectoryRetriesTransientWindowsLocks(t *testing.T) {
	removeCalls, renameCalls, sleepCalls := 0, 0, 0
	err := replaceGenerationDirectoryWith(
		"staging",
		"target",
		6,
		time.Millisecond,
		func(string) error {
			removeCalls++
			if removeCalls == 1 {
				return os.ErrPermission
			}
			return nil
		},
		func(string, string) error {
			renameCalls++
			if renameCalls < 3 {
				return os.ErrPermission
			}
			return nil
		},
		func(time.Duration) { sleepCalls++ },
	)
	if err != nil {
		t.Fatal(err)
	}
	if removeCalls != 2 || renameCalls != 3 || sleepCalls != 3 {
		t.Fatalf("calls remove=%d rename=%d sleep=%d", removeCalls, renameCalls, sleepCalls)
	}
}

func TestReplaceGenerationDirectoryReturnsLastFailure(t *testing.T) {
	want := errors.New("generation is still locked")
	err := replaceGenerationDirectoryWith(
		"staging",
		"target",
		3,
		0,
		func(string) error { return nil },
		func(string, string) error { return want },
		func(time.Duration) {},
	)
	if !errors.Is(err, want) || !strings.Contains(err.Error(), "after 3 attempts") {
		t.Fatalf("error=%v", err)
	}
}

func TestWindowsServiceStopFollowsObservedOrAttemptedRuntime(t *testing.T) {
	for _, test := range []struct {
		name    string
		service journalService
		want    bool
	}{
		{name: "already inactive", service: journalService{Manager: "windows"}, want: false},
		{name: "active before install", service: journalService{Manager: "windows", WasActive: true}, want: true},
		{name: "start command attempted", service: journalService{Manager: "windows", StartAttempted: true}, want: true},
		{name: "start command completed", service: journalService{Manager: "windows", StartedByUs: true}, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := windowsServiceMayNeedStop(test.service); got != test.want {
				t.Fatalf("windowsServiceMayNeedStop()=%t want=%t", got, test.want)
			}
		})
	}
}
