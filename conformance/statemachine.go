package conformance

import "fmt"

// Transfer state machine (PROTOCOL §7.1).
//
// States: NONE, PROPOSED, ACCEPTED, COMMITTED, SETTLED, REJECTED, ABORTED, EXPIRED.
// Events: propose, accept, reject, commit, receipt, abort, expire.
//
// Notable rules encoded here:
//   - abort is valid only before the sender's commit is appended;
//   - a duplicate commit on COMMITTED/SETTLED is idempotent (re-answer with the
//     same receipt), never re-applied;
//   - there is no expiry after commit: a committed transfer resolves only by
//     (retried) receipt.
var transitions = map[string]map[string]string{
	"NONE": {
		"propose": "PROPOSED",
	},
	"PROPOSED": {
		"accept": "ACCEPTED",
		"reject": "REJECTED",
		"abort":  "ABORTED",
		"expire": "EXPIRED",
	},
	"ACCEPTED": {
		"commit": "COMMITTED",
		"abort":  "ABORTED",
		"expire": "EXPIRED",
	},
	"COMMITTED": {
		"receipt": "SETTLED",
		"commit":  "COMMITTED", // idempotent retry
	},
	"SETTLED": {
		"commit": "SETTLED", // late retry: re-send receipt
	},
}

// Transition returns the next state, or an error for an invalid (state, event)
// pair. Terminal states REJECTED, ABORTED, EXPIRED accept no events.
func Transition(state, event string) (string, error) {
	if next, ok := transitions[state][event]; ok {
		return next, nil
	}
	return "", fmt.Errorf("invalid transition: %s + %s", state, event)
}
