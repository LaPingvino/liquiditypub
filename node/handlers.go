package lpnode

import (
	"fmt"
	"log"
	"time"

	"github.com/LaPingvino/liquiditypub/conformance"
)

// Start launches the delivery worker. Call once before federating.
func (n *Node) Start() {
	n.mu.Lock()
	if n.deliveries == nil {
		n.deliveries = make(chan qitem, 1024)
		go n.deliverLoop()
	}
	n.mu.Unlock()
}

type qitem struct {
	to  string
	env map[string]any
}

func (n *Node) deliverLoop() {
	for it := range n.deliveries {
		s := n.sender()
		if s == nil {
			log.Printf("%s: no sender; dropping %v to %s", n.cfg.Base, it.env["type"], it.to)
			continue
		}
		if err := s.Deliver(it.to, it.env); err != nil {
			log.Printf("%s: deliver %v to %s: %v", n.cfg.Base, it.env["type"], it.to, err)
		}
	}
}

// dispatch enqueues an outbound envelope for ordered, in-process delivery. The
// single worker preserves per-channel ordering, which the receiver's seq check
// depends on. Must be called with n.mu released.
func (n *Node) dispatch(to string, env map[string]any) {
	if env == nil {
		return
	}
	n.mu.Lock()
	ch := n.deliveries
	n.mu.Unlock()
	if ch == nil {
		log.Printf("%s: dispatch before Start(); dropping", n.cfg.Base)
		return
	}
	ch <- qitem{to: to, env: env}
}

// ProcessInbound validates and handles one inbound envelope (§4), dispatching
// any reply. It returns the validation verdict; the HTTP layer maps a non-ok
// verdict to 400/403. Delivery success is irrelevant to correctness.
func (n *Node) ProcessInbound(env map[string]any) string {
	fromBase := envStr(env, "from")
	sig, _ := env["sig"].(map[string]any)
	keyid, _ := sig["key"].(string)

	n.mu.Lock()
	if _, ok := n.peerKeys[keyid]; !ok {
		n.mu.Unlock()
		if err := n.fetchPeerKeys(fromBase); err != nil {
			return conformance.VerdictUnknownKey
		}
		n.mu.Lock()
	}

	fromHost := host(fromBase)
	ci := n.inbound(fromHost)
	st := conformance.ValidationState{
		Keys: n.peerKeys, SeenIDs: ci.seenIDs, LastSeq: ci.lastSeq, Now: n.clock(),
	}
	verdict := conformance.ValidateEnvelope(env, st)

	if verdict == conformance.VerdictDuplicate {
		reply := ci.replies[envStr(env, "id")]
		n.mu.Unlock()
		if reply != nil {
			n.dispatch(envStr(reply, "to"), reply)
		}
		return verdict
	}
	if verdict != conformance.VerdictOK {
		n.mu.Unlock()
		return verdict
	}

	reply := n.route(env)

	id := envStr(env, "id")
	ci.seenIDs[id] = true
	if seq, ok := asInt(env["seq"]); ok && seq > ci.lastSeq {
		ci.lastSeq = seq
	}
	if reply != nil {
		ci.replies[id] = reply
	}
	_ = n.persistLocked() // durable before the state change is observable
	n.mu.Unlock()

	if reply != nil {
		n.dispatch(envStr(reply, "to"), reply)
	}
	return verdict
}

// route dispatches by envelope type. Called with n.mu held; handlers mutate
// state directly and return at most one reply envelope.
func (n *Node) route(env map[string]any) map[string]any {
	switch envStr(env, "type") {
	case "contact.propose":
		return n.handleContactPropose(env)
	case "contact.accept":
		return n.handleContactAccept(env)
	case "transfer.propose":
		return n.handleTransferPropose(env)
	case "transfer.accept":
		return n.handleTransferAccept(env)
	case "transfer.reject":
		return n.handleTransferReject(env)
	case "transfer.commit":
		return n.handleTransferCommit(env)
	case "transfer.receipt":
		return n.handleTransferReceipt(env)
	case "transfer.abort":
		return n.handleTransferAbort(env)
	case "member.lookup":
		return n.handleMemberLookup(env)
	case "ping":
		return n.buildSigned("pong", envStr(env, "from"), envStr(env, "id"), map[string]any{})
	case "pong":
		return nil
	case "error":
		log.Printf("%s: peer error re=%v: %v", n.cfg.Base, env["re"], env["payload"])
		return nil
	default:
		return n.errorReply(env, "unknown-type", envStr(env, "type"))
	}
}

func (n *Node) inbound(fromHost string) *channelInbound {
	ci := n.inState[fromHost]
	if ci == nil {
		ci = &channelInbound{seenIDs: map[string]bool{}, replies: map[string]map[string]any{}}
		n.inState[fromHost] = ci
	}
	return ci
}

// fetchPeerKeys pulls a peer's identity document and registers its keys (§3).
func (n *Node) fetchPeerKeys(peerBase string) error {
	s := n.sender()
	if s == nil {
		return fmt.Errorf("no sender to fetch identity")
	}
	doc, err := s.FetchIdentity(peerBase)
	if err != nil {
		return err
	}
	keys, _ := doc["keys"].([]any)
	base := peerBase
	if nd, ok := doc["node"].(map[string]any); ok {
		if b, ok := nd["base"].(string); ok && b != "" {
			base = b
		}
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	var registered int
	for _, k := range keys {
		km, ok := k.(map[string]any)
		if !ok {
			continue
		}
		if km["revoked"] != nil {
			continue
		}
		id, _ := km["id"].(string)
		pubStr, _ := km["public_key"].(string)
		pub, err := mustParseKey(pubStr)
		if err != nil {
			continue
		}
		n.peerKeys[keyID(base, id)] = pub
		registered++
	}
	if registered == 0 {
		return fmt.Errorf("no usable keys in %s identity doc", peerBase)
	}
	return nil
}

// errorReply builds a typed error envelope (§4). Called with n.mu held.
func (n *Node) errorReply(env map[string]any, code, detail string) map[string]any {
	return n.buildSigned("error", envStr(env, "from"), envStr(env, "id"), map[string]any{
		"code":   code,
		"detail": detail,
	})
}

// ── remaining small handlers ─────────────────────────────────────────────────

func (n *Node) handleTransferReject(env map[string]any) map[string]any {
	p, _ := payloadOf(env)
	t := n.transfers[pStr(p, "transfer_id")]
	if t == nil || !t.Outgoing {
		return nil
	}
	if next, err := conformance.Transition(t.State, "reject"); err == nil {
		t.State = next
		if c := n.contacts[t.ContactID]; c != nil && c.BusyTransfer == t.ID {
			c.Busy, c.BusyTransfer = false, ""
		}
	}
	return nil
}

func (n *Node) handleTransferAbort(env map[string]any) map[string]any {
	p, _ := payloadOf(env)
	t := n.transfers[pStr(p, "transfer_id")]
	if t == nil || t.Outgoing {
		return nil
	}
	if next, err := conformance.Transition(t.State, "abort"); err == nil {
		t.State = next
		if c := n.contacts[t.ContactID]; c != nil && c.BusyTransfer == t.ID {
			c.Busy, c.BusyTransfer = false, ""
		}
	}
	return nil
}

func (n *Node) handleMemberLookup(env map[string]any) map[string]any {
	p, ok := payloadOf(env)
	if !ok {
		return n.errorReply(env, "malformed", "missing payload")
	}
	// Only answer over an active contact (§11).
	if c := n.contactByHost[host(envStr(env, "from"))]; c == nil || !c.Active {
		return n.errorReply(env, "no-contact", "lookups require an active contact")
	}
	member := pStr(p, "member")
	m := n.members[localPart(member)]
	res := map[string]any{"member": member, "found": m != nil && m.Active}
	if m != nil && m.Active && m.DisplayName != "" && n.cfg.Transparency == "public" {
		res["display_name"] = m.DisplayName
	}
	return n.buildSigned("member.result", envStr(env, "from"), envStr(env, "id"), res)
}

// ── checkpoint (§8.3) ────────────────────────────────────────────────────────

// Checkpoint builds and signs the node checkpoint document. Public in every
// transparency mode.
func (n *Node) Checkpoint() map[string]any {
	n.mu.Lock()
	defer n.mu.Unlock()
	contacts := make([]any, 0, len(n.contactByHost))
	for _, c := range n.contactByHost {
		ci := n.inState[c.PeerHost]
		var lastSeq int64
		if ci != nil {
			lastSeq = ci.lastSeq
		}
		contacts = append(contacts, map[string]any{
			"peer":               c.PeerBase,
			"contact_id":         c.ID,
			"peer_reserve_here":  c.MyReserveOfPeer,
			"op_seq":             c.OpSeq,
			"channel_root":       c.channelRootB64(),
			"last_seq_processed": lastSeq,
		})
	}
	cp := map[string]any{
		"lp":           "0.2",
		"type":         "checkpoint",
		"node":         n.cfg.Base,
		"created":      n.clock().Format(time.RFC3339),
		"log_seq":      int64(n.led.Len()),
		"log_hash":     n.led.Head(),
		"money_supply": n.led.MoneySupply(),
		"member_count": int64(len(n.members)),
		"current_ud":   n.currentUD,
		"contacts":     contacts,
	}
	sig, err := conformance.SignEnvelope(cp, n.priv)
	if err != nil {
		panic(fmt.Sprintf("sign checkpoint: %v", err))
	}
	cp["sig"] = map[string]any{
		"key":   keyID(n.cfg.Base, n.keyLocalID),
		"alg":   "ed25519",
		"value": b64(sig),
	}
	return cp
}

// OutboxFor returns the ordered envelopes addressed to a peer host (§5.1).
func (n *Node) OutboxFor(peerHost string) []map[string]any {
	n.mu.Lock()
	defer n.mu.Unlock()
	src := n.outbox[peerHost]
	out := make([]map[string]any, len(src))
	copy(out, src)
	return out
}
