package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// Clicking the sidebar theme toggle switches between dark and light mode and saves the preference.
func TestSidebarDarkModeToggle(t *testing.T) {
	var gotSource mygo.ThemeSource
	old := setThemeSource
	setThemeSource = func(s mygo.ThemeSource) { gotSource = s }
	t.Cleanup(func() { setThemeSource = old })

	a := newTestApp()
	a.settings = defaultSettings()
	a.settings.path = filepath.Join(t.TempDir(), "settings.json")
	a.profile = nil // works signed out too

	tt := ui.NewTester(a.view, 1000, 700)
	tt.SetDark(false)
	tt.Frame()

	if err := tt.Click("黑夜模式"); err != nil {
		t.Fatal(err)
	}
	if a.settings.Appearance != "dark" || gotSource != mygo.ThemeDark {
		t.Fatalf("after click: appearance=%q source=%q", a.settings.Appearance, gotSource)
	}

	b, err := os.ReadFile(a.settings.path)
	if err != nil {
		t.Fatal(err)
	}
	var saved settings
	if err := json.Unmarshal(b, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Appearance != "dark" {
		t.Fatalf("saved appearance = %q, want dark", saved.Appearance)
	}

	tt.SetDark(true)
	tt.Frame()
	if !a.pal.dark {
		t.Fatal("palette did not switch to dark")
	}
	if err := tt.Click("浅色模式"); err != nil {
		t.Fatal(err)
	}
	if a.settings.Appearance != "light" || gotSource != mygo.ThemeLight {
		t.Fatalf("after second click: appearance=%q source=%q", a.settings.Appearance, gotSource)
	}
}

// Choosing the theme item in the account menu switches dark and light mode and closes the menu.
func TestAccountMenuDarkModeToggle(t *testing.T) {
	var gotSource mygo.ThemeSource
	old := setThemeSource
	setThemeSource = func(s mygo.ThemeSource) { gotSource = s }
	t.Cleanup(func() { setThemeSource = old })

	a := newTestApp()
	a.settings = defaultSettings()
	a.profile.Nickname = "小明"

	tt := ui.NewTester(a.view, 1000, 700)
	tt.SetDark(false)
	tt.Frame()

	if err := tt.Click("小明"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("黑夜模式"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.accountOpen || a.settings.Appearance != "dark" || gotSource != mygo.ThemeDark {
		t.Fatalf("open=%v appearance=%q source=%q", a.accountOpen, a.settings.Appearance, gotSource)
	}

	tt.SetDark(true)
	tt.Frame()
	if err := tt.Click("小明"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("浅色模式"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.accountOpen || a.settings.Appearance != "light" || gotSource != mygo.ThemeLight {
		t.Fatalf("open=%v appearance=%q source=%q", a.accountOpen, a.settings.Appearance, gotSource)
	}
}
