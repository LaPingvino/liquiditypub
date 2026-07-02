<?php
// index.php — Overview. Currency identity, money supply, membership, current UD,
// checkpoint head, transparency. All derived from the snapshot; no JS required.

declare(strict_types=1);
require __DIR__ . '/lib.php';

$snap = load_snapshot();

$supply      = money_supply($snap);
$series      = supply_series($snap);
$members     = $snap['members'];
$activeCount = 0;
$weightTotal = 0;
foreach ($members as $m) {
    if (!empty($m['Active'])) {
        $activeCount++;
        $weightTotal += (int) ($m['Weight'] ?? 0);
    }
}
[$headSeq, $headHash] = ledger_head($snap);
$currentUd   = (int) ($snap['current_ud'] ?? 0);
$transparency = (string) cfg('transparency', 'pseudonymous');
$contactsOpen = 0;
foreach ($snap['contacts'] as $c) {
    if (!empty($c['Active']) && empty($c['Closed'])) { $contactsOpen++; }
}

page_header('Overview', 'index');
?>
<h1><?= h(cfg('currency_name', 'Currency')) ?> <small>(<?= h(cfg('currency_symbol')) ?>)</small></h1>
<p class="lede"><?= h(cfg('description', '')) ?></p>

<section class="stats" aria-label="Node summary">
  <div class="stat wide card">
    <div>
      <div class="label">Money supply</div>
      <div class="value"><?= h(money_fmt($supply)) ?></div>
      <div class="sub">Total value issued into circulation (derived from the ledger)</div>
    </div>
    <?= sparkline($series) ?>
  </div>

  <div class="stat card">
    <div class="label">Members</div>
    <div class="value"><?= h((string) $activeCount) ?><small> active</small></div>
    <div class="sub"><?= h((string) count($members)) ?> total · UD weight <?= h(weight_fmt($weightTotal)) ?></div>
  </div>

  <div class="stat card">
    <div class="label">Current UD</div>
    <div class="value"><?= h(money_fmt($currentUd)) ?></div>
    <div class="sub">Per <?= h(cfg('ud_period', 'period')) ?> · c = <?= h((string) cfg('c_period_ppm', 0)) ?> ppm</div>
  </div>

  <div class="stat card">
    <div class="label">Contacts / pools</div>
    <div class="value"><?= h((string) $contactsOpen) ?><small> open</small></div>
    <div class="sub"><?= h((string) count($snap['contacts'])) ?> total bilateral pool(s)</div>
  </div>

  <div class="stat card">
    <div class="label">Transparency</div>
    <div class="value" style="font-size:18px">
      <span class="badge <?= $transparency === 'public' ? 'ok' : 'muted' ?>">
        <span class="dot" aria-hidden="true"></span><?= h($transparency) ?>
      </span>
    </div>
    <div class="sub">Log visibility level (PROTOCOL §9.3)</div>
  </div>

  <div class="stat card">
    <div class="label">Checkpoint head</div>
    <div class="value mono">seq <?= h((string) $headSeq) ?></div>
    <div class="sub mono" title="<?= h($headHash) ?>">hash <?= h(short($headHash, 14)) ?></div>
  </div>

  <div class="stat card">
    <div class="label">Node identity</div>
    <div class="value mono"><?= h(cfg('base')) ?></div>
    <div class="sub">Active key <span class="mono"><?= h((string) ($snap['active_key'] ?? '')) ?></span> · since <?= h(substr((string) ($snap['created'] ?? ''), 0, 10)) ?></div>
  </div>
</section>

<p class="lede" style="margin-top:28px">
  State is loaded read-only from a node snapshot. Use
  <a href="actions.php">Actions</a> to queue operator intents for the node layer to apply.
</p>
<?php
page_footer();
