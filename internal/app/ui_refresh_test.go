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

// With NM_FRAMES set, saves the playlist and colorful home at desktop and minimum sizes in both themes for visual inspection.
func TestLibraryRefreshFrames(t *testing.T) {
	dir := os.Getenv("NM_FRAMES")
	if dir == "" {
		t.Skip()
	}
	for _, dark := range []bool{false, true} {
		for _, size := range [][2]int{{1200, 800}, {880, 580}} {
			a := libraryRefreshApp()
			tt := ui.NewTester(a.view, size[0], size[1])
			tt.SetDark(dark)
			tt.Frame()
			tt.Advance(time.Second)
			tt.Frame()
			theme := "light"
			if dark {
				theme = "dark"
			}
			save := func(page string) {
				t.Helper()
				path := filepath.Join(dir, fmt.Sprintf("library-%s-%s-%dx%d.png", page, theme, int(size[0]), int(size[1])))
				f, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				if err := png.Encode(f, tt.Image()); err != nil {
					t.Fatal(err)
				}
			}
			save("playlist")
			tt.Scroll(float32(size[0]-120), 430, 0, 1000)
			tt.Advance(time.Second)
			tt.Frame()
			save("scrolled")
			a.open(route{page: pageHome})
			tt.Frame()
			tt.Advance(time.Second)
			tt.Frame()
			save("home")
		}
	}
}

func libraryRefreshApp() *app {
	a := newTestApp()
	a.settings = defaultSettings()
	a.profile.Nickname = "狗熊"
	pl := &spotify.Playlist{ID: spotify.LikedURI, Name: "我喜欢的音乐"}
	names := []string{"Harunanshiki", "Your Best Friend", "一事無成", "TAKE ME", "The Ecstasy of Gold", "The first ending", "如果爱忘了", "晴天"}
	artists := []string{"稲葉曇", "倉木麻衣", "鄭融, 周柏豪", "G-DRAGON", "Chill Loungers", "MYTH & ROID", "戚薇", "周杰伦"}
	albums := []string{"Harunanshiki", "OVER THE RAINBOW", "23", "Übermensch", "Hollywood Chill Lounge", "TVアニメ「オーバーロード」", "如果爱忘了", "叶惠美"}
	colors := []color.RGBA{{92, 67, 237, 255}, {229, 101, 116, 255}, {64, 177, 198, 255}, {241, 180, 70, 255}, {66, 142, 200, 255}, {168, 93, 168, 255}, {90, 179, 164, 255}, {233, 137, 74, 255}}
	for i, tint := range colors {
		url := fmt.Sprintf("fixture-cover-%d", i)
		pix := image.NewRGBA(image.Rect(0, 0, 160, 160))
		for y := range 160 {
			for x := range 160 {
				k := float32(x+y) / 318
				if (x-80)*(x-80)+(y-80)*(y-80) < 42*42 {
					k *= 0.25
				}
				pix.SetRGBA(x, y, color.RGBA{uint8(float32(tint.R)*(1-k) + 240*k), uint8(float32(tint.G)*(1-k) + 225*k), uint8(float32(tint.B)*(1-k) + 210*k), 255})
			}
		}
		art := &artwork{bmp: ui.NewBitmap(pix), tint: ui.RGB(tint.R, tint.G, tint.B), used: a.art.now()}
		for _, px := range []int{48, 80, 96, 120, 160, 300, 400, 600} {
			a.art.art[spotify.Sized(url, px)] = art
		}
		a.art.art[spotify.Sized(url, 600)+"#backdrop"] = art
		list := spotify.Playlist{ID: fmt.Sprintf("spotify:playlist:fixture-%d", i), Name: []string{"每周新发现", "每日推荐 1", "每日推荐 2", "每日推荐 3", "每日推荐 4", "陪我去流浪", "华语精选", "夜间电台"}[i], CoverURL: url, Note: artists[i] + " · 为你精选"}
		a.mine = append(a.mine, list)
		if len(a.homeSections) == 0 {
			a.homeSections = []spotify.HomeSection{{URI: "fixture-recommendations", Title: "根据你的喜好推荐", Total: 8}, {URI: "fixture-radio", Title: "推荐电台", Total: 8}}
		}
		for section := range a.homeSections {
			a.homeSections[section].Items = append(a.homeSections[section].Items, spotify.HomeItem{List: &list})
		}
	}
	for i := range 40 {
		j := i % len(names)
		tr := spotify.Track{ID: fmt.Sprint(i), UID: fmt.Sprint(i), Name: names[j], Artists: []spotify.Artist{{Name: artists[j]}}, Album: spotify.Album{Name: albums[j], PicURL: fmt.Sprintf("fixture-cover-%d", j)}, Duration: int64(188000 + j*17000)}
		pl.Tracks = append(pl.Tracks, tr)
		a.liked[tr.ID] = true
	}
	a.mine = append([]spotify.Playlist{{ID: spotify.LikedURI, Name: pl.Name}}, a.mine...)
	a.playlists[pl.ID] = pl
	a.route = route{page: pagePlaylist, playlist: pl.ID}
	a.homeTint = ui.Hex("#765bc8")
	a.pl.Cue(pl.Tracks)
	a.pl.Hold(75 * time.Second)
	return a
}
