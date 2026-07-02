<?php
// contacts.php — bilateral pools (PROTOCOL §6, §8). Per peer: both reserves in
// their two currencies, the committed op_seq, the channel root (short), and the
// pool's flags (open/closed, busy/in-flight, diverged). Read-only, no JS.

declare(strict_types=1);
require __DIR__ . '/lib.php';

$snap = load_snapshot();
$mySym = (string) cfg('currency_symbol', '');

page_header('Contacts', 'contacts');
?>
<h1>Contacts &amp; pools</h1>
<p class="lede">Each contact is one bilateral liquidity pool with a peer node.
Both sides mirror the same reserves, operation count, and channel root, so
pricing and reconciliation are deterministic.</p>

<?php if (!$snap['contacts']): ?>
  <p class="notice info">No contacts yet. Open one from <a href="actions.php">Actions</a>.</p>
<?php else: ?>
<div class="card tablecard">
  <table>
    <caption><?= h((string) count($snap['contacts'])) ?> bilateral pool(s)</caption>
    <thead>
      <tr>
        <th scope="col">Peer</th>
        <th scope="col" class="num">My reserve of peer</th>
        <th scope="col" class="num">Peer reserve of me</th>
        <th scope="col" class="num">op_seq</th>
        <th scope="col">Channel root</th>
        <th scope="col">State</th>
      </tr>
    </thead>
    <tbody>
      <?php foreach ($snap['contacts'] as $c):
        $host   = (string) ($c['PeerHost'] ?? '');
        $root   = channel_root_b64($c);
        $active = !empty($c['Active']);
        $closed = !empty($c['Closed']);
        $busy   = !empty($c['Busy']);
        $diverged = !empty($c['Diverged']);
      ?>
      <tr>
        <td>
          <div><strong><?= h($host) ?></strong></div>
          <div class="sub mono" style="color:var(--muted);font-size:12.5px"><?= h((string) ($c['PeerBase'] ?? '')) ?></div>
          <div style="color:var(--muted);font-size:12.5px"><?= !empty($c['IAmProposer']) ? 'I proposed' : 'peer proposed' ?></div>
        </td>
        <td class="num"><?= h(money_fmt((int) ($c['MyReserveOfPeer'] ?? 0))) ?></td>
        <td class="num"><?= h(scale_fmt((int) ($c['PeerReserveOfMe'] ?? 0))) ?> <span class="unit" style="color:var(--muted)">peer</span></td>
        <td class="num"><?= h((string) ($c['OpSeq'] ?? 0)) ?></td>
        <td class="mono" title="<?= h($root) ?>"><?= h(short($root, 12)) ?></td>
        <td>
          <?php if ($diverged): ?>
            <span class="badge warn"><span class="dot" aria-hidden="true"></span>diverged</span>
          <?php elseif ($closed): ?>
            <span class="badge muted"><span class="dot" aria-hidden="true"></span>closed</span>
          <?php elseif ($active): ?>
            <span class="badge ok"><span class="dot" aria-hidden="true"></span>active</span>
          <?php else: ?>
            <span class="badge muted"><span class="dot" aria-hidden="true"></span>pending</span>
          <?php endif; ?>
          <?php if ($busy): ?>
            <span class="badge warn"><span class="dot" aria-hidden="true"></span>busy</span>
          <?php endif; ?>
        </td>
      </tr>
      <?php endforeach; ?>
    </tbody>
  </table>
</div>

<h2>Transfers</h2>
<?php if (!$snap['transfers']): ?>
  <p class="lede">No cross-node transfers recorded.</p>
<?php else: ?>
<div class="card tablecard">
  <table>
    <caption>Cross-node payments through these pools</caption>
    <thead>
      <tr>
        <th scope="col">Direction</th>
        <th scope="col">From → To</th>
        <th scope="col" class="num">Sent</th>
        <th scope="col" class="num">Received</th>
        <th scope="col">State</th>
      </tr>
    </thead>
    <tbody>
      <?php foreach ($snap['transfers'] as $t):
        $out = !empty($t['Outgoing']);
        $state = (string) ($t['State'] ?? '');
        $settled = $state === 'SETTLED' || $state === 'COMMITTED';
      ?>
      <tr>
        <td><?= $out
              ? '<span class="badge muted">outgoing</span>'
              : '<span class="badge muted">incoming</span>' ?></td>
        <td class="mono"><?= h((string) ($t['FromMember'] ?? '')) ?> → <?= h((string) ($t['ToMember'] ?? '')) ?></td>
        <td class="num"><?= h(scale_fmt((int) ($t['SrcAmount'] ?? 0))) ?></td>
        <td class="num"><?= h(scale_fmt((int) ($t['DstAmount'] ?? 0))) ?></td>
        <td><span class="badge <?= $settled ? 'ok' : 'muted' ?>"><span class="dot" aria-hidden="true"></span><?= h($state) ?></span></td>
      </tr>
      <?php endforeach; ?>
    </tbody>
  </table>
</div>
<?php endif; ?>
<?php endif; ?>
<?php
page_footer();
