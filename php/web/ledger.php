<?php
// ledger.php — the append-only log (PROTOCOL §9), newest first, fixed-size pages
// (LP_PAGE_SIZE, mirroring the Go /lp/log paging). Each record shows its seq,
// type, balanced entries, and timestamp. Read-only, no JS.

declare(strict_types=1);
require __DIR__ . '/lib.php';

$snap = load_snapshot();
$log  = $snap['ledger'];

// Newest first.
$records = array_reverse($log);
$total   = count($records);
$pages   = max(1, (int) ceil($total / LP_PAGE_SIZE));

$page = isset($_GET['page']) ? (int) $_GET['page'] : 1;
if ($page < 1) { $page = 1; }
if ($page > $pages) { $page = $pages; }
$offset = ($page - 1) * LP_PAGE_SIZE;
$slice  = array_slice($records, $offset, LP_PAGE_SIZE);

page_header('Ledger', 'ledger');
?>
<h1>Ledger</h1>
<p class="lede">The hash-linked, double-entry log. Every record's entries sum to
zero — the ledger <em>is</em> the money. Newest first.</p>

<div class="card tablecard">
  <table>
    <caption><?= h((string) $total) ?> records · page <?= h((string) $page) ?> of <?= h((string) $pages) ?></caption>
    <thead>
      <tr>
        <th scope="col" class="num">Seq</th>
        <th scope="col">Type</th>
        <th scope="col">Entries</th>
        <th scope="col">Created</th>
      </tr>
    </thead>
    <tbody>
      <?php foreach ($slice as $rec):
        $tx = $rec['tx'] ?? [];
        $entries = $tx['entries'] ?? [];
      ?>
      <tr>
        <td class="num mono"><?= h((string) ($rec['seq'] ?? '')) ?></td>
        <td>
          <span class="type-tag"><?= h((string) ($tx['type'] ?? '')) ?></span>
          <div class="mono" style="color:var(--muted);font-size:11.5px;margin-top:4px" title="<?= h((string) ($rec['hash'] ?? '')) ?>">#<?= h(short((string) ($rec['hash'] ?? ''), 10)) ?></div>
        </td>
        <td>
          <ul class="entries">
            <?php foreach ($entries as $e):
              $amt = (int) ($e['amount'] ?? 0);
            ?>
            <li>
              <span class="acct"><?= h((string) ($e['account'] ?? '')) ?></span>
              <span class="num <?= $amt < 0 ? 'amt-neg' : 'amt-pos' ?>"><?= h(signed_money($amt)) ?></span>
            </li>
            <?php endforeach; ?>
          </ul>
        </td>
        <td class="mono" style="font-size:13px;white-space:nowrap"><?= h(str_replace('T', ' ', (string) ($tx['created'] ?? ''))) ?></td>
      </tr>
      <?php endforeach; ?>
    </tbody>
  </table>
</div>

<?php if ($pages > 1): ?>
<nav class="pager" aria-label="Ledger pages">
  <?php if ($page > 1): ?>
    <a href="?page=<?= h((string) ($page - 1)) ?>" rel="prev">‹ Newer</a>
  <?php else: ?>
    <span class="disabled">‹ Newer</span>
  <?php endif; ?>
  <?php for ($p = 1; $p <= $pages; $p++): ?>
    <?php if ($p === $page): ?>
      <span class="cur" aria-current="page"><?= h((string) $p) ?></span>
    <?php else: ?>
      <a href="?page=<?= h((string) $p) ?>"><?= h((string) $p) ?></a>
    <?php endif; ?>
  <?php endfor; ?>
  <?php if ($page < $pages): ?>
    <a href="?page=<?= h((string) ($page + 1)) ?>" rel="next">Older ›</a>
  <?php else: ?>
    <span class="disabled">Older ›</span>
  <?php endif; ?>
</nav>
<?php endif; ?>
<?php
page_footer();
