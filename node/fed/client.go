// Package fed is the HTTP federation backend: it implements lpnode.Sender by
// POSTing envelopes to peer inboxes (push, §5.2) and fetching peer identity
// documents (§3). A pull-only variant (polling outboxes) is a straightforward
// addition; the push path is what the two-node PoC exercises.
package fed

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/LaPingvino/liquiditypub/conformance"
)

// HTTPSender delivers envelopes over HTTP.
type HTTPSender struct {
	Client *http.Client
}

// New returns an HTTPSender with a sane default timeout.
func New() *HTTPSender {
	return &HTTPSender{Client: &http.Client{Timeout: 15 * time.Second}}
}

// Deliver POSTs one envelope to the peer's inbox (§5.2).
func (s *HTTPSender) Deliver(peerBase string, env map[string]any) error {
	body, err := json.Marshal(env)
	if err != nil {
		return err
	}
	resp, err := s.Client.Post(peerBase+"/lp/inbox", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("inbox %s: HTTP %d", peerBase, resp.StatusCode)
	}
	return nil
}

// FetchOutbox retrieves the envelopes a peer has addressed to myHost, in seq
// order (§5.1). A missing or empty outbox is not an error.
func (s *HTTPSender) FetchOutbox(peerBase, myHost string) ([]map[string]any, error) {
	resp, err := s.Client.Get(peerBase + "/lp/outbox/" + myHost + ".json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("outbox %s: HTTP %d", peerBase, resp.StatusCode)
	}
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return nil, err
	}
	if buf.Len() == 0 {
		return nil, nil
	}
	v, err := conformance.DecodeJSON(buf.Bytes())
	if err != nil {
		return nil, err
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("outbox %s: not a JSON array", peerBase)
	}
	out := make([]map[string]any, 0, len(arr))
	for _, e := range arr {
		if m, ok := e.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out, nil
}

// FetchCheckpoint retrieves a peer's signed checkpoint (§8.3), public in every
// transparency mode. The endpoint is the conventional /lp/checkpoint.json; a
// fully general client would resolve endpoints.checkpoint from the identity
// document first.
func (s *HTTPSender) FetchCheckpoint(peerBase string) (map[string]any, error) {
	resp, err := s.Client.Get(peerBase + "/lp/checkpoint.json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("checkpoint %s: HTTP %d", peerBase, resp.StatusCode)
	}
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return nil, err
	}
	v, err := conformance.DecodeJSON(buf.Bytes())
	if err != nil {
		return nil, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("checkpoint %s: not a JSON object", peerBase)
	}
	return m, nil
}

// FetchIdentity retrieves a peer's identity document, decoded with exact
// integers (§3).
func (s *HTTPSender) FetchIdentity(peerBase string) (map[string]any, error) {
	resp, err := s.Client.Get(peerBase + "/.well-known/liquiditypub")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("identity %s: HTTP %d", peerBase, resp.StatusCode)
	}
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return nil, err
	}
	v, err := conformance.DecodeJSON(buf.Bytes())
	if err != nil {
		return nil, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("identity %s: not a JSON object", peerBase)
	}
	return m, nil
}
