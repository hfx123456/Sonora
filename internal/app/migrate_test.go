package app

import (
	"os"
	"path/filepath"
	"testing"
)

// First launch under Sonora migrates settings, caches and session files from
// an existing Vinyl profile once without modifying the legacy Vinyl files.
func TestMigrateLegacyVinylDataCopiesOnceWithoutOverwriting(t *testing.T) {
	root := t.TempDir()
	userBase := filepath.Join(root, "roaming")
	cacheBase := filepath.Join(root, "local")

	legacyUser := filepath.Join(userBase, "Vinyl")
	legacyCache := filepath.Join(cacheBase, "Vinyl")
	sonoraUser := filepath.Join(userBase, "Sonora")
	sonoraCache := filepath.Join(cacheBase, "Sonora")

	if err := os.MkdirAll(filepath.Join(legacyUser, "lyrics-cache-v1"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(legacyCache, "fusion", "lyrics-v2"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyUser, "settings.json"), []byte(`{"volume":0.65}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyUser, "lyrics-cache-v1", "a.json"), []byte(`{"Key":"k"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyCache, "fusion", "lyrics-v2", "b.json"), []byte(`{"lines":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	migrateLegacyVinylData(sonoraUser, sonoraCache)

	gotSettings, err := os.ReadFile(filepath.Join(sonoraUser, "settings.json"))
	if err != nil || string(gotSettings) != `{"volume":0.65}` {
		t.Fatalf("migrated settings = %q, %v", gotSettings, err)
	}
	if _, err := os.Stat(filepath.Join(sonoraUser, "lyrics-cache-v1", "a.json")); err != nil {
		t.Fatalf("missing migrated lyrics cache: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sonoraCache, "fusion", "lyrics-v2", "b.json")); err != nil {
		t.Fatalf("missing migrated fusion cache: %v", err)
	}

	// Subsequent edits to Sonora settings must not be overwritten by a second call.
	if err := os.WriteFile(filepath.Join(sonoraUser, "settings.json"), []byte(`{"volume":0.2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	migrateLegacyVinylData(sonoraUser, sonoraCache)
	after, _ := os.ReadFile(filepath.Join(sonoraUser, "settings.json"))
	if string(after) != `{"volume":0.2}` {
		t.Fatalf("second migration overwrote user changes: %q", after)
	}
}
