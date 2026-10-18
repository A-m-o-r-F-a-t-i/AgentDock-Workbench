//go:build !windows

package plugin

func retryableGenerationPromotionError(_ error) bool { return false }
