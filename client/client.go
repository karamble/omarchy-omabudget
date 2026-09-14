// Package client talks to the daemon over loopback with the bearer token from
// the config, on behalf of the CLI and the panel.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/karamble/omarchy-omabudget/config"
)

type Client struct {
	Addr  string
	Token string
	HTTP  *http.Client
}

func New(addr, configPath string) (*Client, error) {
	cfg, err := config.Load(configPath)
	if errors.Is(err, config.ErrNotConfigured) {
		return nil, fmt.Errorf("the daemon has never run: start it from the panel")
	}
	if err != nil {
		return nil, err
	}
	return &Client{
		Addr:  addr,
		Token: cfg.APIToken,
		HTTP: &http.Client{
			Timeout: 30 * time.Second,
			// Loopback is exempt from proxying anyway; stating it keeps the
			// property from depending on that.
			Transport: &http.Transport{Proxy: nil},
		},
	}, nil
}

func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}

	url := "http://" + strings.TrimPrefix(c.Addr, "http://") + path
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("the daemon is not answering on %s: %w", c.Addr, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			return errors.New(e.Error)
		}
		return fmt.Errorf("%s %s: %s", method, path, resp.Status)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func PrintJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
