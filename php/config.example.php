<?php
// config.example.php — copy to config.php and edit for your deployment. config.php
// is gitignored (it names your node and its state file). Every value can be
// overridden by an environment variable, which is handy for tests and for
// serverless/cron hosts that inject config through the environment.

declare(strict_types=1);

return [
    // Public origin of this node (no trailing slash). Peers reach the identity
    // doc at <base>/.well-known/liquiditypub.
    'base' => getenv('LP_BASE') ?: 'http://localhost:8080',

    // Where the node's JSON state snapshot lives (must be writable; keep it OUT
    // of the web root in production).
    'state_file' => getenv('LP_STATE_FILE') ?: (__DIR__ . '/state.json'),

    // Where the operator dashboard queues action intents for poll.php to drain.
    'queue_file' => getenv('LP_QUEUE_FILE') ?: (__DIR__ . '/action-queue.jsonl'),

    'name'            => getenv('LP_NAME') ?: 'My Community',
    'currency_name'   => getenv('LP_CURRENCY') ?: 'Credits',
    'currency_symbol' => getenv('LP_SYMBOL') ?: 'C',
    'transparency'    => getenv('LP_TRANSPARENCY') ?: 'pseudonymous',

    // Issuance policy (PROTOCOL §10). c_period_ppm ~274 ≈ 10%/yr at daily periods.
    'c_period_ppm' => (int) (getenv('LP_C_PERIOD_PPM') ?: 274),
    'ud_period'    => getenv('LP_UD_PERIOD') ?: 'P1D',
    'genesis_ud'   => (int) (getenv('LP_GENESIS_UD') ?: 1000000),

    // Reserve offered when we auto-accept an inbound contact (our currency). Set
    // to 0 to refuse auto-accept and open contacts manually.
    'auto_accept_seed' => (int) (getenv('LP_AUTO_ACCEPT_SEED') ?: 500000000),

    // Peers to pull from on each poll.php run (base URLs).
    'peers' => array_filter(explode(',', getenv('LP_PEERS') ?: '')),
];
