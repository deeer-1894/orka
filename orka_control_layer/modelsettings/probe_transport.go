package modelsettings

import (
	"errors"
	"io"
	"net/http"
)

// Preserve the actual body's Close while bounding even a misbehaving provider.
type probeTransport struct{ base http.RoundTripper }

func (t *probeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(r)
	if err != nil {
		return resp, err
	}
	resp.Body = &probeBody{ReadCloser: resp.Body, remaining: maxResponseBytes}
	return resp, nil
}

type probeBody struct {
	io.ReadCloser
	remaining int
}

func (b *probeBody) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		return 0, errors.New("probe response exceeds limit")
	}
	if len(p) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.ReadCloser.Read(p)
	b.remaining -= n
	return n, err
}
