package app

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"vinyl/internal/spotify"
)

func seedTestArtColor(a *app, url string, c color.NRGBA) ui.Color {
	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for y := range 16 {
		for x := range 16 {
			img.SetNRGBA(x, y, c)
		}
	}
	bmp := ui.NewBitmap(img)
	tint := ui.RGBA(c.R, c.G, c.B, float32(c.A)/255)
	for _, px := range []int{96, 120, 200, 320, 400, 540, 600} {
		key := spotify.Sized(url, px)
		a.art.art[key] = &artwork{bmp: bmp, tint: tint}
		a.art.art[key+"#backdrop"] = &artwork{bmp: bmp, tint: tint}
	}
	return tint
}

// Moving the pointer across charts, browse categories, category playlists, and recently played items updates the page's top wash color to match the hovered item.
func TestHoverChangesPageTintAcrossPages(t *testing.T) {
	a := newTestApp()
	a.settings = defaultSettings()
	cPurple := seedTestArtColor(a, "https://example.com/p.jpg", color.NRGBA{R: 110, G: 45, B: 165, A: 255})
	cTeal := seedTestArtColor(a, "https://example.com/t.jpg", color.NRGBA{R: 15, G: 135, B: 145, A: 255})
	cRed := seedTestArtColor(a, "https://example.com/r.jpg", color.NRGBA{R: 185, G: 35, B: 50, A: 255})
	cBlue := seedTestArtColor(a, "https://example.com/b.jpg", color.NRGBA{R: 40, G: 110, B: 220, A: 255})

	a.charts = []spotify.Playlist{
		{ID: "spotify:playlist:ch1", Name: "全球热门歌曲", CoverURL: "https://example.com/p.jpg"},
		{ID: "spotify:playlist:ch2", Name: "全球前 50 名", CoverURL: "https://example.com/t.jpg"},
		{ID: "spotify:playlist:ch3", Name: "新加坡前 50 名", CoverURL: "https://example.com/r.jpg"},
		{ID: "spotify:playlist:ch4", Name: "日本前 50 名", CoverURL: "https://example.com/b.jpg"},
		{ID: "spotify:playlist:ch5", Name: "其他榜单", CoverURL: "https://example.com/b.jpg"},
	}
	a.cats = []spotify.Category{
		{ID: "spotify:page:pop", Name: "流行", Color: "#8d67ab"},
		{ID: "spotify:page:rock", Name: "摇滚", Color: "#e8115b"},
	}
	a.catPages = map[string]*spotify.CategoryPage{
		"spotify:page:pop": {
			Name: "流行",
			Shelves: []spotify.Shelf{{
				Title: "精选歌单",
				Lists: []spotify.Playlist{
					{ID: "spotify:playlist:cat1", Name: "分类歌单1", CoverURL: "https://example.com/p.jpg"},
					{ID: "spotify:playlist:cat2", Name: "分类歌单2", CoverURL: "https://example.com/b.jpg"},
				},
			}},
		},
	}
	a.recentLists = []spotify.Playlist{
		{ID: "spotify:playlist:rec1", Name: "最近歌单1", CoverURL: "https://example.com/p.jpg"},
		{ID: "spotify:playlist:rec2", Name: "最近歌单2", CoverURL: "https://example.com/r.jpg"},
	}
	a.played = []spotify.Track{{ID: "tr:1", Name: "最近曲目", Duration: 180000}}

	tt := ui.NewTester(a.view, 1200, 800)
	hover := func(label string) {
		t.Helper()
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("missing %q", label)
		}
		tt.Move(r.X+r.W/2, r.Y+r.H/2)
		tt.Advance(time.Second)
		tt.Frame()
	}

	a.open(route{page: pageCharts})
	tt.Frame()
	if got := a.pageTint(pageCharts); got != cPurple {
		t.Fatalf("charts initial tint = %+v, want %+v", got, cPurple)
	}
	hover("全球前 50 名")
	if got := a.pageTint(pageCharts); got != cTeal {
		t.Fatalf("charts hovered tint = %+v, want %+v", got, cTeal)
	}
	hover("其他榜单")
	if got := a.pageTint(pageCharts); got != cBlue {
		t.Fatalf("charts playlistCard hovered tint = %+v, want %+v", got, cBlue)
	}

	a.open(route{page: pageBrowse})
	tt.Frame()
	hover("摇滚")
	if got, want := a.pageTint(pageBrowse), ui.Hex("#e8115b"); got != want {
		t.Fatalf("browse hovered tint = %+v, want %+v", got, want)
	}

	a.open(route{page: pageCategory, category: "spotify:page:pop"})
	tt.Frame()
	hover("分类歌单2")
	if got := a.pageTint(pageCategory); got != cBlue {
		t.Fatalf("category hovered tint = %+v, want %+v", got, cBlue)
	}

	a.open(route{page: pageRecent})
	tt.Frame()
	hover("最近歌单2")
	if got := a.pageTint(pageRecent); got != cRed {
		t.Fatalf("recent hovered tint = %+v, want %+v", got, cRed)
	}
}

// With NM_FRAMES set, saves home, charts, playlist, and scrolled local music views in light and dark glass modes for visual inspection.
func TestUnifiedUIFrames(t *testing.T) {
	dir := os.Getenv("NM_FRAMES")
	if dir == "" {
		t.Skip()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	encode := func(name string, tt *ui.Tester) {
		t.Helper()
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
	seedArt := func(a *app, url string, c color.NRGBA) {
		img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
		for y := range 16 {
			for x := range 16 {
				img.SetNRGBA(x, y, c)
			}
		}
		bmp := ui.NewBitmap(img)
		tint := ui.RGBA(c.R, c.G, c.B, float32(c.A)/255)
		for _, px := range []int{96, 120, 200, 400, 600} {
			key := spotify.Sized(url, px)
			a.art.art[key] = &artwork{bmp: bmp, tint: tint}
			a.art.art[key+"#backdrop"] = &artwork{bmp: bmp, tint: tint}
		}
	}
	for _, dark := range []bool{false, true} {
		mode := "light"
		if dark {
			mode = "dark"
		}
		a := newTestApp()
		a.settings = defaultSettings()
		a.homeSections = testSections(2)
		liked := spotify.Playlist{ID: spotify.LikedURI, Name: "我喜欢的音乐"}
		a.mine = []spotify.Playlist{
			liked,
			{ID: "spotify:playlist:1", Name: "RADWIMPS", CoverURL: "https://example.com/c1.jpg"},
			{ID: "spotify:playlist:2", Name: "Bigbang", CoverURL: "https://example.com/c2.jpg"},
			{ID: "spotify:playlist:3", Name: "Heartbreaker", CoverURL: "https://example.com/c3.jpg"},
			{ID: "spotify:playlist:4", Name: "Übermensch", CoverURL: "https://example.com/c4.jpg"},
			{ID: "spotify:playlist:5", Name: "网易云音乐", CoverURL: "https://example.com/c5.jpg"},
			{ID: "spotify:playlist:6", Name: "QQ音乐", CoverURL: "https://example.com/c6.jpg"},
			{ID: "spotify:playlist:7", Name: "2022 年你的最爱歌曲", CoverURL: "https://example.com/c7.jpg"},
		}
		seedArt(a, "https://example.com/c1.jpg", color.NRGBA{R: 58, G: 130, B: 210, A: 255})
		seedArt(a, "https://example.com/c2.jpg", color.NRGBA{R: 40, G: 40, B: 45, A: 255})
		seedArt(a, "https://example.com/c3.jpg", color.NRGBA{R: 245, G: 245, B: 247, A: 255})
		seedArt(a, "https://example.com/c4.jpg", color.NRGBA{R: 215, G: 85, B: 50, A: 255})
		seedArt(a, "https://example.com/c5.jpg", color.NRGBA{R: 48, G: 75, B: 135, A: 255})
		seedArt(a, "https://example.com/c6.jpg", color.NRGBA{R: 60, G: 110, B: 120, A: 255})
		seedArt(a, "https://example.com/c7.jpg", color.NRGBA{R: 80, G: 210, B: 130, A: 255})

		a.charts = []spotify.Playlist{
			{ID: "spotify:playlist:ch1", Name: "全球热门歌曲", CoverURL: "https://example.com/ch1.jpg", Preview: []spotify.ChartLine{
				{First: "BbY WOW", Second: "KAROL G, Judeline"},
				{First: "Housewife", Second: "Tate McRae"},
				{First: "Stop The Wedding!", Second: "Ashe"},
			}},
			{ID: "spotify:playlist:ch2", Name: "新加坡热门歌曲", CoverURL: "https://example.com/ch2.jpg", Preview: []spotify.ChartLine{
				{First: "Housewife", Second: "Tate McRae"},
				{First: "Hong Kong (feat. 王菲季候風)", Second: "Tyson Yoshi"},
				{First: "Nicole Kidman", Second: "ADÉLA"},
			}},
			{ID: "spotify:playlist:ch3", Name: "全球前 50 名", CoverURL: "https://example.com/ch3.jpg", Preview: []spotify.ChartLine{
				{First: "Stop The Wedding!", Second: "Ashe"},
				{First: "BbY WOW", Second: "KAROL G, Judeline"},
				{First: "Housewife", Second: "Tate McRae"},
			}},
			{ID: "spotify:playlist:ch4", Name: "新加坡前 50 名", CoverURL: "https://example.com/ch4.jpg", Preview: []spotify.ChartLine{
				{First: "Hong Kong (feat. 王菲季候風)", Second: "Tyson Yoshi"},
				{First: "Housewife", Second: "Tate McRae"},
				{First: "Nicole Kidman", Second: "ADÉLA"},
			}},
		}
		seedArt(a, "https://example.com/ch1.jpg", color.NRGBA{R: 110, G: 45, B: 165, A: 255})
		seedArt(a, "https://example.com/ch2.jpg", color.NRGBA{R: 175, G: 30, B: 125, A: 255})
		seedArt(a, "https://example.com/ch3.jpg", color.NRGBA{R: 15, G: 95, B: 105, A: 255})
		seedArt(a, "https://example.com/ch4.jpg", color.NRGBA{R: 155, G: 25, B: 40, A: 255})

		var plTracks []spotify.Track
		for i := range 30 {
			plTracks = append(plTracks, spotify.Track{
				ID:       fmt.Sprintf("tr:%d", i),
				Name:     fmt.Sprintf("歌单歌曲 %d", i+1),
				Artists:  []spotify.Artist{{Name: "艺人名称"}},
				Album:    spotify.Album{Name: "专辑名称", PicURL: "https://example.com/c1.jpg"},
				Duration: 210000,
			})
		}
		a.playlists[spotify.LikedURI] = &spotify.Playlist{ID: spotify.LikedURI, Name: "我喜欢的音乐", Tracks: plTracks}

		a.localLoaded = true
		for i := range 30 {
			a.localTracks = append(a.localTracks, spotify.Track{
				ID:        fmt.Sprintf("local:%d", i),
				LocalPath: fmt.Sprintf("track-%d.mp3", i),
				Name:      fmt.Sprintf("本地歌曲 %d", i+1),
				Artists:   []spotify.Artist{{Name: "本地艺人"}},
				Album:     spotify.Album{Name: "本地专辑名称", PicURL: "https://example.com/c4.jpg"},
				Duration:  220000,
			})
		}

		tt := ui.NewTester(a.view, 1200, 800)
		tt.SetDark(dark)

		a.open(route{page: pageHome})
		tt.Advance(time.Second)
		tt.Frame()
		encode("home-"+mode, tt)

		a.open(route{page: pageCharts})
		tt.Advance(time.Second)
		tt.Frame()
		encode("charts-"+mode, tt)
		if !dark {
			if r, ok := tt.Find("新加坡热门歌曲"); ok {
				tt.Move(r.X+r.W/2, r.Y+r.H/2)
				tt.Advance(time.Second)
				tt.Frame()
				encode("charts-hovered-"+mode, tt)
			}
		}

		a.open(route{page: pagePlaylist, playlist: spotify.LikedURI})
		tt.Advance(time.Second)
		tt.Frame()
		tt.Scroll(900, 550, 0, 800)
		tt.Advance(time.Second)
		tt.Frame()
		encode("playlist-scrolled-"+mode, tt)

		a.cats = []spotify.Category{
			{ID: "spotify:page:pop", Name: "流行", Color: "#8d67ab", Image: "https://example.com/ch1.jpg"},
			{ID: "spotify:page:rock", Name: "摇滚", Color: "#e8115b", Image: "https://example.com/ch2.jpg"},
			{ID: "spotify:page:chill", Name: "轻松", Color: "#148a08", Image: "https://example.com/ch3.jpg"},
			{ID: "spotify:page:focus", Name: "专注", Color: "#1e3264", Image: "https://example.com/ch4.jpg"},
		}
		a.recentLists = []spotify.Playlist{
			{ID: "spotify:playlist:rec1", Name: "富士山下 电台", CoverURL: "https://example.com/c1.jpg"},
			{ID: "spotify:playlist:rec2", Name: "U87", CoverURL: "https://example.com/c4.jpg"},
		}
		a.played = []spotify.Track{
			{ID: "tr:1", Name: "一张相片", Artists: []spotify.Artist{{Name: "林宥嘉"}}, Album: spotify.Album{Name: "大小说家", PicURL: "https://example.com/c1.jpg"}, Duration: 200000},
		}
		if !dark {
			a.open(route{page: pageBrowse})
			tt.Frame()
			if r, ok := tt.Find("摇滚"); ok {
				tt.Move(r.X+r.W/2, r.Y+r.H/2)
				tt.Advance(time.Second)
				tt.Frame()
				encode("browse-hovered-"+mode, tt)
			}
			a.open(route{page: pageRecent})
			tt.Frame()
			if r, ok := tt.Find("U87"); ok {
				tt.Move(r.X+r.W/2, r.Y+r.H/2)
				tt.Advance(time.Second)
				tt.Frame()
				encode("recent-hovered-"+mode, tt)
			}
		}

		a.open(route{page: pageLocal})
		tt.Advance(time.Second)
		tt.Frame()
		encode("local-unscrolled-"+mode, tt)
		tt.Scroll(900, 550, 0, 800)
		tt.Advance(time.Second)
		tt.Frame()
		encode("local-scrolled-"+mode, tt)
	}
	for _, page := range []struct {
		name  string
		route route
	}{
		{"home-classic-light", route{page: pageHome}},
		{"charts-classic-light", route{page: pageCharts}},
	} {
		a := newTestApp()
		a.settings = defaultSettings()
		a.settings.Glass = false
		a.homeSections = testSections(1)
		a.mine = []spotify.Playlist{{ID: spotify.LikedURI, Name: "我喜欢的音乐"}, {ID: "spotify:playlist:1", Name: "RADWIMPS"}}
		a.charts = []spotify.Playlist{
			{ID: "ch1", Name: "全球热门歌曲", Preview: []spotify.ChartLine{{First: "Song 1", Second: "Artist 1"}}},
			{ID: "ch2", Name: "新加坡热门歌曲", Preview: []spotify.ChartLine{{First: "Song 2", Second: "Artist 2"}}},
			{ID: "ch3", Name: "全球前 50 名", Preview: []spotify.ChartLine{{First: "Song 3", Second: "Artist 3"}}},
			{ID: "ch4", Name: "新加坡前 50 名", Preview: []spotify.ChartLine{{First: "Song 4", Second: "Artist 4"}}},
		}
		tt := ui.NewTester(a.view, 1200, 800)
		tt.SetDark(false)
		a.open(page.route)
		tt.Advance(time.Second)
		tt.Frame()
		encode(page.name, tt)
	}
}
