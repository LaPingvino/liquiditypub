package lpnode

import (
	"fmt"
	"testing"
)

// recSender records the seqs it successfully delivers and can be told to fail a
// given seq a number of times before succeeding.
type recSender struct {
	delivered []int64
	failSeq   map[int64]int
}

func (r *recSender) Deliver(_ string, env map[string]any) error {
	seq, _ := asInt(env["seq"])
	if r.failSeq[seq] > 0 {
		r.failSeq[seq]--
		return fmt.Errorf("flaky")
	}
	r.delivered = append(r.delivered, seq)
	return nil
}
func (r *recSender) FetchIdentity(string) (map[string]any, error)         { return nil, nil }
func (r *recSender) FetchOutbox(string, string) ([]map[string]any, error) { return nil, nil }
func (r *recSender) FetchCheckpoint(string) (map[string]any, error)       { return nil, nil }

// TestPushHostStopsOnFailure proves the delivery cursor never lets a later seq
// overtake an earlier one that failed: a failure stops the run, and the next run
// retries from the failed seq — so the receiver never sees a gap it would reject
// as stale and pruning would then drop (transport findings 5 & 6).
func TestPushHostStopsOnFailure(t *testing.T) {
	env := func(seq int64) map[string]any {
		return map[string]any{"seq": seq, "to": "https://b.example", "type": "x"}
	}
	n := &Node{
		cfg:    Config{Base: "https://a.example"},
		outbox: map[string][]map[string]any{"b.example": {env(1), env(2), env(3)}},
		pushed: map[string]int64{},
	}
	s := &recSender{failSeq: map[int64]int{2: 1}} // seq 2 fails once

	n.pushHost(s, "b.example") // delivers 1, fails on 2, stops (does NOT send 3)
	if got := fmt.Sprint(s.delivered); got != "[1]" {
		t.Fatalf("after first run delivered %s, want [1] (must not skip past the failed seq)", got)
	}

	n.pushHost(s, "b.example") // retries 2 (now succeeds), then 3
	if got := fmt.Sprint(s.delivered); got != "[1 2 3]" {
		t.Fatalf("after retry delivered %s, want [1 2 3] (in order, no gap)", got)
	}

	n.pushHost(s, "b.example") // nothing left to push
	if got := fmt.Sprint(s.delivered); got != "[1 2 3]" {
		t.Fatalf("re-push resent entries: %s", got)
	}
}
