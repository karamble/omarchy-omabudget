package feed

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The suite never leaves this machine: every server is an httptest one, and
// the only client that reaches further is trapped.

// trusting is the fetch client, trusting the test server's certificate.
func trusting(t *testing.T, srv *httptest.Server) *http.Client {
	t.Helper()
	c := newClient()
	if tr, ok := srv.Client().Transport.(*http.Transport); ok && tr.TLSClientConfig != nil {
		c.Transport.(*http.Transport).TLSClientConfig.RootCAs = tr.TLSClientConfig.RootCAs
	}
	return c
}

// trap fails the test if anything is dialled.
type trap struct{ t *testing.T }

func (tr trap) RoundTrip(req *http.Request) (*http.Response, error) {
	tr.t.Errorf("a request left for %s", req.URL)
	return nil, errors.New("trapped")
}

func serveFile(t *testing.T, name string) http.HandlerFunc {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return func(w http.ResponseWriter, r *http.Request) { w.Write(body) }
}

func pinClock(t *testing.T, day string) {
	t.Helper()
	prev := now
	d, err := time.Parse("2006-01-02", day)
	if err != nil {
		t.Fatal(err)
	}
	now = func() time.Time { return d }
	t.Cleanup(func() { now = prev })
}

func TestFetchReadsEachSource(t *testing.T) {
	pinClock(t, "2026-09-20")
	for _, c := range []struct {
		src  Source
		file string
	}{{ECB, "ecb-daily.xml"}, {Frankfurter, "frankfurter.json"}} {
		srv := httptest.NewTLSServer(serveFile(t, c.file))
		defer srv.Close()
		q, err := fetchWith(context.Background(), trusting(t, srv), c.src.At(srv.URL+"/latest"))
		if err != nil {
			t.Fatalf("%s: %v", c.src.ID, err)
		}
		if q.Base != "EUR" || q.Date != "2026-09-18" || len(q.Rates) != 29 || !q.Rates["USD"].Equal("1.146") {
			t.Errorf("%s: %s %s, %d rates, usd %s", c.src.ID, q.Base, q.Date, len(q.Rates), q.Rates["USD"])
		}
	}
}

func TestFetchRefusesBeforeDialling(t *testing.T) {
	c := &http.Client{Transport: trap{t}}
	for _, raw := range []string{
		"http://example.invalid/latest",
		"http://203.0.113.9/latest",
		"ftp://example.invalid/latest",
		"file:///etc/hosts",
		"://",
	} {
		if _, err := fetchWith(context.Background(), c, ECB.At(raw)); err == nil {
			t.Errorf("%s was fetched", raw)
		}
	}
}

func TestPlainHTTPOnlyToPrivateHosts(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://example.invalid/":        true,
		"http://localhost:8080/latest":    true,
		"http://127.0.0.1:8080/latest":    true,
		"http://[::1]:8080/latest":        true,
		"http://10.0.0.5/latest":          true,
		"http://192.168.1.20:8080/latest": true,
		"http://172.16.0.1/latest":        true,
		"http://[fd00::1]/latest":         true,
		"http://example.invalid/latest":   false,
		"http://8.8.8.8/latest":           false,
		"http://frankfurter.lan/latest":   false,
		"https://":                        true,
		"gopher://localhost/":             false,
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := allowed(u) == nil; got != ok {
			t.Errorf("%s allowed = %v, want %v", raw, got, ok)
		}
	}
	// A plain server on this machine is fetched.
	pinClock(t, "2026-09-20")
	srv := httptest.NewServer(serveFile(t, "frankfurter.json"))
	defer srv.Close()
	if _, err := fetchWith(context.Background(), newClient(), Frankfurter.At(srv.URL+"/v1/latest")); err != nil {
		t.Errorf("a loopback http instance was refused: %v", err)
	}
}

func TestRedirects(t *testing.T) {
	pinClock(t, "2026-09-20")
	elsewhere := httptest.NewTLSServer(serveFile(t, "ecb-daily.xml"))
	defer elsewhere.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/moved", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/daily", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/daily", serveFile(t, "ecb-daily.xml"))
	mux.HandleFunc("/away", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, elsewhere.URL+"/", http.StatusFound) })
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/loop", http.StatusFound) })
	mux.HandleFunc("/down", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.invalid/", http.StatusFound)
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	c := trusting(t, srv)

	if _, err := fetchWith(context.Background(), c, ECB.At(srv.URL+"/moved")); err != nil {
		t.Errorf("a redirect within the host failed: %v", err)
	}
	for _, path := range []string{"/away", "/loop", "/down"} {
		if _, err := fetchWith(context.Background(), c, ECB.At(srv.URL+path)); err == nil {
			t.Errorf("%s was followed", path)
		}
	}
}

func TestFetchRefusesBadResponses(t *testing.T) {
	pinClock(t, "2026-09-20")
	mux := http.NewServeMux()
	mux.HandleFunc("/down", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "later", http.StatusServiceUnavailable) })
	mux.HandleFunc("/huge", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(strings.Repeat("x", maxBody+1))) })
	mux.HandleFunc("/html", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html>301</html>")) })
	mux.HandleFunc("/future", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"amount":1,"base":"EUR","date":"2026-09-22","rates":{"USD":1.1}}`))
	})
	mux.HandleFunc("/tomorrow", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"amount":1,"base":"EUR","date":"2026-09-21","rates":{"USD":1.1}}`))
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	c := trusting(t, srv)
	for _, path := range []string{"/down", "/huge", "/html", "/future"} {
		_, err := fetchWith(context.Background(), c, Frankfurter.At(srv.URL+path))
		if err == nil {
			t.Errorf("%s was accepted", path)
			continue
		}
		if !strings.Contains(err.Error(), "Frankfurter") {
			t.Errorf("%s: the error does not name the source: %v", path, err)
		}
	}
	if _, err := fetchWith(context.Background(), c, Frankfurter.At(srv.URL+"/tomorrow")); err != nil {
		t.Errorf("a day of clock skew was refused: %v", err)
	}
}

func TestClientPolicy(t *testing.T) {
	c := newClient()
	if c.Timeout != 10*time.Second {
		t.Errorf("timeout %s", c.Timeout)
	}
	tr := c.Transport.(*http.Transport)
	if tr.Proxy == nil {
		t.Error("the environment's proxy is ignored")
	}
	if tr.TLSClientConfig == nil || tr.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Errorf("tls config %+v", tr.TLSClientConfig)
	}
	// A server that only speaks TLS 1.1 is refused.
	srv := httptest.NewUnstartedServer(serveFile(t, "ecb-daily.xml"))
	srv.TLS = &tls.Config{MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS11}
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	defer srv.Close()
	if _, err := fetchWith(context.Background(), trusting(t, srv), ECB.At(srv.URL)); err == nil {
		t.Error("a tls 1.1 server was read")
	}
}

func TestFetchHonoursContext(t *testing.T) {
	srv := httptest.NewTLSServer(func() http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }
	}())
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := fetchWith(ctx, trusting(t, srv), ECB.At(srv.URL)); err == nil {
		t.Error("a hung server did not time out")
	}
}
