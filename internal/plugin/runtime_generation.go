package plugin

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func (s *Store) CreateRuntimeGeneration(name, generation, source string) (string, func(), error) {
	if err := validateIdentifier("plugin", name); err != nil {
		return "", nil, err
	}
	if err := validateGeneration(generation); err != nil {
		return "", nil, err
	}
	expected, err := s.generationPath(name, generation)
	if err != nil {
		return "", nil, err
	}
	source, err = filepath.Abs(source)
	if err != nil {
		return "", nil, err
	}
	if !sameFilesystemPath(source, expected) {
		return "", nil, errors.New("Plugin runtime source does not match the selected immutable generation")
	}
	owner, err := newLifecycleOwner()
	if err != nil {
		return "", nil, err
	}
	root := filepath.Join(s.root, ".runtime", name)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", nil, err
	}
	target := filepath.Join(root, "generation-"+generation+"-"+owner)
	if err := copyPackageTree(source, target); err != nil {
		_ = os.RemoveAll(target)
		return "", nil, fmt.Errorf("copy Plugin runtime generation: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(target) }
	return target, cleanup, nil
}
