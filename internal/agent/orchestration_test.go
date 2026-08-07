package agent

import "testing"

func TestParseComposeMetadataUsesOnlyValidatedIdentityLabels(t *testing.T) {
	content := []byte(`
["6687817628f3e5d6be80ea1692004cf7d3019ecb11487f074f8aff65fc22577c","new-api","postgres"]
["2b6be193347d3a1a94ed5762e74b7ba2f30f947ca8e08fc261da10338dfaf01a","new-api","new-api"]
["bad","project","service"]
["3e69a997a7d9b551e645eaa5655daa7fb4419dcbce572106dfe6e8b48dd59db8","project/name","service"]
not json
`)
	metadata := parseComposeMetadata(content)
	if len(metadata) != 2 {
		t.Fatalf("expected two valid Compose identities, got %+v", metadata)
	}
	value, found := findComposeMetadata(metadata, "6687817628f3")
	if !found || value.Project != "new-api" || value.Service != "postgres" {
		t.Fatalf("Compose prefix lookup failed: found=%v value=%+v", found, value)
	}
}

func TestParseSystemdUnitsSelectsCustomActiveAndAllFailed(t *testing.T) {
	content := []byte(`Id=lodge-hub.service
LoadState=loaded
ActiveState=active
SubState=running
FragmentPath=/etc/systemd/system/lodge-hub.service

Id=ssh.service
LoadState=loaded
ActiveState=active
SubState=running
FragmentPath=/usr/lib/systemd/system/ssh.service

Id=fwupd-refresh.service
LoadState=loaded
ActiveState=failed
SubState=failed
FragmentPath=/usr/lib/systemd/system/fwupd-refresh.service

Id=inactive-custom.service
LoadState=loaded
ActiveState=inactive
SubState=dead
FragmentPath=/etc/systemd/system/inactive-custom.service

Id=../../unsafe.service
LoadState=loaded
ActiveState=failed
SubState=failed
FragmentPath=/etc/systemd/system/unsafe.service
`)
	units := parseSystemdUnits(content)
	if len(units) != 4 {
		t.Fatalf("unsafe unit should be rejected: %+v", units)
	}
	byID := make(map[string]systemdUnitMetadata)
	for _, unit := range units {
		byID[unit.ID] = unit
	}
	if !byID["lodge-hub.service"].relevant() || byID["ssh.service"].relevant() {
		t.Fatal("active custom/system unit relevance was classified incorrectly")
	}
	if !byID["fwupd-refresh.service"].relevant() || byID["inactive-custom.service"].relevant() {
		t.Fatal("failed/inactive unit relevance was classified incorrectly")
	}
	if got := byID["lodge-hub.service"].status(); got != "active/running" {
		t.Fatalf("unexpected unit status: %q", got)
	}
}
