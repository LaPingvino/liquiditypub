// Package runner is the black-box federation conformance checker: it takes
// the base URLs of one or two LIVE nodes (any implementation) and verifies
// the read-side contact surface. Active-transfer conformance (propose/accept/
// commit against a peer) is exercised by the PoC integration harness once an
// implementation exists; the checks here need nothing but HTTP GET.
package runner

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Result struct {
	Check string
	OK    bool
	Note  string
}

type IdentityDoc struct {
	Version string `json:"liquiditypub"`
	Node    struct {
		Name string `json:"name"`
		Base string `json:"base"`
	} `json:"node"`
	Currency struct {
		Name       string `json:"name"`
		Symbol     string `json:"symbol"`
		MicroUnits int64  `json:"micro_units"`
	} `json:"currency"`
	Keys []struct {
		ID        string `json:"id"`
		Alg       string `json:"alg"`
		PublicKey string `json:"public_key"`
	} `json:"keys"`
	Endpoints    map[string]string `json:"endpoints"`
	Capabilities []string          `json:"capabilities"`
	Transparency string            `json:"transparency"`
}

type Checkpoint struct {
	LogSeq   int64 `json:"log_seq"`
	Contacts []struct {
		Peer            string `json:"peer"`
		ContactID       string `json:"contact_id"`
		PeerReserveHere int64  `json:"peer_reserve_here"`
		OpSeq           int64  `json:"op_seq"`
		ChannelRoot     string `json:"channel_root"`
	} `json:"contacts"`
}

var client = &http.Client{Timeout: 15 * time.Second}

func getJSON(url string, v any) error {
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// CheckNode validates one node's read surface: identity document shape and a
// public, well-formed checkpoint.
func CheckNode(base string) ([]Result, *IdentityDoc, *Checkpoint) {
	var results []Result
	add := func(check string, ok bool, note string) {
		results = append(results, Result{check, ok, note})
	}

	var id IdentityDoc
	err := getJSON(base+"/.well-known/liquiditypub", &id)
	add("identity: fetch", err == nil, errNote(err))
	if err != nil {
		return results, nil, nil
	}
	add("identity: version 0.2", id.Version == "0.2", id.Version)
	add("identity: node name + base", id.Node.Name != "" && id.Node.Base != "", "")
	add("identity: currency micro_units = 1000000", id.Currency.MicroUnits == 1_000_000, "")
	add("identity: at least one ed25519 key", hasEd25519(id), "")
	add("identity: checkpoint endpoint", id.Endpoints["checkpoint"] != "", "")

	var cp Checkpoint
	err = getJSON(base+id.Endpoints["checkpoint"], &cp)
	add("checkpoint: fetch (public in every transparency mode)", err == nil, errNote(err))
	if err != nil {
		return results, &id, nil
	}
	for _, c := range cp.Contacts {
		add("checkpoint: reserve non-negative for "+c.Peer, c.PeerReserveHere >= 0,
			fmt.Sprintf("%d", c.PeerReserveHere))
	}
	return results, &id, &cp
}

// CrossCheck compares the shared contacts of two nodes: matching contact ids
// must agree on op_seq and channel_root (PROTOCOL §8.3) — the reconciliation
// invariant, observable entirely from public data.
func CrossCheck(cpA, cpB *Checkpoint) []Result {
	var results []Result
	for _, a := range cpA.Contacts {
		for _, b := range cpB.Contacts {
			if a.ContactID != b.ContactID {
				continue
			}
			ok := a.ChannelRoot == b.ChannelRoot && a.OpSeq == b.OpSeq
			note := fmt.Sprintf("op_seq %d/%d", a.OpSeq, b.OpSeq)
			if a.ChannelRoot != b.ChannelRoot {
				note += " CHANNEL ROOT DIVERGENCE (freeze the contact; histories are signed and attributable)"
			}
			results = append(results, Result{"cross-check contact " + a.ContactID, ok, note})
		}
	}
	if len(results) == 0 {
		results = append(results, Result{"cross-check", true, "no shared contacts found"})
	}
	return results
}

func hasEd25519(id IdentityDoc) bool {
	for _, k := range id.Keys {
		if k.Alg == "ed25519" && k.PublicKey != "" {
			return true
		}
	}
	return false
}

func errNote(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
