package feed

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"time"
)

// This is the one file in the module that opens a connection outward. It
// is called from the rate fetch and nowhere else, and never on its own.

const (
	// The panel gives a helper fifteen seconds, so the fetch gives up first.
	timeout = 10 * time.Second
	// A daily quote is a few kilobytes; anything past this is not one.
	maxBody = 1 << 20
	maxHops = 3
)

// now is the clock a quote's date is checked against.
var now = time.Now

// Fetch reads the source's current quote.
func Fetch(ctx context.Context, src Source) (Quote, error) {
	return fetchWith(ctx, newClient(), src)
}

// Download reads the source's response as it came, for keeping as testdata.
func Download(ctx context.Context, src Source) ([]byte, error) {
	return downloadWith(ctx, newClient(), src)
}

// fetchWith is Fetch through a given client, so a test can hand it one that
// trusts a local server.
func fetchWith(ctx context.Context, c *http.Client, src Source) (Quote, error) {
	body, err := downloadWith(ctx, c, src)
	if err != nil {
		return Quote{}, err
	}
	q, err := src.Parse(bytes.NewReader(body))
	if err != nil {
		return Quote{}, err
	}
	// A day of slack for the source's time zone against this machine's.
	if latest := now().UTC().AddDate(0, 0, 1).Format("2006-01-02"); q.Date > latest {
		return Quote{}, fmt.Errorf("%s: published %s, which is in the future", src.Name, q.Date)
	}
	return q, nil
}

// downloadWith performs one GET and returns the body, capped.
func downloadWith(ctx context.Context, c *http.Client, src Source) ([]byte, error) {
	u, err := url.Parse(src.URL)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", src.Name, err)
	}
	if err := allowed(u); err != nil {
		return nil, fmt.Errorf("%s: %w", src.Name, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", src.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", src.Name, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", src.Name, err)
	}
	if len(body) > maxBody {
		return nil, fmt.Errorf("%s: response is larger than %d bytes", src.Name, maxBody)
	}
	return body, nil
}

// newClient is the client every fetch goes through: a deadline, TLS 1.2 or
// better, the environment's proxy, and redirects only within the host.
func newClient() *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:             http.ProxyFromEnvironment,
			TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12},
			DisableKeepAlives: true,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxHops {
				return errors.New("too many redirects")
			}
			if req.URL.Host != via[0].URL.Host {
				return fmt.Errorf("redirect to %s refused: another host", req.URL.Host)
			}
			return allowed(req.URL)
		},
	}
}

// allowed is checked before anything is dialled: https, or plain http only
// to this machine or a private address, which is where an instance run at
// home lives without a certificate.
func allowed(u *url.URL) error {
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if private(u.Hostname()) {
			return nil
		}
		return fmt.Errorf("%s is not https", u.Redacted())
	}
	return fmt.Errorf("%q is not an https url", u.Redacted())
}

// private reports a host that is this machine or on a private network, by
// name or address only: nothing is resolved before the decision.
func private(host string) bool {
	if host == "localhost" {
		return true
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast()
}
