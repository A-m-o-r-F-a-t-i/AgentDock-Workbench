package skill

import "testing"

func TestPackageInputSchemaExposesExplicitUninstallDataPurge(t *testing.T) {
	schema := PackageInputSchema()
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("skill_package properties = %#v", schema["properties"])
	}
	purge, ok := properties["purge_data"].(map[string]any)
	if !ok {
		t.Fatalf("skill_package purge_data schema = %#v", properties["purge_data"])
	}
	if purge["type"] != "boolean" {
		t.Fatalf("skill_package purge_data type = %#v", purge["type"])
	}
}
