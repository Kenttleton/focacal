// Package httpclient provides a shared HTTP client with retry logic for
// all outbound API calls in focacal connectors.
package httpclient

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const (
	maxAttempts  = 3
	initialDelay = 2 * time.Second
)

// DoWithRetry executes req using client and retries on 429 (Too Many Requests)
// and 503 (Service Unavailable) with exponential back-off starting at 2 s.
// If the response includes a Retry-After header, that value overrides the
// computed delay.  The returned body is always fully read and the response
// body closed, even on error.  Network errors are retried up to maxAttempts.
func DoWithRetry(ctx context.Context, client *http.Client, req *http.Request) (body []byte, statusCode int, err error) {
	delay := initialDelay

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, 0, ctx.Err()
			case <-time.After(delay):
			}
			delay *= 2
		}

		// For subsequent attempts, re-create the body from GetBody so that
		// bytes.NewReader-backed requests are rewindable.
		r := req
		if attempt > 0 && req.GetBody != nil {
			newBody, bodyErr := req.GetBody()
			if bodyErr != nil {
				return nil, 0, fmt.Errorf("rebuild request body: %w", bodyErr)
			}
			r = req.Clone(ctx)
			r.Body = newBody
		}

		resp, doErr := client.Do(r)
		if doErr != nil {
			// Network error — retry unless this is the last attempt.
			if attempt == maxAttempts-1 {
				return nil, 0, doErr
			}
			continue
		}

		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		statusCode = resp.StatusCode
		body = b

		if statusCode != http.StatusTooManyRequests && statusCode != http.StatusServiceUnavailable {
			// Non-retriable status — return immediately and let the caller decide.
			return body, statusCode, nil
		}

		if attempt == maxAttempts-1 {
			// Exhausted all retries on a rate-limit or server error.
			return body, statusCode, fmt.Errorf("request failed with status %d after %d attempts", statusCode, maxAttempts)
		}

		// Honour the Retry-After header if present (value in seconds).
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if secs, parseErr := strconv.Atoi(ra); parseErr == nil && secs > 0 {
				delay = time.Duration(secs) * time.Second
			}
		}
	}

	return body, statusCode, nil
}
