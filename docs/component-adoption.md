---
description: Connect a platform component to a PostgreSQL claim and operate its credentials.
audience: [human]
load: When adopting the database service in a component
---

# Adopt the database service in a component

Use a claim to obtain a namespace-local PostgreSQL Secret. The component owns its
tables, migrations, and connection handling; the database operator provisions the
database role and access. An administrator must first [enable the service and make a
provider available](installation-and-usage.md).

## Choose a claim

| Claim | Use when | What the component receives |
| --- | --- | --- |
| `SchemaClaim` | The component can keep its tables in a dedicated schema within a provider database. | A role with access to that schema and a Secret containing its name. |
| `DatabaseClaim` | The component needs a database-scoped role or a dedicated database. | A role with access to the selected database. A named database can be created only when the provider permits it. |

Both claims accept `spec.extensions`; the day-one allow-list is `vector`, and the list is immutable after creation.

For high availability or backups, choose an External provider backed by a database
that meets those requirements. The default Internal provider has neither automated
backup and restore nor high availability. An administrator configures the External
provider; the component selects it with `spec.provider.name` or
`spec.provider.selector`.

## Provision a schema

This example assumes the component namespace `my-app` exists and the
`DatabaseService` default provider is Ready. Save the following as
`schemaclaim.yaml`:

```yaml
apiVersion: infrastructure.opendatahub.io/v1alpha1
kind: SchemaClaim
metadata:
  name: app-schema
  namespace: my-app
spec:
  access: ReadWrite
```

Apply the claim and wait for the operator to provision its role and Secret:

```sh
oc apply -f schemaclaim.yaml
oc wait -n my-app --for=condition=Provisioned schemaclaim/app-schema --timeout=5m
oc get secret sc-app-schema -n my-app
```

The default Secret name for a `SchemaClaim` is `sc-` followed by the claim
name. Gate component startup on `Provisioned=True` and the Secret being present;
the claim's `phase` is only a human-readable summary. If provisioning fails,
inspect `oc describe schemaclaim app-schema -n my-app` and the selected
`DatabaseProvider` conditions.

## Connect and run a migration

Mount the Secret as files in the component Pod. The Secret has discrete fields,
not a connection URI: `host`, `port`, `user`, `password`, `dbname`, and `sslmode`.
`SchemaClaim` adds `schema`; a provider with a CA also adds `ca.crt`. Configure
your PostgreSQL driver from these fields and use `schema` in migrations or the
connection's search path. See the [credential contract](../ARCHITECTURE.md#credential-contract).

This Job is a runnable connection and migration check. Save it as
`database-check.yaml` after creating the claim above:

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: database-check
  namespace: my-app
spec:
  backoffLimit: 0
  template:
    spec:
      restartPolicy: Never
      containers:
      - name: check
        image: postgres:16
        command: [/bin/sh, -ec]
        args:
        - |
          export PGHOST="$(cat /db/host)"
          export PGPORT="$(cat /db/port)"
          export PGUSER="$(cat /db/user)"
          export PGPASSWORD="$(cat /db/password)"
          export PGDATABASE="$(cat /db/dbname)"
          export PGSSLMODE="$(cat /db/sslmode)"
          if [ -f /db/ca.crt ]; then
            export PGSSLROOTCERT=/db/ca.crt
          fi
          schema="$(cat /db/schema)"
          psql -X -qAt -v ON_ERROR_STOP=1 -v schema="$schema" <<'SQL'
          CREATE TABLE IF NOT EXISTS :"schema".adopter_check (value integer PRIMARY KEY);
          INSERT INTO :"schema".adopter_check VALUES (42) ON CONFLICT (value) DO NOTHING;
          SELECT value FROM :"schema".adopter_check WHERE value = 42;
          SQL
        volumeMounts:
        - name: database
          mountPath: /db
          readOnly: true
      volumes:
      - name: database
        secret:
          secretName: sc-app-schema
```

```sh
oc apply -f database-check.yaml
oc wait -n my-app --for=condition=Complete job/database-check --timeout=5m
oc logs -n my-app job/database-check
```

The log should print `42`. If the Job fails, inspect its Pod logs and the claim
conditions. For `sslmode=verify-full`, the consumer must trust the mounted
`ca.crt` and connect using the Secret's `host` value so hostname verification
succeeds. Do not print the password or assembled connection string in logs.

For a `DatabaseClaim`, use the [claim example](installation-and-usage.md#request-a-database)
and mount its Secret, which defaults to the claim name. It has no `schema` key.
Use the same connection fields in the Job above, omit the `schema` file read,
and migrate into an application-owned schema:

```sql
CREATE SCHEMA IF NOT EXISTS app_owned;
CREATE TABLE IF NOT EXISTS app_owned.adopter_check (value integer PRIMARY KEY);
```

Do not assume a `DatabaseClaim` role can create objects in `public`.

## Handle credential changes and removal

If a claim Secret is lost, reconciliation can recreate it with a different
password. A long-running component must reload changed Secret data and replace
its connection pool, or restart its Pods to read the new credentials. Values
loaded into environment variables at Pod creation do not update in that Pod.
The operator does not provide scheduled or user-triggered credential rotation.

Keep the claim's bound `DatabaseProvider` available until the claim is deleted.
Then the operator can remove the PostgreSQL role and credentials Secret. If the
provider is missing, cleanup removes the Secret but cannot revoke or drop the
role or delete claim data, even with `deletionPolicy: Delete`. Previously issued
credentials and active sessions may retain access while PostgreSQL is reachable.
An administrator must disable login, terminate active sessions, and drop the
orphaned role; if `Delete` was requested, also remove the eligible schema or
named database.

Claim data is retained by default. Set `spec.deletionPolicy: Delete` only when
the component intends the operator to delete its eligible schema or named
database. A shared provider default database is never deleted with a claim.
A retained schema keeps the deleted claim's UID as its ownership marker. With
default schema naming, recreating a `SchemaClaim` with the same name selects
that schema but is rejected because the new claim has a different UID. Arrange
administrator-assisted recovery or migration of retained data before recreating
the claim.
