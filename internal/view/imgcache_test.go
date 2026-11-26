package view

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestIconCache(t *testing.T) {
	var hits atomic.Int32
	dp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/images/items/item/501.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write([]byte("\x89PNGfake"))
		case "/images/skill/5473.png":
			w.Header().Set("Content-Type", "text/html") // not an image
			w.Write([]byte("<html>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer dp.Close()

	dir := t.TempDir()
	ic := NewIconCache(dir)
	ic.c.origin = dp.URL
	ctx := context.Background()
	read := func(rel string) (string, error) {
		b, err := ic.Get(ctx, rel)
		return string(b), err
	}
	if b, err := read(ItemIcon(501)); err != nil || b != "\x89PNGfake" {
		t.Fatalf("first fetch: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "images", "items", "item", "501.png")); err != nil {
		t.Errorf("not cached: %v", err)
	}
	if _, err := read(SkillIcon(5473)); err == nil {
		t.Error("html kept as an icon")
	}
	n := hits.Load()
	dp.Close() // offline from here on
	if b, err := read(ItemIcon(501)); err != nil || b != "\x89PNGfake" || hits.Load() != n {
		t.Errorf("offline, cached: %q %v", b, err)
	}
	if _, err := read(ItemIcon(502)); err == nil {
		t.Error("offline, not cached: found")
	}
	for _, p := range []string{"images/../../etc/passwd", "other/x.png", "images/skill/a.svg"} {
		if _, err := ic.Get(ctx, p); err == nil {
			t.Errorf("%s accepted", p)
		}
	}
}
