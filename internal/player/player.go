package player

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"vinyl/internal/localmusic"
	"vinyl/internal/lyrics"
	"vinyl/internal/spotify"
)

// Output plays a track's audio: the web player's (see Web), or in tests
// package audio's.
type Output interface {
	Play()
	Pause()
	Playing() bool
	Seek(time.Duration)
	Position() time.Duration
	Duration() time.Duration
	OnEnd(func()) (off func())
	SetVolume(float64)
	Close()
}

// systemOutput is an Output the system pauses, plays and skips by itself,
// as the web player's (see webOut.OnSystem).
type systemOutput interface {
	OnSystem(func(cmd string, at time.Duration)) (off func())
}

// Bands is how many bands of pitch Levels splits a spectrum into.
const Bands = 24

type RepeatMode int

const (
	RepeatOff RepeatMode = iota
	RepeatAll
	RepeatOne
)

type ShuffleMode int

const (
	ShuffleOff ShuffleMode = iota
	ShuffleOn
	ShuffleSmart
)

// Player plays a queue of tracks. Its methods run on the main thread;
// loading happens on goroutines, whose results post brings back there.
type Player struct {
	post func(func())
	// Web plays what loads; without it, as in tests, nothing does.
	Web    *Web
	Cache  *Cache
	NewWeb func() (*Web, error)
	// Video plays music videos, made by NewVideo the first time one plays.
	Video    *Web
	NewVideo func() (*Web, error)
	idle     *time.Timer
	idleGen  int
	// Leads, if set, returns how far the listener has the music video of
	// a song run ahead of it, where they set it (VideoLead).
	Leads func(id string) (time.Duration, bool)
	// Changed, if set, hears that what plays, where it is or whether it
	// plays changed.
	Changed func()
	Control func(string, any) bool

	queue        []spotify.Track
	order        []int // the queue's indices in play order: shuffled or not
	at           int   // where in order the current track is, -1 for none
	contextURI   string
	shuffle      bool
	smartShuffle bool
	repeat       RepeatMode
	volume       float64

	// SmartFeed supplies recommended tracks for Smart Shuffle, seeded from a track in the queue.
	SmartFeed func(seed spotify.Track, got func([]spotify.Track))
	// ResolveNetEase, if set, resolves a NetEase track for playback: trying
	// NetEase's audio API first, and falling back to local/cloud or Spotify
	// when the track is unavailable or trial-only on NetEase.
	ResolveNetEase func(ctx context.Context, t spotify.Track) (*spotify.Media, error)
	smartGen     int
	smartFeeding bool
	smartAdded   map[string]bool

	// A radio goes on with the tracks its feed gives, as the queue nears
	// its end, for as long as nothing else is played.
	radio    string
	feed     func(got func([]spotify.Track))
	radioGen int  // counts radios, to drop what an old one gives
	feeding  bool // the feed is asked
	awaiting bool // at the queue's end, for what the feed gives

	track     *spotify.Track
	out       Output
	offEnd    func()
	offSystem func()
	// media is where the track's audio is, once resolved; video whether
	// its music video plays rather than its audio, when it has one, and
	// showsVideo whether out is the video.
	media        *spotify.Media
	mediaBitrate int
	mediaFormat  int
	mediaFileID  string
	video        bool
	showsVideo   bool
	lead         time.Duration // how far the video shown likely runs ahead of the song
	fade         fader
	loading      bool
	playing      bool // what the listener wants: false while paused
	err          string
	gen          int // counts loads, to drop the results of stale ones
	skipped      int // the tracks skipped, as they could not play, before this one

	// Spotify turned the track away for asking too often: it loads again
	// once the wait is over, if it is to play then.
	waiting        bool
	throttles      int // the times in a row, to wait longer each
	offline        int // the times in a row the network was down, likewise
	flaky          int // the times this track failed on the network, Spotify answering
	networkRetries int // reloads at position after a temporary certificate error

	lyrics         []lyrics.Line
	lyricsLoaded   bool
	lyricsOverride bool
	// ahead are the lyrics of the track after this one, fetched as this
	// one plays: LRCLIB, busy, can take seconds.
	ahead struct {
		id     string
		lines  []lyrics.Line
		loaded bool
	}
	// upNext is the track after this one resolved as this one plays
	// (mediaAhead): its media, and when, as its CDN URLs are signed for a
	// while.
	upNext struct {
		id    string
		m     *spotify.Media
		at    time.Time
		timer *time.Timer
	}
	cover []byte // the cover the system shows of the track, once loaded
	// Only an actual remote output or an explicitly released idle page
	// needs the native session on macOS. Loading local audio never does.
	nativeShown bool
	idleNative  bool
	pageNamed   *Web // the page last given the current track's metadata

	// pending holds a seek asked for while the track loads.
	pending time.Duration
	// A seek playing fades out, then lands, then fades in: meanwhile the
	// position is where it goes.
	seeking bool
	seekTo  time.Duration
	seekGen int
}

func New(post func(func())) *Player {
	p := &Player{post: post, at: -1, volume: 0.8}
	// The media keys, and the controls of Control Center.
	mygo.NowPlaying.OnCommand(func(cmd mygo.MediaCommand, pos time.Duration) {
		switch cmd {
		case mygo.MediaPlay, mygo.MediaPause:
			if (cmd == mygo.MediaPlay) != p.playing {
				p.TogglePlay()
			}
		case mygo.MediaNext:
			p.Next()
		case mygo.MediaPrevious:
			p.Prev()
		case mygo.MediaSeek:
			p.Seek(pos)
		}
		p.post(func() {})
	})
	return p
}

// sync shows the system, and Changed, where playback is.
func (p *Player) sync() {
	p.updateIdle()
	if p.Changed != nil {
		defer p.Changed()
	}
	if p.track == nil {
		p.hideNativeNowPlaying()
		p.pageNamed = nil
		return
	}
	page := p.pageOut()
	if page != nil {
		if page.w != p.pageNamed || p.nativeShown {
			p.announce(p.track, p.cover)
		}
		return // WebKit follows the page itself.
	}
	if !p.needsNativeNowPlaying() {
		p.hideNativeNowPlaying()
		p.pageNamed = nil
		return
	}
	if !p.nativeShown {
		p.announce(p.track, p.cover)
		return
	}
	state := mygo.PlaybackPaused
	if p.playing {
		state = mygo.PlaybackPlaying
	}
	mygo.NowPlaying.SetPlayback(state, p.SongPosition())
}

// pageNowPlaying is whether the system shows a page playing as a media
// session of its own: WebKit's does, on macOS, which no setting turns off;
// WebView2's is turned off (see app.go).
var pageNowPlaying = runtime.GOOS == "darwin"

func (p *Player) needsNativeNowPlaying() bool {
	return !pageNowPlaying || p.IsRemote() || p.idleNative
}

func (p *Player) hideNativeNowPlaying() {
	if p.nativeShown {
		mygo.NowPlaying.Clear()
		nativeNowPlayingVisible(false)
		p.nativeShown = false
	}
}

// pageOut is the page playing the track, if the system shows it.
func (p *Player) pageOut() *webOut {
	if o, ok := p.out.(*webOut); ok && pageNowPlaying && o.w != nil {
		return o
	}
	return nil
}

// system follows what the system did to what plays rather than Vinyl.
// WebKit pauses and plays the page itself for the media keys, Control
// Center and apps such as Mami, which pauses what plays while one talks:
// Vinyl went on showing it playing, kept it from being let go when idle,
// and told Spotify Connect it played. It passes the page the skips and
// seeks too.
func (p *Player) system(cmd string, at time.Duration) {
	switch cmd {
	case "pause", "play":
		playing := cmd == "play"
		if p.out == nil || p.playing == playing {
			return
		}
		log.Printf("player: %s from the system", cmd)
		p.playing = playing
		if playing {
			// A pause of Vinyl's faded it out.
			p.fade.fade(1, fadeIn, nil)
		}
		p.sync()
	case "next":
		p.Next()
	case "previous":
		p.Prev()
	case "seek":
		p.Seek(at)
	}
}

// announce shows the system the track, and its cover once loaded. Where
// the system shows the page playing it (pageOut), the page shows it, and
// the app nothing beside it: the app showed it too, as a second session
// beside the page's, titled Vinyl and naming no song (#27).
func (p *Player) announce(t *spotify.Track, artwork []byte) {
	if o := p.pageOut(); o != nil {
		o.w.nowPlaying(t, artwork)
		p.hideNativeNowPlaying()
		p.pageNamed = o.w
		return
	}
	p.pageNamed = nil
	if !p.needsNativeNowPlaying() {
		p.hideNativeNowPlaying()
		return
	}
	state := mygo.PlaybackPaused
	if p.playing {
		state = mygo.PlaybackPlaying
	}
	mygo.NowPlaying.Set(mygo.NowPlayingInfo{
		Title:    t.Name,
		Artist:   t.ArtistNames(),
		Album:    t.Album.Name,
		Artwork:  artwork,
		Duration: t.Length(),
		Position: p.SongPosition(),
		State:    state,
		Commands: mygo.MediaPlay | mygo.MediaPause | mygo.MediaNext | mygo.MediaPrevious | mygo.MediaSeek,
	})
	p.nativeShown = true
	nativeNowPlayingVisible(true)
}

// nativeNowPlayingVisible also changes the native client's eligibility on
// macOS: clearing its metadata alone left a stopped card beside WebKit's.
var nativeNowPlayingVisible = setNativeNowPlayingVisible

func (p *Player) ended() {
	if f, ok := p.out.(interface {
		Fault() (string, int, time.Duration)
	}); ok {
		if msg, licence, wait := f.Fault(); msg != "" {
			if strings.HasPrefix(msg, "backendError ") {
				if p.showsVideo {
					if p.Video != nil {
						p.Video.Close()
						p.Video = nil
					}
					p.videoFailed(msg)
					return
				}
				p.pending = p.Position()
				p.stop()
				if p.Web != nil {
					p.Web.Close()
					p.Web = nil
				}
				p.err, p.playing = "音频播放器未能正常运行，点击播放重试", false
				p.sync()
				return
			}
			if strings.HasPrefix(msg, "keySystemError keySystemNoAccess") || strings.HasPrefix(msg, "keySystemError keySystemNoEME") {
				p.err = "当前播放器不支持所需 DRM（Widevine / EME），无法播放加密媒体"
				p.playing = false
				p.sync()
				return
			}
			if p.showsVideo {
				p.videoFailed(msg)
				return
			}
			if strings.HasPrefix(msg, "keySystemError keySystemServerCertificateRequestFailed") {
				p.certificateFailed(licence, wait)
				return
			}
			p.failed(msg, licence, wait)
			return
		}
	}
	if p.control("advance", nil) {
		return
	}
	p.AdvanceLocal()
}

// AdvanceLocal finishes the current track locally (RepeatOne, Radio/SmartFeed,
// RepeatOff stop, or advancing to the next local track) without notifying
// p.Control.
func (p *Player) AdvanceLocal() {
	p.throttles, p.flaky = 0, 0
	if p.repeat == RepeatOne {
		if p.out != nil {
			p.out.Seek(0)
			p.out.Play()
			p.sync()
		}
		return
	}
	if p.at+1 >= len(p.order) && p.feed != nil {
		p.awaitFeed()
		return
	}
	if p.at+1 >= len(p.order) && p.smartShuffle && p.SmartFeed != nil {
		p.awaitSmartFeed()
		return
	}
	if p.at+1 >= len(p.order) && p.repeat == RepeatOff {
		p.playing = false
		p.sync()
		return
	}
	p.nextLocal()
}

func (p *Player) certificateFailed(status int, wait time.Duration) {
	if status == http.StatusTooManyRequests {
		p.throttled(wait)
		return
	}
	if temporaryCertificateStatus(status) && p.networkRetries == 0 {
		p.networkRetries++
		p.again(time.Second, "播放证书暂时不可用，正在重试")
		return
	}
	from := p.Position()
	p.stop()
	p.pending, p.playing, p.err = from, false, "播放证书暂时不可用，点击播放重试"
	p.sync()
}

// failed goes on from a track the page could not play: msg is what it
// reported, licence is how the licence service last answered for it, wait how
// long it asked to wait.
func (p *Player) failed(msg string, licence int, wait time.Duration) {
	switch licence {
	case http.StatusTooManyRequests:
		p.throttled(wait)
	case http.StatusUnauthorized, http.StatusForbidden:
		// Turned down: the rest of the queue would be too, one after
		// another. Or offline, the identity could not be renewed.
		p.unlessOffline(false, func() {
			p.err = "这首歌暂时无法播放"
			p.playing = false
			p.sync()
		})
	default:
		// The page tried again, and could not: on to the next.
		if played, ok := p.out.(interface{ Played() bool }); ok && played.Played() {
			p.flaky = 0
		}
		skipped := p.skipped
		p.unlessOffline(pageNetFault(msg, licence), func() {
			if len(p.order) == 0 {
				p.playing = false
				p.sync()
				return
			}
			curAt := min(max(p.at, 0), len(p.order)-1)
			p.err = "这首歌暂时无法播放"
			p.skip(curAt, skipped)
		})
	}
}

// unlessOffline does then with the track that could not load or play, but
// not with the network down, which no track would play without: the queue
// skipped through all of them and stopped. The track waits for the network
// then, and loads again from where it was. flaky says the network failed it,
// as a timeout or a connection cut: then it is tried again where it was a
// few times even with Spotify answering, as a link or a proxy that drops
// connections now and then answers between its drops; on it went, three
// tracks failed as the identity could not be renewed, and it stopped.
func (p *Player) unlessOffline(flaky bool, then func()) {
	if p.track != nil && p.track.Local() && !p.track.Cloud() && !p.track.NetEase() {
		p.stop()
		then()
		return
	}
	from := p.Position()
	p.stop()
	p.pending, p.loading = from, true
	gen, up := p.gen, online
	go func() {
		ok := up()
		p.post(func() {
			if gen != p.gen {
				return
			}
			p.loading = false
			if ok && flaky && p.flaky < flakyTries {
				p.flaky++
				log.Printf("player: network failed the track, again (%d)", p.flaky)
				p.again(2*time.Second<<(p.flaky-1), "网络不稳定，正在重试")
				return
			}
			if ok {
				then()
				return
			}
			wait := min(2*time.Second<<min(p.offline, 4), 30*time.Second)
			p.offline++
			log.Printf("player: offline, again in %v", wait)
			p.again(wait, "网络已断开，恢复后接着播放")
		})
	}()
	p.sync()
}

// online reports whether Spotify can be reached; tests swap it.
var online = spotify.Reachable

// flakyTries is how many times a track the network failed is tried again,
// Spotify answering, before it is passed over: after 2, 4 and 8 seconds.
const flakyTries = 3

// netFault reports whether err is the network's, as a timeout or a
// connection cut, rather than an answer of Spotify's.
func netFault(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) || errors.Is(err, io.ErrUnexpectedEOF)
}

// pageNetFault reports whether the page could not play the track for the
// network: hls.js's network errors, the media element's, or a licence or
// certificate request no answer came back for (licence 0, or 5xx).
func pageNetFault(msg string, licence int) bool {
	return strings.HasPrefix(msg, "networkError ") || strings.HasPrefix(msg, "media 2 ") || msg == "media 2" ||
		strings.HasPrefix(msg, "keySystemError ") && (licence == 0 || licence >= 500)
}

// throttled waits as Spotify asked, wait, or longer each time if it did not
// say, then loads the track again from where it was.
func (p *Player) throttled(wait time.Duration) {
	if wait <= 0 {
		wait = 30 * time.Second << min(p.throttles, 4)
	}
	wait = min(wait, 30*time.Minute)
	p.throttles++
	log.Printf("player: throttled, again in %v", wait)
	p.again(wait, "Spotify 暂时限流，稍后重试")
}

// again loads the track again from where it is once wait is over, if it is
// to play then; msg says why it waits.
func (p *Player) again(wait time.Duration, msg string) {
	from := p.Position()
	p.stop()
	p.pending, p.waiting = from, true
	p.err = msg
	gen := p.gen
	afterFunc(wait, func() {
		p.post(func() {
			if gen != p.gen || !p.waiting {
				return
			}
			p.waiting = false
			if p.playing {
				p.loadSkipping(p.at, p.skipped, true, p.pending)
			}
		})
	})
	p.sync()
}

// afterFunc runs fn after d, on a goroutine; tests run it when they like.
var afterFunc = func(d time.Duration, fn func()) { time.AfterFunc(d, fn) }

// skip goes on from the track at order[at], which cannot play, unless
// nothing in the queue can: skipped of them so far.
func (p *Player) skip(at, skipped int) {
	if skipped+1 >= len(p.order) || skipped+1 >= 3 {
		if skipped+1 >= 3 {
			p.err = "连续三首无法播放，已停止自动切歌"
		}
		p.playing = false
		p.sync()
		return
	}
	p.loadSkipping((at+1)%len(p.order), skipped+1, p.playing, 0)
}

// PlayQueue replaces the queue with tracks and plays the one at start.
func (p *Player) PlayQueue(tracks []spotify.Track, start int) {
	p.PlayQueueContext(tracks, start, "")
}

// PlayQueueContext replaces the queue with tracks played from contextURI.
func (p *Player) PlayQueueContext(tracks []spotify.Track, start int, contextURI string) {
	if len(tracks) > 0 && p.control("play", QueueRequest{Tracks: tracks, Start: min(max(start, 0), len(tracks)-1), ContextURI: contextURI}) {
		return
	}
	if len(tracks) == 0 {
		return
	}
	p.contextURI = contextURI
	p.endRadio()
	p.smartGen++
	p.smartFeeding = false
	p.smartAdded = nil
	start = min(max(start, 0), len(tracks)-1)
	p.queue = append([]spotify.Track(nil), tracks...)
	p.order = make([]int, len(p.queue))
	for i := range p.order {
		p.order[i] = i
	}
	if p.shuffle {
		p.shuffleOrder(start)
		p.load(0)
		return
	}
	p.load(start)
}

// ContextURI returns the Spotify context URI of the active queue, if known.
func (p *Player) ContextURI() string { return p.contextURI }

// SetContextURI updates the Spotify context URI of the active queue.
func (p *Player) SetContextURI(uri string) { p.contextURI = uri }

// Awaiting reports whether playback is waiting for an async radio or Smart Shuffle feed.
func (p *Player) Awaiting() bool { return p.awaiting }

// PlayRadio plays tracks in their order, then goes on with those feed
// gives as the queue nears its end, until something else is played. The
// radio's name says which it is. feed calls got on the main thread.
func (p *Player) PlayRadio(name string, tracks []spotify.Track, feed func(got func([]spotify.Track))) {
	p.PlayRadioContext(name, "", tracks, feed)
}

// PlayRadioContext plays a radio queue carrying its Spotify playlist contextURI.
func (p *Player) PlayRadioContext(name, contextURI string, tracks []spotify.Track, feed func(got func([]spotify.Track))) {
	if len(tracks) == 0 {
		return
	}
	shuffle, smart := p.shuffle, p.smartShuffle
	p.shuffle, p.smartShuffle = false, false
	p.PlayQueueContext(tracks, 0, contextURI)
	p.shuffle, p.smartShuffle = shuffle, smart
	p.AttachRadio(name, feed)
}

// RadioFrom makes the track playing the first of a radio's queue, which
// goes on with tracks, then with those feed gives: without starting it
// over.
func (p *Player) RadioFrom(name string, tracks []spotify.Track, feed func(got func([]spotify.Track))) {
	if p.track == nil {
		return
	}
	p.smartGen++
	p.smartFeeding = false
	p.smartAdded = nil
	p.queue, p.order, p.at = []spotify.Track{*p.track}, []int{0}, 0
	p.appendTracks(tracks)
	p.AttachRadio(name, feed)
}

// AttachRadio makes the queue the radio name's, which feed goes on with,
// as after restoring a session that played it.
func (p *Player) AttachRadio(name string, feed func(got func([]spotify.Track))) {
	p.endRadio()
	p.radio, p.feed = name, feed
	p.refill()
}

// EndRadio stops the radio playing: the song playing plays on, but the
// songs the radio queued after it go, and it adds no more.
func (p *Player) EndRadio() {
	if p.radio == "" && p.feed == nil {
		return
	}
	awaiting := p.awaiting
	p.endRadio()
	if p.at >= 0 && p.at+1 < len(p.order) {
		kept := make([]spotify.Track, p.at+1)
		for i, k := range p.order[:p.at+1] {
			kept[i] = p.queue[k]
		}
		p.queue, p.order = kept, make([]int, len(kept))
		for i := range p.order {
			p.order[i] = i
		}
	}
	if awaiting {
		// At its end, waiting for more: nothing plays on.
		p.playing = false
		p.sync()
	}
}

func (p *Player) endRadio() {
	p.radioGen++
	p.radio, p.feed, p.feeding, p.awaiting = "", nil, false, false
}

// Radio returns the name of the radio playing, or "".
func (p *Player) Radio() string { return p.radio }

// refill asks the radio for more once the queue is near its end.
func (p *Player) refill() {
	if p.feed == nil || p.feeding || p.at < len(p.order)-2 {
		return
	}
	p.feeding = true
	gen := p.radioGen
	p.feed(func(more []spotify.Track) {
		if gen != p.radioGen {
			return
		}
		p.feeding = false
		p.appendTracks(more)
		switch {
		case !p.awaiting:
		case p.at+1 < len(p.order):
			p.awaiting = false
			p.networkRetries = 0
			p.loadSkipping(p.at+1, 0, p.playing, 0)
		default:
			// The radio has nothing more: it stops at its end.
			p.awaiting, p.playing = false, false
			p.err = "电台暂时没有更多歌曲"
			p.sync()
		}
	})
}

// awaitFeed waits at the end of a radio's queue for what its feed gives.
func (p *Player) awaitFeed() {
	p.awaiting = true
	p.refill()
}

// appendTracks adds tracks to the end of the queue, but those in it.
func (p *Player) appendTracks(tracks []spotify.Track) {
	in := make(map[string]bool, len(p.queue))
	for _, t := range p.queue {
		in[t.ID] = true
	}
	for _, t := range tracks {
		if !in[t.ID] {
			in[t.ID] = true
			p.order = append(p.order, len(p.queue))
			p.queue = append(p.queue, t)
		}
	}
}

// PlayNext puts tr after the track playing, to play next: moved there if
// queued already. With nothing queued, it plays tr. Played through Spotify
// Connect, Spotify is asked to queue it: what it holds is what plays next,
// and put here only, tr was gone at its next word.
func (p *Player) PlayNext(tr spotify.Track) {
	if p.at < 0 || len(p.order) == 0 {
		p.PlayQueue([]spotify.Track{tr}, 0)
		return
	}
	if (p.track == nil || p.track.ID != tr.ID) && p.control("queue", tr) {
		return
	}
	if i := slices.IndexFunc(p.queue, func(t spotify.Track) bool { return t.ID == tr.ID }); i >= 0 {
		k := slices.Index(p.order, i)
		if k == p.at {
			return
		}
		p.order = slices.Delete(p.order, k, k+1)
		if k < p.at {
			p.at--
		}
		p.order = slices.Insert(p.order, p.at+1, i)
	} else {
		p.order = slices.Insert(p.order, p.at+1, len(p.queue))
		p.queue = append(p.queue, tr)
	}
	p.sync()
}

// AddToQueue puts tr at the end of the queue, and reports whether it did:
// not if queued already. With nothing queued, it plays tr. Played through
// Spotify Connect, Spotify is asked to queue it, as PlayNext: the queue
// here is then what is left of the playlist playing, and a song further
// down it was turned away as queued.
func (p *Player) AddToQueue(tr spotify.Track) bool {
	if p.at < 0 || len(p.order) == 0 {
		p.PlayQueue([]spotify.Track{tr}, 0)
		return true
	}
	if p.control("queue", tr) {
		return true
	}
	if slices.ContainsFunc(p.queue, func(t spotify.Track) bool { return t.ID == tr.ID }) {
		return false
	}
	p.order = append(p.order, len(p.queue))
	p.queue = append(p.queue, tr)
	p.sync()
	return true
}

func shuffleTrackKey(tr spotify.Track) string {
	return tr.DedupKey()
}

// shuffleOrder shuffles the order, with first first, skipping duplicate songs
// (same ID or same normalized title+artist) so shuffle does not replay repeats.
func (p *Player) shuffleOrder(first int) {
	if len(p.queue) == 0 {
		p.order = nil
		return
	}
	order := make([]int, len(p.queue))
	for i := range order {
		order[i] = i
	}
	rand.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
	for i, v := range order {
		if v == first {
			order[0], order[i] = order[i], order[0]
			break
		}
	}
	deduped := make([]int, 0, len(order))
	seenID := make(map[string]bool, len(order))
	keyPos := make(map[string]int, len(order))
	for _, idx := range order {
		tr := p.queue[idx]
		if len(deduped) > 0 && tr.ID != "" && seenID[tr.ID] {
			continue
		}
		dk := shuffleTrackKey(tr)
		if len(deduped) > 0 && dk != "" {
			if pos, exists := keyPos[dk]; exists {
				// Never replace index 0 (the currently playing track); for
				// upcoming duplicates, prefer Spotify > local/cloud > NetEase.
				if pos > 0 && tr.SourcePriority() > p.queue[deduped[pos]].SourcePriority() {
					deduped[pos] = idx
					if tr.ID != "" {
						seenID[tr.ID] = true
					}
				}
				continue
			}
		}
		if tr.ID != "" {
			seenID[tr.ID] = true
		}
		if dk != "" {
			keyPos[dk] = len(deduped)
		}
		deduped = append(deduped, idx)
	}
	p.order = deduped
}

func (p *Player) ToggleShuffle() {
	next := !p.shuffle
	if p.control("shuffle", next) {
		if !next {
			p.smartShuffle = false
			p.removeUnplayedSmartTracks()
		}
		return
	}
	p.shuffle = !p.shuffle
	if !p.shuffle {
		p.smartShuffle = false
		p.removeUnplayedSmartTracks()
	}
	if len(p.order) == 0 {
		return
	}
	cur := p.order[max(p.at, 0)]
	if p.shuffle {
		p.shuffleOrder(cur)
		p.at = 0
	} else {
		p.order = make([]int, len(p.queue))
		for i := range p.order {
			p.order[i] = i
		}
		p.at = cur
	}
}

// CycleShuffle advances through ShuffleOff -> ShuffleOn -> ShuffleSmart -> ShuffleOff.
func (p *Player) CycleShuffle() ShuffleMode {
	switch p.ShuffleMode() {
	case ShuffleOff:
		p.smartShuffle = false
		p.ToggleShuffle()
	case ShuffleOn:
		if !p.shuffle {
			p.ToggleShuffle()
		}
		if p.shuffle {
			p.smartShuffle = true
			p.control("smart_shuffle", true)
			p.refillSmart()
		}
	case ShuffleSmart:
		p.smartShuffle = false
		p.removeUnplayedSmartTracks()
		if p.shuffle {
			p.ToggleShuffle()
		}
	}
	return p.ShuffleMode()
}

// SetShuffleMode restores or sets the shuffle mode directly without sending a Connect toggle.
func (p *Player) SetShuffleMode(mode ShuffleMode) {
	if mode < ShuffleOff || mode > ShuffleSmart {
		mode = ShuffleOff
	}
	wasShuffled := p.shuffle
	p.shuffle = mode != ShuffleOff
	p.smartShuffle = mode == ShuffleSmart
	if !p.smartShuffle {
		p.removeUnplayedSmartTracks()
	}
	if len(p.order) > 0 && p.shuffle != wasShuffled {
		cur := p.order[max(p.at, 0)]
		if p.shuffle {
			p.shuffleOrder(cur)
			p.at = 0
		} else {
			for i := range p.order {
				p.order[i] = i
			}
			p.at = cur
		}
	}
	if p.smartShuffle {
		p.refillSmart()
	}
}

func (p *Player) SetRepeatMode(mode RepeatMode) {
	if mode < RepeatOff || mode > RepeatOne {
		mode = RepeatOff
	}
	p.repeat = mode
}

// IsSmartTrack reports whether track id was interleaved by Smart Shuffle.
func (p *Player) IsSmartTrack(id string) bool {
	return id != "" && p.smartAdded[id]
}

func (p *Player) removeUnplayedSmartTracks() {
	p.smartGen++
	p.smartFeeding = false
	if p.awaiting && p.feed == nil {
		p.awaiting = false
		p.playing = false
		p.sync()
	}
	if len(p.smartAdded) == 0 || len(p.order) == 0 {
		p.smartAdded = nil
		return
	}
	curIdx := -1
	if p.at >= 0 && p.at < len(p.order) {
		curIdx = p.order[p.at]
	}
	keepInQueue := make([]bool, len(p.queue))
	for pos, qIdx := range p.order {
		id := p.queue[qIdx].ID
		if pos <= p.at || !p.smartAdded[id] {
			keepInQueue[qIdx] = true
		}
	}
	remap := make([]int, len(p.queue))
	var newQueue []spotify.Track
	for i, tr := range p.queue {
		if keepInQueue[i] {
			remap[i] = len(newQueue)
			newQueue = append(newQueue, tr)
		} else {
			remap[i] = -1
		}
	}
	var newOrder []int
	newAt := p.at
	for pos, qIdx := range p.order {
		if mapped := remap[qIdx]; mapped >= 0 {
			if qIdx == curIdx {
				newAt = len(newOrder)
			}
			newOrder = append(newOrder, mapped)
		} else if pos < p.at && newAt > 0 {
			newAt--
		}
	}
	p.queue = newQueue
	p.order = newOrder
	p.at = min(max(newAt, -1), len(newOrder)-1)
	if p.at >= 0 && p.at < len(p.order) {
		t := p.queue[p.order[p.at]]
		p.track = &t
	}
	p.smartAdded = nil
}

func (p *Player) refillSmart() {
	if !p.smartShuffle || p.SmartFeed == nil || p.smartFeeding || len(p.order) == 0 || p.feed != nil || p.IsRemote() {
		if p.awaiting && p.feed == nil && !p.smartFeeding {
			p.finishSmartEnd()
		}
		return
	}
	start := max(p.at, 0)
	upcomingSmart := 0
	for i := start + 1; i < min(len(p.order), start+5); i++ {
		if p.smartAdded[p.queue[p.order[i]].ID] {
			upcomingSmart++
		}
	}
	if upcomingSmart > 0 && start+1 < len(p.order) {
		return
	}
	seed, ok := p.smartSeedTrack()
	if !ok {
		if p.awaiting && p.feed == nil {
			p.finishSmartEnd()
		}
		return
	}
	p.smartFeeding = true
	p.smartGen++
	gen := p.smartGen
	p.SmartFeed(seed, func(more []spotify.Track) {
		if gen != p.smartGen || !p.smartShuffle {
			return
		}
		p.smartFeeding = false
		added := p.injectSmartTracks(more)
		switch {
		case !p.awaiting:
			if added > 0 {
				p.sync()
			}
		case p.at+1 < len(p.order):
			p.awaiting = false
			p.networkRetries = 0
			p.loadSkipping(p.at+1, 0, p.playing, 0)
		default:
			p.finishSmartEnd()
		}
	})
}

func (p *Player) finishSmartEnd() {
	p.awaiting = false
	if p.repeat == RepeatAll && len(p.order) > 0 {
		p.networkRetries = 0
		p.loadSkipping(0, 0, p.playing, 0)
		return
	}
	p.playing = false
	p.sync()
}

func (p *Player) awaitSmartFeed() {
	p.awaiting = true
	p.refillSmart()
}

func (p *Player) smartSeedTrack() (spotify.Track, bool) {
	if p.track != nil && !p.track.Local() && !p.track.Episode && p.track.ID != "" {
		return *p.track, true
	}
	for i := max(p.at, 0); i < len(p.order); i++ {
		t := p.queue[p.order[i]]
		if !t.Local() && !t.Episode && t.ID != "" && !p.smartAdded[t.ID] {
			return t, true
		}
	}
	for _, t := range p.queue {
		if !t.Local() && !t.Episode && t.ID != "" {
			return t, true
		}
	}
	return spotify.Track{}, false
}

func (p *Player) injectSmartTracks(tracks []spotify.Track) int {
	if len(tracks) == 0 || len(p.order) == 0 {
		return 0
	}
	in := make(map[string]bool, len(p.queue))
	for _, t := range p.queue {
		in[t.ID] = true
	}
	if p.smartAdded == nil {
		p.smartAdded = map[string]bool{}
	}
	insertAt := max(p.at, 0) + 2
	added := 0
	for _, t := range tracks {
		if !t.Playable() || t.ID == "" || in[t.ID] {
			continue
		}
		in[t.ID] = true
		p.smartAdded[t.ID] = true
		qIdx := len(p.queue)
		p.queue = append(p.queue, t)
		pos := min(insertAt, len(p.order))
		p.order = slices.Insert(p.order, pos, qIdx)
		insertAt = pos + 3
		added++
		if added >= 4 {
			break
		}
	}
	return added
}

func (p *Player) CycleRepeat() {
	mode := (p.repeat + 1) % 3
	if p.control("repeat", mode) {
		return
	}
	p.repeat = mode
}

// Current returns the track playing or loading, or nil.
func (p *Player) Current() *spotify.Track { return p.track }

// Queue returns the tracks queued, in the order they were given.
func (p *Player) Queue() []spotify.Track { return p.queue }

// Playing reports what the listener wants: false while paused.
func (p *Player) Playing() bool { return p.playing }

// Loading reports whether the current track is still loading.
func (p *Player) Loading() bool { return p.loading || p.awaiting }

// Ready reports whether the current track has loaded, to play.
func (p *Player) Ready() bool { return p.out != nil }

// Err returns why the current track did not play, or "".
func (p *Player) Err() string { return p.err }

func (p *Player) Shuffle() bool      { return p.shuffle }
func (p *Player) SmartShuffle() bool { return p.smartShuffle }
func (p *Player) ShuffleMode() ShuffleMode {
	if p.smartShuffle {
		return ShuffleSmart
	}
	if p.shuffle {
		return ShuffleOn
	}
	return ShuffleOff
}
func (p *Player) Repeat() RepeatMode { return p.repeat }

// Lyrics returns the current track's lyrics, and whether they have loaded.
func (p *Player) Lyrics() ([]lyrics.Line, bool) { return p.lyrics, p.lyricsLoaded }

// AdoptLyrics replaces the current track's in-memory lyrics when uri matches,
// preventing an earlier background lookup from overwriting them later.
func (p *Player) AdoptLyrics(uri string, lines []lyrics.Line) {
	if p.track == nil || p.track.URI() != uri || len(lines) == 0 {
		return
	}
	p.lyrics = append([]lyrics.Line(nil), lines...)
	p.lyricsLoaded = true
	p.lyricsOverride = true
}

// UseFallbackLyrics shows lyrics while the current track's lookup continues.
func (p *Player) UseFallbackLyrics(uri string, lines []lyrics.Line) {
	if !p.lyricsLoaded && p.track != nil && p.track.URI() == uri {
		p.lyrics = append([]lyrics.Line(nil), lines...)
	}
}

// PlayOrder returns the queue's indices in play order, and where the
// current track is among them, -1 for none.
func (p *Player) PlayOrder() ([]int, int) { return p.order, p.at }

func (p *Player) MediaInfo() (bitrate, format int, fileID string) {
	return p.mediaBitrate, p.mediaFormat, p.mediaFileID
}

func (p *Player) Next() {
	if p.control("skip_next", nil) {
		return
	}
	p.nextLocal()
}

func (p *Player) nextLocal() {
	if len(p.order) == 0 {
		return
	}
	at := p.at + 1
	if at >= len(p.order) {
		if p.feed != nil {
			p.stop()
			p.playing = true
			p.awaitFeed()
			return
		}
		if p.smartShuffle && p.SmartFeed != nil {
			p.stop()
			p.playing = true
			p.awaitSmartFeed()
			return
		}
		at = 0
	}
	p.load(at)
}

func (p *Player) Prev() {
	if p.control("skip_prev", nil) {
		return
	}
	if len(p.order) == 0 {
		return
	}
	// As every player does: the start of this track, then the one before.
	if p.Position() > 3*time.Second {
		p.Seek(0)
		return
	}
	p.PrevTrack()
}

// PrevTrack plays the track before this one, however far this one has
// played, as flicking the cover back does.
func (p *Player) PrevTrack() {
	if p.control("skip_prev_track", nil) {
		return
	}
	if len(p.order) == 0 {
		return
	}
	at := p.at - 1
	if at < 0 {
		at = len(p.order) - 1
	}
	p.load(at)
}

// Jump plays the queue's track i.
func (p *Player) Jump(i int) {
	if i >= 0 && i < len(p.queue) && (p.control("jump", i) || p.control("play", QueueRequest{Tracks: p.queue, Start: i, ContextURI: p.contextURI})) {
		return
	}
	for k, v := range p.order {
		if v == i {
			p.load(k)
			return
		}
	}
}

// TogglePlay pauses, fading out, or plays, fading in.
func (p *Player) TogglePlay() {
	if p.control("toggle", nil) {
		return
	}
	if p.out == nil {
		switch {
		case p.loading || p.waiting || p.awaiting:
			// It plays once loaded, or not.
			p.playing = !p.playing
			p.sync()
		case len(p.order) > 0:
			p.networkRetries = 0
			p.loadSkipping(max(p.at, 0), 0, true, p.pending)
		}
		return
	}
	if f, ok := p.out.(interface {
		Fault() (string, int, time.Duration)
	}); ok {
		if msg, _, _ := f.Fault(); msg != "" && !p.playing && len(p.order) > 0 {
			from := p.Position()
			p.networkRetries = 0
			p.loadSkipping(min(max(p.at, 0), len(p.order)-1), 0, true, from)
			return
		}
	}
	out := p.out
	if p.playing {
		p.playing = false
		p.fade.fade(0, fadeOut, func() {
			p.post(func() {
				if p.out == out && !p.playing {
					out.Pause()
					p.sync()
				}
			})
		})
		p.sync()
		return
	}
	p.playing = true
	if !p.seeking && !out.Playing() {
		if d := out.Duration(); d > time.Second && out.Position() >= d-time.Second {
			out.Seek(0)
		}
		out.Play()
	}
	p.fade.fade(1, fadeIn, nil)
	p.sync()
}

// seekGap is how long a seek waits for what plays to fade out, which the
// audio player does over 5 ms, as it pauses: within the next buffer or
// two it sends the system.
const seekGap = 30 * time.Millisecond

// Seek moves to t. Playing, it pauses and plays again around the jump,
// which the audio player fades over a few milliseconds, rather than
// cutting from one sound to the other with a click.
func (p *Player) Seek(t time.Duration) {
	if p.control("seek", t) {
		return
	}
	if p.out == nil {
		p.pending = t
		return
	}
	out := p.out
	// Paused, it jumps at once; but a seek on its way paused it only for
	// the jump, and this one takes its place, playing on from there.
	if !out.Playing() && !p.seeking {
		out.Seek(t)
		p.sync()
		return
	}
	p.seekGen++
	gen := p.seekGen
	p.seeking, p.seekTo = true, t
	out.Pause()
	time.AfterFunc(seekGap, func() {
		p.post(func() {
			if gen != p.seekGen || p.out != out {
				return
			}
			p.seeking = false
			out.Seek(t)
			if p.playing || !p.fade.silent() {
				out.Play()
			}
			p.sync()
		})
	})
	p.sync()
}

func (p *Player) SetVolume(v float64) {
	if p.control("volume", v) {
		return
	}
	p.volume = v
	p.applyGain()
}

// applyGain sets the volume of what plays: the listener's, which the ear
// hears in powers.
func (p *Player) applyGain() {
	p.fade.setGain(p.volume * p.volume)
}

func (p *Player) Position() time.Duration {
	switch {
	case p.out == nil:
		return p.pending
	case p.seeking:
		return p.seekTo
	}
	return p.out.Position()
}

// SongPosition is where the song is: the position, or as its music video
// plays, the moment of the song the video is at, which its lyrics follow.
func (p *Player) SongPosition() time.Duration {
	pos := p.Position()
	if p.showsVideo {
		pos = max(pos-p.VideoLead(), 0)
		if p.track != nil && p.track.Length() > 0 {
			pos = min(pos, p.track.Length())
		}
	}
	return pos
}

// VideoLead is how far the music video playing runs ahead of the song:
// what is sung at the song's t is at the video's t+VideoLead. It is the
// listener's, where they set it (Leads), else a guess (leadGuess).
func (p *Player) VideoLead() time.Duration {
	if !p.showsVideo {
		return 0
	}
	return p.leadFor(nil)
}

// leadFor is VideoLead for the current track with its video v, or the one
// shown when v is nil.
func (p *Player) leadFor(v *spotify.Video) time.Duration {
	if p.track != nil && p.Leads != nil {
		if d, ok := p.Leads(p.track.ID); ok {
			return d
		}
	}
	if v != nil {
		return leadGuess(p.track, v)
	}
	return p.lead
}

// maxLeadGuess is the most a music video runs longer than its song by to
// be taken for a few seconds before it and after: longer ones may as well
// have scenes of their own.
const maxLeadGuess = 10 * time.Second

// leadGuess is how far v, the music video of tr, likely runs ahead of it:
// Spotify tells nothing of it, nor do the lyrics, which follow the song.
// What a video a few seconds longer than its song has more may come before
// the song or after it: taken for half before, the lyrics are at most half
// of it off, wherever it is. Else the two are taken to start together.
func leadGuess(tr *spotify.Track, v *spotify.Video) time.Duration {
	// A video podcast's episode is its video: the two keep the same time.
	if tr == nil || v == nil || tr.Episode || tr.Length() <= 0 {
		return 0
	}
	extra := v.Duration - tr.Length()
	if extra <= 0 || extra > maxLeadGuess {
		return 0
	}
	return extra / 2
}

func (p *Player) Duration() time.Duration {
	if p.track == nil {
		return 0
	}
	if p.out != nil && (p.showsVideo || p.track.Local() || p.media != nil && p.media.File != "") {
		if d := p.out.Duration(); d > 0 || p.showsVideo || p.track.Local() {
			// The video's or file's own: it can run longer than metadata, or shorter.
			return d
		}
	}
	return p.track.Length()
}

// Levels returns the spectrum of what plays now. The page decrypts and
// plays the audio out of reach, so it is a stand-in, as Spotify's own apps
// animate theirs: waves across the bands, neighbors rising together,
// moving with the position, and still while paused.
func (p *Player) Levels() ([Bands]float32, bool) {
	var lv [Bands]float32
	if p.out == nil || !p.playing {
		return lv, false
	}
	t := p.Position().Seconds()
	for i := range lv {
		f := float64(i) / (Bands - 1) // from the lowest band to the highest
		v := 0.55 - 0.25*f
		v += 0.28 * math.Sin(2*math.Pi*(1.7*t+1.3*f))
		v += 0.16 * math.Sin(2*math.Pi*(2.9*t-0.7*f)+1)
		v += 0.08 * math.Sin(2*math.Pi*(5.3*t+3.1*f)+2)
		lv[i] = float32(min(1, max(0.05, v)))
	}
	return lv, true
}

func (p *Player) updateIdle() {
	if p.Web == nil || p.playing || p.loading {
		if p.idle != nil {
			p.idle.Stop()
			p.idle = nil
		}
		p.idleGen++
		return
	}
	if p.idle != nil {
		return
	}
	p.idleGen++
	gen := p.idleGen
	p.idle = time.AfterFunc(5*time.Minute, func() {
		p.post(func() { p.releaseIdle(gen) })
	})
}

func (p *Player) releaseIdle(gen int) {
	if gen != p.idleGen || p.playing || p.loading || p.Web == nil {
		return
	}
	p.pending = p.Position()
	p.CloseWeb()
	p.idleNative = true
	p.sync()
}

func (p *Player) CloseWeb() {
	p.idleNative = false
	if p.idle != nil {
		p.idle.Stop()
		p.idle = nil
	}
	p.idleGen++
	p.stop()
	if p.Web != nil {
		p.Web.Close()
		p.Web = nil
	}
	if p.Video != nil {
		p.Video.Close()
		p.Video = nil
	}
}

// Clear stops playback and drops the current queue, track, radio, and lyrics
// (used when signing out of a Spotify account).
func (p *Player) Clear() {
	p.gen++
	p.stop()
	p.upNext.id, p.upNext.m, p.upNext.at = "", nil, time.Time{}
	p.ahead.id, p.ahead.lines, p.ahead.loaded = "", nil, false
	p.cover = nil
	p.endRadio()
	p.smartGen++
	p.smartFeeding = false
	p.smartAdded = nil
	p.awaiting = false
	p.loading = false
	p.playing = false
	p.waiting = false
	p.err = ""
	p.pending = 0
	p.queue = nil
	p.order = nil
	p.at = -1
	p.track = nil
	p.media = nil
	p.mediaBitrate, p.mediaFormat, p.mediaFileID = 0, 0, ""
	p.lyrics = nil
	p.lyricsLoaded = false
	p.lyricsOverride = false
	p.sync()
}

func (p *Player) stop() {
	if p.upNext.timer != nil {
		p.upNext.timer.Stop()
		p.upNext.timer = nil
		if p.upNext.m == nil {
			p.upNext.id = ""
		}
	}
	if p.offEnd != nil {
		p.offEnd()
		p.offEnd = nil
	}
	if p.offSystem != nil {
		p.offSystem()
		p.offSystem = nil
	}
	if out := p.out; out != nil {
		// Paused first, it fades out over a few milliseconds, rather than
		// stopping with a click.
		out.Pause()
		time.AfterFunc(100*time.Millisecond, out.Close)
	}
	p.out = nil
	p.showsVideo = false
	p.seeking = false
	p.fade.attach(nil, 1)
}

// isPlayableLocalFile reports whether info is a readable regular file,
// including hydrated Windows Cloud Filter (OneDrive) files whose reparse point
// causes Go's Mode().IsRegular() to return false.
func isPlayableLocalFile(info fs.FileInfo) bool {
	if info.IsDir() {
		return false
	}
	const nonFile = fs.ModeDir | fs.ModeSymlink | fs.ModeNamedPipe | fs.ModeSocket | fs.ModeDevice | fs.ModeCharDevice
	return info.Mode()&nonFile == 0
}

// load plays the track at order[at], skipping those that cannot play.
func (p *Player) load(at int) {
	p.networkRetries = 0
	p.loadSkipping(at, 0, true, 0)
}

// loadSkipping loads the track at order[at], to play or not, from from,
// skipping those that cannot play: skipped of them so far.
func (p *Player) loadSkipping(at, skipped int, play bool, from time.Duration) {
	gen, t := p.cue(at, play, from)
	p.loading, p.skipped = true, skipped
	p.updateIdle()
	resolveMedia := resolve
	if t.NetEase() {
		resolveNetEase := p.ResolveNetEase
		resolveMedia = func(string) (*spotify.Media, error) {
			if resolveNetEase == nil {
				return nil, errors.New("网易云音源解析器未初始化")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			return resolveNetEase(ctx, t)
		}
	} else if t.Cloud() {
		resolveMedia = func(string) (*spotify.Media, error) {
			if t.CloudURL == "" {
				return nil, errors.New("云盘歌曲地址无效")
			}
			if cached := localmusic.CachedCloudAudio(t.ID); cached != "" {
				return &spotify.Media{LocalPath: cached, Duration: t.Length()}, nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			streamURL, headers, err := localmusic.ResolveCloudMedia(ctx, t.CloudURL)
			if err != nil {
				return nil, err
			}
			localmusic.CacheCloudAudioAsync(t, streamURL, headers)
			return &spotify.Media{
				File:     streamURL,
				Headers:  headers,
				Duration: t.Length(),
			}, nil
		}
	} else if t.Local() {
		resolveMedia = func(string) (*spotify.Media, error) {
			file, err := os.Open(t.LocalPath)
			if err != nil {
				return nil, err
			}
			defer file.Close()
			info, err := file.Stat()
			if err != nil {
				return nil, err
			}
			if !isPlayableLocalFile(info) {
				return nil, errors.New("本地歌曲文件不存在")
			}
			var probe [1]byte
			if _, err := file.ReadAt(probe[:], 0); err != nil {
				return nil, err
			}
			return &spotify.Media{LocalPath: t.LocalPath, Duration: t.Length()}, nil
		}
	}
	if m := p.takeAhead(t.ID); m != nil {
		resolveMedia = func(string) (*spotify.Media, error) { return m, nil }
	}
	video, findVideo := p.video, resolveVideo
	go func() {
		m, err := resolveMedia(t.URI())
		var v *spotify.Video
		if err == nil && video && m.Video != "" {
			v = videoOf(m, findVideo)
		}
		p.post(func() {
			if gen != p.gen {
				return
			}
			p.loading = false
			var webErr error
			if err == nil && p.Web == nil && p.NewWeb != nil {
				p.Web, webErr = p.NewWeb()
			}
			if err == nil && webErr == nil && p.Web == nil {
				webErr = errNoWeb
			}
			if webErr != nil {
				log.Printf("player: %s: %v", t.URI(), webErr)
				p.err, p.playing = "播放器初始化失败，请重试", false
				p.sync()
				return
			}
			if err != nil {
				log.Printf("player: %s: %v", t.URI(), err)
				var thr *spotify.Throttled
				if errors.As(err, &thr) {
					p.throttled(thr.Wait)
					return
				}
				if t.Local() && !t.Cloud() && !t.NetEase() {
					p.err, p.playing = "本地文件无法读取，请下载到本机后重新扫描", false
					p.sync()
					return
				}
				if t.Cloud() || t.NetEase() {
					p.unlessOffline(netFault(err), func() {
						if len(p.order) == 0 {
							p.playing = false
							p.sync()
							return
						}
						curAt := min(max(p.at, 0), len(p.order)-1)
						if t.NetEase() {
							p.err = "网易云歌曲暂时无法播放"
						} else {
							p.err = "云盘歌曲暂时无法播放"
						}
						p.skip(curAt, skipped)
					})
					return
				}
				if !errors.Is(err, spotify.ErrNoAudioHere) {
					p.unlessOffline(netFault(err), func() {
						if len(p.order) == 0 {
							p.playing = false
							p.sync()
							return
						}
						curAt := min(max(p.at, 0), len(p.order)-1)
						p.err = "这首歌暂时无法播放"
						p.skip(curAt, skipped)
					})
					return
				}
				p.err = "这一集在这台电脑上放不了"
				// Its podcast's other episodes are kept as it is, and
				// would not play either: they are not asked for one
				// after another, as the songs after one that fails are.
				if n := len(p.order); n > 1 {
					curAt := min(max(p.at, 0), n-1)
					if after := p.queue[p.order[(curAt+1)%n]]; after.Episode && after.Album.ID == t.Album.ID {
						p.playing = false
						p.sync()
						return
					}
					p.skip(curAt, skipped)
					return
				}
				if len(p.order) > 0 {
					p.skip(min(max(p.at, 0), len(p.order)-1), skipped)
				} else {
					p.playing = false
					p.sync()
				}
				return
			}
			p.offline = 0
			p.media = m
			if m != nil {
				p.mediaBitrate, p.mediaFormat, p.mediaFileID = m.Bitrate, m.Format, m.FileID
			} else {
				p.mediaBitrate, p.mediaFormat, p.mediaFileID = 0, 0, ""
			}
			if !p.video {
				v = nil
			}
			p.open(gen, m, v)
			if p.video && !video && m.Video != "" && p.out != nil {
				p.reopen()
			}
			p.mediaAhead()
		})
	}()
}

// open plays the current track, of load gen, from p.pending: its music
// video v, if there is one to play and somewhere to play it, or else its
// audio m.
func (p *Player) open(gen int, m *spotify.Media, v *spotify.Video) {
	p.idleNative = false
	if v != nil && p.Video == nil && p.NewVideo != nil {
		var err error
		if p.Video, err = p.NewVideo(); err != nil {
			log.Printf("player: video: %v", err)
		}
	}
	p.lead = 0
	if v != nil && p.Video != nil {
		p.out, p.showsVideo = p.Video.OpenVideo(v), true
		p.lead = leadGuess(p.track, v)
	} else {
		if p.Web == nil && p.NewWeb != nil {
			var err error
			if p.Web, err = p.NewWeb(); err != nil {
				log.Printf("player: %v", err)
			}
		}
		if p.Web == nil {
			p.err, p.playing = "这首歌暂时无法播放", false
			p.sync()
			return
		}
		p.out = p.Web.Open(m)
	}
	// From its middle, as a session picked up again, it fades in.
	level := 1.0
	if p.pending > 0 {
		level = 0
	}
	out := p.out
	p.fade.attach(out, level)
	p.applyGain()
	p.offEnd = out.OnEnd(func() {
		p.post(func() {
			if gen == p.gen && p.out == out {
				p.ended()
			}
		})
	})
	if sysOut, ok := out.(systemOutput); ok {
		p.offSystem = sysOut.OnSystem(func(cmd string, at time.Duration) {
			p.post(func() {
				if gen == p.gen && p.out == out {
					p.system(cmd, at)
				}
			})
		})
	}
	if p.pending > 0 {
		// Not past the end, as a video shorter than the song would be.
		to := p.pending
		if d := p.out.Duration(); d > time.Second {
			to = min(to, d-time.Second)
		}
		p.out.Seek(to)
		p.pending = 0
	}
	if p.playing {
		p.out.Play()
		p.fade.fade(1, fadeIn, nil)
	}
	p.sync()
}

// videoOf resolves m's music video with find, nil if it can't be: the song
// plays. find is resolveVideo, read before the goroutine videoOf runs on,
// as tests put theirs back.
func videoOf(m *spotify.Media, find func(string) (*spotify.Video, error)) *spotify.Video {
	v, err := find(m.Video)
	if err != nil {
		log.Printf("player: video %s: %v", m.Video, err)
	}
	return v
}

// HasVideo reports whether the current track has a music video, as far as
// is known: once its media is resolved.
func (p *Player) HasVideo() bool { return p.media != nil && p.media.Video != "" }

// VideoOn reports whether music videos play, for the tracks that have one.
func (p *Player) VideoOn() bool { return p.video }

// ShowsVideo reports whether the current track's music video plays, in
// Video's page.
func (p *Player) ShowsVideo() bool { return p.showsVideo && p.out != nil }

// VideoNext turns music videos on for the track about to be played, its
// video playing rather than the song first; the one playing plays on as it
// does, rather than load its own video only to stop.
func (p *Player) VideoNext() {
	p.video = true
	p.sync()
}

// SetVideo plays music videos rather than the songs, or the songs again:
// the current track's from where it is, and the tracks after it.
func (p *Player) SetVideo(on bool) {
	if p.video == on {
		return
	}
	p.video = on
	p.reopen()
}

// reopen plays the current track again from where it is: its music video
// or its audio, as p.video says. Not playing or loading yet, it loads as
// that says once it plays.
func (p *Player) reopen() {
	if p.out == nil || p.media == nil || p.showsVideo == (p.video && p.media.Video != "") {
		p.sync()
		return
	}
	// Where the song is, which the song or its video goes on from.
	m, gen, from := p.media, p.gen, p.SongPosition()
	p.stop()
	p.pending = from
	if !p.video {
		p.open(gen, m, nil)
		return
	}
	p.loading = true
	find := resolveVideo
	go func() {
		v := videoOf(m, find)
		p.post(func() {
			if gen != p.gen || p.out != nil {
				return
			}
			p.loading = false
			if !p.video {
				p.open(gen, m, nil)
				return
			}
			if v == nil {
				p.video, p.err = false, "MV 暂时无法播放"
			} else if p.pending > 0 {
				p.pending = max(p.pending+p.leadFor(v), 0)
			}
			p.open(gen, m, v)
		})
	}()
	p.sync()
}

// videoFailed goes back to the song from a music video that could not
// play, from where the video was.
func (p *Player) videoFailed(msg string) {
	log.Printf("player: video failed, the song plays: %s", msg)
	p.video, p.err = false, "MV 暂时无法播放"
	p.reopen()
}

// cue makes the track at order[at] the current one, at from, to play or
// not, and loads its cover and lyrics but not its audio, which
// loadSkipping goes on to load. Its generation and track are returned.
func (p *Player) cue(at int, play bool, from time.Duration) (int, spotify.Track) {
	p.stop()
	p.idleNative = false
	p.gen++
	gen := p.gen
	p.at = at
	t := p.queue[p.order[at]]
	if p.track == nil || p.track.ID != t.ID {
		p.flaky = 0
		p.networkRetries = 0
	}
	p.track = &t
	p.loading, p.playing, p.err = false, play, ""
	p.media = nil
	p.mediaBitrate, p.mediaFormat, p.mediaFileID = 0, 0, ""
	p.awaiting, p.waiting = false, false
	p.lyrics, p.lyricsLoaded, p.lyricsOverride = nil, false, false
	p.pending = from
	p.cover = nil
	p.announce(&t, nil)
	p.refill()
	p.refillSmart()

	go func() {
		cover, err := spotify.Fetch(spotify.Sized(t.Album.PicURL, 600))
		if err == nil {
			p.post(func() {
				if gen == p.gen {
					// The track as it is now: Connect may have named its
					// artist since.
					p.cover = cover
					p.announce(p.track, cover)
				}
			})
		}
	}()

	if p.ahead.id == t.ID && p.ahead.loaded {
		p.lyrics, p.lyricsLoaded = p.ahead.lines, true
		p.lyricsAhead()
		return gen, t
	}
	if p.ahead.id == t.ID {
		return gen, t
	}
	go func() {
		lines := lyricsOf(t)
		p.post(func() {
			if gen == p.gen {
				if !p.lyricsOverride {
					if len(p.lyrics) == 0 || (len(lines) > 0 && (p.lyrics[0].Unsynced || !lines[0].Unsynced)) {
						p.lyrics = lines
					}
					p.lyricsLoaded = true
				}
				p.lyricsAhead()
			}
		})
	}()
	return gen, t
}

// lyricsAhead fetches the lyrics of the track after this one, once this
// one's are in, so they show as it begins.
func (p *Player) lyricsAhead() {
	if !p.lyricsLoaded || p.at+1 >= len(p.order) {
		return
	}
	next := p.queue[p.order[p.at+1]]
	if next.Episode || p.ahead.id == next.ID {
		return
	}
	p.ahead.id, p.ahead.lines, p.ahead.loaded = next.ID, nil, false
	go func() {
		lines := lyricsOf(next)
		p.post(func() {
			if p.ahead.id == next.ID {
				p.ahead.lines = lines
				p.ahead.loaded = true
				if p.track != nil && p.track.ID == next.ID && !p.lyricsLoaded && !p.lyricsOverride {
					p.lyrics = lines
					p.lyricsLoaded = true
					p.lyricsAhead()
				}
			}
		})
	}()
}

// aheadDelay is how long after a track opens the next one is resolved:
// once this one's start has come, so as not to slow it.
var aheadDelay = 3 * time.Second

// aheadFresh is how long media resolved ahead is played from: its CDN URLs
// are signed for a while.
const aheadFresh = 10 * time.Minute

// mediaAhead resolves the track after this one as this one starts, and has
// the web player fetch the start of its file: the next track then starts
// without waiting on the service or the CDN, only on its licence. Only a
// web player that serves its page fetches ahead.
func (p *Player) mediaAhead() {
	if p.Web == nil || p.Web.server == nil || len(p.order) < 2 {
		return
	}
	// The one Next goes to: after the last, the first, but for a radio,
	// which goes on with what its feed gives.
	at := p.at + 1
	if at >= len(p.order) {
		if p.feed != nil {
			return
		}
		at = 0
	}
	next := p.queue[p.order[at]]
	if next.Local() {
		return
	}
	if p.upNext.id == next.ID && (p.upNext.timer != nil || (p.upNext.m != nil && time.Since(p.upNext.at) <= aheadFresh)) {
		return
	}
	if p.upNext.timer != nil {
		p.upNext.timer.Stop()
		p.upNext.timer = nil
	}
	p.upNext.id, p.upNext.m = next.ID, nil
	resolveMedia, web := resolve, p.Web
	gen := p.gen
	p.upNext.timer = time.AfterFunc(aheadDelay, func() {
		m, err := resolveMedia(next.URI())
		if err != nil {
			log.Printf("player: ahead %s: %v", next.URI(), err)
			p.post(func() {
				if p.gen == gen && p.upNext.id == next.ID {
					p.upNext.id, p.upNext.timer = "", nil
				}
			})
			return
		}
		p.post(func() {
			if p.gen == gen && p.upNext.id == next.ID && p.Web == web {
				p.upNext.m, p.upNext.at, p.upNext.timer = m, time.Now(), nil
				go web.Prefetch(m)
			}
		})
	})
}

// takeAhead returns the media of track id resolved ahead, if it is still
// fresh, and forgets it.
func (p *Player) takeAhead(id string) *spotify.Media {
	m, at := p.upNext.m, p.upNext.at
	if p.upNext.id != id || m == nil || time.Since(at) > aheadFresh {
		return nil
	}
	p.upNext.id, p.upNext.m = "", nil
	return m
}

var errNoWeb = errors.New("no web player to play in")

// resolve finds where a track's audio is, and resolveVideo its music
// video; tests swap them.
var (
	resolve      = spotify.ResolveMedia
	resolveVideo = spotify.ResolveVideo
)

// Session is where playback was left, to pick up again.
type Session struct {
	Queue        []spotify.Track `json:"queue"`
	Order        []int           `json:"order"`
	At           int             `json:"at"`
	ContextURI   string          `json:"contextURI,omitempty"`
	Shuffle      bool            `json:"shuffle"`
	SmartShuffle bool            `json:"smartShuffle,omitempty"`
	SmartAdded   map[string]bool `json:"smartAdded,omitempty"`
	Repeat       RepeatMode      `json:"repeat"`
	Position     int64           `json:"position"` // milliseconds
	Radio        string          `json:"radio,omitempty"`
}

// Session returns where playback is, or false with nothing queued.
func (p *Player) Session() (Session, bool) {
	if p.track == nil || p.at < 0 {
		return Session{}, false
	}
	return Session{
		Queue:        p.queue,
		Order:        p.order,
		At:           p.at,
		ContextURI:   p.contextURI,
		Shuffle:      p.shuffle,
		SmartShuffle: p.smartShuffle,
		SmartAdded:   p.smartAdded,
		Repeat:       p.repeat,
		Position:     p.SongPosition().Milliseconds(),
		Radio:        p.radio,
	}, true
}

// Restore picks up a session where it was left, paused. Its track's audio
// loads once it plays: loading asks Spotify for a licence, and starts the
// web view's Widevine module, which takes 60 MB.
func (p *Player) Restore(s Session) {
	n := len(s.Queue)
	if n == 0 || len(s.Order) != n || s.At < 0 || s.At >= n {
		return
	}
	seen := make([]bool, n)
	for _, i := range s.Order {
		if i < 0 || i >= n || seen[i] {
			return
		}
		seen[i] = true
	}
	p.queue, p.order = s.Queue, s.Order
	p.contextURI = s.ContextURI
	p.shuffle = s.Shuffle || s.SmartShuffle
	p.smartShuffle = s.SmartShuffle
	p.smartAdded = s.SmartAdded
	p.repeat = max(s.Repeat, RepeatOff) % 3
	position := min(max(s.Position, 0), int64((1<<63-1)/time.Millisecond))
	p.cue(s.At, false, time.Duration(position)*time.Millisecond)
}
