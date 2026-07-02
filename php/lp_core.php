<?php
// lp_core.php — the arithmetic/crypto core of a LiquidityPub node, in PHP.
//
// This is the load-bearing half of an independent Profile-A implementation for
// cheap shared hosting: everything the protocol pins bit-for-bit lives here —
// canonical JSON (JCS), the record/channel hash chain, pool pricing, the UD
// formula, and Ed25519 signing. It is verified against the SAME conformance
// vectors as the Go reference (see test_vectors.php), so a PHP node and a Go
// node produce identical bytes. Nothing here holds state; that is the node
// layer's job.
//
// Requirements on the host: gmp (exact pricing), hash (sha256), and — for the
// signing/verifying paths only — sodium (bundled with PHP 7.2+). The pure-data
// functions need neither and run anywhere.

declare(strict_types=1);

namespace lp;

// ── Canonical JSON (RFC 8785 / JCS profile, PROTOCOL §2) ─────────────────────
//
// Mirrors conformance/reference.go writeCanonical exactly: object keys sorted by
// byte order, no insignificant whitespace, integers only (floats are forbidden
// and rejected), Go's encoding/json string escaping with HTML escaping OFF but
// U+2028/U+2029 always escaped.
//
// Decode protocol JSON with json_decode($s, false): objects become stdClass and
// arrays become lists, so an empty object {} is never confused with an empty
// array [] (both would collapse to array() under associative decoding).

function canonical($v): string
{
    if ($v === null) {
        return 'null';
    }
    if (is_bool($v)) {
        return $v ? 'true' : 'false';
    }
    if (is_int($v)) {
        return (string) $v;
    }
    if (is_float($v)) {
        throw new \InvalidArgumentException('float in protocol JSON (PROTOCOL §2 forbids floats)');
    }
    if (is_string($v)) {
        return canonical_string($v);
    }
    if (is_array($v)) {
        // A JSON array (list). Associative arrays are not produced by
        // json_decode(..., false); callers hand-building objects must use
        // stdClass. A non-list array here is a programming error.
        if ($v !== [] && !array_is_list($v)) {
            throw new \InvalidArgumentException('associative array; build objects as stdClass');
        }
        $parts = [];
        foreach ($v as $e) {
            $parts[] = canonical($e);
        }
        return '[' . implode(',', $parts) . ']';
    }
    if ($v instanceof \stdClass) {
        $keys = array_keys(get_object_vars($v));
        sort($keys, SORT_STRING); // byte order == Go sort.Strings on ASCII keys
        $parts = [];
        foreach ($keys as $k) {
            $parts[] = canonical_string((string) $k) . ':' . canonical($v->{$k});
        }
        return '{' . implode(',', $parts) . '}';
    }
    throw new \InvalidArgumentException('unsupported type in protocol JSON: ' . gettype($v));
}

// canonical_string escapes exactly as Go's encoding/json with SetEscapeHTML(false):
// \" \\ \n \r \t as short forms, other C0 controls as \u00xx, <>& left bare,
// U+2028/U+2029 escaped, all other valid UTF-8 passed through unchanged.
function canonical_string(string $s): string
{
    $out = '"';
    $len = strlen($s);
    $i = 0;
    while ($i < $len) {
        $b = ord($s[$i]);
        if ($b === 0x22) {            // "
            $out .= '\\"';
            $i++;
        } elseif ($b === 0x5c) {      // backslash
            $out .= '\\\\';
            $i++;
        } elseif ($b === 0x0a) {
            $out .= '\\n';
            $i++;
        } elseif ($b === 0x0d) {
            $out .= '\\r';
            $i++;
        } elseif ($b === 0x09) {
            $out .= '\\t';
            $i++;
        } elseif ($b < 0x20) {
            $out .= sprintf('\\u%04x', $b);
            $i++;
        } elseif ($b < 0x80) {
            $out .= $s[$i];
            $i++;
        } else {
            // Multibyte UTF-8. Special-case U+2028/U+2029 (Go always escapes
            // these); otherwise copy the whole sequence verbatim.
            $n = $b >= 0xf0 ? 4 : ($b >= 0xe0 ? 3 : 2);
            $seq = substr($s, $i, $n);
            if ($seq === "\xe2\x80\xa8") {
                $out .= '\\u2028';
            } elseif ($seq === "\xe2\x80\xa9") {
                $out .= '\\u2029';
            } else {
                $out .= $seq;
            }
            $i += $n;
        }
    }
    return $out . '"';
}

// ── Hashing / encoding ───────────────────────────────────────────────────────

function sha256_raw(string $bytes): string
{
    return hash('sha256', $bytes, true);
}

// b64url encodes raw bytes as base64url without padding (PROTOCOL §2).
function b64url(string $bytes): string
{
    return rtrim(strtr(base64_encode($bytes), '+/', '-_'), '=');
}

function b64url_decode(string $s): string
{
    $s = strtr($s, '-_', '+/');
    $pad = strlen($s) % 4;
    if ($pad) {
        $s .= str_repeat('=', 4 - $pad);
    }
    return base64_decode($s, true);
}

// record_hash / envelope hash preimage: SHA-256 of the canonical form, returned
// base64url (PROTOCOL §9.2). The caller passes the value with any hash/sig
// member already removed.
function struct_hash_b64(object $v): string
{
    return b64url(sha256_raw(canonical($v)));
}

// ── Channel hash (PROTOCOL §8.2) ─────────────────────────────────────────────

function channel_root0(string $contactId): string
{
    return sha256_raw($contactId); // 32 raw bytes
}

// channel_next chains one committed op: SHA-256(prevRaw ‖ JCS([type,id,src,dst])).
function channel_next(string $prevRaw, string $opType, string $opId, int $src, int $dst): string
{
    $jcs = canonical([$opType, $opId, $src, $dst]);
    return sha256_raw($prevRaw . $jcs);
}

// ── Pool pricing (PROTOCOL §6.2), exact via GMP ──────────────────────────────
//
// Returns an int dst_amount, or throws with one of the canonical reasons
// ('non-positive', 'empty-pool', 'dust').

function pool_price(int $rSrc, int $rDst, int $src): int
{
    if ($src <= 0) {
        throw new \RuntimeException('non-positive');
    }
    if ($rSrc <= 0 || $rDst <= 0) {
        throw new \RuntimeException('empty-pool');
    }
    $num = \gmp_mul(\gmp_init($rDst), \gmp_init($src));
    $den = \gmp_add(\gmp_init($rSrc), \gmp_init($src));
    $dst = \gmp_div_q($num, $den); // floor for non-negative operands
    if (\gmp_cmp($dst, 0) === 0) {
        throw new \RuntimeException('dust');
    }
    return \gmp_intval($dst);
}

// ── Universal Dividend (PROTOCOL §10) ────────────────────────────────────────

function ud_base(int $moneySupply, int $cPeriodPpm, int $udWeightTotal): int
{
    if ($udWeightTotal <= 0) {
        return 0;
    }
    $n = \gmp_mul(\gmp_init($moneySupply), \gmp_init($cPeriodPpm));
    return \gmp_intval(\gmp_div_q($n, \gmp_init($udWeightTotal)));
}

function ud_recipient(int $udBase, int $weight): int
{
    $n = \gmp_mul(\gmp_init($udBase), \gmp_init($weight));
    return \gmp_intval(\gmp_div_q($n, \gmp_init(1000000)));
}

// ── Ed25519 (PROTOCOL §4) — requires the sodium extension ────────────────────

function have_sodium(): bool
{
    return function_exists('sodium_crypto_sign_detached');
}

// sign_detached signs the canonical bytes with a seed (32 bytes). Returns the
// raw 64-byte signature.
function sign_detached(string $canonicalBytes, string $seed32): string
{
    $kp = \sodium_crypto_sign_seed_keypair($seed32);
    $sk = \sodium_crypto_sign_secretkey($kp);
    return \sodium_crypto_sign_detached($canonicalBytes, $sk);
}

function verify_detached(string $canonicalBytes, string $sigRaw, string $pub32): bool
{
    return \sodium_crypto_sign_verify_detached($sigRaw, $canonicalBytes, $pub32);
}

// sign_envelope signs an envelope object (stdClass) minus its sig member and
// returns the base64url signature value.
function sign_envelope(object $env, string $seed32): string
{
    $bare = clone $env;
    unset($bare->sig);
    return b64url(sign_detached(canonical($bare), $seed32));
}
