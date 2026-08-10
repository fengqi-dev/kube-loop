# Management Plane E2E

`verify.sh` is invoked by `e2e/helm/run.sh` against the real Controller in the
dedicated Minikube profile. It verifies strict UI security headers, break-glass
Management Session exchange, CSRF rejection, optimistic concurrent policy
publish and durable Principal Token Family revocation.

The browser fixture runs the production SQLite repositories, Management
Session service, authorizer, revision service, chi v5 HTTP API and embedded UI.
Its same-origin test providers exercise the browser OIDC PKCE/callback and AD
password paths before the real Management Token exchange; Provider protocol,
TLS and directory behavior remain covered by the OIDC/AD provider tests.

```bash
go run ./e2e/admin/browserfixture --listen 127.0.0.1:18181
```

Open `http://127.0.0.1:18181/api/v2/admin/ui/`. The fixture accepts:

- OIDC: `Fixture OIDC` (automatic same-origin authorization redirect)
- AD: user `administrator`, password `password`
- break-glass: `valid`

The fixture binds loopback only and must not be used as a production server.
