//go:build !windows

package desktopruntime

import (
	"context"
	"errors"
)

func platformVerifyTailscale(context.Context, string) (TunnelStatus, error) {
	return TunnelStatus{}, errors.New("native Funnel verification requires Windows Desktop")
}

func platformRepairTailscale(context.Context, string) error {
	return errors.New("native Funnel repair requires Windows Desktop")
}
