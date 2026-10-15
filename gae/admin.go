package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"cloud.google.com/go/datastore"
	lpnode "github.com/LaPingvino/liquiditypub/node"
	"github.com/LaPingvino/liquiditypub/node/httpapi"
	"github.com/LaPingvino/liquiditypub/node/ledger"
)

// lpGroup is a group's registry entry (namespace lp): which group nodes this
// host opens at start. The group's money lives in its own node store.
type lpGroup struct {
	Name      string    `datastore:"name,noindex"`
	CreatedAt time.Time `datastore:"created_at,noindex"`
}

func groupKey(gid string) *datastore.Key {
	k := datastore.NameKey("LPGroup", gid, nil)
	k.Namespace = ns
	return k
}

// openGroups opens every registered group (at start).
func openGroups(ctx context.Context, db *datastore.Client, h *Host) error {
	var gs []lpGroup
	q := datastore.NewQuery("LPGroup").Namespace(ns)
	keys, err := db.GetAll(ctx, q, &gs)
	if err != nil {
		return err
	}
	for i, k := range keys {
		if _, err := h.Open(k.Name, gs[i].Name); err != nil {
			return fmt.Errorf("group %s: %w", k.Name, err)
		}
	}
	log.Printf("opened %d groups", len(keys))
	return nil
}

// adminAPI is what Kafumu's app calls (Authorization: Bearer <token from
// LPConfig>): groups, members, balances, payments. Everything a member does
// goes through Kafumu, which knows who is signed in; this side only checks
// the token. db is nil when running without Datastore (groups then live in
// memory only).
func adminAPI(h *Host, db *datastore.Client, token string) http.Handler {
	mux := http.NewServeMux()
	auth := func(f func(w http.ResponseWriter, r *http.Request) (any, error)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			out, err := f(w, r)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			json.NewEncoder(w).Encode(out)
		}
	}
	group := func(gid string) (*lpnode.Node, error) {
		if n := h.Group(gid); n != nil {
			return n, nil
		}
		return nil, fmt.Errorf("no group %q", gid)
	}
	body := func(r *http.Request, v any) error {
		return json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(v)
	}

	// POST {gid, name}: start a group (its own node, its own K$ and dividend).
	mux.HandleFunc("POST /lp/admin/group", auth(func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in struct{ Gid, Name string }
		if err := body(r, &in); err != nil {
			return nil, err
		}
		if h.Group(in.Gid) != nil {
			return nil, fmt.Errorf("group %q exists", in.Gid)
		}
		if _, err := h.Open(in.Gid, in.Name); err != nil {
			return nil, err
		}
		if db != nil {
			if _, err := db.Put(r.Context(), groupKey(in.Gid), &lpGroup{Name: in.Name, CreatedAt: time.Now().UTC()}); err != nil {
				return nil, err
			}
		}
		return map[string]string{"gid": in.Gid, "base": h.Base(in.Gid)}, nil
	}))
	// POST {gid, member}: a member joins (receives the dividend from now on).
	mux.HandleFunc("POST /lp/admin/member", auth(func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in struct{ Gid, Member string }
		if err := body(r, &in); err != nil {
			return nil, err
		}
		n, err := group(in.Gid)
		if err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, n.AddMember(in.Member, "", 0)
	}))
	// GET ?gid=&member=: balance (micro-units) and the current dividend.
	mux.HandleFunc("GET /lp/admin/balance", auth(func(w http.ResponseWriter, r *http.Request) (any, error) {
		n, err := group(r.FormValue("gid"))
		if err != nil {
			return nil, err
		}
		m := r.FormValue("member")
		cp := n.Checkpoint()
		return map[string]any{"member": m, "active": n.MemberActive(m), "balance": n.Balance(ledger.MemberPrefix + m),
			"current_ud": cp["current_ud"], "members": cp["member_count"], "money_supply": cp["money_supply"]}, nil
	}))
	// POST {gid, from, to, amount}: a payment inside a group.
	mux.HandleFunc("POST /lp/admin/pay", auth(func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in struct {
			Gid, From, To string
			Amount        int64
		}
		if err := body(r, &in); err != nil {
			return nil, err
		}
		n, err := group(in.Gid)
		if err != nil {
			return nil, err
		}
		rec, err := n.Pay(in.From, in.To, in.Amount)
		if err != nil {
			return nil, err
		}
		return map[string]any{"log_seq": rec.Seq}, nil
	}))
	// POST {a, b, seed}: link two groups (a contact pool at UD parity).
	mux.HandleFunc("POST /lp/admin/link", auth(func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in struct {
			A, B string
			Seed int64
		}
		if err := body(r, &in); err != nil {
			return nil, err
		}
		if in.Seed <= 0 {
			in.Seed = 100_000_000
		}
		return map[string]bool{"ok": true}, h.Link(in.A, in.B, in.Seed)
	}))
	// POST {from_gid, from, to_gid, to, amount}: pay across linked groups,
	// priced by their pool; GET ?gid=&id= for its state.
	mux.HandleFunc("POST /lp/admin/transfer", auth(func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in struct {
			FromGid string `json:"from_gid"`
			From    string
			ToGid   string `json:"to_gid"`
			To      string
			Amount  int64
		}
		if err := body(r, &in); err != nil {
			return nil, err
		}
		a, err := group(in.FromGid)
		if err != nil {
			return nil, err
		}
		b, err := group(in.ToGid)
		if err != nil {
			return nil, err
		}
		id, err := a.StartTransfer(h.Base(in.ToGid), in.From+"@"+a.Host(), in.To+"@"+b.Host(), in.Amount, "")
		if err != nil {
			return nil, err
		}
		return map[string]string{"id": id}, nil
	}))
	mux.HandleFunc("GET /lp/admin/transfer", auth(func(w http.ResponseWriter, r *http.Request) (any, error) {
		n, err := group(r.FormValue("gid"))
		if err != nil {
			return nil, err
		}
		return map[string]string{"state": n.TransferState(r.FormValue("id"))}, nil
	}))
	return mux
}

// groupReads serves a group's public documents at /lp/g/<gid>/… (identity,
// checkpoint, log per its transparency): the same surface as any node.
func groupReads(h *Host) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/lp/g/")
		gid, path, _ := strings.Cut(rest, "/")
		n := h.Group(gid)
		if n == nil || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/" + path
		httpapi.Handler(n).ServeHTTP(w, r2)
	}
}
