package main

import "testing"

// Every verified payload must retain its required checksum. Checksums for the
// immutable archives consumed by the built-in updater are public Release
// assets; checksums for direct-install packages remain internal evidence.
func TestReleaseCatalogRetainsPayloadChecksumContracts(t *testing.T) {
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
		wantPublic := payload.Kind == "binary-archive" || payload.Kind == "desktop-update"
		if !exists || checksum.Kind != "checksum" || checksum.Required != payload.Required || checksum.PublicContract != wantPublic {
			t.Errorf("payload %s has the wrong checksum contract: %+v (want public=%t)", payload.Name, checksum, wantPublic)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("checksum regression must exercise actual release payloads")
	}
}
