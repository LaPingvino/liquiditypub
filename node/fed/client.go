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
