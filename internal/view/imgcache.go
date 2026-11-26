package view

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Icons (items, skills, monsters, classes) come from Divine Pride's static
// site. With Options.ImageCache (the desktop app) the page asks this server
// instead: each icon is fetched once, the first time it is shown, kept in
// the cache folder and served from there afterwards, also offline. Without
// it (a hosted server) the page links Divine Pride directly.

const (
	dpStatic    = "https://static.divine-pride.net"
	maxImgBytes = 2 << 20
	missTTL     = 24 * time.Hour // an icon Divine Pride does not have is asked again after this
)

// dpImgPath: the only paths the cache serves (one image each, no listing).
var dpImgPath = regexp.MustCompile(`^images/(items/item|items/collection|skill|mobs/png|jobs)/[A-Za-z0-9_]{1,64}\.png$`)

type imgCache struct {
	dir    string
	origin string // dpStatic; tests point it elsewhere
	client *http.Client
	locks  sync.Map // path -> *sync.Mutex: one download per icon at a time
}

func newImgCache(dir string) *imgCache {
	return &imgCache{dir: dir, origin: dpStatic, client: &http.Client{Timeout: 15 * time.Second}}
}

// get returns the cached file of an icon, fetching it once when missing.
func (c *imgCache) get(ctx context.Context, rel string) (string, error) {
	file := filepath.Join(c.dir, filepath.FromSlash(rel))
	have := func() bool { _, err := os.Stat(file); return err == nil }
	if have() {
		return file, nil
	}
	mu, _ := c.locks.LoadOrStore(rel, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	if have() { // fetched by another request meanwhile
		return file, nil
	}
	if st, err := os.Stat(file + ".miss"); err == nil && time.Since(st.ModTime()) < missTTL {
		return "", os.ErrNotExist
	}
	if err := c.fetch(ctx, rel, file); err != nil {
		return "", err
	}
	return file, nil
}

// fetch downloads one icon into file (only PNG/image responses, size-capped).
func (c *imgCache) fetch(ctx context.Context, rel, file string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.origin+"/"+rel, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "ragnarok-of-building")
	res, err := c.client.Do(req)
	if err != nil {
		return err // offline: try again next time
	}
	defer res.Body.Close()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	if res.StatusCode == http.StatusNotFound {
		os.WriteFile(file+".miss", nil, 0o644)
		return os.ErrNotExist
	}
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "image/") {
		return os.ErrNotExist
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, maxImgBytes+1))
	if err != nil || len(b) > maxImgBytes || len(b) == 0 {
		return os.ErrNotExist
	}
	tmp := file + ".part"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

// IconCache is the icon cache for the native app: the same folder and
// rules as the web app's /img/dp.
type IconCache struct{ c *imgCache }

// NewIconCache keeps icons in dir.
func NewIconCache(dir string) *IconCache { return &IconCache{newImgCache(dir)} }

// Get returns the PNG of an icon path (ItemIcon, SkillIcon...),
// downloading it once when missing.
func (ic *IconCache) Get(ctx context.Context, rel string) ([]byte, error) {
	if !dpImgPath.MatchString(rel) {
		return nil, os.ErrNotExist
	}
	file, err := ic.c.get(ctx, rel)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(file)
}

// Icon paths, as Divine Pride's static site has them.
func ItemIcon(id int) string { return fmt.Sprintf("images/items/item/%d.png", id) }

// ItemArt is the item window's picture (75x100, on white).
func ItemArt(id int) string { return fmt.Sprintf("images/items/collection/%d.png", id) }

func SkillIcon(id int) string   { return fmt.Sprintf("images/skill/%d.png", id) }
func MonsterIcon(id int) string { return fmt.Sprintf("images/mobs/png/%d.png", id) }
func JobIcon(id int) string     { return fmt.Sprintf("images/jobs/icon_jobs_%d.png", id) }
