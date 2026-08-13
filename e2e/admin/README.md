# Management Plane E2E

`verify.sh` is invoked by `e2e/helm/run.sh` against the real Control Plane in the
dedicated Minikube profile. It verifies strict UI security headers, break-glass
Management Session exchange, CSRF rejection, optimistic concurrent policy
publish and durable Principal Token Family revocation.

The browser fixture runs the production SQLite repositories, Management
Session service, authorizer, revision service, chi v5 HTTP API and embedded UI.
Its same-origin test provider exercises the browser OIDC PKCE/callback before
the real Management Token exchange.

```bash
go run ./e2e/admin/browserfixture --listen 127.0.0.1:18181
```

Open `http://127.0.0.1:18181/api/admin/ui/`. The fixture accepts:

- OIDC: `Fixture OIDC` (automatic same-origin authorization redirect)
- break-glass: `valid`

The fixture binds loopback only and must not be used as a production server.
