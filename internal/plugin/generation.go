package plugin

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/fs/atomicfile"
	skills "github.com/uvwt/agentdock/internal/skill"
)

const lifecycleTransactionSchemaVersion = 1

func newLifecycleOwner() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("create Plugin lifecycle owner: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

func (s *Store) generationPath(name, generation string) (string, error) {
	if err := validateIdentifier("plugin", name); err != nil {
		return "", err
	}
	if err := validateGeneration(generation); err != nil {
		return "", err
	}
	return filepath.Join(s.root, ".state", "generations", name, generation), nil
}

func validateGeneration(value string) error {
	value = strings.TrimSpace(value)
	if len(value) != 32 {
		return errors.New("Plugin generation must be a 128-bit hexadecimal identifier")
	}
	if _, err := hex.DecodeString(value); err != nil {
		return errors.New("Plugin generation must be hexadecimal")
	}
	return nil
}

func (s *Store) transactionPath(name string) (string, error) {
	if err := validateIdentifier("plugin", name); err != nil {
		return "", err
	}
	return filepath.Join(s.root, ".state", "transactions", name+".json"), nil
}

func (s *Store) transactionWorkPath(name, owner string) (string, error) {
	if err := validateIdentifier("plugin", name); err != nil {
		return "", err
	}
	if err := validateGeneration(owner); err != nil {
		return "", err
	}
	return filepath.Join(s.root, ".state", "transactions", "data", name+"-"+owner), nil
}

func (s *Store) readNamedState(name string) (State, error) {
	if err := validateIdentifier("plugin", name); err != nil {
		return State{}, err
	}
	var state State
	if err := readStrictJSON(filepath.Join(s.root, ".state", name+".json"), maxStateBytes, &state); err != nil {
		return State{}, err
	}
	return state, nil
}

func (s *Store) writeNamedState(name string, state State) error {
	if err := validateIdentifier("plugin", name); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return newError("PLUGIN_STATE_WRITE_FAILED", "encode plugin state", nil, err)
	}
	data = append(data, '\n')
	path := filepath.Join(s.root, ".state", name+".json")
	if err := atomicWrite(path, data); err != nil {
		return newError("PLUGIN_STATE_WRITE_FAILED", "write plugin state", map[string]any{"path": path}, err)
	}
	return nil
}

func atomicWrite(path string, data []byte) error {
	return atomicfile.Write(path, data, 0o600)
}

func stateFingerprint(state *State) (string, error) {
	if state == nil {
		return "absent", nil
	}
	data, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func (s *Store) currentStateFingerprint(name string) (string, *State, error) {
	state, err := s.readNamedState(name)
	if errors.Is(err, os.ErrNotExist) {
		fingerprint, digestErr := stateFingerprint(nil)
		return fingerprint, nil, digestErr
	}
	if err != nil {
		return "", nil, err
	}
	fingerprint, err := stateFingerprint(&state)
	return fingerprint, &state, err
}

func packageDigestAt(path string) (string, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", true, errors.New("Plugin package path is a symlink")
	}
	if !info.IsDir() {
		return "", true, errors.New("Plugin package path is not a regular directory")
	}
	digest, err := skills.DigestPackageContent(path)
	if err != nil {
		return "", true, err
	}
	return digest, true, nil
}

func (s *Store) ensureInstalledGenerations() error {
	release, err := s.acquire()
	if err != nil {
		return err
	}
	defer release()

	records, err := s.scanUnlocked()
	if err != nil {
		return err
	}
	for _, name := range sortedPackageNames(records) {
		record := records[name]
		packageDigest, err := skills.DigestPackageContent(record.root)
		if err != nil {
			return fmt.Errorf("digest installed Plugin %s for generation migration: %w", name, err)
		}
		state := normalizeState(record.state, record)
		if state.PackageDigest != "" && state.PackageDigest != packageDigest {
			return newError("PLUGIN_PACKAGE_DRIFT", "installed Plugin package does not match persisted digest", map[string]any{"plugin": name}, nil)
		}
		generation := state.Generation
		if generation == "" {
			generation, err = newLifecycleOwner()
			if err != nil {
				return err
			}
		}
		generationPath, err := s.generationPath(name, generation)
		if err != nil {
			return err
		}
		digest, exists, err := packageDigestAt(generationPath)
		if err != nil {
			return fmt.Errorf("inspect Plugin %s generation: %w", name, err)
		}
		if exists && digest != packageDigest {
			return newError("PLUGIN_GENERATION_DRIFT", "Plugin generation does not match visible package", map[string]any{"plugin": name, "generation": generation}, nil)
		}
		if !exists {
			stage, err := os.MkdirTemp(s.tempRoot, "generation-bootstrap-")
			if err != nil {
				return err
			}
			candidate := filepath.Join(stage, "package")
			if err := copyPackageTree(record.root, candidate); err != nil {
				_ = os.RemoveAll(stage)
				return err
			}
			if err := os.MkdirAll(filepath.Dir(generationPath), 0o700); err != nil {
				_ = os.RemoveAll(stage)
				return err
			}
			if err := os.Rename(candidate, generationPath); err != nil {
				_ = os.RemoveAll(stage)
				return err
			}
			_ = os.RemoveAll(stage)
		}
		state.Generation = generation
		state.PackageDigest = packageDigest
		if state.InstalledAt.IsZero() {
			state.InstalledAt = time.Now().UTC()
		}
		if err := s.writeNamedState(name, state); err != nil {
			return err
		}
	}
	return nil
}
