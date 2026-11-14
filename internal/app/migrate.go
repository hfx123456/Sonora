package app

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
)

const migrationMarker = ".vinyl-migrated"

// migrateLegacyVinylData copies settings, playback state, caches, and sealed
// cookies from an earlier Vinyl install into Sonora's own directories once,
// leaving Vinyl's directory untouched so both apps remain isolated.
func migrateLegacyVinylData(userDir, cacheDir string) {
	if userDir == "" {
		return
	}
	marker := filepath.Join(userDir, migrationMarker)
	if _, err := os.Stat(marker); err == nil {
		return
	}
	if _, err := os.Stat(filepath.Join(userDir, "settings.json")); err == nil {
		_ = os.MkdirAll(userDir, 0o700)
		_ = os.WriteFile(marker, []byte("existing\n"), 0o600)
		return
	}

	legacyUser := filepath.Join(filepath.Dir(userDir), "Vinyl")
	legacyCache := ""
	if cacheDir != "" {
		legacyCache = filepath.Join(filepath.Dir(cacheDir), "Vinyl")
	}

	if _, err := os.Stat(legacyUser); err != nil {
		return
	}

	_ = os.MkdirAll(userDir, 0o700)
	for _, name := range []string{"settings.json", "played.json", "played-from.json", "session.json", "home.json", "queries.json"} {
		copyFileIfMissing(filepath.Join(legacyUser, name), filepath.Join(userDir, name))
	}
	for _, dir := range []string{"playlist-cache-v1", "lyrics-cache-v1"} {
		copyDirIfMissing(filepath.Join(legacyUser, dir), filepath.Join(userDir, dir))
	}

	if cacheDir != "" && legacyCache != "" {
		_ = os.MkdirAll(cacheDir, 0o700)
		copyDirIfMissing(filepath.Join(legacyCache, "fusion", "lyrics-v2"), filepath.Join(cacheDir, "fusion", "lyrics-v2"))
	}

	// Re-seal legacy DPAPI account cookies under Sonora's own entropy so users stay signed in.
	cookieSrcDir, cookieDstDir := userDir, userDir
	if runtime.GOOS == "windows" {
		cookieSrcDir, cookieDstDir = legacyCache, cacheDir
	} else {
		cookieSrcDir = legacyUser
	}
	if cookieSrcDir != "" && cookieDstDir != "" {
		migrateAccountCookies(filepath.Join(cookieSrcDir, "account-cookies"), filepath.Join(cookieDstDir, "account-cookies"))
	}

	_ = os.WriteFile(marker, []byte("migrated\n"), 0o600)
}

func migrateAccountCookies(src, dst string) {
	if _, err := os.Stat(dst); err == nil {
		return
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return
	}
	plain, err := unsealLegacyVinyl(data)
	if err != nil || len(plain) == 0 {
		return
	}
	sealed, err := sealCookies(plain)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(dst), 0o700)
	_ = os.WriteFile(dst, sealed, 0o600)
}

func copyFileIfMissing(src, dst string) {
	if _, err := os.Stat(dst); err == nil {
		return
	}
	in, err := os.Open(src)
	if err != nil {
		return
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".migrate-*")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	_, copyErr := io.Copy(tmp, in)
	closeErr := tmp.Close()
	if copyErr != nil || closeErr != nil {
		os.Remove(tmpName)
		return
	}
	if os.Rename(tmpName, dst) != nil {
		os.Remove(tmpName)
	}
}

func copyDirIfMissing(src, dst string) {
	info, err := os.Stat(src)
	if err != nil || !info.IsDir() {
		return
	}
	_ = filepath.WalkDir(src, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil || rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			_ = os.MkdirAll(target, 0o700)
			return nil
		}
		if entry.Type().IsRegular() {
			copyFileIfMissing(path, target)
		}
		return nil
	})
}
