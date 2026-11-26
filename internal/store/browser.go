//go:build js

package store

import (
	"errors"
	"fmt"
	"syscall/js"
)

// Browser keeps each key in the page's localStorage, under "rob:<key>".
// The data stays in this browser only (another browser or a cleared site
// starts empty).
func Browser() Store { return browserStore{} }

type browserStore struct{}

const browserPrefix = "rob:"

var errMissing = errors.New("store: no such key")

// storage is localStorage, or an error where the browser blocks it (some
// private windows).
func storage() (v js.Value, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("store: localStorage: %v", r)
		}
	}()
	v = js.Global().Get("localStorage")
	if !v.Truthy() {
		return v, errors.New("store: no localStorage")
	}
	return v, nil
}

func (browserStore) Read(key string) (b []byte, err error) {
	ls, err := storage()
	if err != nil {
		return nil, err
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("store: %v", r)
		}
	}()
	v := ls.Call("getItem", browserPrefix+key)
	if v.IsNull() || v.IsUndefined() {
		return nil, errMissing
	}
	return []byte(v.String()), nil
}

// Write stores b as text (the program keeps JSON only). A full storage
// (about 5 MB per site) comes back as an error.
func (browserStore) Write(key string, b []byte) (err error) {
	ls, err := storage()
	if err != nil {
		return err
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("store: %v", r)
		}
	}()
	ls.Call("setItem", browserPrefix+key, string(b))
	return nil
}
