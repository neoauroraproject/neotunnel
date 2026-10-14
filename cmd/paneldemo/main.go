// Command paneldemo serves the panel with sample data over plain HTTP on
// localhost, already logged in. For screenshots and UI development only.
//
//	go run ./cmd/paneldemo -addr 127.0.0.1:8099
//	open http://127.0.0.1:8099/demo/
package main

import (
	"bytes"
	"flag"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"

	"neotunnel/internal/web"
	"neotunnel/internal/web/demo"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8099", "listen address")
	bench := flag.Bool("bench", true, "start a sample benchmark so the speed page has results")
	flag.Parse()

	srv := &web.Server{Backend: demo.New(), Path: "/demo"}
	h := srv.Handler()

	// Log in once and attach that session to every request.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/demo/api/login", strings.NewReader(`{"user":"admin","password":"demo"}`)))
	cookie := rec.Result().Cookies()[0]
	if *bench {
		req := httptest.NewRequest("POST", "/demo/api/jobs", bytes.NewReader([]byte(`{"kind":"bench","target":"germany"}`)))
		req.AddCookie(cookie)
		req.Header.Set("X-NeoTunnel", "1")
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	log.Printf("demo panel: http://%s/demo/", *addr)
	log.Fatal(http.ListenAndServe(*addr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie("nt_s"); err != nil {
			r.AddCookie(cookie)
		}
		h.ServeHTTP(w, r)
	})))
}
