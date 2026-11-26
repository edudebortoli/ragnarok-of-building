package gamedata

import (
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
)

// Read takes the gzipped catalog and the same JSON already unzipped.
func TestReadGzipOrPlain(t *testing.T) {
	const doc = `{}`
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write([]byte(doc))
	zw.Close()
	for name, r := range map[string]*bytes.Reader{"gzip": bytes.NewReader(gz.Bytes()), "plain": bytes.NewReader([]byte(doc))} {
		if _, err := Read(r); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := Read(strings.NewReader("not json")); err == nil {
		t.Error("garbage read")
	}
}
