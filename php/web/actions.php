<?php
// actions.php — operator actions. Each form POSTs here. On POST we validate the
// inputs and, if valid, append an *intent* to the action queue (LP_QUEUE_FILE).
//
// STUB BOUNDARY: this file never mutates the ledger or contacts. It only records
// what the operator asked for. The real node layer drains the queue, re-validates
// against live state under the file lock, and applies the effects. The wiring is
// intentionally obvious so that layer drops straight in.

declare(strict_types=1);
require __DIR__ . '/lib.php';

$snap = load_snapshot();

$errors = [];
$done   = null;   // action name on success (post/redirect/get)
$old    = [];     // sticky field values on error

if ($_SERVER['REQUEST_METHOD'] === 'POST') {
    $old    = $_POST;
    $action = (string) ($_POST['action'] ?? '');

    if (!csrf_check()) {
        $errors[] = 'Security token invalid or expired. Please reload and try again.';
    } else {
        $intent = ['action' => $action];

        switch ($action) {
            case 'open_contact':
                $peer = trim((string) ($_POST['peer_base'] ?? ''));
                $seed = units_to_micro((string) ($_POST['seed'] ?? ''));
                if (!preg_match('#^https?://[^\s/]+#i', $peer)) {
                    $errors[] = 'Peer base must be an http(s) URL, e.g. https://hilltop.example.';
                }
                if ($seed === null || $seed <= 0) {
                    $errors[] = 'Opening reserve (my seed) must be a positive amount.';
                }
                $intent += ['peer_base' => $peer, 'my_seed_micro' => $seed];
                break;

            case 'send_transfer':
                $from = trim((string) ($_POST['from_member'] ?? ''));
                $to   = trim((string) ($_POST['to'] ?? ''));
                $amt  = units_to_micro((string) ($_POST['amount'] ?? ''));
                if ($from === '') {
                    $errors[] = 'Choose a paying member.';
                }
                if (!preg_match('/^[^@\s]+@[^@\s]+$/', $to)) {
                    $errors[] = 'Recipient must be in the form name@peer-host.';
                }
                if ($amt === null || $amt <= 0) {
                    $errors[] = 'Amount must be a positive number of units.';
                }
                $intent += ['from_member' => $from, 'to' => $to, 'src_amount_micro' => $amt];
                break;

            case 'adjust_reserve':
                $host  = trim((string) ($_POST['peer_host'] ?? ''));
                $delta = units_to_micro((string) ($_POST['delta'] ?? ''));
                if ($host === '') {
                    $errors[] = 'Choose a contact to adjust.';
                }
                if ($delta === null || $delta === 0) {
                    $errors[] = 'Reserve delta must be a non-zero amount (negative to withdraw).';
                }
                $intent += ['peer_host' => $host, 'delta_micro' => $delta];
                break;

            case 'run_ud':
                if (($_POST['confirm'] ?? '') !== 'yes') {
                    $errors[] = 'Tick the box to confirm running a UD distribution.';
                }
                // No further inputs: the node computes recipients from live weights.
                break;

            case 'add_member':
                $name = trim((string) ($_POST['name'] ?? ''));
                $disp = trim((string) ($_POST['display_name'] ?? ''));
                $w    = units_to_micro((string) ($_POST['weight'] ?? '1'));
                if (!preg_match('/^[a-z0-9._-]{1,64}$/i', $name)) {
                    $errors[] = 'Account name may contain letters, digits, dot, dash, underscore.';
                }
                if ($w === null || $w <= 0) {
                    $errors[] = 'Weight must be a positive multiple (1 = standard member).';
                }
                $intent += ['name' => $name, 'display_name' => $disp, 'weight_micro' => $w];
                break;

            case 'deactivate_member':
                $name = trim((string) ($_POST['name'] ?? ''));
                if ($name === '') {
                    $errors[] = 'Choose a member to deactivate.';
                }
                $intent += ['name' => $name];
                break;

            default:
                $errors[] = 'Unknown action.';
        }

        if (!$errors) {
            if (queue_intent($intent)) {
                header('Location: actions.php?ok=' . rawurlencode($action));
                exit;
            }
            $errors[] = 'Could not write to the action queue (check file permissions).';
        }
    }
}

// A convenience map of active members for the select inputs.
$memberOpts = [];
foreach ($snap['members'] as $m) {
    if (!empty($m['Active'])) {
        $memberOpts[(string) $m['Name']] = (string) ($m['DisplayName'] ?? $m['Name']);
    }
}
$contactOpts = [];
foreach ($snap['contacts'] as $c) {
    if (empty($c['Closed'])) {
        $contactOpts[(string) $c['PeerHost']] = (string) $c['PeerHost'];
    }
}

function old_val(array $old, string $key, string $default = ''): string
{
    return h($old[$key] ?? $default);
}

page_header('Actions', 'actions');
?>
<h1>Operator actions</h1>
<p class="lede">These forms record an <strong>intent</strong> for the node to
apply. This dashboard never mutates the ledger itself — it validates your input
and queues it. Read views elsewhere stay a faithful mirror of committed state.</p>

<?php if (isset($_GET['ok'])): ?>
  <div class="notice done" role="status">
    <strong>Intent queued.</strong>
    Recorded <span class="mono"><?= h((string) $_GET['ok']) ?></span> to the action queue.
    The node applies it (and any peer exchange it needs) on the next <span class="mono">poll.php</span> run.
  </div>
<?php endif; ?>

<?php if ($errors): ?>
  <div class="notice err" role="alert">
    <strong>Please fix the following:</strong>
    <ul style="margin:6px 0 0 18px;padding:0">
      <?php foreach ($errors as $e): ?><li><?= h($e) ?></li><?php endforeach; ?>
    </ul>
  </div>
<?php endif; ?>

<div class="actiongrid">

  <form class="action card" method="post" action="actions.php">
    <?= csrf_field() ?>
    <input type="hidden" name="action" value="open_contact">
    <h3>Open contact</h3>
    <p class="hint">Propose a bilateral pool with a peer node (PROTOCOL §6.1).</p>
    <label class="field"><span>Peer base URL</span>
      <input type="url" name="peer_base" placeholder="https://hilltop.example"
             value="<?= old_val($old, 'peer_base') ?>" required></label>
    <label class="field"><span>Opening reserve <span class="unit">(units of <?= h(cfg('currency_symbol')) ?>)</span></span>
      <input type="text" inputmode="decimal" name="seed" placeholder="500.00"
             value="<?= old_val($old, 'seed') ?>" required></label>
    <button class="primary" type="submit">Queue: open contact</button>
  </form>

  <form class="action card" method="post" action="actions.php">
    <?= csrf_field() ?>
    <input type="hidden" name="action" value="send_transfer">
    <h3>Send transfer</h3>
    <p class="hint">Pay a member on a peer node through a pool (PROTOCOL §7).</p>
    <label class="field"><span>From member</span>
      <select name="from_member" required>
        <option value="">Choose…</option>
        <?php foreach ($memberOpts as $n => $d): ?>
          <option value="<?= h($n) ?>" <?= (($old['from_member'] ?? '') === $n) ? 'selected' : '' ?>><?= h($d) ?> (<?= h($n) ?>)</option>
        <?php endforeach; ?>
      </select></label>
    <label class="field"><span>To <span class="unit">(name@peer-host)</span></span>
      <input type="text" name="to" placeholder="dan@hilltop.example"
             value="<?= old_val($old, 'to') ?>" required></label>
    <label class="field"><span>Amount <span class="unit">(units)</span></span>
      <input type="text" inputmode="decimal" name="amount" placeholder="10.00"
             value="<?= old_val($old, 'amount') ?>" required></label>
    <button class="primary" type="submit">Queue: send transfer</button>
  </form>

  <form class="action card" method="post" action="actions.php">
    <?= csrf_field() ?>
    <input type="hidden" name="action" value="adjust_reserve">
    <h3>Adjust reserve</h3>
    <p class="hint">Change our reserve in a pool by consent (PROTOCOL §8.4). Negative withdraws.</p>
    <label class="field"><span>Contact</span>
      <select name="peer_host" required>
        <option value="">Choose…</option>
        <?php foreach ($contactOpts as $host): ?>
          <option value="<?= h($host) ?>" <?= (($old['peer_host'] ?? '') === $host) ? 'selected' : '' ?>><?= h($host) ?></option>
        <?php endforeach; ?>
      </select></label>
    <label class="field"><span>Delta <span class="unit">(units, may be negative)</span></span>
      <input type="text" inputmode="decimal" name="delta" placeholder="-50.00"
             value="<?= old_val($old, 'delta') ?>" required></label>
    <button class="primary" type="submit">Queue: adjust reserve</button>
  </form>

  <form class="action card" method="post" action="actions.php">
    <?= csrf_field() ?>
    <input type="hidden" name="action" value="run_ud">
    <h3>Run Universal Dividend</h3>
    <p class="hint">Distribute this period's UD to active members by weight (PROTOCOL §10).</p>
    <p class="hint">Current UD: <strong><?= h(money_fmt((int) ($snap['current_ud'] ?? 0))) ?></strong> per standard member.</p>
    <label class="field" style="display:flex;gap:8px;align-items:center">
      <input type="checkbox" name="confirm" value="yes" style="width:auto" <?= (($old['confirm'] ?? '') === 'yes') ? 'checked' : '' ?>>
      <span style="margin:0">Yes, queue a UD run now</span></label>
    <button class="primary" type="submit">Queue: run UD</button>
  </form>

  <form class="action card" method="post" action="actions.php">
    <?= csrf_field() ?>
    <input type="hidden" name="action" value="add_member">
    <h3>Add member</h3>
    <p class="hint">Admit a new account to the currency (PROTOCOL §11).</p>
    <label class="field"><span>Account name</span>
      <input type="text" name="name" placeholder="hank"
             value="<?= old_val($old, 'name') ?>" required></label>
    <label class="field"><span>Display name</span>
      <input type="text" name="display_name" placeholder="Hank Ito"
             value="<?= old_val($old, 'display_name') ?>"></label>
    <label class="field"><span>Weight <span class="unit">(1 = standard member)</span></span>
      <input type="text" inputmode="decimal" name="weight" placeholder="1"
             value="<?= old_val($old, 'weight', '1') ?>"></label>
    <button class="primary" type="submit">Queue: add member</button>
  </form>

  <form class="action card" method="post" action="actions.php">
    <?= csrf_field() ?>
    <input type="hidden" name="action" value="deactivate_member">
    <h3>Deactivate member</h3>
    <p class="hint">Stop an account from receiving future dividends.</p>
    <label class="field"><span>Member</span>
      <select name="name" required>
        <option value="">Choose…</option>
        <?php foreach ($memberOpts as $n => $d): ?>
          <option value="<?= h($n) ?>" <?= (($old['name'] ?? '') === $n) ? 'selected' : '' ?>><?= h($d) ?> (<?= h($n) ?>)</option>
        <?php endforeach; ?>
      </select></label>
    <button class="primary" type="submit">Queue: deactivate member</button>
  </form>

</div>
<?php
page_footer();
