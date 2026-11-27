package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"vinyl/internal/qtweb"
	"vinyl/internal/spotify"
)

// signin is the web player's window, open only while it is needed. Hidden,
// it signs in as the listener last did and hands what it gets to package
// spotify, again whenever that runs out; shown, it is where the listener
// signs in. Closed in between, its page gives back the memory it took.
type signin struct {
	win           *mygo.Window
	qtID          uint64
	qtCancel      context.CancelFunc
	renewCancel   context.CancelFunc
	renewCancelID uint64
	cookies       string
	shown         bool // the window shows, for the listener to sign in
	gen           int  // counts the times it showed, to stop the wait of one closed
}

// captureMu lets one capture use the window at a time.
var captureMu sync.Mutex

// renewCancelMu guards signin.renewCancel / renewCancelID across goroutines
// without putting a sync.Mutex value inside app.
var renewCancelMu sync.Mutex

// pendingCookieClear ensures an explicit sign-out clears browser cookies
// strictly before any immediately subsequent login window opens, without
// wiping a new sign-in's cookies if the background sign-out goroutine runs late.
var pendingCookieClear atomic.Bool

func (a *app) setRenewCancel(cancel context.CancelFunc) uint64 {
	renewCancelMu.Lock()
	defer renewCancelMu.Unlock()
	a.signin.renewCancelID++
	id := a.signin.renewCancelID
	a.signin.renewCancel = cancel
	return id
}

func (a *app) clearRenewCancel(id uint64) {
	renewCancelMu.Lock()
	if a.signin.renewCancelID == id {
		a.signin.renewCancel = nil
	}
	renewCancelMu.Unlock()
}

func (a *app) cancelRenew() {
	renewCancelMu.Lock()
	cancel := a.signin.renewCancel
	a.signin.renewCancel = nil
	renewCancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// boot signs in as the listener last did, if the web player still is, then
// loads the home page.
func (a *app) boot() {
	a.homeLoading = true
	spotify.Renew = a.renewIdentity
	spotify.RenewFull = a.renewWebViewIdentity
	// Browsing works signed out too: the app gets itself the identity an
	// anonymous visitor has, instead of waiting for someone to sign in.
	spotify.RenewGuest = spotify.AnonymousIdentity
	gen := a.accountGen
	epoch := accountEpoch()
	go func() {
		// Nobody ever signed in: there is nothing to renew, and loading
		// the web player would only take ten seconds to say so. The
		// visitor's pages show at once instead.
		kept := hasAccountCookies()
		a.post(func() {
			if gen != a.accountGen || a.quitting || accountEpoch() != epoch {
				return
			}
			if !kept {
				a.signedIn(nil)
				return
			}
			a.setAccountSession(true)
			askAccount(a, func(prof *spotify.Profile, err error) {
				if accountEpoch() == epoch {
					a.tookSignIn(gen, 0, prof, err)
				}
			})
		})
	}()
}

// keptAccount signs in as the listener whose cookies are kept: renews the
// identity if need be, through package spotify, so that a page opened
// meanwhile, from what restoreHome shows, waits on this renewal rather than
// loading the web player a second time; then asks who they are. It blocks.
func keptAccount() (*spotify.Profile, error) {
	if _, err := spotify.CurrentIdentity(); err != nil {
		return nil, err
	}
	prof, err := spotify.Account()
	if err != nil {
		log.Printf("account: %v", err)
	}
	return prof, err
}

// askAccount runs keptAccount off the main thread and hands what it found
// to took, on it.
var askAccount = func(a *app, took func(*spotify.Profile, error)) {
	go func() {
		prof, err := keptAccount()
		a.post(func() { took(prof, err) })
	}()
}

// signInAfter runs fn on the main thread after d.
var signInAfter = func(a *app, d time.Duration, fn func()) {
	time.AfterFunc(d, func() { a.post(fn) })
}

// How long after a sign-in that did not get through, as when the app opens
// before the network is up, it is asked again: the first wait, doubled
// each time up to the last.
const (
	signInFirst = 10 * time.Second
	signInLast  = 2 * time.Minute
)

// tookSignIn takes what keptAccount found on try (from 0), on the main
// thread. gen is a.accountGen as it was asked: signed in or out since, what
// it found is no one's. Turned down, nobody is signed in. Not got through,
// offline say, the listener is signed in still as far as anyone knows: what
// showed last time stays rather than the sign-in page, and it is asked
// again, as Connect and all else signedIn starts wait on it. They were left
// out for as long as the app ran, once it opened before the network did.
func (a *app) tookSignIn(gen, try int, prof *spotify.Profile, err error) {
	if gen != a.accountGen || a.quitting {
		return
	}
	if prof != nil || errors.Is(err, spotify.ErrSignedOut) {
		if try > 0 {
			log.Printf("sign in: through on try %d", try+1)
		}
		a.signedIn(prof)
		return
	}
	wait := min(signInFirst<<min(try, 8), signInLast)
	log.Printf("sign in: %v; again in %v", err, wait)
	wasSession := a.hasAccountSession()
	epoch := accountEpoch()
	if try == 0 {
		if a.profile != nil {
			a.homeLoading = false
		} else {
			// Nothing was kept to show: show visitor pages without leaving
			// accountSession=true during the offline wait (which would force
			// guest browsing into account renewal / hidden WebView timeouts).
			a.signedIn(nil)
			gen = a.accountGen
			epoch = accountEpoch()
		}
	} else if a.profile == nil {
		// Retry failed while still offline in visitor mode: keep guest
		// browsing cleanly signed out until the next backoff attempt.
		a.setAccountSession(false)
	}
	signInAfter(a, wait, func() {
		if gen != a.accountGen || a.quitting || accountEpoch() != epoch {
			return
		}
		if wasSession && a.profile == nil {
			a.setAccountSession(true)
		}
		askAccount(a, func(prof *spotify.Profile, err error) {
			if accountEpoch() == epoch {
				a.tookSignIn(gen, try+1, prof, err)
			}
		})
	})
}

// signinWindow returns the web player's window, at url: the one open, or a
// new one, hidden. It runs on the main thread.
func (a *app) signinWindow(url string) *mygo.Window {
	s := &a.signin
	if s.win != nil {
		s.win.Page().LoadURL(url)
		return s.win
	}
	w := mygo.NewWindow(mygo.WindowOptions{
		Title: "登录 Spotify", Width: 480, Height: 720, Hidden: true, URL: url,
		Page: mygo.PageOptions{PreloadScript: spotify.CaptureJS, UserAgentSuffix: spotify.WebViewAgentSuffix},
	})
	w.OnClosed(func() {
		// Closed by the listener, as they gave up signing in.
		if s.win == w {
			s.win, s.shown = nil, false
			s.gen++
			if a.profile == nil && a.homeLoading && !a.quitting {
				a.signedIn(nil)
			}
		}
	})
	s.win = w
	return w
}

// closeSignin closes the web player's window, on the main thread.
func (a *app) closeSignin() {
	a.cancelRenew()
	if cancel := a.signin.qtCancel; cancel != nil {
		a.signin.qtCancel, a.signin.qtID, a.signin.shown = nil, 0, false
		cancel()
	}
	if w := a.signin.win; w != nil {
		a.signin.win, a.signin.shown = nil, false
		w.Close()
	}
}

// renewIdentity loads the web player out of sight and waits for its token,
// anonymous if nobody is signed in. It blocks, so it runs on a goroutine.
func (a *app) renewIdentity() (spotify.Identity, error) {
	if !a.hasAccountSession() {
		return spotify.Identity{}, spotify.ErrSignedOut
	}
	if qtweb.Enabled() {
		return a.renewQtIdentity()
	}
	// A sign-out meanwhile makes what this finds no one's.
	epoch := accountEpoch()
	// The cookies kept when the listener signed in are enough for a token of
	// their own: one request, where the web view would load a page, ask, and
	// be read for the answer. Stale ones are dropped; with none, or Spotify
	// turning them down, it falls through to the web view.
	kept := loadAccountCookies()
	if kept != "" {
		id, err := spotify.AccountIdentity(kept)
		switch {
		case err == nil && (accountEpoch() != epoch || !a.hasAccountSession()):
			return spotify.Identity{}, spotify.ErrSignedOut
		case err == nil:
			log.Printf("renew: as the listener, without the web view")
			// What loadEmail asks with, as it would the web view's.
			mygo.RunOnMain(func() {
				if accountEpoch() == epoch && a.hasAccountSession() {
					a.signin.cookies = kept
				}
			})
			return id, nil
		case errors.Is(err, spotify.ErrStaleCookies):
			log.Printf("renew: the cookies kept are signed out; dropped")
			dropAccountCookies(kept)
			kept = ""
		default:
			log.Printf("renew as the listener, without the web view: %v", err)
		}
	}
	return a.renewWebViewIdentity()
}

// renewWebViewIdentity loads the hidden web player page to capture a
// full-scope token when a cookie-only renewal is insufficient.
func (a *app) renewWebViewIdentity() (spotify.Identity, error) {
	if !a.hasAccountSession() {
		return spotify.Identity{}, spotify.ErrSignedOut
	}
	if qtweb.Enabled() {
		return a.renewQtIdentityFull()
	}
	epoch := accountEpoch()
	kept := loadAccountCookies()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cancelID := a.setRenewCancel(cancel)
	defer a.clearRenewCancel(cancelID)
	captureMu.Lock()
	defer captureMu.Unlock()
	if ctx.Err() != nil || accountEpoch() != epoch || !a.hasAccountSession() {
		return spotify.Identity{}, spotify.ErrSignedOut
	}
	var w *mygo.Window
	mygo.RunOnMain(func() {
		if ctx.Err() == nil && accountEpoch() == epoch && a.hasAccountSession() && !a.quitting {
			w = a.signinWindow(spotify.WebPlayerURL)
		}
	})
	if w == nil {
		return spotify.Identity{}, spotify.ErrSignedOut
	}
	id, err := capture(ctx, w.Page(), false)
	cookies := ""
	switch {
	case err == nil && (accountEpoch() != epoch || !a.hasAccountSession()):
		id, err = spotify.Identity{}, spotify.ErrSignedOut
	case err == nil && !id.Anonymous:
		cookies = accountCookies()
		// Kept for the next renewal to ask with, so that the web view is
		// opened for signing in, and not once an hour after.
		saveAccountCookies(epoch, cookies)
	case err == nil:
		// Signed out on the web player too: stop spawning hidden web views on
		// subsequent guest requests until the listener signs in again.
		a.setAccountSession(false)
		if kept != "" {
			dropAccountCookies(kept)
		}
	}
	mygo.RunOnMain(func() {
		if accountEpoch() == epoch && a.hasAccountSession() {
			a.signin.cookies = cookies
		}
		if !a.signin.shown {
			a.closeSignin()
		}
	})
	return id, err
}

// capture waits for the web player on page to have a token, as CaptureJS
// notes it, and its client token, which comes a moment later. Unless
// anonymous will do, it waits on for the listener to sign in.
func capture(ctx context.Context, page *mygo.Page, signedIn bool) (spotify.Identity, error) {
	return captureIdentity(ctx, page.EvalContext, signedIn)
}

func captureIdentity(ctx context.Context, eval func(context.Context, string) (any, error), signedIn bool) (spotify.Identity, error) {
	var since time.Time
	for {
		select {
		case <-ctx.Done():
			return spotify.Identity{}, ctx.Err()
		case <-time.After(400 * time.Millisecond):
		}
		v, err := eval(ctx, "JSON.stringify(window.__vinyl || null)")
		if errors.Is(err, errQtAuthClosed) {
			return spotify.Identity{}, err
		}
		s, _ := v.(string)
		if err != nil || s == "" || s == "null" {
			continue
		}
		id, ok := spotify.ParseIdentity(s)
		if !ok || id.Anonymous && signedIn {
			continue
		}
		if since.IsZero() {
			since = time.Now()
		}
		if id.ClientToken != "" || time.Since(since) > 8*time.Second {
			return id, nil
		}
	}
}

// openLogin shows the web player's sign-in page, and signs the app in once
// the listener has.
func (a *app) openLogin() {
	if qtweb.Enabled() {
		a.openQtLogin()
		return
	}
	a.closeSignin()
	s := &a.signin
	s.gen++
	gen := s.gen
	s.shown = true
	epoch := bumpAccountEpoch()
	go func() {
		id, cookies, ok := func() (spotify.Identity, string, bool) {
			captureMu.Lock()
			defer captureMu.Unlock()
			if pendingCookieClear.Swap(false) {
				if err := mygo.Cookies.Clear(); err != nil {
					log.Printf("sign out: %v", err)
				}
			}
			var w *mygo.Window
			mygo.RunOnMain(func() {
				if gen == s.gen && !a.quitting {
					w = a.signinWindow(spotify.LoginURL)
					w.Show()
					w.Focus()
				}
			})
			if w == nil {
				return spotify.Identity{}, "", false
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			// Closing the window stops the wait.
			go func() {
				for ctx.Err() == nil {
					time.Sleep(time.Second)
					var closed bool
					mygo.RunOnMain(func() { closed = gen != s.gen || a.quitting })
					if closed {
						cancel()
					}
				}
			}()
			id, err := capture(ctx, w.Page(), true)
			if err != nil || accountEpoch() != epoch {
				return spotify.Identity{}, "", false
			}
			cookies := accountCookies()
			if ctx.Err() != nil || accountEpoch() != epoch {
				return spotify.Identity{}, "", false
			}
			accepted := false
			mygo.RunOnMain(func() {
				if gen == s.gen && !a.quitting && accountEpoch() == epoch && ctx.Err() == nil {
					accepted = true
				}
			})
			if !accepted {
				return spotify.Identity{}, "", false
			}
			saveAccountCookies(epoch, cookies)
			a.setAccountSession(true)
			spotify.SetIdentity(id)
			return id, cookies, true
		}()
		if !ok {
			return
		}
		_ = id
		// captureMu is released before calling spotify.Account so a 401
		// renewal inside Account() cannot deadlock on captureMu.
		prof, err := spotify.Account()
		a.post(func() {
			if gen != s.gen || a.quitting || accountEpoch() != epoch {
				return
			}
			a.signin.cookies = cookies
			a.closeSignin()
			if err != nil {
				a.toast = "登录失败，请稍后再试"
				return
			}
			a.signedIn(prof)
		})
	}()
}

// signedIn shows what is personal to prof, or to nobody.
func (a *app) signedIn(prof *spotify.Profile) {
	// The one restoreHome showed: what showed stays until it loads again.
	same := prof != nil && a.profile != nil && a.profile.UserID == prof.UserID
	if same && prof.Email == "" {
		// Until loadEmail asks again, for the picture it finds.
		prof.Email = a.profile.Email
	}
	a.accountGen++
	a.loadingAr = map[string]bool{}
	a.loadingRel = map[string]bool{}
	a.radioStarting = ""
	a.mineGen++
	a.mineLoading, a.mineAgain = false, false
	a.mineChecked = time.Time{}
	a.profile = prof
	if a.win != nil {
		a.startConnect()
	}
	a.liked, a.liking = nil, nil
	a.loadingPl = map[string]bool{}
	a.playlistSync.active = ""
	a.plThenPlay, a.plRefreshAgain = nil, nil
	if !same {
		a.homeFacet = ""
		a.homeShelves = nil
		a.mine, a.saved, a.followed = nil, nil, nil
		a.homeSections, a.homeShortcuts, a.daily, a.dailyURI = nil, nil, nil, ""
		a.facets, a.sectionAll = nil, nil
		a.loading, a.loadErr = nil, nil
		a.artists = map[string]*spotify.ArtistPage{}
		a.arErr = map[string]string{}
		// Who listens changed: load the lists again.
		a.playlists = map[string]*spotify.Playlist{}
		a.playlistQueries = nil
		a.playlistSorts = nil
		a.playlistOrderCache = playlistOrderCache{}
		a.playlistFilterCache = trackFilterCache{}
		a.loadingPl = map[string]bool{}
		a.playlistSync = playlistSyncState{}
		a.plErr = map[string]string{}
	}
	if prof == nil {
		a.setAccountSession(false)
		a.signin.cookies = ""
		a.spots = nil
		// Nobody is signed in: the app browses as the visitor the web
		// player shows someone who has not signed in, so search, the home
		// page and every page Spotify keeps for everyone stay. What is a
		// listener's own stays empty.
		a.mine, a.saved, a.followed, a.liked = nil, nil, nil, nil
		a.recentLists, a.recentArtists = nil, nil
		a.keepHome()
		a.show(a.route)
		if a.searched != "" {
			a.runSearch(a.searched)
		}
		a.loadHome()
		return
	}
	a.setAccountSession(true)
	// Playback picks up where it was left, once there is someone to play to.
	a.restoreSession()
	if !same {
		a.show(a.route)
	}
	if a.searched != "" {
		a.runSearch(a.searched)
	}
	a.loadHome()
	a.loadLiked(prof.UserID)
	a.loadMine(prof.UserID)
	a.loadEmail(prof)
}

func accountCookies() string {
	if qtweb.Enabled() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		value, _ := qtAccountCookies(ctx)
		return value
	}
	cookies, err := mygo.Cookies.List(mygo.CookieFilter{URL: spotify.AccountURL})
	if err != nil {
		return ""
	}
	pairs := make([]string, len(cookies))
	for i, k := range cookies {
		pairs[i] = k.Name + "=" + k.Value
	}
	return strings.Join(pairs, "; ")
}

func (a *app) loadEmail(prof *spotify.Profile) {
	cookies := a.signin.cookies
	a.signin.cookies = ""
	if cookies == "" {
		return
	}
	go func() {
		email, err := spotify.AccountEmail(cookies)
		if err != nil {
			log.Printf("account email: %v", err)
			return
		}
		a.post(func() {
			prof.Email = email
			if a.profile == prof {
				a.keepHome()
			}
		})
	}()
}

// gravatar is where Gravatar has the picture of email: none, a 404, if it
// has none.
func gravatar(email string) string {
	if email == "" {
		return ""
	}
	h := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return "https://gravatar.com/avatar/" + hex.EncodeToString(h[:]) + "?s=256&d=404"
}

// profileAvatar is the listener's picture: theirs on Spotify, else their
// email's on Gravatar, else, as Spotify and mail apps show who has none,
// the first letter of their name on the color Spotify gives them.
func (a *app) profileAvatar(c *ui.Context, prof *spotify.Profile, size float32) {
	url := prof.AvatarURL
	if url == "" {
		url = gravatar(prof.Email)
	}
	bmp := bitmap(a.art.get(spotify.Sized(url, int(size*3)), false))
	bg, ink := a.pal.press, a.pal.muted
	if v := prof.AvatarColor; v > 0 {
		r, g, b := uint8(v>>16), uint8(v>>8), uint8(v)
		bg, ink = ui.RGB(r, g, b), ui.RGB(255, 255, 255)
		// Dark on a light color, as on Spotify's.
		if 0.2126*float32(r)+0.7152*float32(g)+0.0722*float32(b) > 140 {
			ink = ui.RGBA(0, 0, 0, 0.85)
		}
	}
	name := strings.TrimSpace(prof.Nickname)
	if name == "" {
		name = prof.UserID
	}
	first, _ := utf8.DecodeRuneInString(name)
	box := ui.Box(c).Size(size, size).Radius(size / 2).Shrink(0).Center().Background(bg)
	in := artIn(box, bmp)
	box.Children(func() {
		if in < 1 && first != utf8.RuneError {
			ui.Text(c, string(unicode.ToUpper(first))).FontSize(size * 0.44).FontWeight(700).TextColor(ink)
		}
		artImage(c, bmp, size/2, in)
	})
}

func (a *app) signOut() {
	a.setAccountSession(false)
	a.signin.gen++
	a.closeSignin()
	a.naming = nameDialog{}
	a.deleting = deleteDialog{}
	a.plThenPlay = nil
	a.plRefreshAgain = nil
	a.recentLoading = false
	// Pause playback and deregister Connect while the account token is still
	// valid, before wiping credentials and cookies.
	if a.pl.Playing() {
		a.pl.TogglePlay()
	}
	a.stopConnect()
	if cur := a.pl.Current(); cur != nil && !cur.Local() {
		a.pl.Clear()
	}
	a.clearSession()
	pendingCookieClear.Store(true)
	forgetAccountCookies()
	// Forgets the guest too, so that browsing goes on as a new one rather
	// than as whoever was here before.
	spotify.SignOut()
	a.signedIn(nil)
	go func() {
		if qtweb.Enabled() {
			if err := clearQtAccountPending(); err != nil {
				log.Printf("sign out: %v", err)
			}
			return
		}
		captureMu.Lock()
		defer captureMu.Unlock()
		if !pendingCookieClear.Swap(false) {
			return
		}
		if err := mygo.Cookies.Clear(); err != nil {
			log.Printf("sign out: %v", err)
		}
	}()
}

// signedOutPage stands in for the home page when even the visitor's one
// could not be loaded, signed out: the way in, which needs nothing of the
// network the page did not reach.
func (a *app) signedOutPage(c *ui.Context) {
	p := a.pal
	top, bottom := a.insets(c)
	ui.Column(c).Grow(1).Padding(top, 0, bottom, 0).Center().Gap(10).Children(func() {
		ui.Text(c, "连不上 Spotify").FontSize(20).FontWeight(700)
		ui.Text(c, "登录后可以听歌，免费账号也能完整播放").FontSize(13).TextColor(p.muted).Margin(0, 0, 8, 0)
		if pillButton(c.Key("login"), iconUser, "登录 Spotify", p.accent, p.onAccent).Clicked() {
			a.openLogin()
		}
		if ui.Button(c, "重试").Clicked() {
			a.loadHome()
		}
	})
}

func (a *app) profileName() string {
	if a.profile == nil {
		return ""
	}
	return a.profile.Nickname
}

// accountRow sits at the foot of the sidebar: who is signed in, with a
// dropdown to open the settings or sign out, or a way to sign in.
func (a *app) accountRow(c *ui.Context) {
	p := a.pal
	b := ui.ButtonBase(c.Key("account")).Grow(1).Shrink(1).MinWidth(0).Height(40).PaddingX(8).Gap(10).Radius(10).AlignItems(ui.Center).Justify(ui.Start).FocusRing(false)
	if b.Hovered() || a.accountOpen && a.profile != nil {
		b.Background(p.hover)
	}
	if a.profile == nil {
		a.accountOpen = false
		b.Label("登录").Children(func() {
			ui.Box(c).Size(26, 26).Radius(13).Background(p.press).Center().Children(func() {
				ui.Icon(c, iconUser).Size(14, 14).TextColor(p.muted)
			})
			ui.Text(c, "登录 Spotify").FontSize(13).FontWeight(500).TextColor(p.muted)
		})
		if b.Clicked() {
			a.openLogin()
		}
		return
	}
	prof := a.profile
	b.Label(prof.Nickname).Expanded(a.accountOpen).Children(func() {
		a.profileAvatar(c, prof, 26)
		ui.Text(c, prof.Nickname).FontSize(13).FontWeight(600).SingleLine().Grow(1).Shrink(1).MinWidth(0)
		ui.Icon(c, iconUpDown).Size(13, 13).TextColor(p.faint)
	})
	if b.Clicked() {
		a.accountOpen = !a.accountOpen
	}
	a.accountMenu(c, b)
}

// accountMenu drops up from the account row, as wide as it, in the colors
// of the volume popover: the settings, then signing out.
func (a *app) accountMenu(c *ui.Context, row ui.Element) {
	pop := popoverColors(a.pal.dark)
	ui.PopoverBase(c.Key("account-menu"), row, &a.accountOpen, func(panel ui.Element) {
		panel.AttachTo(row, ui.AnchorTopStart, ui.AnchorBottomStart).Margin(0, 0, 6, 0).Padding(5).Gap(1).
			Role(ui.RoleMenu).FocusGroup(ui.Vertical).Label("账户").TransformOrigin(0.5, 1).
			Transition(ui.ElementTransition{Animation: &ui.Snappy, Enter: &ui.Motion{Scale: 0.96, Y: 4}, Exit: &ui.Motion{Scale: 0.98}})
		a.styleGlassPanel(panel, 14, pop.fill, pop.edge, 24, 8, ui.RGBA(0, 0, 0, 0.2))
		if w := row.Bounds().W; w > 0 {
			panel.Width(max(w, 184))
		}
		shortcut := "Ctrl+,"
		if runtime.GOOS == "darwin" {
			shortcut = "⌘,"
		}
		if a.menuItem(c, iconUser, "个人主页", "").AutoFocus().Clicked() {
			a.accountOpen = false
			a.goTo(route{page: pageUser, user: a.profile.UserID})
		}
		if a.menuItem(c, iconSettings, "设置…", shortcut).Clicked() {
			a.accountOpen = false
			a.openSettings()
		}
		// A box shorter than a device pixel snaps to none of them at a
		// fractional scale; a Divider keeps one at every scale.
		ui.Divider(c).Background(pop.edge).Margin(4, 8)
		if a.menuItem(c, iconSignOut, "退出登录", "").Clicked() {
			a.accountOpen = false
			a.signOut()
		}
	})
}

// menuItem is an item of a dropdown: an icon, its label, and the shortcut
// that does the same, if any.
func (a *app) menuItem(c *ui.Context, icon *ui.SVG, label, shortcut string) ui.Element {
	p := a.pal
	b := ui.ButtonBase(c.Key(label)).Role(ui.RoleMenuItem).Label(label).Height(30).PaddingX(8).Gap(9).Radius(7).
		AlignItems(ui.Center).Justify(ui.Start).FocusRing(false)
	if b.Hovered() || b.FocusVisible() {
		b.Background(p.press)
	}
	b.Children(func() {
		ui.Icon(c, icon).Size(15, 15).TextColor(p.muted)
		ui.Text(c, label).FontSize(13).Grow(1)
		if shortcut != "" {
			ui.Text(c, shortcut).FontSize(12).TextColor(p.faint)
		}
	})
	return b
}

// avatar is a round picture of someone, or a circle of empty until it loads.
func (a *app) avatar(c *ui.Context, url string, size float32, empty ui.Color) {
	bmp := bitmap(a.art.get(spotify.Sized(url, int(size*3)), false))
	box := ui.Box(c).Size(size, size).Radius(size / 2).Shrink(0)
	in := artIn(box, bmp)
	if in < 1 {
		box.Background(empty)
	}
	box.Children(func() { artImage(c, bmp, size/2, in) })
}

var userLibrary = spotify.UserLibrary

// loadMine loads the listener's playlists, for the sidebar.
func (a *app) loadMine(uid string) {
	if a.quitting || a.profile == nil || a.profile.UserID != uid {
		return
	}
	if a.mineLoading {
		a.mineAgain = true
		return
	}
	a.mineLoading = true
	a.mineChecked = time.Now()
	a.mineGen++
	gen := a.mineGen
	get := userLibrary
	go func() {
		lib, err := get(uid)
		a.post(func() { a.receiveMine(uid, gen, lib, err) })
	}()
}

func (a *app) receiveMine(uid string, gen int, lib *spotify.Library, err error) {
	if a.quitting || gen != a.mineGen || a.profile == nil || a.profile.UserID != uid {
		return
	}
	a.mineLoading = false
	if a.mineAgain {
		a.mineAgain = false
		a.loadMine(uid)
		return
	}
	if err != nil {
		log.Printf("library refresh: %v", err)
		return
	}
	if lib != nil {
		a.mine, a.saved, a.followed = lib.Made, lib.Saved, lib.Artists
		a.keepHome()
		a.syncPlaylistCaches(time.Now())
	}
}

func (a *app) refreshMine(now time.Time) {
	if a.quitting || a.profile == nil || a.mineLoading || now.Sub(a.mineChecked) < 5*time.Second {
		return
	}
	if a.liked == nil {
		a.loadLiked(a.profile.UserID) // failed at sign in: every heart empty
	}
	a.loadMine(a.profile.UserID)
}

func (a *app) scheduleMine() {
	if a.quitting {
		return
	}
	a.mineTimer = time.AfterFunc(30*time.Second, func() {
		a.post(func() {
			a.refreshMine(time.Now())
			a.scheduleMine()
		})
	})
}

const (
	sidebarSectionPinned   = "pinned"
	sidebarSectionMine     = "mine"
	sidebarSectionSaved    = "saved"
	sidebarSectionFollowed = "followed"
)

type pinnedEntry struct {
	pl   *spotify.Playlist
	mine bool
}

func (a *app) sidebarCollapsed(section string) bool {
	return a.settings.SidebarCollapsed[section]
}

func (a *app) toggleSidebarSection(section string) {
	if a.settings.SidebarCollapsed == nil {
		a.settings.SidebarCollapsed = map[string]bool{}
	}
	if a.settings.SidebarCollapsed[section] {
		delete(a.settings.SidebarCollapsed, section)
	} else {
		a.settings.SidebarCollapsed[section] = true
	}
	a.settings.save()
}

func (a *app) expandSidebarSection(section string) {
	if !a.settings.SidebarCollapsed[section] {
		return
	}
	delete(a.settings.SidebarCollapsed, section)
	a.settings.save()
}

func (a *app) isPlaylistPinned(id string) bool {
	return slices.Contains(a.settings.PinnedPlaylists, id)
}

func (a *app) inLibrary(id string) bool {
	return slices.ContainsFunc(a.mine, func(pl spotify.Playlist) bool { return pl.ID == id }) ||
		slices.ContainsFunc(a.saved, func(pl spotify.Playlist) bool { return pl.ID == id })
}

func (a *app) togglePlaylistPinned(id string) {
	if i := slices.Index(a.settings.PinnedPlaylists, id); i >= 0 {
		a.settings.PinnedPlaylists = slices.Delete(slices.Clone(a.settings.PinnedPlaylists), i, i+1)
		a.settings.save()
		a.toast = "已取消置顶"
		return
	}
	a.settings.PinnedPlaylists = append(slices.Clone(a.settings.PinnedPlaylists), id)
	if a.settings.SidebarCollapsed[sidebarSectionPinned] {
		delete(a.settings.SidebarCollapsed, sidebarSectionPinned)
	}
	a.settings.save()
	a.toast = "已置顶歌单"
}

func (a *app) unpinPlaylist(id string) int {
	i := slices.Index(a.settings.PinnedPlaylists, id)
	if i < 0 {
		return -1
	}
	a.settings.PinnedPlaylists = slices.Delete(slices.Clone(a.settings.PinnedPlaylists), i, i+1)
	a.settings.save()
	return i
}

func (a *app) restorePinnedPlaylist(id string, idx int) {
	if idx < 0 || a.isPlaylistPinned(id) {
		return
	}
	pos := min(idx, len(a.settings.PinnedPlaylists))
	a.settings.PinnedPlaylists = slices.Insert(slices.Clone(a.settings.PinnedPlaylists), pos, id)
	a.settings.save()
}

func (a *app) movePinnedPlaylist(id string, delta int) {
	visible := a.pinnedPlaylists()
	vi := slices.IndexFunc(visible, func(e pinnedEntry) bool { return e.pl.ID == id })
	vj := vi + delta
	if vi < 0 || vj < 0 || vj >= len(visible) {
		return
	}
	i := slices.Index(a.settings.PinnedPlaylists, visible[vi].pl.ID)
	j := slices.Index(a.settings.PinnedPlaylists, visible[vj].pl.ID)
	if i < 0 || j < 0 {
		return
	}
	next := slices.Clone(a.settings.PinnedPlaylists)
	next[i], next[j] = next[j], next[i]
	a.settings.PinnedPlaylists = next
	a.settings.save()
}

func (a *app) pinnedPlaylists() []pinnedEntry {
	if len(a.settings.PinnedPlaylists) == 0 {
		return nil
	}
	out := make([]pinnedEntry, 0, len(a.settings.PinnedPlaylists))
	seen := make(map[string]bool, len(a.settings.PinnedPlaylists))
	for _, id := range a.settings.PinnedPlaylists {
		if id == "" || seen[id] {
			continue
		}
		if i := slices.IndexFunc(a.mine, func(pl spotify.Playlist) bool { return pl.ID == id }); i >= 0 {
			seen[id] = true
			out = append(out, pinnedEntry{pl: &a.mine[i], mine: true})
			continue
		}
		if i := slices.IndexFunc(a.saved, func(pl spotify.Playlist) bool { return pl.ID == id }); i >= 0 {
			seen[id] = true
			out = append(out, pinnedEntry{pl: &a.saved[i], mine: false})
		}
	}
	return out
}

// sidebarSectionHeader renders a collapsible sidebar section title with a
// chevron aligned to the x=10 left axis of nav/playlist icons, a compact count
// badge while collapsed, and optional trailing actions aligned to right-16.
func (a *app) sidebarSectionHeader(c *ui.Context, section, title string, count int, prevHadItems bool, trailing func()) bool {
	p := a.pal
	collapsed := a.sidebarCollapsed(section)
	topMargin := float32(0)
	if prevHadItems {
		topMargin = 10
	}
	bottomMargin := float32(0)
	if !collapsed {
		bottomMargin = 2
	}
	ui.Row(c.Key("sec-" + section)).AlignItems(ui.Center).Gap(4).Margin(topMargin, 0, bottomMargin, 0).Children(func() {
		tip := "折叠" + title
		rot := float32(0)
		if collapsed {
			tip = "展开" + title
			rot = -90
		}
		// PaddingLeft=7 + iconDown's ~3px internal glyph inset aligns the
		// chevron's left stroke cleanly on x=10 with nav icons and covers below.
		b := ui.ButtonBase(c.Key("toggle")).Grow(1).Shrink(1).MinWidth(0).Height(30).Padding(0, 6, 0, 7).Gap(7).Radius(8).
			AlignItems(ui.Center).Justify(ui.Start).FocusRing(false).Label(title).Tooltip(tip)
		fg := p.muted
		if b.Hovered() {
			b.Background(p.hover)
			fg = p.text
		}
		b.Children(func() {
			ui.Icon(c.Key("chev"), iconDown).Size(13, 13).TextColor(fg).Shrink(0).
				AnimatePose(ui.Pose{Rotate: rot}, ui.Snappy)
			ui.Text(c, title).FontSize(13).FontWeight(600).TextColor(fg).SingleLine().Shrink(1).MinWidth(0)
			if collapsed && count > 0 {
				ui.Textf(c, "%d", count).FontSize(12).FontFeatures("tnum").TextColor(p.faint).Shrink(0)
			}
		})
		if b.Clicked() {
			a.toggleSidebarSection(section)
			collapsed = a.sidebarCollapsed(section)
		}
		if trailing != nil {
			trailing()
		}
	})
	return collapsed
}

// pinnedPlaylistSection lists playlists the listener pinned above "我的歌单".
func (a *app) pinnedPlaylistSection(c *ui.Context, prevHadItems bool) (shown, expanded bool) {
	entries := a.pinnedPlaylists()
	if len(entries) == 0 {
		return false, false
	}
	if a.sidebarSectionHeader(c, sidebarSectionPinned, "置顶歌单", len(entries), prevHadItems, nil) {
		return true, false
	}
	if a.settings.MyPlaylistGrid {
		ui.Grid(c.Key("pinned-grid")).Columns(2).Gap(6).Children(func() {
			for i, e := range entries {
				a.myPlaylist(c, e.pl, e.mine, true, true, i, len(entries))
			}
		})
		return true, true
	}
	for i, e := range entries {
		a.myPlaylist(c, e.pl, e.mine, false, true, i, len(entries))
	}
	return true, true
}

// myPlaylists lists the listener's playlists in the sidebar: those they
// made, if mine, with a button to make one and a menu to rename them.
// Pinned playlists also remain listed here so the full library never has gaps.
func (a *app) myPlaylists(c *ui.Context, section, title string, lists []spotify.Playlist, mine, prevHadItems bool) (shown, expanded bool) {
	if len(lists) == 0 {
		return false, false
	}
	var trailing func()
	if mine {
		trailing = func() {
			p := a.pal
			// 26x26 button with MarginRight(3) centers "+" at right-16, matching
			// the trailing pin icon on pinned playlist rows.
			add := ui.ButtonBase(c.Key("new")).Size(26, 26).Margin(0, 3, 0, 0).Shrink(0).Center().Radius(7).
				FocusRing(false).Label("新建歌单").Tooltip("新建歌单")
			fg := p.muted
			if add.Hovered() {
				add.Background(p.hover)
				fg = p.text
			}
			add.Children(func() { ui.Icon(c, iconPlus).Size(14, 14).TextColor(fg) })
			if add.Clicked() {
				a.newPlaylist(nil)
			}
		}
	}
	if a.sidebarSectionHeader(c, section, title, len(lists), prevHadItems, trailing) {
		return true, false
	}
	if a.settings.MyPlaylistGrid {
		ui.Grid(c.Key(title + "-grid")).Columns(2).Gap(6).Children(func() {
			for i := range lists {
				a.myPlaylist(c, &lists[i], mine, true, false, -1, 0)
			}
		})
		return true, true
	}
	for i := range lists {
		a.myPlaylist(c, &lists[i], mine, false, false, -1, 0)
	}
	return true, true
}

// myPlaylist is one of the listener's lists in the sidebar: a row, or in the
// grid a cover over its name.
func (a *app) myPlaylist(c *ui.Context, pl *spotify.Playlist, mine, grid, pinned bool, pinIdx, pinCount int) {
	p := a.pal
	selected := a.route.page == pagePlaylist && a.route.playlist == pl.ID && !a.nowPlaying
	key := pl.ID
	if pinned {
		key = "pinned-" + pl.ID
	}
	b := ui.ButtonBase(c.Key(key)).Radius(10).FocusRing(false).Label(pl.Name)
	if grid {
		b.Column().Padding(6).Gap(6).AlignItems(ui.Stretch)
	} else {
		// PaddingX(10) aligns covers cleanly with navItem icons (x=10) and
		// aligns the trailing 12px pin icon center at right-16.
		b.Height(34).PaddingX(10).Gap(10).AlignItems(ui.Center).Justify(ui.Start)
	}
	a.styleSidebarItem(b, selected, b.Hovered())
	pinColor := p.faint
	if selected {
		pinColor = p.accent
	}
	b.Children(func() {
		if grid {
			ui.Box(c).AspectRatio(1).Radius(6).Children(func() { a.listArt(c, pl, 200, 6) })
			if pinned {
				ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, iconPin).Size(10.5, 10.5).TextColor(pinColor).Shrink(0)
					ui.Text(c, pl.Name).FontSize(12.5).SingleLine().Shrink(1).MinWidth(0)
				})
			} else {
				ui.Text(c, pl.Name).FontSize(12.5).SingleLine()
			}
			return
		}
		if pl.Liked() {
			ui.Box(c).Size(22, 22).Shrink(0).Children(func() { likedArt(c, 5, 12) })
		} else {
			a.cover(c, pl.Cover(), 80, 22, 5).Shrink(0)
		}
		ui.Text(c, pl.Name).FontSize(13).SingleLine().Grow(1).Shrink(1).MinWidth(0)
		if pinned {
			ui.Icon(c, iconPin).Size(12, 12).TextColor(pinColor).Shrink(0)
		}
	})
	b.ContextMenu(func(m *ui.Menu) {
		if pinned || a.isPlaylistPinned(pl.ID) {
			if m.Item("取消置顶").Chosen() {
				a.togglePlaylistPinned(pl.ID)
			}
			if pinned {
				if pinIdx > 0 {
					if m.Item("上移").Chosen() {
						a.movePinnedPlaylist(pl.ID, -1)
					}
				}
				if pinIdx >= 0 && pinIdx < pinCount-1 {
					if m.Item("下移").Chosen() {
						a.movePinnedPlaylist(pl.ID, 1)
					}
				}
			}
		} else {
			if m.Item("置顶歌单").Chosen() {
				a.togglePlaylistPinned(pl.ID)
			}
		}
		switch {
		case mine && !pl.Liked():
			m.Separator()
			if m.Item("重命名…").Chosen() {
				a.startRename(pl)
			}
			m.Separator()
			if m.Item("删除歌单…").Chosen() {
				a.confirmDelete(pl)
			}
		case !mine:
			m.Separator()
			if m.Item("取消收藏").Chosen() {
				a.toggleSaved(pl)
			}
		}
	})
	if b.Clicked() {
		a.open(route{page: pagePlaylist, playlist: pl.ID})
	}
}
