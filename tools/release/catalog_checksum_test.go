package main

import "testing"

// Public downloads are installer-only, but every verified payload must retain
// its required internal checksum. Do not weaken this independent contract when
// changing which artifacts are exposed on the Release page.
func TestReleaseCatalogRetainsInternalPayloadChecksums(t *testing.T) {
	catalog := ReleaseCatalog()
	byName := make(map[string]Artifact, len(catalog))
	for _, artifact := range catalog {
		if _, exists := byName[artifact.Name]; exists {
			t.Fatalf("duplicate release artifact: %s", artifact.Name)
		}
		byName[artifact.Name] = artifact
	}
	checked := 0
	for _, payload := range catalog {
		if payload.Kind == "checksum" || payload.Kind == "bootstrap" {
			continue
		}
		checksum, exists := byName[payload.Name+".sha256"]
		if !exists || checksum.Kind != "checksum" || checksum.Required != payload.Required || checksum.PublicContract {
			t.Errorf("payload %s lost its internal checksum contract: %+v", payload.Name, checksum)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("checksum regression must exercise actual release payloads")
	}
}
