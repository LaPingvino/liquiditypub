package lpnode

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/LaPingvino/liquiditypub/conformance"
)

// Start launches the delivery worker. Call once before federating.
func (n *Node) Start() {
	n.mu.Lock()
	if n.pushSig == nil {
		n.pushSig = make(chan struct{}, 1)
		go n.pushWorker()
	}
	n.mu.Unlock()
	n.wakePush()
}

// pushWorker delivers each peer's outbox to it, in seq order, stopping at the
// first failure so a transient error is retried on the next wake rather than
// letting a later seq overtake it (which the receiver would reject as stale and
// pruning would then drop). A periodic tick retries anything a failure left
// behind even absent new traffic; the pull baseline is the ultimate backstop.
func (n *Node) pushWorker() {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-n.pushSig:
		case <-t.C:
		}
		s := n.sender()
		if s == nil {
			continue
		}
		n.mu.Lock()
		hosts := make([]string, 0, len(n.outbox))
		for h := range n.outbox {
			hosts = append(hosts, h)
		}
		n.mu.Unlock()
		for _, h := range hosts {
			n.pushHost(s, h)
		}
	}
}

// pushHost delivers the not-yet-acknowledged outbox entries for one peer in
// ascending seq order, advancing the per-peer cursor only on a successful send
// and stopping at the first failure. Idempotent: the cursor is in-memory, so a
// restart re-pushes and the receiver dedups by id.
func (n *Node) pushHost(s Sender, host string) {
	for {
		n.mu.Lock()
		var env map[string]any
		var seq int64
		var base string
		for _, e := range n.outbox[host] {
			if sq, ok := asInt(e["seq"]); ok && sq > n.pushed[host] {
				env, seq, base = e, sq, envStr(e, "to")
				break
			}
		}
		n.mu.Unlock()
		if env == nil {
			return
		}
		if err := s.Deliver(base, env); err != nil {
			log.Printf("%s: deliver %v to %s (seq %d): %v — will retry", n.cfg.Base, env["type"], host, seq, err)
			return
		}
		n.mu.Lock()
		if seq > n.pushed[host] {
			n.pushed[host] = seq
		}
		n.mu.Unlock()
	}
}

// wakePush nudges the push worker without blocking (the signal channel coalesces
// wakeups). Safe to call with or without n.mu held.
func (n *Node) wakePush() {
	select {
	case n.pushSig <- struct{}{}:
	default:
	}
}

// dispatch signals the push worker that peer `to` has new outbox entries. The
// envelope is already durably recorded in the outbox by buildSigned (the `env`
// argument is that same envelope, kept for call-site clarity), so delivery reads
// from the outbox in seq order rather than trusting enqueue order. Must be called
// with n.mu released.
func (n *Node) dispatch(to string, env map[string]any) {
	n.wakePush()
}

// directDeliver sends one envelope out-of-band, bypassing the seq cursor. Used
// only to re-answer a duplicate request with its cached reply: the receiver
// dedups by id, so reordering this single resend is harmless.
func (n *Node) directDeliver(to string, env map[string]any) {
	if env == nil {
		return
	}
	go func() {
		if s := n.sender(); s != nil {
			_ = s.Deliver(to, env)
		}
	}()
}

// ProcessInbound validates and handles one inbound envelope (§4), dispatching
// any reply. It returns the validation verdict; the HTTP layer maps a non-ok
// verdict to 400/403. Delivery success is irrelevant to correctness.
func (n *Node) ProcessInbound(env map[string]any) string {
	// §14: reject an envelope whose major version we do not implement, before any
	// signature work — its shape is not guaranteed to match what we verify.
	if major, _, _ := strings.Cut(envStr(env, "lp"), "."); major != "0" {
		return conformance.VerdictMalformed
	}
	// §4: an envelope must be addressed to us. Compare hosts so scheme/trailing
	// differences don't matter.
	if to := envStr(env, "to"); to != "" && host(to) != host(n.cfg.Base) {
		return conformance.VerdictMalformed
	}
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
			// The peer retried a request it never saw our answer to; resend the
			// cached reply directly (bypassing the seq cursor, which already
			// counts it as delivered).
			n.directDeliver(envStr(reply, "to"), reply)
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
	case "contact.update":
		return n.handleContactUpdate(env)
	case "contact.close":
		return n.handleContactClose(env)
	case "key.announce":
		return n.handleKeyAnnounce(env)
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
	case "reserve.adjust":
		return n.handleReserveAdjust(env)
	case "reserve.accept":
		return n.handleReserveAccept(env)
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

// senderContact resolves the contact referenced by contactID *and* verifies the
// authenticated envelope sender is that contact's peer. ValidateEnvelope has
// already bound env["from"] to a key the sender published (see keyBoundToSender),
// so this is a real authorization check: a peer may only drive operations on its
// own contact, never a transfer/contact whose opaque id it merely learned (§7,
// §13). Returns nil when the sender does not own the object. Caller holds n.mu.
func (n *Node) senderContact(env map[string]any, contactID string) *Contact {
	c := n.contacts[contactID]
	if c == nil || c.PeerHost != host(envStr(env, "from")) {
		return nil
	}
	return c
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
	c := n.senderContact(env, t.ContactID)
	if c == nil {
		return nil // not the counterparty: ignore, never touch the state machine
	}
	if next, err := conformance.Transition(t.State, "reject"); err == nil {
		t.State = next
		if c.BusyTransfer == t.ID {
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
	c := n.senderContact(env, t.ContactID)
	if c == nil {
		return nil // not the counterparty: ignore, never touch the state machine
	}
	if next, err := conformance.Transition(t.State, "abort"); err == nil {
		t.State = next
		if c.BusyTransfer == t.ID {
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
