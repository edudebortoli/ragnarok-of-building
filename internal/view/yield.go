//go:build !js

package view

// yield lets the browser draw during long searches (see yield_js.go); with
// real threads there is nothing to do.
func yield() {}
