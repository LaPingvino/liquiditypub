package main

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/LaPingvino/liquiditypub/conformance"
	lpnode "github.com/LaPingvino/liquiditypub/node"
	"github.com/LaPingvino/liquiditypub/node/store"
)

// Host runs many nodes in one process: a clearing house (PROTOCOL §12).
// Kafumu's groups are each their own CoffeeBucks node — own key, own ledger,
// own dividend — and two groups trade only through a contact pool they chose
// to open. Between nodes on the same host, envelopes go straight from one
// node to the other (inproc): the full signed protocol, no HTTP. Contacts are
// keyed by the peer's host, so every group gets one: <gid>.<groupDomain>.
type Host struct {
	groupDomain string                      // e.g. "g.kafumu.com"
	newStore    func(id string) store.Store // where a group's state lives
	cfg         func(gid, name string) lpnode.Config

	mu    sync.RWMutex
	nodes map[string]*lpnode.Node // by host (gid.groupDomain)
	stop  chan struct{}
}

var gidRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,30}$`)

// NewHost: groups as <gid>.<groupDomain>, stored by newStore, configured by cfg.
func NewHost(groupDomain string, newStore func(string) store.Store, cfg func(gid, name string) lpnode.Config) *Host {
	return &Host{groupDomain: groupDomain, newStore: newStore, cfg: cfg, nodes: map[string]*lpnode.Node{}, stop: make(chan struct{})}
}

// Base is a group's base URL.
func (h *Host) Base(gid string) string { return "https://" + gid + "." + h.groupDomain }

// Group returns a running group, or nil.
func (h *Host) Group(gid string) *lpnode.Node {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.nodes[gid+"."+h.groupDomain]
}

// Groups lists the open groups' ids.
func (h *Host) Groups() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]string, 0, len(h.nodes))
	for hst := range h.nodes {
		out = append(out, strings.TrimSuffix(hst, "."+h.groupDomain))
	}
	return out
}

// Open starts group gid (named name), restoring it from its store if it
// exists, creating it otherwise.
func (h *Host) Open(gid, name string) (*lpnode.Node, error) {
	if !gidRE.MatchString(gid) {
		return nil, fmt.Errorf("bad group id %q", gid)
	}
	if n := h.Group(gid); n != nil {
		return n, nil
	}
	cfg := h.cfg(gid, name)
	cfg.Base = h.Base(gid)
	n, err := lpnode.Restore(cfg, h.newStore("g-"+gid))
	if err != nil {
		return nil, err
	}
	n.SetSender(inproc{h})
	h.mu.Lock()
	if old := h.nodes[n.Host()]; old != nil { // raced with another Open
		h.mu.Unlock()
		return old, nil
	}
	h.nodes[n.Host()] = n
	h.mu.Unlock()
	n.Start()
	n.StartExpirySweeper(30*time.Second, h.stop)
	return n, nil
}

// Link opens a contact pool between two groups, a's seed in a's currency;
// b accepts with its configured seed (both a hundred dividends by default:
// UD parity, DESIGN §5 — the price floats from there).
func (h *Host) Link(a, b string, seed int64) error {
	na, nb := h.Group(a), h.Group(b)
	if na == nil || nb == nil {
		return fmt.Errorf("link %s-%s: both groups must be open", a, b)
	}
	_, err := na.OpenContact(h.Base(b), seed, "linked on Kafumu")
	return err
}

// RunUD issues one dividend period in every open group (the daily cron).
func (h *Host) RunUD() (int, error) {
	h.mu.RLock()
	ns := make([]*lpnode.Node, 0, len(h.nodes))
	for _, n := range h.nodes {
		ns = append(ns, n)
	}
	h.mu.RUnlock()
	for _, n := range ns {
		if _, err := n.RunUD(); err != nil {
			return 0, fmt.Errorf("%s: %w", n.Host(), err)
		}
	}
	return len(ns), nil
}

// inproc delivers between nodes on this host (lpnode.Sender).
type inproc struct{ h *Host }

func (s inproc) node(base string) (*lpnode.Node, error) {
	s.h.mu.RLock()
	defer s.h.mu.RUnlock()
	for hst, n := range s.h.nodes {
		if base == "https://"+hst || base == "https://"+hst+"/" {
			return n, nil
		}
	}
	return nil, fmt.Errorf("no node %s on this host", base)
}

func (s inproc) Deliver(peerBase string, env map[string]any) error {
	n, err := s.node(peerBase)
	if err != nil {
		return err
	}
	if v := n.ProcessInbound(env); v != conformance.VerdictOK && v != conformance.VerdictDuplicate {
		return fmt.Errorf("%s refused: %s", peerBase, v)
	}
	return nil
}

func (s inproc) FetchIdentity(peerBase string) (map[string]any, error) {
	n, err := s.node(peerBase)
	if err != nil {
		return nil, err
	}
	return n.IdentityDoc(), nil
}

func (s inproc) FetchOutbox(peerBase, myHost string) ([]map[string]any, error) {
	n, err := s.node(peerBase)
	if err != nil {
		return nil, err
	}
	return n.OutboxFor(myHost), nil
}

func (s inproc) FetchCheckpoint(peerBase string) (map[string]any, error) {
	n, err := s.node(peerBase)
	if err != nil {
		return nil, err
	}
	return n.Checkpoint(), nil
}
