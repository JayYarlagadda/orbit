# Database Migrations

Migrations use zero-padded sequence numbers and paired `.up.sql` and
`.down.sql` files. The migration command applies them in lexical order, owns
the transaction, records each version atomically, and holds a PostgreSQL
advisory lock so only one process migrates at a time.

```powershell
$env:ORBIT_DATABASE_URL = 'postgres://orbit:orbit@localhost:5432/orbit?sslmode=disable'
go run ./cmd/orbit-migrate -direction up
go run ./cmd/orbit-migrate -direction down -steps 1
```

Rules:

- every schema change is represented by a new migration;
- an applied migration is never edited;
- destructive down migrations are for disposable development databases only;
- state transitions and audit writes use the `orbit` schema explicitly;
- migration tests run against the pinned PostgreSQL release in CI.
- migration SQL does not contain transaction-control statements because the
  runner supplies the transaction.
