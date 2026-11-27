package app

import (
	"encoding/json"
	"image"
	"image/png"
	"os"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"

	"vinyl/internal/spotify"
)

func TestSidebarCollapsibleSections(t *testing.T) {
	a := editTestApp()
	a.route = route{page: pageHome}
	a.homeLoading = true
	a.followed = []spotify.ArtistCard{{ID: "ar1", Name: "陶喆"}}
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()

	// All sections start expanded.
	for _, name := range []string{"我喜欢的音乐", "通勤", "睡前", "飙升榜", "陶喆"} {
		if _, ok := tt.Find(name); !ok {
			t.Fatalf("expected %q visible before collapsing", name)
		}
	}

	// Collapsing "我的歌单" hides its playlists while keeping "+" available.
	if err := tt.Click("我的歌单"); err != nil {
		t.Fatalf("click 我的歌单: %v", err)
	}
	tt.Frame()
	if !a.sidebarCollapsed(sidebarSectionMine) {
		t.Fatal("我的歌单 did not mark collapsed")
	}
	for _, name := range []string{"我喜欢的音乐", "通勤", "睡前"} {
		if _, ok := tt.Find(name); ok {
			t.Fatalf("expected %q hidden while 我的歌单 is collapsed", name)
		}
	}
	if _, ok := tt.Find("新建歌单"); !ok {
		t.Fatal("new playlist '+' button disappeared when 我的歌单 collapsed")
	}
	// Other sections remain visible.
	if _, ok := tt.Find("飙升榜"); !ok {
		t.Fatal("我的收藏 should remain expanded when 我的歌单 collapses")
	}

	// Collapsing "我的收藏" and "关注的艺人" hides their entries too.
	if err := tt.Click("我的收藏"); err != nil {
		t.Fatalf("click 我的收藏: %v", err)
	}
	if err := tt.Click("关注的艺人"); err != nil {
		t.Fatalf("click 关注的艺人: %v", err)
	}
	tt.Frame()
	if _, ok := tt.Find("飙升榜"); ok {
		t.Fatal("飙升榜 still visible after collapsing 我的收藏")
	}
	if _, ok := tt.Find("陶喆"); ok {
		t.Fatal("陶喆 still visible after collapsing 关注的艺人")
	}

	// Clicking "我的歌单" again expands it.
	if err := tt.Click("我的歌单"); err != nil {
		t.Fatalf("re-expand 我的歌单: %v", err)
	}
	tt.Frame()
	if a.sidebarCollapsed(sidebarSectionMine) {
		t.Fatal("我的歌单 did not expand on second click")
	}
	if _, ok := tt.Find("睡前"); !ok {
		t.Fatal("睡前 did not reappear after expanding 我的歌单")
	}
}

func TestSidebarPinnedPlaylistsSectionAboveMine(t *testing.T) {
	a := editTestApp()
	a.route = route{page: pageHome}
	a.homeLoading = true
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()

	// No pinned section initially.
	if _, ok := tt.Find("置顶歌单"); ok {
		t.Fatal("置顶歌单 should not render when no playlists are pinned")
	}

	// Pin a user playlist ("睡前") and a saved playlist ("飙升榜") via context menu.
	if err := tt.RightClick("睡前"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("置顶歌单"); err != nil {
		t.Fatalf("pin 睡前: %v (menu=%q)", err, tt.Menu())
	}
	tt.Frame()

	if err := tt.RightClick("飙升榜"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("置顶歌单"); err != nil {
		t.Fatalf("pin 飙升榜: %v (menu=%q)", err, tt.Menu())
	}
	tt.Frame()

	if !slices.Equal(a.settings.PinnedPlaylists, []string{pl11, pl20}) {
		t.Fatalf("PinnedPlaylists = %v, want [%s %s]", a.settings.PinnedPlaylists, pl11, pl20)
	}

	// "置顶歌单" section sits strictly above "我的歌单", and pinned playlists sit above "我的歌单".
	pinHdr, ok1 := tt.Find("置顶歌单")
	mineHdr, ok2 := tt.Find("我的歌单")
	pinnedItem, ok3 := tt.Find("睡前")
	unpinnedItem, ok4 := tt.Find("通勤")
	if !ok1 || !ok2 || !ok3 || !ok4 {
		t.Fatalf("missing sidebar elements: pinHdr=%v mineHdr=%v pinned=%v unpinned=%v", ok1, ok2, ok3, ok4)
	}
	if !(pinHdr.Y < pinnedItem.Y && pinnedItem.Y < mineHdr.Y && mineHdr.Y < unpinnedItem.Y) {
		t.Fatalf("layout order wrong: pinHdr.Y=%v pinned.Y=%v mineHdr.Y=%v unpinned.Y=%v",
			pinHdr.Y, pinnedItem.Y, mineHdr.Y, unpinnedItem.Y)
	}

	// Collapsing "我的歌单" keeps "置顶歌单" visible!
	if err := tt.Click("我的歌单"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if _, ok := tt.Find("睡前"); !ok {
		t.Fatal("pinned playlist should stay visible when 我的歌单 is collapsed")
	}
	if _, ok := tt.Find("通勤"); ok {
		t.Fatal("unpinned playlist should hide when 我的歌单 is collapsed")
	}

	// Reordering pinned playlists via "上移" swaps 飙升榜 above 睡前.
	if err := tt.RightClick("飙升榜"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("上移"); err != nil {
		t.Fatalf("move up 飙升榜: %v (menu=%q)", err, tt.Menu())
	}
	tt.Frame()
	if !slices.Equal(a.settings.PinnedPlaylists, []string{pl20, pl11}) {
		t.Fatalf("after 上移 PinnedPlaylists = %v, want [%s %s]", a.settings.PinnedPlaylists, pl20, pl11)
	}
	b1, _ := tt.Find("飙升榜")
	b2, _ := tt.Find("睡前")
	if b1.Y >= b2.Y {
		t.Fatalf("after 上移 飙升榜.Y=%v should be above 睡前.Y=%v", b1.Y, b2.Y)
	}

	// Collapsing "置顶歌单" while "我的歌单" is expanded proves pinned playlists
	// STILL stay listed inside "我的歌单" / "我的收藏" (not removed from them).
	if err := tt.Click("我的歌单"); err != nil { // re-expand 我的歌单
		t.Fatal(err)
	}
	if err := tt.Click("置顶歌单"); err != nil { // collapse 置顶歌单
		t.Fatal(err)
	}
	tt.Frame()
	mineHdr, _ = tt.Find("我的歌单")
	keptInMine, okKept := tt.Find("睡前")
	if !okKept || keptInMine.Y <= mineHdr.Y {
		t.Fatalf("pinned 睡前 disappeared from 我的歌单: found=%v y=%v mineHdr.Y=%v", okKept, keptInMine.Y, mineHdr.Y)
	}
	// Right-clicking the copy inside "我的歌单" offers "取消置顶" (not duplicate "置顶歌单").
	if err := tt.RightClick("睡前"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("取消置顶"); err != nil {
		t.Fatalf("unpin 睡前 from 我的歌单: %v (menu=%q)", err, tt.Menu())
	}
	tt.Frame()
	if a.isPlaylistPinned(pl11) {
		t.Fatal("睡前 remained pinned after 取消置顶")
	}
	// Re-expand "置顶歌单" (飙升榜 is still pinned).
	if err := tt.Click("置顶歌单"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if _, ok := tt.Find("飙升榜"); !ok {
		t.Fatal("飙升榜 missing after re-expanding 置顶歌单")
	}
}

func TestPinLikedSongsAndDetailMenuAndGridAndPersistence(t *testing.T) {
	a := editTestApp()
	a.settings.MyPlaylistGrid = true
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()

	// Pin "通勤" from the playlist page's "更多" menu.
	if err := tt.Click("更多"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("置顶歌单"); err != nil {
		t.Fatalf("pin from detail 更多 menu: %v (menu=%q)", err, tt.Menu())
	}
	tt.Frame()
	if !a.isPlaylistPinned(pl10) {
		t.Fatal("通勤 was not pinned from detail menu")
	}

	// Pin "我喜欢的音乐" from sidebar right-click menu.
	if err := tt.RightClick("我喜欢的音乐"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("置顶歌单"); err != nil {
		t.Fatalf("pin 我喜欢的音乐: %v (menu=%q)", err, tt.Menu())
	}
	tt.Frame()
	if !a.isPlaylistPinned(spotify.LikedURI) {
		t.Fatal("我喜欢的音乐 was not pinned")
	}

	// Pin the last remaining playlist in a.mine ("睡前"): "我的歌单" header and "+" stay visible.
	if err := tt.RightClick("睡前"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("置顶歌单"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if _, ok := tt.Find("我的歌单"); !ok {
		t.Fatal("我的歌单 header should stay visible when all own playlists are pinned")
	}
	if _, ok := tt.Find("新建歌单"); !ok {
		t.Fatal("新建歌单 button should stay visible when all own playlists are pinned")
	}

	// Settings JSON round-trip preserves SidebarCollapsed and PinnedPlaylists.
	a.toggleSidebarSection(sidebarSectionMine)
	raw, err := json.Marshal(a.settings)
	if err != nil {
		t.Fatal(err)
	}
	loaded := defaultSettings()
	if err := json.Unmarshal(raw, &loaded); err != nil {
		t.Fatal(err)
	}
	if !loaded.SidebarCollapsed[sidebarSectionMine] {
		t.Fatal("SidebarCollapsed did not round-trip through JSON")
	}
	if !slices.Equal(loaded.PinnedPlaylists, []string{pl10, spotify.LikedURI, pl11}) {
		t.Fatalf("PinnedPlaylists JSON round-trip = %v", loaded.PinnedPlaylists)
	}
}

func TestSidebarSectionAlignmentAndCompactCollapsedSpacing(t *testing.T) {
	a := editTestApp()
	a.route = route{page: pageHome}
	a.homeLoading = true
	a.followed = []spotify.ArtistCard{{ID: "ar1", Name: "陶喆"}}
	a.settings.PinnedPlaylists = []string{spotify.LikedURI}
	a.settings.SidebarCollapsed = map[string]bool{
		sidebarSectionMine:     true,
		sidebarSectionSaved:    true,
		sidebarSectionFollowed: true,
	}
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()

	// Section headers and playlist rows share the same horizontal container edge.
	pinHdr, ok1 := tt.Find("置顶歌单")
	likedRow, ok2 := tt.Find("我喜欢的音乐")
	mineHdr, ok3 := tt.Find("我的歌单")
	savedHdr, ok4 := tt.Find("我的收藏")
	followedHdr, ok5 := tt.Find("关注的艺人")
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 {
		t.Fatalf("missing elements: %v %v %v %v %v", ok1, ok2, ok3, ok4, ok5)
	}
	if pinHdr.X != likedRow.X || mineHdr.X != likedRow.X || savedHdr.X != likedRow.X {
		t.Fatalf("left edges misaligned: pinHdr.X=%v likedRow.X=%v mineHdr.X=%v savedHdr.X=%v",
			pinHdr.X, likedRow.X, mineHdr.X, savedHdr.X)
	}
	// Collapsed consecutive headers must stay compact (<= 8px gap, not 24px).
	gap1 := savedHdr.Y - (mineHdr.Y + mineHdr.H)
	gap2 := followedHdr.Y - (savedHdr.Y + savedHdr.H)
	if gap1 < 0 || gap1 > 8 || gap2 < 0 || gap2 > 8 {
		t.Fatalf("collapsed section spacing too large: gap1=%v gap2=%v", gap1, gap2)
	}

	if out := os.Getenv("SIDEBAR_SNAP"); out != "" {
		img := tt.Image()
		crop := img.SubImage(image.Rect(0, 220, int(sidebarWidth), 560))
		f, err := os.Create(out)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, crop); err != nil {
			t.Fatal(err)
		}
	}
}
