# LiquidityPub — Design Foundations

**Status:** Working notes for v0.2 — July 2026
**Companion:** [PROTOCOL.md](PROTOCOL.md) (the normative spec these notes justify)

This document records the *reasoning*: what LiquidityPub is, the decisions that shape
the protocol, the alternatives considered, and the questions still open. The spec says
*what*; this says *why*.

---

## 1. What this is

**LiquidityPub is ActivityPub for money.** Each community — a neighborhood, a coop, a
collective — runs a **node**. A node is a currency plus a monetary policy: it issues
its own credits (UBI/dividend, manual grants, later demurrage) and keeps a double-entry
ledger for its members. Nodes federate the way Mastodon instances do: they discover
each other, establish trust, and exchange value across currency boundaries.

The lineage is the **libre currency / monnaie libre** movement: Free Money in Silvio
Gesell's sense and, much more directly, the **Théorie Relative de la Monnaie (TRM)**
behind Duniter and Ğ1 — money issued symmetrically to every member as a **Universal
Dividend**, with no privileged issuer. LiquidityPub is that idea **re-founded on cheap
federated servers instead of a blockchain**, so that any local community can stand one
up in an afternoon. What Duniter buys with a blockchain and a global Web of Trust, we
buy with community-run nodes and bilateral credit limits (§4, §6).

Two promises, in order of importance:

1. **Within a community:** a boring, correct, auditable ledger. Integer arithmetic,
   double-entry, a conservation invariant that anyone can check.
2. **Between communities:** exchange with *bounded, chosen* risk. No node can lose
   more to a peer than the credit it explicitly extended.

And two design values we keep on purpose:

**Radically cheap to run.** If running a node requires a DevOps team, the communities
this is for won't run one.

**The protocol is a membrane, not a rulebook.** It specifies only the *contact
surface* — identity, messages, contacts, transfers, published checkpoints. How a node
works inside is deliberately unconstrained: weird internal implementations, novel
issuance schemes, community funds with multiplied dividends, entirely different ledger
designs — all welcome, none requiring protocol changes. Peers never audit internals;
they read what a node *publishes*, compensate for how it behaves through exchange
values and trust, and if a system fails, they stop accepting that coin. Discipline
lives at the interface; freedom and growth of insight live inside the node.

---

## 2. Why federation, not blockchain

Community currencies have a trusted operator *already* — the person who keeps the LETS
books, the coop treasurer. Blockchains exist to remove the trusted operator; they solve
a trustlessness problem these communities do not have, and charge for it in UX (keys,
gas, wallets), infrastructure, and coupling to speculative ecosystems. Projects like
Circles UBI and Trustlines Network demonstrated both that the *economics* (trust lines,
UBI, community credit) work and that the *chain* is the adoption bottleneck.

What we do take from that world is the cheap part: **signed, hash-linked, append-only
logs.** Tamper-evidence does not require consensus. A node that publishes a signed log
cannot silently rewrite history — members and peer nodes would catch it. That single
property does most of the honest-operator enforcement a chain would give us, at the
cost of a signature per transaction.

---

## 3. The trust ladder: who trusts whom, for what

**Members → their node.** Members trust the node for custody and honest bookkeeping.
Mitigations, in increasing strength:
- The node publishes a hash-linked, signed transaction log → history rewriting is
  detectable (tamper-*evidence*, not tamper-*proofness* — that's enough here).
- Optionally, members hold their own keys and sign their own transactions → the node
  cannot *forge* member activity, only censor it. This is the **sovereign profile**;
  node-held keys are the **custodial profile**. The ledger format is identical, so a
  custodial node can upgrade later. Passkeys (WebAuthn) make member-held keys plausible
  for non-technical humans in 2026 — no seed phrases. Crucially, none of this crosses
  the membrane: cross-node transfers are node-vouched and collateralized by *node*
  reserves, so a peer never needs to know how (or whether) members hold keys. Custody
  is internal and can grow with the technology, node by node; the first
  implementations assume custodial.

**Node → node.** Peers trust each other for exactly one economic thing: *bounded
exposure*, expressed as a credit limit on a trust line (§4). Authenticity comes from
node keys; availability is never assumed (messages are idempotent and retryable).

**Nobody → global anything.** There is no global ledger, no global identity, no global
exchange rate. Every cross-community relationship is bilateral and explicit.

---

## 4. Settlement: the fulcrum decision

When Alice on node R pays Bob on node H, *something* must account for the fact that R's
credits and H's credits are different currencies with different issuers. Options
considered:

**(a) Naive burn-and-mint.** R burns Alice's credits, H mints for Bob, nobody records
the inter-node position. Rejected: this is unbounded inflation export. A malicious or
just badly-run node can drain the federation. It isn't a simpler version of the answer;
it's the absence of an answer.

**(b) Forced parity at trust points (the Circles approach).** Declare all community
credits equal in value and let trust contacts gate acceptance. Rejected: it imports the
false assumption that every community's currency carries equal real value, which is
both economically wrong (communities differ in what backs their credit) and gameable —
mint-and-dump against the parity is exactly the CRC-farming pattern. The relative value
of two community currencies is *information*; a protocol that erases it invites
arbitrage against its honest users.

**(c) Fixed-rate bilateral clearing with credit limits.** Mirrored clearing accounts,
correspondent-banking style (WIR and Sardex run this at real scale): the receiving node
issues local currency against a claim on the sender, capped by a unilaterally set
credit limit, at a negotiated fixed rate. This was this document's original choice.
Rejected as a *protocol mode* for two structural irritations — the receiving side
*does* issue money against an IOU (exposure by construction), and the rate is a
hostage of manual renegotiation — and, decisively, because it needs no protocol
support at all: a node that wants peg-like behavior can imitate it on its own side of
a pool, defending a target rate with reserve adjustments, currency-board style (see d).

**(d) Reciprocal reserves with a per-contact exchange — chosen.** Each node holds a
real wallet *on the peer's ledger* — a virtual participation in the other local system.
Those two reciprocal reserves form the liquidity of the **contact point**, and they
price exchanges like a constant-product market maker: the rate at each contact is
*relative, local, and maintained by the flows themselves* — a mini exchange built into
the protocol instead of a negotiated constant.

Worked example. Riverside (currency ʀ) and Hilltop (currency ʜ) open a contact. Each
seeds the other's wallet: Riverside issues 500ʀ to `node:hilltop` on its ledger,
Hilltop issues 1,000ʜ to `node:riverside` on its own. The seed ratio sets the opening
rate: 1ʀ ≈ 2ʜ. (Between UD nodes, UD parity is the natural default *suggestion* for
this ratio — see §5.)

*Alice@Riverside pays Bob@Hilltop 10ʀ:* with reserves (Rʙ = 500ʀ, Hᴀ = 1000ʜ) and
constant product k = Rʙ × Hᴀ, the payout is `y = Hᴀ·x/(Rʙ+x)` = 1000·10/510 ≈ 19.6ʜ
(2% price impact on a small pool).

| Ledger | Entries | Reserve after |
|---|---|---|
| Riverside | alice −10ʀ, node:hilltop +10ʀ | Hilltop holds 510ʀ |
| Hilltop | node:riverside −19.6ʜ, bob +19.6ʜ | Riverside holds 980.4ʜ |

Both legs are **ordinary internal payments** — no special machinery inside either
ledger, everything auditable in the two published logs, and the pool invariant
(k preserved by trades, changed only by explicit liquidity events) publicly checkable.
The new spot rate is 980.4/510 ≈ 1.92ʜ/ʀ: one-way flow has moved the price against
itself. Reverse flow moves it back.

Why this beats (c):

- **No unbacked issuance, ever.** Bob is paid from ʜ that already exists. Money is
  created only by each node's own issuance policy, never at contact points.
- **Collateral symmetry.** Each community's maximum loss is its own reserve held at the
  peer — and a peer that confiscates it forfeits its own reserve held at you. Seeded at
  equal value, net theft at opening ≈ 0. This replaces the credit limit as the exposure
  bound, and it's mutual by construction.
- **Imbalance is a price, not a cliff.** Persistent one-way flow makes its own rate
  worse continuously — a live signal the communities can respond to (top up reserves,
  trade back, or let the price stand) instead of a hard stop plus renegotiation.
- **Sybil self-throttling.** A sockpuppet-UD node dumping junk currency drives its own
  price toward zero as it drains the pool; extraction is capped by the reserve *and*
  decelerating all the way there (§6).

Costs, honestly: pools must be **seeded** before the first payment (seed size and
source are node decisions — the seed is simultaneously opening liquidity and
collateral); small pools mean visible slippage; and pool state must be strictly
serialized per contact (one in-flight operation at a time — trivial at community
scale, specified in the protocol). The protocol is **pool-only**: fixed rates, pegs,
crawling bands, whatever — any rate *policy* is implementable node-side as a reserve
management strategy over the same pool primitive, so none of them need to exist in the
protocol. This is the membrane principle (§1) applied to exchange-rate regimes.

The invariants, which are the heart of the protocol:

- **Local conservation** (per node): `SUM(all ledger entries) = 0`, always.
- **Contact consistency** (per contact): both sides applied the same operations in the
  same order — tracked as a **channel hash** over committed operations, published in
  both sides' signed checkpoints. Same hash ⇒ same history ⇒ same reserves ⇒ same
  price, by construction. Divergence freezes the contact, attributably.

**(e) Multi-hop routing — deliberately deferred.** RipplePay and Interledger route
payments across chains of trust relationships. Contact pools make this *more* natural
later, not less — a multi-hop payment is a chained swap through consecutive pools, and
prices compose — but pathfinding and cross-ledger atomicity are still hard, and putting
them in v1 is how the project stalls. ILP's two-phase semantics are the model to steal
when we get there.

---

## 5. Monetary policy: the Universal Dividend

The reference monetary policy — the reason the project exists — is the TRM's: every
member receives an equal **Universal Dividend (UD)**, and issuance is calibrated so the
money supply grows at a chosen rate *c* per year (Ğ1 uses ≈10%/year), distributed
equally. The consequences the TRM derives from this are the ones we want:

- **Symmetry across space and time.** Newcomers are not structurally disadvantaged;
  an early member's hoard shrinks *relative to* the growing supply. Gesell's Freigeld
  achieves this with explicit demurrage (nominal decay); UD growth achieves the same
  thing in relative terms with no confiscatory bookkeeping — balances quietly melt
  against the UD instead of being taxed. (This is why explicit demurrage drops from
  "roadmap item" to "probably unnecessary": UD growth *is* the gentler demurrage.)
- **The relative view.** The honest unit of account is the UD, not the raw unit.
  "30 UD" means the same thing in year 1 and year 20, on any node with the same *c*.
  Nodes should offer UD-denominated display, and the protocol should carry the current
  UD value so wallets can render either view.
- **UD parity as anchor, not law.** If two nodes both run UD issuance, one UD on each
  represents the same nominal thing: one member-period of universal issuance. That
  makes UD-for-UD the *natural default seed ratio* when opening a contact pool (§4d) —
  a principled starting price where strangers would otherwise have none — and the
  natural **display anchor**: with per-contact floating rates, wallets stay
  comprehensible by quoting everything in UDs. But parity is where the market *starts*,
  not where it is held: actual relative value floats per contact, priced by the pool.
  (Forcing parity is the Circles mistake — §4b.) A federation of UD currencies with
  parity-seeded floating contacts is the genuinely new object here; Duniter cannot
  express it, because Ğ1 is a single global currency.

Crucially, per the membrane principle (§1), all of this is the *reference* policy, not
a mandate. A node may direct dividends to a community project fund with its own UD —
even with a multiplier — or to a peer's node wallet as a liquidity drip, or run
something stranger. The protocol only asks that the aggregate be visible (weight
totals, money supply, member count in the identity document and checkpoints), so peers
can compensate for how each node works in their exchange values and trust — and stop
accepting the coin of a system that fails.

On arithmetic: **Ğ1 compatibility is explicitly a non-goal** (Ğ1 is barely compatible
with itself — one of the reasons this project exists). The TRM provides the rationale
for a reference formula, but **experimenting with the arithmetic is part of the
project's purpose**: nodes publish whatever discretization they actually run, and the
federation is the laboratory where variants compete on observed behavior.

## 6. Sybil resistance and the UD problem

A UD per member, plus exchangeability across nodes, creates an obvious attack: spin up
a node full of sockpuppets, collect the dividend, spend it into honest nodes. This is
exactly the problem Duniter's global Web of Trust exists to solve — and the WoT (with
its certification ceremonies, distance rules, and revocation mechanics) is a large part
of what makes libre currency hard to adopt.

Our answer is structurally different and much cheaper, because we have something
Duniter deliberately doesn't: a community operator. **Admission is the node's social
process** — the coop knows its members; that *is* the web of trust, done where trust
actually lives. Across nodes, no global identity is needed — the contact pool (§4d)
prices and collateralizes the risk:

> **Bounded extraction.** A malicious node's maximum extraction from the federation is
> the sum of the reserves its peers placed in pools with it, minus its own reserves
> forfeited at those peers — and pool pricing decelerates the drain all the way to
> that bound, because dumping a junk currency collapses its own rate.

Two reinforcing mechanisms:
- **Observability.** Because ledgers are published as signed logs (§7), a node's
  *actual* issuance behavior is auditable. Peers can see that "SockpuppetCoin" minted
  10× its member count last week, and withdraw pool liquidity accordingly. Monetary
  policy becomes reputation.
- **Prices as risk pricing.** The pool already discounts a suspect currency
  continuously as it is dumped; peers never face a binary trust/block choice — the
  market response is built in, and shrinking the reserve is the escalation.

---

## 7. The ledger as a signed, published log

The canonical ledger is an **append-only, hash-linked log** of transactions. Each entry
references the hash of the previous one; the node signs periodic **checkpoints**
(sequence number, log head hash, reserve balances per contact). This buys, in one
mechanism:

- tamper-evidence against history rewriting (§3);
- cheap peer reconciliation (compare checkpoints against the invariant in §4);
- **static-file reads** — the log and checkpoints are just JSON files (§8);
- provable member statements, and later, portable membership ("here is my signed
  history from my old node").

**Privacy** is the tension: publishing a full ledger conflicts with member privacy.
This becomes a per-node **transparency level**:
- `public` — full log, real usernames (some communities will genuinely want this);
- `pseudonymous` — full log, opaque account ids; the member↔account mapping is private
  to the node (**recommended default**);
- `peers` — log visible to trusted peers only; public sees only checkpoints
  (conservation and issuance totals remain publicly verifiable).

---

## 8. Deployment profiles — including the "no server" question

Decompose what a node must do:

| Capability | Needs |
|---|---|
| Serve identity doc, log, checkpoints, outbox | **Static files** |
| Accept + validate inbound signed messages | Small compute |
| Apply writes (order transactions, enforce limits) | Small compute + storage, single-writer |
| Scheduled issuance | Cron-ish trigger |

The protocol commitment that falls out: **every read is designed to be servable as
static files, and the entire write surface is one operation — "apply this signed
message."** Minimizing the trusted computing base helps every profile, including the
cheapest.

- **Profile A — active server.** A Go single binary + SQLite; and a **PHP + SQLite
  variant as a first-class target, not legacy** — it is the profile for people who
  don't trust the big clouds and want to self-host on any €2 shared host. Supports push
  federation (inbox) and everything else.
- **Profile B — serverless.** Cloudflare Pages Functions + D1 + cron triggers, or
  Google App Engine + Firestore — conceptually and price-wise near-twins (both free at
  community-currency scale; GAE forces a storage abstraction since it has no persistent
  SQLite, which is healthy pressure anyway). "No server you administer" — but code
  still runs server-side; that part is unavoidable for the write path.
- **Profile C — HTML-only, via a clearing house.** A truly client-side-only node is
  impossible: *someone* must order writes and answer transfer negotiations, and pool
  pricing is a live conversation — a node that answers by CI job every few hours makes
  payments feel like postal mail. (An earlier draft tried "repo-as-node" with CI
  validation; it survives only as the *mirror format* below.) Instead, the cheapest
  communities run an **HTML-only node**: the identity document lives on their own
  static host (the root of sovereignty — it names their keys and endpoints), the
  endpoints point at a **clearing house**, and an operator key held by the clearing
  house sits in the `keys` array next to the community's own master key. All live
  operation happens at the clearing house; the community's static host serves
  **backup-like access** — signed checkpoints and log mirrors on infrastructure the
  community controls. Firing your clearing house is one key revocation plus a
  repointed endpoint, and your provable history is already in your own hands.

A **clearing house** is just a Profile A/B node operating several thin nodes under
delegated keys — no new protocol machinery, only a usage pattern of existing fields.
Two properties fall out: transfers between communities hosted at the same clearing
house clear internally with zero federation latency (multi-hop lite, through the
service layer rather than the protocol); and clearing houses can be run by coops and
federations of communities for each other. The risk to watch is **centralization
gravity** — clearing houses will accumulate hosted communities the way large fediverse
servers accumulated users. The structural mitigations: exit is cheap by design,
mirrored state makes migration real rather than theoretical, and the market for
clearing houses is open. Named here so it stays watched.

Pull-based federation remains the interoperable baseline — checkpoints, logs, and
outboxes as poll-able documents are what make mirrors, audits, and CDN-fronted nodes
work. It is also why signatures live **on the objects, not the transport** (unlike
v0.1's HTTP Signatures): an object signature survives being served from a CDN, a
static mirror, or a git remote. HTTP-level signatures die at the first cache.

*Aside worth exploring:* the log-shaped ledger maps beautifully onto **Dolt** —
commits, diffs, verifiable history, and pull/push replication for free. As the mirror
and migration format for Profile C communities ("your currency's history is a repo you
own"), it is a natural fit.

---

## 9. Prior art we're standing on

- **Silvio Gesell — Freigeld** — the "free money" lineage: money that circulates
  because hoarding it costs; we get the effect via relative melting (§5), not stamps.
- **TRM (Stéphane Laborde) / Duniter / Ğ1** — the direct conceptual ancestor: libre
  currency, Universal Dividend, the relative view. What we remove is the blockchain and
  the global Web of Trust; what we add is federation of *many* UD currencies with
  parity-defaulted trust lines (§5) — a shape Ğ1's single-currency design can't express.
- **LETS / timebanks** — the social model: community-issued credit, human admission.
- **WIR Bank, Sardex** — bilateral clearing and imbalance management at real economic
  scale for decades; proof the economics work without a chain.
- **RipplePay (2004)** — trust lines and multi-hop payment routing; our deferred endgame.
- **Trustlines Network, Circles UBI** — adjacent ideas on-chain; instructive for the
  mechanism design, the adoption cost of the chain, and (Circles) for why forced parity
  at trust points fails (§4b).
- **Constant-product market makers (Uniswap et al.)** — the pricing formula for contact
  pools (§4d), stripped of the chain: bilateral, off-chain, between known communities.
- **Central-bank swap lines** — the closest traditional analogue to reciprocal seeded
  reserves between currency issuers.
- **Interledger (ILP)** — transfer semantics across independent ledgers: two-phase
  commit, conditions/fulfillments, amounts-as-claims. The reference for our transfer
  state machine, and for multi-hop later.
- **ActivityPub + FEP-8b32 object integrity proofs** — federation shape, and the
  object-signature-over-transport-signature lesson.
- **nostr / Secure Scuttlebutt** — signed logs + pull/relay distribution, the pattern
  that makes near-static nodes possible.
- **GNU Taler** — where to look when we want real payment privacy later.

---

## 10. Open questions and settled ones

Settled by the membrane principle (§1): seed source and sizing, dividend recipients
and multipliers, exchange-rate regimes (pegs are node-side reserve policy, §4c/d),
member key custody (never crosses the membrane, §3), and every other internal policy
are **node decisions**, published as aggregate facts and priced by peers at the
contact. UD arithmetic is a reference model and an explicit experiment surface (§5).
Discovery is settled socially: first contact is manual, further discovery crawls
outward through the optional peer lists of existing connections.

Genuinely open:

1. **The audit protocol** — the replacement for mandated transparency, and the next
   substantial design job. We cannot (and don't want to) force fully public ledgers;
   instead, nodes *assert properties* at the interface ("conservation holds", "issuance
   follows my declared policy", "my log is available to peers"), peers verify what
   their access level allows, and the results flow back as structured signals: trust
   assessments that can automatically adjust reserve sizing over time, and anomaly
   reports that can tell a peer "your checkpoint doesn't match our channel history" —
   surfacing bugs as easily as fraud. Auditing as a first-class federation activity
   rather than transparency as a mandate.
2. **The covenant DSL** — a small, deliberately non-Turing-complete predicate language
   in the spirit of Bitcoin Script, but shaped like **Miniscript, not Script**: a
   declarative, composable expression tree (JSON-walkable, so even the cheapest
   profiles evaluate it without a compiler) over a fixed vocabulary — amounts, rates,
   time windows, counters over operation history, key thresholds, hash preimages,
   boolean composition. Deterministic, total, fail-closed. One language, three roles:
   **delegation constraints** (the identity doc attaches a master-key-signed policy to
   a clearing house's operator key — "transfers ≤ 50 UD/day, no reserve adjustments
   without co-signature" — and compliance is mechanically verifiable from published
   operations, so violations are provable breaches, not disputes); **self-binding
   assertions** (the audit protocol's claims become programs evaluated over published
   state — reproducible audits instead of prose); and **condition primitives**
   (hash-preimage + timelock on transfers is the HTLC pattern, making future multi-hop
   a protocol usage rather than a redesign). Guardrail in blood: predicates only —
   programs authorize or refuse, never execute, mutate, or loop. Membrane-clean: it
   evaluates only interface-visible state and binds only what nodes voluntarily
   publish. Prior art: Miniscript, Interledger crypto-conditions; the EVM is the
   cautionary tale. (Design work, v0.3, alongside the audit protocol it feeds.)

   **The endgame this opens:** a node that publishes *all* its operating rules as
   covenant programs can be implemented **fully client-side** — every wallet and peer
   re-executes the rules against the published, ordered log and verifies consistency.
   The operator's irreducible role collapses to **sequencing** (double-spend prevention
   needs one total order), **availability** (transfers are live conversations), and
   **admission** (knowing the humans is social work). A clearing house then becomes a
   *rule-blind sequencer* — it need not understand its hosted nodes' policies, because
   deviation is caught by any honest verifier (the optimistic-rollup /
   Certificate-Transparency pattern: order dumbly, verify universally). This also
   structurally cures implementation drift — the Ğ1 disease: implementations cannot
   diverge semantically when the semantics are the node's own published programs, not
   each codebase's reading of a spec. The protocol shrinks toward envelopes, ordering,
   and DSL evaluation rules; each node carries its own semantics with it.
3. **Sealed outboxes** — *deferred to build-and-test, deliberately.* Encrypting
   peer-addressed payloads to the recipient's node key is structurally cheap to
   retrofit: it touches no ledger semantics, no pool math, no state machine; the
   envelope frame stays cleartext either way, and ed25519→X25519 conversion keeps the
   door open by construction. The clearing-house model already removed the worst
   scenario (live outboxes on world-readable static hosts no longer exist; mirrors can
   omit outboxes). PoCs ship unsealed with the `sealed-outbox` capability reserved;
   whether the final rule is "mandatory", "tied to transparency level", or "per-contact
   opt-in" gets decided from field experience of what actually leaks and who cares.

---

## 11. Roadmap

1. **v0.2 spec** (PROTOCOL.md, this iteration) — settle the foundation: envelopes,
   trust lines, clearing, transfer state machine, both transport bindings, profiles,
   UD issuance.
2. **PoC 1 — Go single binary** (Profile A): one node end-to-end, then two nodes doing
   push-federation transfers on real servers; validates the state machine.
3. **PoC 2 — Cloudflare Pages + D1** (Profile B): same schema, proves the serverless
   claim.
4. **PoC 3 — clearing house + HTML-only nodes** (Profile C): one clearing house
   operating several thin communities under delegated keys, static mirrors on the
   communities' own hosts as exit insurance; internal clearing between co-hosted
   nodes as the multi-hop-lite demo.
5. **The audit protocol + covenant DSL** (v0.3 material): one predicate language
   serving delegation constraints on operator keys, machine-checkable self-binding
   assertions, verification levels, trust-adjustment signaling, and anomaly/bug
   reporting between nodes — with hash+timelock primitives reserved as the seed of
   future multi-hop.
6. **Then** multi-hop routing, member portability — each gated on the previous layer
   being boring and correct.
