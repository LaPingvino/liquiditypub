package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"cloud.google.com/go/datastore"
)

// DSStore keeps a node's snapshot in Datastore (lpnode/store.Store).
//
// A snapshot is one JSON object whose "ledger" array only ever grows (the
// ledger is append-only, PROTOCOL §9.2). Storing it as one blob would hit the
// 1 MiB entity limit within months and rewrite the whole history on every
// payment, so it is taken apart:
//
//   - each ledger record is its own entity (LPRecord, written once);
//   - the rest of the snapshot (keys, members, contacts, transfers, outboxes)
//     goes into LPStateChunk entities of at most chunkSize bytes;
//   - LPState says how many of each there are, with a Version that makes this
//     process the single writer: a Save only succeeds on the version it last
//     saw, so a second writer (two instances during a deploy) gets ErrConflict
//     instead of silently forking the ledger.
//
// Everything lives in the namespace "lp", keyed by the node's id.
type DSStore struct {
	db   *datastore.Client
	node string

	mu      sync.Mutex
	version int64 // LPState.Version this process last loaded or saved
	records int   // ledger records already stored
}

// ErrConflict: another writer saved since this process loaded.
var ErrConflict = errors.New("lp store: another writer saved meanwhile")

const (
	ns        = "lp"
	chunkSize = 900_000
	putBatch  = 500
)

type lpState struct {
	Version   int64     `datastore:"version,noindex"`
	Records   int       `datastore:"records,noindex"`
	Chunks    int       `datastore:"chunks,noindex"`
	UpdatedAt time.Time `datastore:"updated_at,noindex"`
}

type lpBlob struct {
	Data []byte `datastore:"data,noindex"`
}

// NewDSStore stores the node with id node (one per hosted node).
func NewDSStore(db *datastore.Client, node string) *DSStore { return &DSStore{db: db, node: node} }

func (s *DSStore) stateKey() *datastore.Key {
	k := datastore.NameKey("LPState", s.node, nil)
	k.Namespace = ns
	return k
}

func (s *DSStore) recordKey(i int) *datastore.Key {
	k := datastore.NameKey("LPRecord", fmt.Sprintf("%s/%012d", s.node, i), nil)
	k.Namespace = ns
	return k
}

func (s *DSStore) chunkKey(i int) *datastore.Key {
	k := datastore.NameKey("LPStateChunk", fmt.Sprintf("%s/%04d", s.node, i), nil)
	k.Namespace = ns
	return k
}

// Load returns the stored snapshot, or (nil, nil) for a new node.
func (s *DSStore) Load() ([]byte, error) {
	ctx := context.Background()
	s.mu.Lock()
	defer s.mu.Unlock()
	var st lpState
	if err := s.db.Get(ctx, s.stateKey(), &st); err == datastore.ErrNoSuchEntity {
		s.version, s.records = 0, 0
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	rest, err := s.getBlobs(ctx, st.Chunks, s.chunkKey)
	if err != nil {
		return nil, fmt.Errorf("lp store: state chunks: %w", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(join(rest), &m); err != nil {
		return nil, fmt.Errorf("lp store: state: %w", err)
	}
	recs, err := s.getBlobs(ctx, st.Records, s.recordKey)
	if err != nil {
		return nil, fmt.Errorf("lp store: ledger: %w", err)
	}
	raw := make([]json.RawMessage, len(recs))
	for i, r := range recs {
		raw[i] = r
	}
	if m["ledger"], err = json.Marshal(raw); err != nil {
		return nil, err
	}
	s.version, s.records = st.Version, st.Records
	return json.Marshal(m)
}

// Save stores a snapshot: the new ledger records, then the rest of the state
// and its counts in one transaction guarded by the version.
func (s *DSStore) Save(data []byte) error {
	ctx := context.Background()
	s.mu.Lock()
	defer s.mu.Unlock()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	var recs []json.RawMessage
	if len(m["ledger"]) > 0 {
		if err := json.Unmarshal(m["ledger"], &recs); err != nil {
			return err
		}
	}
	if len(recs) < s.records {
		return fmt.Errorf("lp store: ledger shrank from %d to %d records (it is append-only)", s.records, len(recs))
	}
	delete(m, "ledger")
	rest, err := json.Marshal(m)
	if err != nil {
		return err
	}
	// New records first, outside the transaction (a dividend can add hundreds
	// at once). Until LPState counts them they are invisible to Load, and a
	// conflicting writer overwrites the same keys, so this is safe.
	for i := s.records; i < len(recs); i += putBatch {
		end := min(i+putBatch, len(recs))
		keys := make([]*datastore.Key, 0, end-i)
		vals := make([]*lpBlob, 0, end-i)
		for j := i; j < end; j++ {
			keys = append(keys, s.recordKey(j))
			vals = append(vals, &lpBlob{Data: recs[j]})
		}
		if _, err := s.db.PutMulti(ctx, keys, vals); err != nil {
			return fmt.Errorf("lp store: ledger records: %w", err)
		}
	}
	var chunks [][]byte
	for i := 0; i < len(rest); i += chunkSize {
		chunks = append(chunks, rest[i:min(i+chunkSize, len(rest))])
	}
	var newVersion int64
	_, err = s.db.RunInTransaction(ctx, func(tx *datastore.Transaction) error {
		var st lpState
		if err := tx.Get(s.stateKey(), &st); err != nil && err != datastore.ErrNoSuchEntity {
			return err
		}
		if st.Version != s.version {
			return ErrConflict
		}
		for i, c := range chunks {
			if _, err := tx.Put(s.chunkKey(i), &lpBlob{Data: c}); err != nil {
				return err
			}
		}
		for i := len(chunks); i < st.Chunks; i++ { // the state got smaller
			if err := tx.Delete(s.chunkKey(i)); err != nil {
				return err
			}
		}
		newVersion = st.Version + 1
		_, err := tx.Put(s.stateKey(), &lpState{Version: newVersion, Records: len(recs), Chunks: len(chunks), UpdatedAt: time.Now().UTC()})
		return err
	})
	if err != nil {
		return err
	}
	s.version, s.records = newVersion, len(recs)
	return nil
}

func (s *DSStore) getBlobs(ctx context.Context, n int, key func(int) *datastore.Key) ([][]byte, error) {
	out := make([][]byte, 0, n)
	for i := 0; i < n; i += 1000 {
		end := min(i+1000, n)
		keys := make([]*datastore.Key, 0, end-i)
		for j := i; j < end; j++ {
			keys = append(keys, key(j))
		}
		vals := make([]lpBlob, len(keys))
		if err := s.db.GetMulti(ctx, keys, vals); err != nil {
			return nil, err
		}
		for _, v := range vals {
			out = append(out, v.Data)
		}
	}
	return out, nil
}

func join(bs [][]byte) []byte {
	var out []byte
	for _, b := range bs {
		out = append(out, b...)
	}
	return out
}

// adminToken returns the node's admin token, made on first use and kept in
// Datastore (LPConfig/<node>), never in app.yaml or git. Kafumu's app reads it
// from the same project to call the admin API.
func adminToken(ctx context.Context, db *datastore.Client, node string) (string, error) {
	k := datastore.NameKey("LPConfig", node, nil)
	k.Namespace = ns
	var c struct {
		AdminToken string `datastore:"admin_token,noindex"`
	}
	_, err := db.RunInTransaction(ctx, func(tx *datastore.Transaction) error {
		if err := tx.Get(k, &c); err == nil && c.AdminToken != "" {
			return nil
		} else if err != nil && err != datastore.ErrNoSuchEntity {
			return err
		}
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		c.AdminToken = hex.EncodeToString(b)
		_, err := tx.Put(k, &c)
		return err
	})
	return c.AdminToken, err
}
