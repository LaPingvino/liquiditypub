package lpnode_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	lpnode "github.com/LaPingvino/liquiditypub/node"
	"github.com/LaPingvino/liquiditypub/node/httpapi"
)

// TestLogTransparencyGating checks the §9.3 log visibility rules through the
// HTTP handler: pseudonymous logs are open, "peers" logs require an
// active-peer header, and checkpoints are public in every mode.
func TestLogTransparencyGating(t *testing.T) {
	get := func(base, path, peerHeader string) int {
		req, _ := http.NewRequest(http.MethodGet, base+path, nil)
		if peerHeader != "" {
			req.Header.Set("X-LP-Peer", peerHeader)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	// Pseudonymous node: log is open.
	pseudo, err := lpnode.NewNode(lpnode.Config{
		Base: "https://pub.example", Name: "Pub", CurrencyName: "P", CurrencySymbol: "P",
		CPeriodPpm: 274, UDPeriod: "P1D", GenesisUD: 1_000_000, Transparency: "pseudonymous",
	})
	if err != nil {
		t.Fatal(err)
	}
	sp := httptest.NewServer(httpapi.Handler(pseudo))
	defer sp.Close()
	if code := get(sp.URL, "/lp/log/head.json", ""); code != http.StatusOK {
		t.Errorf("pseudonymous log head = %d, want 200", code)
	}

	// "peers"-level node with a real contact: log is gated.
	pn, err := lpnode.NewNode(lpnode.Config{
		Base: "https://peers.example", Name: "Peers", CurrencyName: "P", CurrencySymbol: "P",
		CPeriodPpm: 274, UDPeriod: "P1D", GenesisUD: 1_000_000, Transparency: "peers",
		AutoAcceptSeed: 500_000_000, Members: []lpnode.MemberConfig{{Name: "alice", Grant: 1_000_000}},
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := lpnode.NewNode(lpnode.Config{
		Base: "https://friend.example", Name: "Friend", CurrencyName: "F", CurrencySymbol: "F",
		CPeriodPpm: 274, UDPeriod: "P1D", GenesisUD: 1_000_000,
		AutoAcceptSeed: 500_000_000, Members: []lpnode.MemberConfig{{Name: "bob", Grant: 1_000_000}},
	})
	if err != nil {
		t.Fatal(err)
	}
	sh := &busShim{m: map[string]*lpnode.Node{
		"https://peers.example": pn, "https://friend.example": other,
	}}
	pn.SetSender(sh)
	other.SetSender(sh)
	pn.Start()
	other.Start()
	if _, err := pn.OpenContact("https://friend.example", 500_000_000, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool { return pn.IsActivePeer("https://friend.example") })

	srv := httptest.NewServer(httpapi.Handler(pn))
	defer srv.Close()

	// Checkpoint is public regardless of transparency.
	if code := get(srv.URL, "/lp/checkpoint.json", ""); code != http.StatusOK {
		t.Errorf("peers checkpoint = %d, want 200 (always public)", code)
	}
	// Log without a peer header is forbidden.
	if code := get(srv.URL, "/lp/log/head.json", ""); code != http.StatusForbidden {
		t.Errorf("peers log without header = %d, want 403", code)
	}
	// Log with an unknown peer header is forbidden.
	if code := get(srv.URL, "/lp/log/head.json", "https://stranger.example"); code != http.StatusForbidden {
		t.Errorf("peers log with unknown peer = %d, want 403", code)
	}
	// Log with an active-peer header is allowed.
	if code := get(srv.URL, "/lp/log/head.json", "https://friend.example"); code != http.StatusOK {
		t.Errorf("peers log with active peer = %d, want 200", code)
	}
}

// busShim is a minimal in-process Sender for the gating test.
type busShim struct{ m map[string]*lpnode.Node }

func (s *busShim) Deliver(p string, e map[string]any) error {
	if n := s.m[p]; n != nil {
		n.ProcessInbound(e)
	}
	return nil
}
func (s *busShim) FetchIdentity(p string) (map[string]any, error) { return s.m[p].IdentityDoc(), nil }
func (s *busShim) FetchOutbox(p, h string) ([]map[string]any, error) {
	if n := s.m[p]; n != nil {
		return n.OutboxFor(h), nil
	}
	return nil, nil
}
func (s *busShim) FetchCheckpoint(p string) (map[string]any, error) {
	if n := s.m[p]; n != nil {
		return n.Checkpoint(), nil
	}
	return nil, nil
}
