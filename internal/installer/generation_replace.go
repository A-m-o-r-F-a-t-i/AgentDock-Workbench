package installer

import (
	"fmt"
	"os"
	"time"
)

const (
	generationReplaceAttempts = 80
	generationReplaceDelay    = 250 * time.Millisecond
)

// replaceGenerationDirectory publishes a fully prepared generation only after
// the previous directory is gone. Windows can keep a just-closed executable or
// directory handle briefly after every owning process has exited; bounded retry
// prevents that transient delete/rename window from destroying same-version
// repair while the rollback journal remains the authoritative recovery source.
func replaceGenerationDirectory(staging, target string) error {
	return replaceGenerationDirectoryWith(
		staging,
		target,
		generationReplaceAttempts,
		generationReplaceDelay,
		os.RemoveAll,
		os.Rename,
		time.Sleep,
	)
}

func replaceGenerationDirectoryWith(
	staging, target string,
	attempts int,
	delay time.Duration,
	removeAll func(string) error,
	rename func(string, string) error,
	sleep func(time.Duration),
) error {
	if attempts < 1 {
		return fmt.Errorf("replace generation: attempts must be positive")
	}
	removed := false
	var last error
	for attempt := 1; attempt <= attempts; attempt++ {
		if !removed {
			if err := removeAll(target); err != nil && !os.IsNotExist(err) {
				last = fmt.Errorf("remove previous generation: %w", err)
			} else {
				removed = true
			}
		}
		if removed {
			if err := rename(staging, target); err == nil {
				return nil
			} else {
				last = fmt.Errorf("publish prepared generation: %w", err)
			}
		}
		if attempt < attempts {
			sleep(delay)
		}
	}
	return fmt.Errorf("replace generation after %d attempts: %w", attempts, last)
}
