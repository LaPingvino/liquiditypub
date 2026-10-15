package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	tr := ok("POST", "/lp/admin/transfer", `{"from_gid":"barreiro","from":"u0a1b2c3d4e5f6071","to_gid":"arnhem","to":"u2222222222222222","amount":100000}`)
	waitFor(t, "settled", func() bool {
		return ok("GET", "/lp/admin/transfer?gid=barreiro&id="+tr["id"].(string), "")["state"] == "SETTLED"
	})
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
