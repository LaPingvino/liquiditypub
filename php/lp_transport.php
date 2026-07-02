<?php
// lp_transport.php — the HTTP federation client (PROTOCOL §5). It fetches a
// peer's identity document, outbox, and checkpoint, and pushes envelopes to a
// peer's inbox. The pull binding (fetchOutbox) is the mandatory baseline; push
// (pushInbox) is best-effort on top. Kept dependency-free (ext/curl only) so it
// runs on cheap hosting.

declare(strict_types=1);

namespace lp;

const LP_IDENTITY_PATH_T = '/.well-known/liquiditypub';

class HttpTransport
{
    public function __construct(private int $timeoutSec = 10) {}

    private function get(string $url): ?array
    {
        $ch = curl_init($url);
        curl_setopt_array($ch, [
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_TIMEOUT        => $this->timeoutSec,
            CURLOPT_FOLLOWLOCATION => false,
        ]);
        $out = curl_exec($ch);
        $code = curl_getinfo($ch, CURLINFO_HTTP_CODE);
        curl_close($ch);
        if ($out === false || $code < 200 || $code >= 300) {
            return null;
        }
        $v = json_decode((string) $out, true);
        return is_array($v) ? $v : null;
    }

    /** fetchIdentity returns a peer's §3 identity document, or null on failure. */
    public function fetchIdentity(string $peerBase): ?array
    {
        return $this->get(rtrim($peerBase, '/') . LP_IDENTITY_PATH_T);
    }

    /** fetchOutbox returns the envelopes a peer has addressed to myHost (§5.1). */
    public function fetchOutbox(string $peerBase, string $myHost): array
    {
        $v = $this->get(rtrim($peerBase, '/') . '/lp/outbox/' . rawurlencode($myHost) . '.json');
        return is_array($v) ? $v : [];
    }

    public function fetchCheckpoint(string $peerBase): ?array
    {
        return $this->get(rtrim($peerBase, '/') . '/lp/checkpoint.json');
    }

    /** pushInbox POSTs one envelope to a peer's inbox (§5.2), returning the HTTP code. */
    public function pushInbox(string $peerBase, array $env): int
    {
        $ch = curl_init(rtrim($peerBase, '/') . '/lp/inbox');
        curl_setopt_array($ch, [
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_TIMEOUT        => $this->timeoutSec,
            CURLOPT_POST           => true,
            CURLOPT_POSTFIELDS     => json_encode($env, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE),
            CURLOPT_HTTPHEADER     => ['Content-Type: application/json'],
        ]);
        curl_exec($ch);
        $code = (int) curl_getinfo($ch, CURLINFO_HTTP_CODE);
        curl_close($ch);
        return $code;
    }
}
