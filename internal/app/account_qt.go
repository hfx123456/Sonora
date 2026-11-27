package app

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"

	"vinyl/internal/qtweb"
	"vinyl/internal/spotify"
)

var qtCookieMu sync.Mutex
var qtAuthSequence atomic.Uint64
var errQtAuthClosed = errors.New("Qt 登录窗口已关闭")

// Migration runs once per profile. The old store remains intact; only the browser
// receives cookie values, through private stdio, preserving their scope and expiry.
func ensureQtCookies() error {
	qtCookieMu.Lock()
	defer qtCookieMu.Unlock()
	if qtweb.Migrated() {
		return nil
	}
	kept, err := mygo.Cookies.List(mygo.CookieFilter{URL: spotify.AccountURL})
	if err != nil {
		return err
	}
	values := []map[string]any{}
	signedIn := false
	for _, k := range kept {
		domain := strings.TrimPrefix(strings.ToLower(k.Domain), ".")
		if domain != "spotify.com" && !strings.HasSuffix(domain, ".spotify.com") {
			continue
		}
		if !k.Expires.IsZero() && k.Expires.Before(time.Now()) {
			continue
		}
		item := map[string]any{"name": k.Name, "value": k.Value, "domain": k.Domain, "path": k.Path, "secure": k.Secure, "httpOnly": k.HTTPOnly, "sameSite": string(k.SameSite)}
		if !k.Expires.IsZero() {
			item["expires"] = k.Expires.Unix()
		}
		values = append(values, item)
		signedIn = signedIn || k.Name == "sp_dc" && k.Value != ""
	}
	if len(values) > 0 {
		client, release, err := qtweb.Acquire()
		if err != nil {
			return err
		}
		defer release()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := client.Call(ctx, "cookies.import", map[string]any{"cookies": values}); err != nil {
			return err
		}
	}
	if err := qtweb.SetKnownLogin(signedIn); err != nil {
		return err
	}
	return qtweb.MarkMigrated()
}

func hasAccountCookies() bool {
	if qtweb.Enabled() {
		if err := ensureQtCookies(); err != nil {
			// A runtime or network failure is not a sign-out; let normal renewal retry.
			log.Printf("Qt account migration unavailable")
			return true
		}
		return qtweb.KnownLogin()
	}
	return accountCookies() != "" || loadAccountCookies() != ""
}

func qtAccountCookies(ctx context.Context) (string, error) {
	if err := ensureQtCookies(); err != nil {
		return "", err
	}
	client, release, err := qtweb.Acquire()
	if err != nil {
		return "", err
	}
	defer release()
	// Changes observed after sign-in can use the direct API; a cold store is
	// read by the browser-token path without exporting HttpOnly cookies.
	raw, err := client.Call(ctx, "cookies", map[string]any{"url": spotify.AccountURL})
	var value string
	if err == nil {
		err = json.Unmarshal(raw, &value)
	}
	return value, err
}

func closeQtAuth(client *qtweb.Client, id uint64) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := client.Call(ctx, "auth.close", map[string]any{"id": id}); err == nil {
		log.Printf("Qt identity: capture page closed")
	}
}

func captureQt(ctx context.Context, client *qtweb.Client, id uint64, signedIn bool) (spotify.Identity, error) {
	return captureIdentity(ctx, func(ctx context.Context, script string) (any, error) {
		raw, err := client.Call(ctx, "auth.eval", map[string]any{"id": id, "script": script})
		var value any
		if err != nil {
			return nil, errQtAuthClosed
		}
		err = json.Unmarshal(raw, &value)
		return value, err
	}, signedIn)
}

func (a *app) renewQtIdentity() (spotify.Identity, error) {
	return a.renewQtIdentityMode(false)
}

func (a *app) renewQtIdentityFull() (spotify.Identity, error) {
	return a.renewQtIdentityMode(true)
}

func (a *app) renewQtIdentityMode(full bool) (spotify.Identity, error) {
	if !a.hasAccountSession() {
		return spotify.Identity{}, spotify.ErrSignedOut
	}
	epoch := accountEpoch()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cancelID := a.setRenewCancel(cancel)
	defer a.clearRenewCancel(cancelID)
	captureMu.Lock()
	defer captureMu.Unlock()
	if ctx.Err() != nil || accountEpoch() != epoch || !a.hasAccountSession() {
		return spotify.Identity{}, spotify.ErrSignedOut
	}
	var (
		kept string
		err  error
	)
	if !full {
		kept, err = qtAccountCookies(ctx)
		if err != nil {
			return spotify.Identity{}, err
		}
		if kept != "" {
			if id, err := spotify.AccountIdentityWithContext(ctx, kept); err == nil {
				if accountEpoch() != epoch || !a.hasAccountSession() {
					return spotify.Identity{}, spotify.ErrSignedOut
				}
				mygo.RunOnMain(func() {
					if accountEpoch() == epoch && a.hasAccountSession() {
						a.signin.cookies = kept
					}
				})
				log.Printf("Qt identity: renewed from cookies")
				return id, nil
			} else if ctx.Err() != nil {
				return spotify.Identity{}, ctx.Err()
			}
		}
	}
	if ctx.Err() != nil || accountEpoch() != epoch || !a.hasAccountSession() {
		return spotify.Identity{}, spotify.ErrSignedOut
	}
	log.Printf("Qt identity: browser renewal (full=%v)", full)
	client, release, err := qtweb.Acquire()
	if err != nil {
		return spotify.Identity{}, err
	}
	defer release()
	id := qtAuthSequence.Add(1)
	mygo.RunOnMain(func() {
		if ctx.Err() != nil || accountEpoch() != epoch || !a.hasAccountSession() || a.quitting {
			cancel()
			return
		}
		a.signin.qtID, a.signin.qtCancel = id, cancel
	})
	defer func() {
		closeQtAuth(client, id)
		mygo.RunOnMain(func() {
			if a.signin.qtID == id {
				a.signin.qtID, a.signin.qtCancel = 0, nil
			}
		})
	}()
	if !full {
		// A tiny same-origin JSON document can use HttpOnly cookies without
		// enumerating them or starting the complete Spotify web application.
		identity, err := spotify.AccountIdentityViaBrowserWithContext(ctx, func(url string) ([]byte, error) {
			return qtTokenBody(ctx, client, id, url)
		})
		if accountEpoch() != epoch || !a.hasAccountSession() {
			return spotify.Identity{}, spotify.ErrSignedOut
		}
		if err == nil {
			qtweb.SetKnownLogin(true)
			log.Printf("Qt identity: renewed through browser token API")
			return identity, nil
		}
		if ctx.Err() != nil {
			return spotify.Identity{}, ctx.Err()
		}
	}
	if ctx.Err() != nil || accountEpoch() != epoch || !a.hasAccountSession() {
		return spotify.Identity{}, spotify.ErrSignedOut
	}
	log.Printf("Qt identity: full web player fallback")
	if _, err := client.Call(ctx, "auth.open", map[string]any{"id": id, "url": spotify.WebPlayerURL, "preload": spotify.CaptureJS}); err != nil {
		return spotify.Identity{}, err
	}
	identity, err := captureQt(ctx, client, id, false)
	if err != nil {
		return spotify.Identity{}, err
	}
	if accountEpoch() != epoch || !a.hasAccountSession() {
		return spotify.Identity{}, spotify.ErrSignedOut
	}
	qtweb.SetKnownLogin(!identity.Anonymous)
	if identity.Anonymous {
		a.setAccountSession(false)
	} else {
		kept, _ = qtAccountCookies(ctx)
		mygo.RunOnMain(func() {
			if accountEpoch() == epoch && a.hasAccountSession() {
				a.signin.cookies = kept
			}
		})
	}
	return identity, nil
}

func (a *app) openQtLogin() {
	a.closeSignin()
	s := &a.signin
	s.gen++
	gen, epoch := s.gen, bumpAccountEpoch()
	ctx, cancel := context.WithCancel(context.Background())
	s.qtCancel, s.shown = cancel, true
	go func() {
		defer cancel()
		defer a.post(func() {
			if gen == s.gen {
				s.qtID, s.qtCancel, s.shown = 0, nil, false
				if a.profile == nil && a.homeLoading && !a.quitting {
					a.signedIn(nil)
				}
			}
		})
		kept, accepted := func() (string, bool) {
			captureMu.Lock()
			defer captureMu.Unlock()
			if pendingCookieClear.Swap(false) {
				if err := clearQtCookiesLocked(); err != nil {
					log.Printf("sign out: %v", err)
				}
			}
			if err := ensureQtCookies(); err != nil || ctx.Err() != nil {
				a.post(func() {
					if gen == s.gen && ctx.Err() == nil {
						a.toast = "登录数据迁移未完成"
					}
				})
				return "", false
			}
			client, release, err := qtweb.Acquire()
			if err != nil {
				a.post(func() {
					if gen == s.gen {
						a.toast = "无法启动登录组件"
					}
				})
				return "", false
			}
			defer release()
			id := qtAuthSequence.Add(1)
			mygo.RunOnMain(func() {
				if gen != s.gen || a.quitting {
					cancel()
					return
				}
				s.qtID = id
			})
			defer closeQtAuth(client, id)
			if _, err := client.Call(ctx, "auth.open", map[string]any{"id": id, "url": spotify.LoginURL, "preload": spotify.CaptureJS, "show": true}); err != nil {
				return "", false
			}
			identity, err := captureQt(ctx, client, id, true)
			if err != nil || accountEpoch() != epoch {
				return "", false
			}
			kept, _ := qtAccountCookies(ctx)
			if ctx.Err() != nil || accountEpoch() != epoch {
				return "", false
			}
			ok := false
			mygo.RunOnMain(func() {
				if gen == s.gen && !a.quitting && accountEpoch() == epoch && ctx.Err() == nil {
					a.setAccountSession(true)
					spotify.SetIdentity(identity)
					ok = true
				}
			})
			return kept, ok
		}()
		if !accepted {
			return
		}
		// captureMu is released before spotify.Account so any 401 renewal
		// cannot deadlock on captureMu.
		prof, err := spotify.Account()
		a.post(func() {
			if gen != s.gen || a.quitting || accountEpoch() != epoch {
				return
			}
			a.closeSignin()
			if err != nil {
				a.toast = "登录失败，请稍后再试"
				return
			}
			qtweb.SetKnownLogin(true)
			a.signin.cookies = kept
			a.signedIn(prof)
		})
	}()
}

func clearQtAccountPending() error {
	captureMu.Lock()
	defer captureMu.Unlock()
	if !pendingCookieClear.Swap(false) {
		return nil
	}
	return clearQtCookiesLocked()
}

func clearQtCookiesLocked() error {
	qtCookieMu.Lock()
	defer qtCookieMu.Unlock()
	if err := qtweb.SetKnownLogin(false); err != nil {
		return err
	}
	// Explicit logout must not import the old native store on the next start.
	if err := qtweb.MarkMigrated(); err != nil {
		return err
	}
	client, release, err := qtweb.Acquire()
	if err != nil {
		return err
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	_, err = client.Call(ctx, "cookies.clear", nil)
	if err != nil {
		return errors.New("Qt 登录数据清理未完成")
	}
	return nil
}
