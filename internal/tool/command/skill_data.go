package command

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/fs/securepath"
	"github.com/uvwt/agentdock/internal/skilldata"
)

// ensureManagedSkillDataDir resolves the canonical runtime-data path and
// performs the one-time migration from the legacy data/skills tree.
func (svc *Service) ensureManagedSkillDataDir(skillName string) (string, error) {
	if skillName == "" {
		return "", nil
	}
	dataDir, err := skilldata.Ensure(svc.config(), skillName)
	if err != nil {
		return "", toolErrorDetails("SKILL_DATA_DIR_INVALID", "prepare managed Skill data directory", "runtime", map[string]any{
			"skill": skillName, "reason": err.Error(),
		})
	}
	return dataDir, nil
}

func reservedSkillEnvironmentError(key string) error {
	return fmt.Errorf("environment variable %q is reserved by the Skill runtime", key)
}

func (svc *Service) ensurePluginDataDir(name string) error {
	cfg := svc.config()
	if _, err := config.PluginDataDir(cfg, name); err != nil {
		return err
	}
	root, err := os.OpenRoot(cfg.AgentDockHome)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, relative := range []string{"plugins", filepath.Join("plugins", ".data"), filepath.Join("plugins", ".data", name)} {
		info, err := root.Lstat(relative)
		if errors.Is(err, os.ErrNotExist) {
			if err = root.Mkdir(relative, 0700); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			info, err = root.Lstat(relative)
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("Plugin data directory contains an unsafe component: %s", relative)
		}
		if err := securepath.EnsurePrivate(filepath.Join(cfg.AgentDockHome, relative)); err != nil {
			return err
		}
	}
	return nil
}
