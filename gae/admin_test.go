package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LaPingvino/liquiditypub/node/ledger"
	"github.com/LaPingvino/liquiditypub/node/store"
)

// The admin API as Kafumu calls it: token required; start two groups, add
// members, dividend, pay inside, link, pay across; public reads per group.
func TestAdminAPI(t *testing.T) {
	h := NewHost("g.kafumu.test", func(string) store.Store { return store.Nop{} }, groupCfg)
	mux := http.NewServeMux()
	mux.Handle("/lp/admin/", adminAPI(h, nil, "tok"))
	mux.Handle("/lp/g/", groupReads(h))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	call := func(method, path, body, token string) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	ok := func(method, path, body string) map[string]any {
		t.Helper()
		code, out := call(method, path, body, "tok")
		if code != 200 {
			t.Fatalf("%s %s: %d %v", method, path, code, out)
		}
		return out
	}
	if code, _ := call("POST", "/lp/admin/group", `{"gid":"x1","name":"X"}`, ""); code != 401 {
		t.Fatalf("no token: %d", code)
	}
	if code, _ := call("POST", "/lp/admin/group", `{"gid":"x1","name":"X"}`, "wrong"); code != 401 {
		t.Fatalf("wrong token: %d", code)
	}
	ok("POST", "/lp/admin/group", `{"gid":"barreiro","name":"Barreiro"}`)
	ok("POST", "/lp/admin/group", `{"gid":"arnhem","name":"Arnhem"}`)
	if code, _ := call("POST", "/lp/admin/group", `{"gid":"arnhem","name":"Again"}`, "tok"); code != 400 {
		t.Fatal("a group was started twice")
	}
	ok("POST", "/lp/admin/member", `{"gid":"barreiro","member":"u0a1b2c3d4e5f6071"}`)
	ok("POST", "/lp/admin/member", `{"gid":"barreiro","member":"u1111111111111111"}`)
	ok("POST", "/lp/admin/member", `{"gid":"arnhem","member":"u2222222222222222"}`)
	if _, err := h.RunUD(); err != nil {
		t.Fatal(err)
	}
	bal := ok("GET", "/lp/admin/balance?gid=barreiro&member=u0a1b2c3d4e5f6071", "")
	if bal["balance"].(float64) <= 0 || bal["active"] != true {
		t.Fatalf("balance after a dividend: %v", bal)
	}
	ok("POST", "/lp/admin/pay", `{"gid":"barreiro","from":"u0a1b2c3d4e5f6071","to":"u1111111111111111","amount":100000}`)
	if code, out := call("POST", "/lp/admin/pay", `{"gid":"barreiro","from":"u1111111111111111","to":"u0a1b2c3d4e5f6071","amount":999999999999}`, "tok"); code != 400 {
		t.Fatalf("an overdraft went through: %v", out)
	}
	ok("POST", "/lp/admin/link", `{"a":"barreiro","b":"arnhem"}`)
	waitFor(t, "the link", func() bool { return h.Group("barreiro").ContactActive(h.Base("arnhem")) })
	req, _ := http.NewRequest("GET", srv.URL+"/lp/admin/links?gid=barreiro", nil)
	req.Header.Set("Authorization", "Bearer tok")
	lres, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var links []map[string]string
	json.NewDecoder(lres.Body).Decode(&links)
	if len(links) != 1 || links[0]["gid"] != "arnhem" || links[0]["name"] != "Arnhem" {
		t.Fatalf("links: %v", links)
	}
	q := ok("GET", "/lp/admin/quote?from_gid=barreiro&to_gid=arnhem&amount=100000", "")
	if q["dst"].(float64) <= 0 || q["dst"].(float64) > 100000 {
		t.Fatalf("quote at UD parity, a small amount: %v", q)
	}
	if code, _ := call("GET", "/lp/admin/quote?from_gid=barreiro&to_gid=nowhere&amount=1", "", "tok"); code != 400 {
		t.Fatal("a quote between unlinked groups")
	}
	before := h.Group("arnhem").Balance(ledger.MemberPrefix + "u2222222222222222")
	tr := ok("POST", "/lp/admin/transfer", `{"from_gid":"barreiro","from":"u0a1b2c3d4e5f6071","to_gid":"arnhem","to":"u2222222222222222","amount":100000}`)
	waitFor(t, "settled", func() bool {
		return ok("GET", "/lp/admin/transfer?gid=barreiro&id="+tr["id"].(string), "")["state"] == "SETTLED"
	})
	if got := h.Group("arnhem").Balance(ledger.MemberPrefix+"u2222222222222222") - before; float64(got) != q["dst"].(float64) {
		t.Fatalf("the payee got %d, the quote said %v", got, q["dst"])
	}
	// Moving: u0a1 moves everything from barreiro to a new account in arnhem.
	ok("POST", "/lp/admin/member", `{"gid":"arnhem","member":"u0a1b2c3d4e5f6071"}`)
	mv := ok("POST", "/lp/admin/move", `{"from_gid":"barreiro","member":"u0a1b2c3d4e5f6071","to_gid":"arnhem","to":"u0a1b2c3d4e5f6071"}`)
	if mv["moved"].(float64) <= 0 || h.Group("barreiro").MemberActive("u0a1b2c3d4e5f6071") || h.Group("barreiro").Balance(ledger.MemberPrefix+"u0a1b2c3d4e5f6071") != 0 {
		t.Fatalf("move: %v", mv)
	}
	if h.Group("arnhem").Balance(ledger.MemberPrefix+"u0a1b2c3d4e5f6071") <= 0 {
		t.Fatal("nothing arrived in arnhem")
	}
	// Leaving: give to the other member; then leaving again fails.
	ok("POST", "/lp/admin/member", `{"gid":"barreiro","member":"u3333333333333333"}`)
	ok("POST", "/lp/admin/leave", `{"gid":"barreiro","member":"u1111111111111111","mode":"give","to":"u3333333333333333"}`)
	if code, _ := call("POST", "/lp/admin/leave", `{"gid":"barreiro","member":"u1111111111111111","mode":"destroy"}`, "tok"); code != 400 {
		t.Fatal("left twice")
	}
	// Rejoining reactivates the same member; joining twice while active fails.
	if out := ok("POST", "/lp/admin/member", `{"gid":"barreiro","member":"u1111111111111111"}`); out["rejoined"] != true || !h.Group("barreiro").MemberActive("u1111111111111111") {
		t.Fatalf("rejoin: %v", out)
	}
	if code, _ := call("POST", "/lp/admin/member", `{"gid":"barreiro","member":"u1111111111111111"}`, "tok"); code != 400 {
		t.Fatal("joined twice while active")
	}
	if code, _ := call("POST", "/lp/admin/move", `{"from_gid":"barreiro","member":"u3333333333333333","to_gid":"arnhem","to":"nobody"}`, "tok"); code != 400 {
		t.Fatal("moved to an account that doesn't exist")
	}
	res, err := http.Get(srv.URL + "/lp/g/arnhem/.well-known/liquiditypub")
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("group identity: %v %v", res.StatusCode, err)
	}
	var doc map[string]any
	json.NewDecoder(res.Body).Decode(&doc)
	if nd, _ := doc["node"].(map[string]any); nd["base"] != h.Base("arnhem") {
		t.Fatalf("identity doc: %v", doc["node"])
	}
	if res, _ := http.Get(srv.URL + "/lp/g/nowhere/.well-known/liquiditypub"); res.StatusCode != 404 {
		t.Fatal("an unknown group answered")
	}
}
