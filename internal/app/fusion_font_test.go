package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// Choosing a lyric font applies it and keeps it for the next launch.
func TestFusionFontSelectionAndPersistence(t *testing.T) {
	old := fusionFontFamilies
	fusionFontFamilies = func() []ui.FontFamily { return []ui.FontFamily{{Name: "Test Serif"}, {Name: "Test Sans"}} }
	defer func() { fusionFontFamilies = old }()
	a := newTestApp()
	a.settings = defaultSettings()
	a.settings.path = filepath.Join(t.TempDir(), "settings.json")
	a.open(route{page: pageSettings})
	tt := ui.NewTester(a.view, 1200, 1400)
	tt.Frame()
	if err := tt.Click("默认字体"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if err := tt.Click("Test Serif"); err != nil {
		t.Fatalf("%v; shows %q", err, tt.Texts())
	}
	tt.Frame()
	tt.Frame()
	if a.settings.FusionFont != "Test Serif" || a.fusionFont() != "Test Serif, "+fusionFont {
		t.Fatalf("font not applied: %q", a.fusionFont())
	}
	b, err := os.ReadFile(a.settings.path)
	if err != nil {
		t.Fatal(err)
	}
	var restored settings
	if err := json.Unmarshal(b, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.FusionFont != "Test Serif" {
		t.Fatalf("font not saved: %q", restored.FusionFont)
	}
}

// Chinese font selection must override the Latin font for Chinese originals and translations and survive restart.
func TestFusionChineseFontSelectionAndPersistence(t *testing.T) {
	old := fusionFontFamilies
	fusionFontFamilies = func() []ui.FontFamily { return []ui.FontFamily{{Name: "Test Chinese"}, {Name: "Test Serif"}} }
	defer func() { fusionFontFamilies = old }()
	application := newTestApp()
	application.settings = defaultSettings()
	application.settings.FusionFont = "Test Serif"
	application.settings.path = filepath.Join(t.TempDir(), "settings.json")
	application.open(route{page: pageSettings})
	tester := ui.NewTester(application.view, 1200, 1400)
	tester.Frame()
	if err := tester.Click(fusionFollowFontLabel); err != nil {
		t.Fatal(err)
	}
	tester.Advance(300 * time.Millisecond)
	tester.Frame()
	if err := tester.Click("Test Chinese"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	tester.Frame()
	if application.settings.FusionChineseFont != "Test Chinese" || application.settings.FusionFont != "Test Serif" {
		t.Fatal("Chinese selection changed the other font or was not applied")
	}
	for _, text := range []string{"一起长大的约定", "谢谢 有你在身边真好"} {
		if !strings.HasPrefix(application.fusionFontForText(text), "Test Chinese,") {
			t.Fatal("Chinese text did not use the selected Chinese font")
		}
	}
	if application.fusionFontForText("Best Friend") != application.fusionFont() || application.fusionFontForText("ありがとう 君がいて") != application.fusionFont() {
		t.Fatal("Chinese font replaced the other language's selection")
	}
	data, err := os.ReadFile(application.settings.path)
	if err != nil {
		t.Fatal(err)
	}
	var restored settings
	if err := json.Unmarshal(data, &restored); err != nil || restored.FusionChineseFont != "Test Chinese" || restored.FusionFont != "Test Serif" {
		t.Fatal("independent fonts were not saved")
	}
}
