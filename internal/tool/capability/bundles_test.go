package capability

import "testing"

func TestBuiltinBundlesAreUniqueAndResolvable(t *testing.T) {
	seenBundles := map[string]bool{}
	seenTools := map[string]string{}
	for _, bundle := range Bundles() {
		if bundle.Name == "" || bundle.Description == "" || len(bundle.Tools) == 0 {
			t.Fatalf("invalid bundle: %#v", bundle)
		}
		if seenBundles[bundle.Name] {
			t.Fatalf("duplicate bundle %q", bundle.Name)
		}
		seenBundles[bundle.Name] = true
		resolved, ok := ByName(bundle.Name)
		if !ok || len(resolved.Tools) != len(bundle.Tools) {
			t.Fatalf("bundle %q is not resolvable: %#v", bundle.Name, resolved)
		}
		for _, tool := range bundle.Tools {
			if owner := seenTools[tool]; owner != "" {
				t.Fatalf("tool %q belongs to both %q and %q", tool, owner, bundle.Name)
			}
			seenTools[tool] = bundle.Name
			mapped, ok := ForTool(tool)
			if !ok || mapped.Name != bundle.Name {
				t.Fatalf("tool %q maps to %#v", tool, mapped)
			}
		}
	}
	for _, required := range []string{"mcp_manage", "plugin_manage", "skill_package", "file_publish"} {
		if seenTools[required] == "" {
			t.Fatalf("required low-frequency tool %q is not bundled", required)
		}
	}
}

func TestCapabilityLoadRequestValidation(t *testing.T) {
	for _, request := range []Request{
		{Action: ActionList},
		{Action: ActionLoad, Name: BundleSystemManagement},
		{Action: ActionLoad, Name: BundleArtifactPublishing},
	} {
		if err := Validate(request); err != nil {
			t.Fatalf("Validate(%#v) error = %v", request, err)
		}
	}
	for _, request := range []Request{
		{},
		{Action: ActionList, Name: BundleSystemManagement},
		{Action: ActionLoad},
		{Action: ActionLoad, Name: "unknown"},
	} {
		if err := Validate(request); err == nil {
			t.Fatalf("Validate(%#v) unexpectedly succeeded", request)
		}
	}
}
