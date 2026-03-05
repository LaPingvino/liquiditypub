# LiquidityPub

**Federated local money system — PHP + SQLite, no framework**

Each LiquidityPub node runs a community currency with configurable issuance (UBI/dividend).
Members transact within a node. Nodes federate via a simple signed-message protocol.

## Requirements

- PHP 8.0+
- SQLite (via PHP PDO — enabled on most shared hosts)
- Apache with `mod_rewrite` (or Nginx with try_files equivalent)

## Deploy

1. Upload all files to your web root
2. Visit `https://yoursite.com/install.php`
3. Complete the setup wizard
4. Share your node URL with potential members

## File Structure

```
index.php          Front controller / router
install.php        First-run setup wizard
.htaccess          Rewrite rules
src/               Core PHP classes
pages/             Page handlers
api/               Federation API stubs
db/                Schema SQL (SQLite file created at runtime)
assets/            CSS
docs/              Protocol specification
```

## Federation Protocol

See [docs/PROTOCOL.md](docs/PROTOCOL.md) or visit the [GitHub Pages site](https://liquiditypub.github.io/liquiditypub/).

## Verification Checklist

1. Visit `/install.php` → complete wizard → redirects to `/`
2. Register two members (Alice, Bob)
3. `/admin` → trigger manual mint → verify both wallets show balance
4. Login as Alice → `/pay` → send to Bob → verify ledger balances
5. Check `/.well-known/liquiditypub` returns valid JSON
6. Confirm double-entry: sum of all `ledger_entries` = 0

## License

MIT
