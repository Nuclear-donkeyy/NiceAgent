package modelprovider

import (
	"bytes"
	"context"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type RetryPolicy struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

type FallbackPolicy struct {
	Targets []ProviderTarget
}

type ProviderTarget struct {
	Provider string
	Model    string
	BaseURL  string
}

func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts: 3,
		BaseDelay:   200 * time.Millisecond,
		MaxDelay:    2 * time.Second,
	}
}

type RetryTransport struct {
	Base     http.RoundTripper
	Policy   RetryPolicy
	Tracker  *UsageTracker
	Redactor Redactor
	Sleep    func(context.Context, time.Duration) error
}

func (t RetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	policy := normalizeRetryPolicy(t.Policy)
	body, err := snapshotRequestBody(req)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		attemptReq := cloneRequestWithBody(req, body)
		resp, err := base.RoundTrip(attemptReq)
		providerErr := t.providerError(resp, err)
		if providerErr == nil {
			return resp, nil
		}
		lastErr = providerErr
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		if !providerErr.Retryable || attempt == policy.MaxAttempts || req.Context().Err() != nil {
			return nil, providerErr
		}
		if t.Tracker != nil {
			t.Tracker.ObserveRetry()
		}
		if err := t.sleep(req.Context(), retryDelay(policy, attempt, resp)); err != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

func (t RetryTransport) providerError(resp *http.Response, err error) *ProviderError {
	if err != nil {
		classified := ClassifyProviderError(err)
		classified.Message = t.Redactor.RedactString(classified.Message)
		return classified
	}
	if resp == nil || resp.StatusCode < 400 {
		return nil
	}
	return ProviderErrorForStatus(resp.StatusCode, responseSnippet(resp), t.Redactor)
}

func (t RetryTransport) sleep(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	if t.Sleep != nil {
		return t.Sleep(ctx, duration)
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func normalizeRetryPolicy(policy RetryPolicy) RetryPolicy {
	defaults := DefaultRetryPolicy()
	if policy.MaxAttempts <= 0 {
		policy.MaxAttempts = defaults.MaxAttempts
	}
	if policy.BaseDelay <= 0 {
		policy.BaseDelay = defaults.BaseDelay
	}
	if policy.MaxDelay <= 0 {
		policy.MaxDelay = defaults.MaxDelay
	}
	return policy
}

func snapshotRequestBody(req *http.Request) ([]byte, error) {
	if req.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	return body, nil
}

func cloneRequestWithBody(req *http.Request, body []byte) *http.Request {
	next := req.Clone(req.Context())
	if body != nil {
		next.Body = io.NopCloser(bytes.NewReader(body))
		next.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
	}
	return next
}

func responseSnippet(resp *http.Response) string {
	if resp == nil || resp.Body == nil {
		return ""
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = resp.Status
	}
	return message
}

func retryDelay(policy RetryPolicy, attempt int, resp *http.Response) time.Duration {
	if resp != nil {
		if delay := retryAfterDelay(resp.Header.Get("Retry-After")); delay > 0 {
			if delay > policy.MaxDelay {
				return policy.MaxDelay
			}
			return delay
		}
	}
	power := math.Pow(2, float64(attempt-1))
	delay := time.Duration(float64(policy.BaseDelay) * power)
	if delay > policy.MaxDelay {
		return policy.MaxDelay
	}
	return delay
}

func retryAfterDelay(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		return time.Until(when)
	}
	return 0
}
