<?php
// members.php — the community roster: name, display name, weight (as a standard
// multiple), current balance, and active state. Read-only, no JS.

declare(strict_types=1);
require __DIR__ . '/lib.php';

$snap = load_snapshot();
$bal  = balances($snap);

// Stable order: active first, then by display name.
$members = $snap['members'];
usort($members, function ($a, $b) {
    $aa = empty($a['Active']) ? 1 : 0;
    $bb = empty($b['Active']) ? 1 : 0;
    if ($aa !== $bb) { return $aa <=> $bb; }
    return strcasecmp((string) ($a['DisplayName'] ?? $a['Name'] ?? ''),
                      (string) ($b['DisplayName'] ?? $b['Name'] ?? ''));
});

page_header('Members', 'members');
?>
<h1>Members</h1>
<p class="lede">Every account that holds this currency. Weight is the member's
share of each Universal Dividend, as a multiple of a standard member.</p>

<div class="card tablecard">
  <table>
    <caption><?= h((string) count($members)) ?> members</caption>
    <thead>
      <tr>
        <th scope="col">Account</th>
        <th scope="col">Display name</th>
        <th scope="col" class="num">Weight</th>
        <th scope="col" class="num">Balance</th>
        <th scope="col">Status</th>
      </tr>
    </thead>
    <tbody>
      <?php foreach ($members as $m):
        $name = (string) ($m['Name'] ?? '');
        $b    = (int) ($bal['m:' . $name] ?? 0);
        $active = !empty($m['Active']);
      ?>
      <tr>
        <td class="mono">m:<?= h($name) ?></td>
        <td><?= h((string) ($m['DisplayName'] ?? '')) ?></td>
        <td class="num"><?= h(weight_fmt((int) ($m['Weight'] ?? 0))) ?></td>
        <td class="num"><?= h(money_fmt($b)) ?></td>
        <td>
          <?php if ($active): ?>
            <span class="badge ok"><span class="dot" aria-hidden="true"></span>active</span>
          <?php else: ?>
            <span class="badge muted"><span class="dot" aria-hidden="true"></span>inactive</span>
          <?php endif; ?>
        </td>
      </tr>
      <?php endforeach; ?>
    </tbody>
  </table>
</div>
<?php
page_footer();
