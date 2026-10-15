package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/datastore"
	"github.com/LaPingvino/liquiditypub/conformance"
	lpnode "github.com/LaPingvino/liquiditypub/node"
	"github.com/LaPingvino/liquiditypub/node/httpapi"
	"github.com/LaPingvino/liquiditypub/node/ledger"
)

// lpGroup is a group's registry entry (namespace lp): which group nodes this
// host opens at start. The group's money lives in its own node store.
type lpGroup struct {
	Name      string    `datastore:"name,noindex"`
	CreatedAt time.Time `datastore:"created_at,noindex"`
	// Renamed by its members (96k); empty = the default.
	Currency string `datastore:"currency,noindex"`
	Symbol   string `datastore:"symbol,noindex"`
	// EmptySince: when its last member left (zero while anyone is in it);
	// 90 days later the group is deleted (96m).
	EmptySince time.Time `datastore:"empty_since"`
}

// GroupGrace is how long a group with nobody in it waits for someone to
// come back with an invite before it is deleted.
const GroupGrace = 90 * 24 * time.Hour

// markEmpty records whether a group has anyone in it (after a leave or a join).
func markEmpty(ctx context.Context, db *datastore.Client, gid string, n *lpnode.Node) {
	if db == nil {
		return
	}
	empty := n.ActiveMembers() == 0
	k := groupKey(gid)
	db.RunInTransaction(ctx, func(tx *datastore.Transaction) error {
		var g lpGroup
		if err := tx.Get(k, &g); err != nil {
			return err
		}
		switch {
		case empty && g.EmptySince.IsZero():
			g.EmptySince = time.Now().UTC()
		case !empty && !g.EmptySince.IsZero():
			g.EmptySince = time.Time{}
		default:
			return nil
		}
		_, err := tx.Put(k, &g)
		return err
	})
}

// sweepGroups deletes groups that have had nobody in them for GroupGrace:
// links closed and reserves retired (Host.Remove), then their stored state
// and registry entry. Returns how many went.
func sweepGroups(ctx context.Context, db *datastore.Client, h *Host, now time.Time) (int, error) {
	var gs []lpGroup
	keys, err := db.GetAll(ctx, datastore.NewQuery("LPGroup").Namespace(ns).FilterField("empty_since", ">", time.Time{}).FilterField("empty_since", "<", now.Add(-GroupGrace)), &gs)
	if err != nil {
		return 0, err
	}
	gone := 0
	for _, k := range keys {
		gid := k.Name
		if n := h.Group(gid); n != nil && n.ActiveMembers() > 0 { // someone came back meanwhile
			continue
		}
		if h.Group(gid) != nil {
			if err := h.Remove(gid); err != nil {
				return gone, err
			}
		}
		if err := NewDSStore(db, "g-"+gid).Delete(ctx); err != nil {
			return gone, err
		}
		if err := db.Delete(ctx, k); err != nil {
			return gone, err
		}
		log.Printf("group %s deleted: nobody in it for %s", gid, GroupGrace)
		gone++
	}
	return gone, nil
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
		n, err := h.Open(k.Name, gs[i].Name)
		if err != nil {
			return fmt.Errorf("group %s: %w", k.Name, err)
		}
		n.SetIdentity("", gs[i].Currency, gs[i].Symbol)
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
		defer markEmpty(r.Context(), db, in.Gid, n)
		if n.MemberKnown(in.Member) && !n.MemberActive(in.Member) { // rejoining (96i)
			return map[string]bool{"ok": true, "rejoined": true}, n.Reactivate(in.Member)
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
			"current_ud": cp["current_ud"], "members": n.ActiveMembers(), "money_supply": cp["money_supply"]}, nil
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
	// GET ?gid=: the groups gid is linked with (an active pool), with names.
	mux.HandleFunc("GET /lp/admin/links", auth(func(w http.ResponseWriter, r *http.Request) (any, error) {
		gid := r.FormValue("gid")
		n, err := group(gid)
		if err != nil {
			return nil, err
		}
		out := []map[string]string{}
		for _, other := range h.Groups() {
			if other != gid && n.ContactActive(h.Base(other)) {
				out = append(out, map[string]string{"gid": other, "name": nodeName(h.Group(other))})
			}
		}
		return out, nil
	}))
	// GET ?from_gid=&to_gid=&amount=: what the payee would get for amount,
	// priced by the pool between the two groups right now (PROTOCOL §6.2).
	mux.HandleFunc("GET /lp/admin/quote", auth(func(w http.ResponseWriter, r *http.Request) (any, error) {
		a, err := group(r.FormValue("from_gid"))
		if err != nil {
			return nil, err
		}
		cv, ok := a.ContactByPeer(h.Base(r.FormValue("to_gid")))
		if !ok || !cv.Active {
			return nil, fmt.Errorf("these groups aren't linked")
		}
		amount, err := strconv.ParseInt(r.FormValue("amount"), 10, 64)
		if err != nil {
			return nil, err
		}
		dst, err := conformance.PoolPrice(cv.MyReserveOfPeer, cv.PeerReserveOfMe, amount)
		if err != nil {
			return nil, err
		}
		return map[string]int64{"amount": amount, "dst": dst}, nil
	}))
	// POST {gid, name?, currency?, symbol?}: rename the group, its currency or
	// its symbol (any member may: Kafumu checks that); kept in the registry.
	mux.HandleFunc("POST /lp/admin/rename", auth(func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in struct{ Gid, Name, Currency, Symbol string }
		if err := body(r, &in); err != nil {
			return nil, err
		}
		n, err := group(in.Gid)
		if err != nil {
			return nil, err
		}
		in.Name, in.Currency, in.Symbol = clean(in.Name), clean(in.Currency), clean(in.Symbol)
		if len([]rune(in.Name)) > 60 || len([]rune(in.Currency)) > 30 || len([]rune(in.Symbol)) > 5 {
			return nil, fmt.Errorf("too long: name 60, currency 30, symbol 5 characters")
		}
		if in.Name == "" && in.Currency == "" && in.Symbol == "" {
			return nil, fmt.Errorf("nothing to rename")
		}
		if db != nil {
			k := groupKey(in.Gid)
			if _, err := db.RunInTransaction(r.Context(), func(tx *datastore.Transaction) error {
				var g lpGroup
				if err := tx.Get(k, &g); err != nil && err != datastore.ErrNoSuchEntity {
					return err
				}
				if in.Name != "" {
					g.Name = in.Name
				}
				if in.Currency != "" {
					g.Currency = in.Currency
				}
				if in.Symbol != "" {
					g.Symbol = in.Symbol
				}
				_, err := tx.Put(k, &g)
				return err
			}); err != nil {
				return nil, err
			}
		}
		n.SetIdentity(in.Name, in.Currency, in.Symbol)
		return map[string]bool{"ok": true}, nil
	}))
	// POST {gid, member, mode: destroy|redistribute|give|keep, to?}: leave a
	// group; the balance goes as chosen, then no more dividend.
	mux.HandleFunc("POST /lp/admin/leave", auth(func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in struct{ Gid, Member, Mode, To string }
		if err := body(r, &in); err != nil {
			return nil, err
		}
		n, err := group(in.Gid)
		if err != nil {
			return nil, err
		}
		defer markEmpty(r.Context(), db, in.Gid, n)
		return map[string]bool{"ok": true}, n.Leave(in.Member, in.Mode, in.To)
	}))
	// POST {from_gid, member, to_gid, to}: move to another group: the whole
	// balance through the pool to your account there, then leave. Waits for
	// the transfer to settle (in process: milliseconds); if it doesn't, you
	// stay a member with your balance.
	mux.HandleFunc("POST /lp/admin/move", auth(func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in struct {
			FromGid string `json:"from_gid"`
			Member  string
			ToGid   string `json:"to_gid"`
			To      string
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
		if !b.MemberActive(in.To) {
			return nil, fmt.Errorf("no account %q in %s yet", in.To, in.ToGid)
		}
		moved := a.Balance(ledger.MemberPrefix + in.Member)
		if moved > 0 {
			id, err := a.StartTransfer(h.Base(in.ToGid), in.Member+"@"+a.Host(), in.To+"@"+b.Host(), moved, "")
			if err != nil {
				return nil, err
			}
			for end := time.Now().Add(20 * time.Second); a.TransferState(id) != "SETTLED"; time.Sleep(20 * time.Millisecond) {
				if time.Now().After(end) {
					return nil, fmt.Errorf("the move didn't settle (state %s); you're still a member", a.TransferState(id))
				}
			}
		}
		defer markEmpty(r.Context(), db, in.FromGid, a)
		defer markEmpty(r.Context(), db, in.ToGid, b)
		return map[string]int64{"moved": moved}, a.Leave(in.Member, lpnode.LeaveKeep, "")
	}))
	return mux
}

func nodeName(n *lpnode.Node) string {
	if n == nil {
		return ""
	}
	if nd, ok := n.IdentityDoc()["node"].(map[string]any); ok {
		if s, ok := nd["name"].(string); ok {
			return s
		}
	}
	return ""
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

// clean trims a display text and drops control characters.
func clean(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s))
}
