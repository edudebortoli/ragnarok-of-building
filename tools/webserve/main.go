// webserve serves the web build (dist/web) for testing on this machine:
//
//	go run ./tools/webserve              # http://localhost:8080
//	go run ./tools/webserve -dir dist/web -addr localhost:9000
//
// It sets the WebAssembly type and turns off caching, so a rebuild shows on
// the next reload.
package main

import (
	"flag"
	"log"
	"mime"
	"net/http"
)

func main() {
	dir := flag.String("dir", "dist/web", "folder to serve")
	addr := flag.String("addr", "localhost:8080", "address to listen on")
	flag.Parse()
	mime.AddExtensionType(".wasm", "application/wasm")
	files := http.FileServer(http.Dir(*dir))
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		files.ServeHTTP(w, r)
	})
	log.Printf("serving %s on http://%s", *dir, *addr)
	log.Fatal(http.ListenAndServe(*addr, h))
}
