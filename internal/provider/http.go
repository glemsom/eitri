package provider

import (
	"context"
	"io"
	"net/http"
	"time"
)

// providerMaxRetries is the number of retry attempts after the first failed
// provider request, so a transient failure is tried up to
// providerMaxRetries+1 times in total.
var providerMaxRetries = 3

// providerRetryDelay is the pause between retry attempts.
var providerRetryDelay = 10 * time.Second

func resolveClient(c *http.Client) *http.Client {
	if c == nil {
		return http.DefaultClient
	}
	return c
}

// doWithRetry issues req, retrying transient failures before giving up. It
// retries on transport errors (connection refused/reset, timeouts, DNS) and on
// 5xx provider responses, pausing providerRetryDelay between attempts. Success
// (2xx) and client errors (4xx) are returned without retry so callers keep
// handling them. A cancelled or expired context stops retries immediately and
// returns the context error.
//
// Between attempts req.Body is replayed from req.GetBody; http.NewRequest*
// populates GetBody for the *bytes.Reader bodies this package sends, so each
// retry replays the full request.
func doWithRetry(ctx context.Context, client *http.Client, req *http.Request) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt <= providerMaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(providerRetryDelay):
			}
			if req.GetBody != nil {
				if b, err := req.GetBody(); err == nil {
					req.Body = b
				}
			}
		}
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode < 500 {
			return resp, nil
		}
		if resp != nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			if err == nil {
				err = &HTTPError{Code: resp.StatusCode, Body: string(body)}
			}
		}
		lastErr = err
	}
	return nil, lastErr
}
