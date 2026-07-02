<?php
// lib.php — shared helpers for the LiquidityPub operator dashboard.
//
// This is a *read* surface over a node snapshot plus a thin, honest action
// queue. It never mutates money: the ledger is the node's job. Everything the
// views need — state loading, integer-only money formatting, escaping, the
// page chrome, CSRF — lives here so the pages stay small and declarative.

declare(strict_types=1);

// ── Configuration seams ──────────────────────────────────────────────────────

// The ONE place state comes from. Swap this path for a real node snapshot
// (byte-compatible with the Go `store.Store` schema, see node/persist.go) and
// every view follows. In production this would read the flock-guarded snapshot
// blob the node writes; nothing else changes.
const LP_STATE_FILE  = __DIR__ . '/sample-state.json';

// Where operator intents are appended. The real node layer drains this queue,
// validates against live state, and applies the mutations. Until then the
// dashboard only ever *records intent* here — it fakes no ledger changes.
const LP_QUEUE_FILE  = __DIR__ . '/action-queue.jsonl';

const LP_MICRO       = 1000000;   // 1 unit = 1,000,000 micro-units (PROTOCOL §2)
const LP_PAGE_SIZE   = 100;       // fixed log page size, mirrors the Go /lp/log paging

// ── State loading (the single seam) ──────────────────────────────────────────

/**
 * load_snapshot() is the only function that knows where node state lives or how
 * it is shaped. Returns an associative array of the decoded snapshot (plus the
 * adjacent `config` identity block). Callers treat it as read-only.
 */
function load_snapshot(): array
{
    static $cache = null;
    if ($cache !== null) {
        return $cache;
    }
    $raw = @file_get_contents(LP_STATE_FILE);
    if ($raw === false) {
        http_response_code(500);
        exit('Unable to read node state at ' . htmlspecialchars(LP_STATE_FILE, ENT_QUOTES));
    }
    $data = json_decode($raw, true);
    if (!is_array($data)) {
        http_response_code(500);
        exit('Node state is not valid JSON.');
    }
    // Defensive defaults so a partial snapshot never fatals a view.
    $data += [
        'config' => [], 'members' => [], 'ledger' => [], 'contacts' => [],
        'transfers' => [], 'own_keys' => [], 'peer_keys' => [], 'out_seq' => [],
        'current_ud' => 0, 'active_key' => '', 'created' => '',
    ];
    $cache = $data;
    return $cache;
}

/** cfg() reads a node-identity/config value with a fallback. */
function cfg(string $key, $default = '')
{
    $c = load_snapshot()['config'] ?? [];
    return $c[$key] ?? $default;
}

// ── Money & weights: integer micro-units end to end, never a float ───────────

/**
 * scale_fmt formats an integer count of millionths as a decimal string, doing
 * all arithmetic on integers (no float ever touches money). Trailing zeros are
 * trimmed but at least $minDec decimals are kept. The whole part is grouped.
 */
function scale_fmt(int $micro, int $minDec = 2): string
{
    $neg   = $micro < 0;
    $abs   = $neg ? -$micro : $micro;
    $whole = intdiv($abs, LP_MICRO);
    $frac  = $abs % LP_MICRO;                       // 0 .. 999999
    $fracS = str_pad((string) $frac, 6, '0', STR_PAD_LEFT);
    $fracS = rtrim($fracS, '0');
    if (strlen($fracS) < $minDec) {
        $fracS = str_pad($fracS, $minDec, '0');
    }
    $wholeS = number_format($whole, 0, '.', ',');   // grouping only; $whole is an int
    $out    = $wholeS . ($fracS === '' ? '' : '.' . $fracS);
    return ($neg ? '-' : '') . $out;
}

/** money_fmt renders micro-units as currency, e.g. 100000000 -> "100.00 ʀ". */
function money_fmt(int $micro, bool $withSymbol = true): string
{
    $s = scale_fmt($micro, 2);
    if ($withSymbol) {
        $sym = (string) cfg('currency_symbol', '⨂');
        $s .= ' ' . $sym;
    }
    return $s;
}

/** signed_money adds an explicit +/- so ledger legs read as debits/credits. */
function signed_money(int $micro): string
{
    $sign = $micro > 0 ? '+' : '';
    return $sign . money_fmt($micro);
}

/** weight_fmt renders a micro-weight as a standard-member multiple, e.g. "2.0×". */
function weight_fmt(int $microWeight): string
{
    return scale_fmt($microWeight, 1) . '×';
}

/**
 * units_to_micro parses an operator-entered decimal amount (in whole units) to
 * integer micro-units, doing string arithmetic so no float ever touches money.
 * Returns null if the input is not a well-formed amount (≤6 decimal places).
 */
function units_to_micro(string $s): ?int
{
    $s = trim($s);
    if ($s === '' || !preg_match('/^-?\d{1,15}(\.\d{1,6})?$/', $s)) {
        return null;
    }
    $neg = $s[0] === '-';
    $s = ltrim($s, '-');
    $parts = explode('.', $s);
    $whole = (int) $parts[0];
    $frac  = (int) str_pad($parts[1] ?? '', 6, '0');   // right-pad to 6 digits
    $micro = $whole * LP_MICRO + $frac;
    if (!is_int($micro)) {
        // Whole part was large enough that the multiply overflowed int64 and PHP
        // promoted it to float — reject rather than return an imprecise amount.
        return null;
    }
    return $neg ? -$micro : $micro;
}

/**
 * queue_intent appends one operator intent as a JSON line under an exclusive
 * lock. THIS IS A STUB SEAM: the dashboard records intent only. The real node
 * layer must drain this queue, validate each intent against live state, and
 * apply the ledger/contact mutations. Nothing here changes money.
 */
function queue_intent(array $intent): bool
{
    $intent['queued_at'] = gmdate('c');
    $line = json_encode($intent, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE);
    if ($line === false) {
        return false;
    }
    $fh = @fopen(LP_QUEUE_FILE, 'a');
    if (!$fh) {
        return false;
    }
    $ok = false;
    if (flock($fh, LOCK_EX)) {
        $ok = fwrite($fh, $line . "\n") !== false;
        fflush($fh);
        flock($fh, LOCK_UN);
    }
    fclose($fh);
    return $ok;
}

// ── Ledger derivations (mirror node/ledger/ledger.go) ────────────────────────

/** balances() sums every entry into an account -> balance map. */
function balances(array $snap): array
{
    $bal = [];
    foreach ($snap['ledger'] as $rec) {
        foreach (($rec['tx']['entries'] ?? []) as $e) {
            $acct = (string) ($e['account'] ?? '');
            $bal[$acct] = ($bal[$acct] ?? 0) + (int) ($e['amount'] ?? 0);
        }
    }
    return $bal;
}

/** money_supply() = -(issuance + treasury), matching Ledger.MoneySupply(). */
function money_supply(array $snap): int
{
    $bal = balances($snap);
    return -(($bal['issuance'] ?? 0) + ($bal['treasury'] ?? 0));
}

/** supply_series() returns the running money supply after each record, for a sparkline. */
function supply_series(array $snap): array
{
    $series = [];
    $issuance = 0;
    $treasury = 0;
    foreach ($snap['ledger'] as $rec) {
        foreach (($rec['tx']['entries'] ?? []) as $e) {
            $a = (string) ($e['account'] ?? '');
            $amt = (int) ($e['amount'] ?? 0);
            if ($a === 'issuance') { $issuance += $amt; }
            elseif ($a === 'treasury') { $treasury += $amt; }
        }
        $series[] = -($issuance + $treasury);
    }
    return $series;
}

/** ledger_head() returns [seq, hash] of the checkpoint-worthy log head. */
function ledger_head(array $snap): array
{
    $log = $snap['ledger'];
    if (!$log) {
        return [0, ''];
    }
    $last = $log[count($log) - 1];
    return [(int) ($last['seq'] ?? count($log)), (string) ($last['hash'] ?? '')];
}

// ── Small presentation helpers ───────────────────────────────────────────────

/** h() escapes for HTML text/attribute context. Every dynamic value uses this. */
function h($s): string
{
    return htmlspecialchars((string) $s, ENT_QUOTES, 'UTF-8');
}

/** shorten a hash/id/root for display; full value goes in a title tooltip. */
function short(string $s, int $keep = 10): string
{
    if (strlen($s) <= $keep) {
        return $s;
    }
    return substr($s, 0, $keep) . '…';
}

/** channel_root_b64 prefers the base64 Roots[opSeq]; falls back to the byte array. */
function channel_root_b64(array $contact): string
{
    $op = (int) ($contact['OpSeq'] ?? 0);
    $roots = $contact['Roots'] ?? [];
    if (is_array($roots) && isset($roots[$op]) && $roots[$op] !== '') {
        return (string) $roots[$op];
    }
    $bytes = $contact['ChannelRoot'] ?? [];
    if (is_array($bytes) && $bytes) {
        $raw = '';
        foreach ($bytes as $b) {
            $raw .= chr(((int) $b) & 0xff);
        }
        return rtrim(strtr(base64_encode($raw), '+/', '-_'), '=');
    }
    return '';
}

// ── CSRF (action forms only) ─────────────────────────────────────────────────

function lp_session(): void
{
    if (session_status() !== PHP_SESSION_ACTIVE) {
        session_start();
    }
}

function csrf_token(): string
{
    lp_session();
    if (empty($_SESSION['csrf'])) {
        $_SESSION['csrf'] = bin2hex(random_bytes(32));
    }
    return $_SESSION['csrf'];
}

function csrf_field(): string
{
    return '<input type="hidden" name="csrf" value="' . h(csrf_token()) . '">';
}

function csrf_check(): bool
{
    lp_session();
    $sent = $_POST['csrf'] ?? '';
    return is_string($sent) && !empty($_SESSION['csrf'])
        && hash_equals((string) $_SESSION['csrf'], $sent);
}

// ── Inline SVG sparkline (server-side; no JS, no charting library) ────────────

/**
 * sparkline() draws a simple inline SVG line of the given integer series. Purely
 * decorative context for the money supply; carries no interaction.
 */
function sparkline(array $values, int $w = 220, int $hgt = 44): string
{
    $n = count($values);
    if ($n === 0) {
        return '';
    }
    if ($n === 1) {
        $values = [$values[0], $values[0]];
        $n = 2;
    }
    $min = min($values);
    $max = max($values);
    $span = ($max - $min) ?: 1;
    $pad = 3;
    $iw = $w - 2 * $pad;
    $ih = $hgt - 2 * $pad;
    $pts = [];
    foreach ($values as $i => $v) {
        $x = $pad + ($iw * $i / ($n - 1));
        $y = $pad + $ih - ($ih * ($v - $min) / $span);
        $pts[] = round($x, 2) . ',' . round($y, 2);
    }
    $poly = implode(' ', $pts);
    $last = explode(',', $pts[$n - 1]);
    $svg  = '<svg class="spark" viewBox="0 0 ' . $w . ' ' . $hgt . '" width="' . $w . '" height="' . $hgt
          . '" role="img" aria-label="Money supply trend over the ledger" preserveAspectRatio="none">';
    $svg .= '<polyline fill="none" stroke="currentColor" stroke-width="2" stroke-linejoin="round" '
          . 'stroke-linecap="round" points="' . h($poly) . '"/>';
    $svg .= '<circle cx="' . h($last[0]) . '" cy="' . h($last[1]) . '" r="2.5" fill="currentColor"/>';
    $svg .= '</svg>';
    return $svg;
}

// ── Page chrome ──────────────────────────────────────────────────────────────

function page_header(string $title, string $active): void
{
    $name = (string) cfg('name', 'LiquidityPub');
    $sym  = (string) cfg('currency_symbol', '');
    $nav = [
        'index'    => ['index.php', 'Overview'],
        'members'  => ['members.php', 'Members'],
        'contacts' => ['contacts.php', 'Contacts'],
        'ledger'   => ['ledger.php', 'Ledger'],
        'actions'  => ['actions.php', 'Actions'],
    ];
    echo "<!doctype html>\n<html lang=\"en\">\n<head>\n";
    echo '<meta charset="utf-8">';
    echo '<meta name="viewport" content="width=device-width, initial-scale=1">';
    echo '<title>' . h($title) . ' · ' . h($name) . "</title>\n";
    echo '<link rel="stylesheet" href="style.css">';
    echo "</head>\n<body>\n";
    echo '<a class="skip" href="#main">Skip to content</a>';
    echo '<header class="site"><div class="wrap">';
    echo '<div class="brand"><span class="mark" aria-hidden="true">' . h($sym ?: '◇') . '</span>';
    echo '<span class="brandtext"><strong>' . h($name) . '</strong>'
       . '<span class="sub">LiquidityPub node</span></span></div>';
    echo '<nav aria-label="Primary"><ul>';
    foreach ($nav as $key => [$href, $label]) {
        $cur = $key === $active ? ' class="cur" aria-current="page"' : '';
        echo '<li><a' . $cur . ' href="' . h($href) . '">' . h($label) . '</a></li>';
    }
    echo '</ul></nav>';
    echo '</div></header>';
    echo '<main id="main" class="wrap">';
}

function page_footer(): void
{
    $base = (string) cfg('base', '');
    echo '</main><footer class="site"><div class="wrap">';
    echo '<span>' . h($base) . '</span>';
    echo '<span>Operator dashboard · read views work without JavaScript</span>';
    echo '</div></footer></body></html>';
}
