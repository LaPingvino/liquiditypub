package lpnode

import (
	"time"

	"github.com/LaPingvino/liquiditypub/conformance"
)

// Pull transport (PROTOCOL §5.1) — the mandatory baseline. A node fetches each
// peer's outbox addressed to it and feeds the envelopes through the same
// validation path as push. Because ProcessInbound is idempotent (id dedup +
// per-channel seq), re-fetching an outbox that still contains processed
// entries is harmless; only genuinely new envelopes have an effect.

// PollPeer fetches one peer's outbox for us and processes every envelope in
// order. It returns the count newly accepted (verdict ok).
func (n *Node) PollPeer(peerBase string) (int, error) {
	s := n.sender()
	if s == nil {
		return 0, nil
	}
	envs, err := s.FetchOutbox(peerBase, n.Host())
	if err != nil {
		return 0, err
	}
	accepted := 0
	for _, env := range envs {
		if n.ProcessInbound(env) == conformance.VerdictOK {
			accepted++
		}
	}
	return accepted, nil
}

// PollAll polls every open contact peer once (§5.1) and reconciles its
// checkpoint (§8.3). Peers with a pending but not-yet-active contact are
// included so the proposer picks up the acceptance.
func (n *Node) PollAll() {
	for _, base := range n.openPeerBases() {
		_, _ = n.PollPeer(base)
		_, _ = n.ReconcilePeer(base)
		_, _, _ = n.RefreshPeerKeys(base) // enforce key revocation (§3, §13)
	}
}

// StartPulling launches a background poller at the given cadence (SHOULD be
// ≤ 15 min for live profiles, §12). It stops when stop is closed. Safe to run
// alongside push: pull just picks up anything push missed.
func (n *Node) StartPulling(interval time.Duration, stop <-chan struct{}) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				n.PollAll()
			}
		}
	}()
}

// StartKeyRefresher periodically re-fetches every open peer's identity document
// and prunes keys it now marks revoked (§3, §13). A push-only node that never
// polls outboxes still needs this, or a stolen-but-revoked key would be accepted
// on its inbox indefinitely. Runs until stop is closed.
func (n *Node) StartKeyRefresher(interval time.Duration, stop <-chan struct{}) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				for _, base := range n.openPeerBases() {
					_, _, _ = n.RefreshPeerKeys(base)
				}
			}
		}
	}()
}

// openPeerBases snapshots the base URLs of all non-closed contacts.
func (n *Node) openPeerBases() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]string, 0, len(n.contactByHost))
	for _, c := range n.contactByHost {
		if !c.Closed {
			out = append(out, c.PeerBase)
		}
	}
	return out
}
