package plugin

import (
	"errors"
	"os"
	"time"
)

const (
	generationPromotionRetryWindow   = 15 * time.Second
	generationPromotionRetryInterval = 200 * time.Millisecond
)

// promoteGeneration atomically publishes one immutable Plugin generation. A
// concurrent publisher that produced the same digest is an idempotent success.
// Windows may temporarily deny a directory rename while ACL propagation,
// antivirus, or another reader releases a handle, so only those transient
// platform errors receive a short bounded retry.
func promoteGeneration(candidate, destination, expectedDigest, pluginName, generation string) error {
	started := time.Now()
	deadline := started.Add(generationPromotionRetryWindow)
	attempts := 0

	for {
		attempts++
		renameErr := os.Rename(candidate, destination)
		if renameErr == nil {
			return nil
		}

		digest, exists, inspectErr := packageDigestAt(destination)
		if inspectErr != nil {
			if (retryableGenerationPromotionError(renameErr) || retryableGenerationPromotionError(inspectErr)) && time.Now().Before(deadline) {
				time.Sleep(generationPromotionRetryInterval)
				continue
			}
			return newError(
				"PLUGIN_GENERATION_PROMOTION_FAILED",
				"inspect Plugin generation after promotion failure",
				map[string]any{"plugin": pluginName, "generation": generation, "attempts": attempts},
				errors.Join(renameErr, inspectErr),
			)
		}
		if exists {
			if digest == expectedDigest {
				return nil
			}
			return newError(
				"PLUGIN_GENERATION_DRIFT",
				"Plugin generation does not match visible package",
				map[string]any{"plugin": pluginName, "generation": generation},
				renameErr,
			)
		}
		if !retryableGenerationPromotionError(renameErr) || !time.Now().Before(deadline) {
			return newError(
				"PLUGIN_GENERATION_PROMOTION_FAILED",
				"publish immutable Plugin generation",
				map[string]any{
					"plugin":     pluginName,
					"generation": generation,
					"attempts":   attempts,
					"elapsed_ms": time.Since(started).Milliseconds(),
				},
				renameErr,
			)
		}
		time.Sleep(generationPromotionRetryInterval)
	}
}
