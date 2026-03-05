# LiquidityPub Federation Protocol

**Version:** 0.1-draft
**Status:** Living specification — not yet stable
**Repository:** https://github.com/liquiditypub/liquiditypub

---

## Overview

LiquidityPub is a federated local money system. Each **node** runs its own community currency with configurable issuance rules. Nodes federate by discovering each other, establishing trust, and exchanging value across currency boundaries.

This document specifies:
1. The **Node Identity Document** format (`/.well-known/liquiditypub`)
2. The **Inbox message** format (`POST /api/inbox`)
3. The **message types** for federation
4. **Signature scheme** for authenticated messages

---

## 1. Node Identity Document

Every LiquidityPub node MUST expose a JSON document at:

```
GET /.well-known/liquiditypub
Content-Type: application/json
```

### Schema

```json
{
  "liquiditypub": "0.1",
  "node": {
    "name": "Sunflower Collective",
    "description": "A local mutual credit community",
    "inbox": "https://example.com/api/inbox"
  },
  "currency": {
    "name": "Sunflower Credits",
    "symbol": "☀",
    "issuance": "periodic"
  },
  "stats": {
    "active_members": 42
  },
  "public_key": "-----BEGIN PUBLIC KEY-----\n...\n-----END PUBLIC KEY-----"
}
```

### Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `liquiditypub` | string | yes | Protocol version (currently `"0.1"`) |
| `node.name` | string | yes | Human-readable node name |
| `node.description` | string | no | Node description |
| `node.inbox` | URL | yes | Full URL of the inbox endpoint |
| `currency.name` | string | yes | Name of the local currency |
| `currency.symbol` | string | yes | Currency symbol (1-4 chars) |
| `currency.issuance` | enum | yes | `"periodic"`, `"manual"`, or `"demurrage"` |
| `stats.active_members` | integer | no | Count of active members |
| `public_key` | string | yes | PEM-encoded RSA/Ed25519 public key for signature verification |

---

## 2. Inbox Endpoint

Federated messages are sent to the inbox:

```
POST /api/inbox
Content-Type: application/json
Signature: keyId="https://sender.example.com/.well-known/liquiditypub",algorithm="rsa-sha256",headers="(request-target) host date digest",signature="..."
```

### Envelope Format

All inbox messages share a common envelope:

```json
{
  "type": "ping",
  "from": "https://sender.example.com",
  "timestamp": "2026-03-05T12:00:00Z",
  "id": "urn:uuid:550e8400-e29b-41d4-a716-446655440000",
  "signature": "<base64-encoded-signature>",
  "payload": { }
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `type` | string | yes | Message type (see §3) |
| `from` | URL | yes | Sender's node base URL |
| `timestamp` | ISO8601 | yes | Message creation time (UTC) |
| `id` | URN | yes | Globally unique message identifier |
| `signature` | string | yes | Base64-encoded signature over canonical message |
| `payload` | object | yes | Message-type-specific data |

---

## 3. Message Types

### 3.1 `ping`

Health check / reachability probe. No payload required.

**Response:**
```json
{ "status": "ok", "node": "Sunflower Collective", "pong": true }
```

---

### 3.2 `exchange_request`

Request to exchange value between a member of the sender node and a member of the recipient node.

**Payload:**
```json
{
  "from_member": "alice@sender.example.com",
  "to_member":   "bob@recipient.example.com",
  "amount":      1000000,
  "currency":    "Sunflower Credits",
  "note":        "Shared resource payment"
}
```

- `amount` is in **micro-units** (integer). 1 credit = 1,000,000 micro-units.
- Exchange rate negotiation is out-of-scope for v0.1; nodes must have a pre-established trust relationship with an agreed rate.

**Response (202 Accepted):**
```json
{
  "status": "accepted",
  "exchange_id": "urn:uuid:..."
}
```

---

### 3.3 `exchange_confirm`

Confirms that the receiver processed an accepted exchange request.

**Payload:**
```json
{
  "exchange_id": "urn:uuid:...",
  "status":      "settled",
  "settled_at":  "2026-03-05T12:01:00Z"
}
```

---

### 3.4 `member_lookup`

Query whether a member identifier is known at a node.

**Payload:**
```json
{
  "member": "bob@recipient.example.com"
}
```

**Response:**
```json
{
  "found":        true,
  "display_name": "Bob Smith",
  "node":         "https://recipient.example.com"
}
```

Privacy note: nodes SHOULD require a trust relationship before answering member_lookup requests.

---

## 4. Signature Scheme

LiquidityPub uses **HTTP Signatures** (draft-cavage-http-signatures) for authenticating inbox messages.

### Signing

The sender MUST:
1. Include a `Digest` header: `SHA-256=<base64(sha256(body))>`
2. Include a `Date` header in RFC 7231 format
3. Sign the string `(request-target) host date digest` using the node's private key
4. Include the `Signature` header referencing the key at `/.well-known/liquiditypub`

### Verification

The receiver MUST:
1. Fetch the sender's identity document from `from` + `/.well-known/liquiditypub`
2. Verify the signature against the `public_key` in that document
3. Reject messages with `timestamp` older than 5 minutes (replay protection)
4. Reject messages with duplicate `id` values (idempotency cache, 24h window)

> **PoC Note:** Signature verification is scaffolded but not enforced in v0.1. Do not use in production without implementing this.

---

## 5. Trust Model

Trust between nodes is expressed as an integer `trust_level` (0–100):

| Range | Meaning |
|-------|---------|
| 0 | Blocked / no interaction |
| 1–25 | Discovery only (can ping) |
| 26–75 | Member lookup permitted |
| 76–99 | Exchange requests permitted |
| 100 | Full trust (future: automatic settlement) |

Trust is asymmetric: node A may trust node B at level 80 while B trusts A at level 30.

---

## 6. Ledger Convention

All monetary amounts are stored and transmitted as **micro-units** (integer):

- `1 credit = 1,000,000 micro-units`
- No floating-point arithmetic is used anywhere
- Double-entry bookkeeping: every transaction creates two ledger entries summing to zero

---

## 7. Roadmap (Post-PoC)

- [ ] Full HTTP Signature verification
- [ ] Exchange rate negotiation protocol
- [ ] Demurrage issuance type
- [ ] Member identity portability (move wallet between nodes)
- [ ] Multi-hop payments (routing across trust graph)
- [ ] OpenAPI spec for all endpoints

---

*LiquidityPub v0.1-draft — March 2026*
