<?php
// poll.php — the cron driver (PROTOCOL §5.1, §10, §12). Run it on a schedule
// (≤15 min for a live node). Each run: drain the operator action queue, federate
// with configured peers (push our outbox + pull theirs to quiescence), and —
// with --ud — issue one Universal Dividend period.
//
//   * / 15 * * * *  php /path/to/php/poll.php        # federate every 15 min
//   0 0 * * *       php /path/to/php/poll.php --ud   # one UD period daily
//
// Config comes from php/config.php (copy config.example.php); every value is also
// overridable by environment variable, which suits cron and serverless hosts.

declare(strict_types=1);

require __DIR__ . '/lp_node.php';

use lp\Node;
use lp\Store;
use lp\HttpTransport;

$cfg = is_file(__DIR__ . '/config.php')
    ? require __DIR__ . '/config.php'
    : require __DIR__ . '/config.example.php';

$node = new Node(new Store($cfg['state_file']), $cfg);
$node->setTransport(new HttpTransport());
$node->initKey();

$queue = $cfg['queue_file'] ?? (__DIR__ . '/action-queue.jsonl');
$drained = $node->drainActionQueue($queue);
$fed = $node->federate(new HttpTransport(), (array) ($cfg['peers'] ?? []));

$ranUd = false;
if (in_array('--ud', $argv, true)) {
    $node->runUD();
    $ranUd = true;
}

echo json_encode([
    'drained'   => $drained,
    'federated' => $fed,
    'ran_ud'    => $ranUd,
], JSON_UNESCAPED_SLASHES) . "\n";
