# Hub authentication

The Hub stores an Argon2id password verifier in `config.json`; it never needs
the plaintext login password after startup. Browser sessions are signed with a
separate 32-byte random key stored in `session-secret`. Changing either value
invalidates existing sessions.

Authentication is defense in depth. The Hub must still remain tailnet-only.

## Generate a password verifier

Run this on a trusted terminal after installing the new Hub binary:

```bash
read -rsp 'Lodge password: ' LODGE_NEW_PASSWORD; echo
printf '%s\n' "$LODGE_NEW_PASSWORD" | lodge-hub --hash-password
unset LODGE_NEW_PASSWORD
```

The command rejects interactive stdin so the password is never echoed by the
Hub process. Use a unique password of at least 12 characters and place only the
printed `$argon2id$...` value in the configuration:

```json
{
  "passwordHash": "$argon2id$v=19$m=65536,t=3,p=1$...$...",
  "agents": []
}
```

Keep the configuration readable only by its service owner:

```bash
chmod 0600 /etc/lodge-hub/config.json
```

Unknown fields, duplicate Agent IDs, empty Agent tokens, and non-HTTP Agent
URLs are rejected at startup.

## Plaintext migration

The legacy `"password":"..."` field remains temporarily supported. On each
startup, the Hub converts it to Argon2id only in memory and emits a warning.
Because the salt changes on every restart, existing browser sessions expire.

Migration procedure:

1. Back up `/etc/lodge-hub/config.json` with owner-only permissions.
2. Ensure the file and its containing directory are owned by the Hub service
   account and the file is mode `0600`.
3. Run the built-in atomic migration without printing the password or verifier:

   ```bash
   sudo -u lodge lodge-hub --config /etc/lodge-hub/config.json \
     --migrate-config-password
   ```

   The command writes and fsyncs an owner-only temporary file in the same
   directory, refuses a concurrent config change, and atomically replaces the
   original. Running it again is a no-op.
4. Restart `lodge-hub` and verify that the plaintext migration warning is gone.
5. Confirm `/etc/lodge-hub/session-secret` exists with mode `0600`, then test
   login and logout from a tailnet browser.

Rollback is limited to restoring the owner-private backup and restarting the
service. The backup contains a plaintext password, so securely delete it after
the migration has been verified.

## Failure behavior

- Invalid or deliberately expensive Argon2 parameters fail closed at startup.
- Password verification concurrency is bounded so parallel login floods cannot
  multiply the 64 MiB Argon2 working set without limit.
- An absent or weak session key prevents authenticated mode from starting.
- A loose-permission or symlinked session-secret file is rejected.
- Password changes and session-key rotations invalidate all browser sessions.
