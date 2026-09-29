package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/uvwt/agentdock/internal/snapshot"
)

func (r *Runtime) cachedCommonSkillIndex(ctx context.Context) (*capabilityCommonSkillIndex, snapshot.Info, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, snapshot.Info{}, err
	}
	root := filepath.Join(home, ".agents", "skills")
	files := r.contextSnapshots.commonFiles
	files.Add(root)
	keyFor := func() (source, cache string, err error) {
		if err := files.Sync(ctx); err != nil {
			return "", "", err
		}
		sourceRevision, cacheRevision := files.Revisions()
		stamp := snapshot.Stamps(root)
		return fmt.Sprintf("%s|%s|%s", root, sourceRevision, stamp), fmt.Sprintf("%s|%s|%s", root, cacheRevision, stamp), nil
	}
	for attempt := 0; attempt < 3; attempt++ {
		source, key, err := keyFor()
		if err != nil {
			return nil, snapshot.Info{}, err
		}
		value, info, err := r.contextSnapshots.common.Get(ctx, key, func(buildCtx context.Context) (*capabilityCommonSkillIndex, error) {
			before := files.Revision()
			value, err := commonSkillCapabilityIndexContext(buildCtx, files.Add)
			if err != nil {
				return nil, err
			}
			if err := buildCtx.Err(); err != nil {
				return nil, err
			}
			if before != files.Revision() {
				return nil, errContextVersionChanged
			}
			return value, nil
		})
		if errors.Is(err, errContextVersionChanged) {
			continue
		}
		if err != nil {
			return nil, info, err
		}
		after, _, err := keyFor()
		if err != nil {
			return nil, info, err
		}
		if source != after {
			continue
		}
		copy := *value
		copy.Items = append([]capabilityCommonSkillItem{}, value.Items...)
		return &copy, info, nil
	}
	return nil, snapshot.Info{}, errContextVersionChanged
}
