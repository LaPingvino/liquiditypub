<?php
declare(strict_types=1);

// Autoload src/
foreach (glob(__DIR__ . '/src/*.php') as $f) require_once $f;

if (Database::isInstalled()) {
    header('Location: /');
    exit;
}

$errors = [];
$step   = isset($_POST['step']) ? (int)$_POST['step'] : 1;

if ($_SERVER['REQUEST_METHOD'] === 'POST' && $step === 2) {
    $nodeName        = trim($_POST['node_name'] ?? '');
    $currencyName    = trim($_POST['currency_name'] ?? '');
    $currencySymbol  = trim($_POST['currency_symbol'] ?? '¤');
    $description     = trim($_POST['description'] ?? '');
    $issuanceType    = $_POST['issuance_type'] ?? 'manual';
    $issuanceAmount  = (int)($_POST['issuance_amount'] ?? 100);
    $issuanceInterval= (float)($_POST['issuance_interval_hours'] ?? 24);
    $adminPassword   = $_POST['admin_password'] ?? '';
    $adminPassword2  = $_POST['admin_password2'] ?? '';

    if ($nodeName === '')       $errors[] = 'Node name is required.';
    if ($currencyName === '')   $errors[] = 'Currency name is required.';
    if ($currencySymbol === '') $errors[] = 'Currency symbol is required.';
    if (strlen($adminPassword) < 8) $errors[] = 'Admin password must be at least 8 characters.';
    if ($adminPassword !== $adminPassword2) $errors[] = 'Passwords do not match.';

    if (empty($errors)) {
        $pdo = Database::getInstance();
        Node::ensureSystemAccounts();

        // Generate Ed25519-compatible key pair stub (OpenSSL RSA for compatibility)
        $publicKey = '';
        $privateKey = '';
        if (function_exists('openssl_pkey_new')) {
            $res = openssl_pkey_new(['private_key_bits' => 2048, 'private_key_type' => OPENSSL_KEYTYPE_RSA]);
            if ($res) {
                openssl_pkey_export($res, $privateKey);
                $details   = openssl_pkey_get_details($res);
                $publicKey = $details['key'] ?? '';
            }
        }

        Node::setMany([
            'name'                   => $nodeName,
            'currency_name'          => $currencyName,
            'currency_symbol'        => $currencySymbol,
            'description'            => $description,
            'issuance_type'          => in_array($issuanceType, ['periodic','manual']) ? $issuanceType : 'manual',
            'issuance_amount'        => (string)max(0, $issuanceAmount),
            'issuance_interval_hours'=> (string)max(1, $issuanceInterval),
            'admin_password_hash'    => password_hash($adminPassword, PASSWORD_BCRYPT),
            'public_key'             => $publicKey,
            'private_key'            => $privateKey,
            'installed_at'           => date('Y-m-d H:i:s'),
        ]);

        header('Location: /?installed=1');
        exit;
    }
}

$title = 'LiquidityPub — Install';
?>
<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title><?= htmlspecialchars($title) ?></title>
<link rel="stylesheet" href="/assets/style.css">
</head>
<body>
<div class="container install">
  <header>
    <h1>⚙️ LiquidityPub Setup</h1>
    <p class="subtitle">First-run configuration wizard</p>
  </header>

  <?php if (!empty($errors)): ?>
  <div class="alert alert-error">
    <ul><?php foreach ($errors as $e): ?><li><?= htmlspecialchars($e) ?></li><?php endforeach ?></ul>
  </div>
  <?php endif ?>

  <form method="POST" action="/install.php">
    <input type="hidden" name="step" value="2">

    <section class="card">
      <h2>Node Identity</h2>
      <label>Node Name
        <input type="text" name="node_name" required placeholder="Sunflower Collective"
               value="<?= htmlspecialchars($_POST['node_name'] ?? '') ?>">
      </label>
      <label>Description
        <textarea name="description" rows="2" placeholder="A local mutual credit community"><?= htmlspecialchars($_POST['description'] ?? '') ?></textarea>
      </label>
    </section>

    <section class="card">
      <h2>Currency</h2>
      <div class="row2">
        <label>Currency Name
          <input type="text" name="currency_name" required placeholder="Sunflower Credits"
                 value="<?= htmlspecialchars($_POST['currency_name'] ?? '') ?>">
        </label>
        <label>Symbol
          <input type="text" name="currency_symbol" required placeholder="☀" maxlength="4"
                 value="<?= htmlspecialchars($_POST['currency_symbol'] ?? '¤') ?>">
        </label>
      </div>
    </section>

    <section class="card">
      <h2>Issuance / UBI</h2>
      <label>Issuance Type
        <select name="issuance_type">
          <option value="manual" <?= ($_POST['issuance_type'] ?? 'manual') === 'manual' ? 'selected' : '' ?>>Manual (admin triggers)</option>
          <option value="periodic" <?= ($_POST['issuance_type'] ?? '') === 'periodic' ? 'selected' : '' ?>>Periodic (automatic)</option>
        </select>
      </label>
      <div class="row2">
        <label>Amount per issuance (micro-units)
          <input type="number" name="issuance_amount" min="0" value="<?= htmlspecialchars((string)($_POST['issuance_amount'] ?? '100000')) ?>">
          <small>1 credit = 1,000,000 micro-units. 100000 = 0.1 credits.</small>
        </label>
        <label>Interval (hours, for periodic)
          <input type="number" name="issuance_interval_hours" min="1" step="0.5"
                 value="<?= htmlspecialchars((string)($_POST['issuance_interval_hours'] ?? '24')) ?>">
        </label>
      </div>
    </section>

    <section class="card">
      <h2>Admin Password</h2>
      <div class="row2">
        <label>Password (min 8 chars)
          <input type="password" name="admin_password" required minlength="8" autocomplete="new-password">
        </label>
        <label>Confirm Password
          <input type="password" name="admin_password2" required minlength="8" autocomplete="new-password">
        </label>
      </div>
    </section>

    <button type="submit" class="btn btn-primary btn-lg">Install LiquidityPub →</button>
  </form>
</div>
</body>
</html>
