// Package httpapi serves a node's contact surface over HTTP (PROTOCOL §3–5,
// §8.3, §9.2) and provides a small admin API for driving a node in demos and
// tests. The read surface (identity, outbox, checkpoint, log) is fully static
// in shape, so a mirror can serve it from flat files.
package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/LaPingvino/liquiditypub/conformance"
	lpnode "github.com/LaPingvino/liquiditypub/node"
)

// Handler builds the HTTP mux for a node.
func Handler(n *lpnode.Node) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/.well-known/liquiditypub", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, n.IdentityDoc())
	})

	mux.HandleFunc("/lp/checkpoint.json", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, n.Checkpoint())
	})

	// Outbox: /lp/outbox/{peer-host}.json (§5.1).
	mux.HandleFunc("/lp/outbox/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/lp/outbox/")
		name = strings.TrimSuffix(name, ".json")
		writeJSON(w, http.StatusOK, n.OutboxFor(name))
	})

	// Log head + pages (§9.2).
	mux.HandleFunc("/lp/log/head.json", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, n.LogHead())
	})
	mux.HandleFunc("/lp/log/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, n.LogRecords())
	})

	// Inbox: push binding (§5.2). Body is one envelope.
	mux.HandleFunc("/lp/inbox", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		body, err := readAll(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"code": "malformed", "detail": err.Error()})
			return
		}
		decoded, err := conformance.DecodeJSON(body)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"code": "malformed", "detail": err.Error()})
			return
		}
		env, ok := decoded.(map[string]any)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]any{"code": "malformed", "detail": "not an object"})
			return
		}
		verdict := n.ProcessInbound(env)
		if verdict != conformance.VerdictOK && verdict != conformance.VerdictDuplicate {
			writeJSON(w, statusFor(verdict), map[string]any{"code": verdict})
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"status": "accepted"})
	})

	// ── admin API (out of protocol; for demos/tests) ──
	mux.HandleFunc("/admin/contact", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Peer string `json:"peer"`
			Seed int64  `json:"seed"`
			Note string `json:"note"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		id, err := n.OpenContact(req.Peer, req.Seed, req.Note)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"contact_id": id})
	})

	mux.HandleFunc("/admin/transfer", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Peer string `json:"peer"`
			From string `json:"from"`
			To   string `json:"to"`
			Src  int64  `json:"src"`
			Note string `json:"note"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		id, err := n.StartTransfer(req.Peer, req.From, req.To, req.Src, req.Note)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"transfer_id": id})
	})

	mux.HandleFunc("/admin/transfer/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/admin/transfer/")
		writeJSON(w, http.StatusOK, map[string]any{"state": n.TransferState(id)})
	})

	mux.HandleFunc("/admin/adjust", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Peer  string `json:"peer"`
			Delta int64  `json:"delta"`
			Memo  string `json:"memo"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		id, err := n.AdjustReserve(req.Peer, req.Delta, req.Memo)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"adjust_id": id})
	})

	mux.HandleFunc("/admin/ud", func(w http.ResponseWriter, r *http.Request) {
		udBase, err := n.RunUD()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ud_base": udBase})
	})

	return mux
}

func statusFor(verdict string) int {
	switch verdict {
	case conformance.VerdictUnknownKey, conformance.VerdictBadSignature:
		return http.StatusForbidden
	default:
		return http.StatusBadRequest
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func readAll(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	return io.ReadAll(io.LimitReader(r.Body, 1<<20))
}
