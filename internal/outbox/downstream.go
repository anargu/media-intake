package outbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"time"
	"uuid"
)

type DeliveryKind uint8

const (
	Delivered DeliveryKind = iota
	TransientFailure
	PermanentFailure
)

type DeliveryResult struct {
	Kind       DeliveryKind
	Diagnostic string
}

// completedAttempts includes the attempt that just failed. The worker persists
// the terminal state when a failure is no longer eligible for retry.
func (r DeliveryResult) ShouldRetry(completedAttempts, maxAttempts int) bool {
	return r.Kind == TransientFailure && completedAttempts > 0 && completedAttempts < maxAttempts
}

type DownstreamClient struct {
	endpoint string
	timeout  time.Duration
	http     *http.Client
}

func NewDownstreamClient(endpoint string, timeout time.Duration, transport http.RoundTripper) (*DownstreamClient, error) {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return nil, fmt.Errorf("invalid downstream endpoint")
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("downstream timeout must be positive")
	}
	if transport == nil {
		transport = http.DefaultTransport
	}

	return &DownstreamClient{
		endpoint: endpoint,
		timeout:  timeout,
		http: &http.Client{
			Transport:     transport,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}}, nil
}

// Deliver sends one attempt. openFrame reopens the original persisted frame
// each time; the caller resolves its path. Only metadata/framing is buffered.
func (c *DownstreamClient) Deliver(ctx context.Context, event Outbox, openFrame func() (io.ReadCloser, error)) DeliveryResult {
	if event.ID == (uuid.UUID{}) || event.CaptureID == (uuid.UUID{}) || len(event.Payload) > 64*1024 {
		return DeliveryResult{PermanentFailure, "invalid persisted event metadata"}
	}

	var payload CaptureAcceptedPayload
	err := json.Unmarshal(event.Payload, &payload)
	if err != nil || payload.Version != 1 || payload.Capture.ID != event.CaptureID.String() {
		return DeliveryResult{PermanentFailure, "invalid persisted event metadata"}
	}
	frame, err := openFrame()
	if err != nil {
		return DeliveryResult{TransientFailure, "frame unavailable"}
	}
	defer frame.Close()

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	// prepare request
	request, err := c.newRequest(ctx, event, frame)
	if err != nil {
		return encodingFailure()
	}
	response, err := c.http.Do(request)
	if err != nil {
		return DeliveryResult{TransientFailure, "downstream transport failure"}
	}

	defer response.Body.Close()
	// Response bodies and raw transport errors are not included in delivery diagnostics
	status := response.StatusCode
	switch {
	case status >= 200 && status < 300:
		return DeliveryResult{Kind: Delivered}
	case status == 408 || status == 425 || status == 429 || status >= 500:
		return DeliveryResult{TransientFailure, fmt.Sprintf("downstream HTTP %d", status)}
	default:
		return DeliveryResult{PermanentFailure, fmt.Sprintf("downstream HTTP %d", status)}
	}
}

func (c *DownstreamClient) newRequest(ctx context.Context, event Outbox, frame io.Reader) (*http.Request, error) {
	var envelope bytes.Buffer
	writer := multipart.NewWriter(&envelope)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="metadata"`)
	header.Set("Content-Type", "application/json")
	metadata, err := writer.CreatePart(header)
	if err != nil {
		return nil, err
	}
	if _, err := metadata.Write(event.Payload); err != nil {
		return nil, err
	}

	header = make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="frame"; filename="frame"`)
	header.Set("Content-Type", "application/octet-stream")
	if _, err := writer.CreatePart(header); err != nil {
		return nil, err
	}

	prefix := bytes.Clone(envelope.Bytes())
	if err := writer.Close(); err != nil {
		return nil, err
	}
	suffix := bytes.Clone(envelope.Bytes()[len(prefix):])
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, io.MultiReader(bytes.NewReader(prefix), frame, bytes.NewReader(suffix)))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Idempotency-Key", event.ID.String())
	return request, nil
}

func encodingFailure() DeliveryResult {
	return DeliveryResult{PermanentFailure, "cannot encode downstream request"}
}
