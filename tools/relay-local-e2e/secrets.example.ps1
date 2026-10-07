# Copy to <repo>\.local\relay-e2e\secrets.ps1 and fill in the placeholders (that directory is git-ignored).
# These values are for a throw-away local database only. Never reuse production credentials here.
#
# Generate the random values in PowerShell, for example:
#   -join ((1..32) | ForEach-Object { '{0:x2}' -f (Get-Random -Maximum 256) })    # 64 hex characters
# (Get-Random is fine for a local test; use a proper CSPRNG for anything real.)

$env:DATA_DIR = (Resolve-Path '.local/relay-e2e/master').Path   # master data dir (created by start-master.ps1)
$env:AUTO_SETUP = 'true'                                        # let the master create its schema and admin on first start

# Local PostgreSQL / Redis. Use a dedicated, empty database and Redis db number.
$env:DATABASE_HOST = '127.0.0.1'
$env:DATABASE_PORT = '5432'
$env:DATABASE_USER = 'postgres'
$env:DATABASE_PASSWORD = '<your local postgres password>'
$env:DATABASE_DBNAME = 'sub2api_relay_e2e'
$env:REDIS_HOST = '127.0.0.1'
$env:REDIS_PORT = '6379'
$env:REDIS_DB = '5'

# Admin account created on first start (used by drive.py and the admin UI).
$env:ADMIN_EMAIL = 'relay-e2e-admin@example.test'
$env:ADMIN_PASSWORD = '<choose a password>'

$env:SERVER_HOST = '127.0.0.1'
$env:SERVER_PORT = '18080'
$env:SERVER_MODE = 'release'
$env:JWT_SECRET = '<64 hex characters>'

# Master side of the master/node channel. Plain TLS port, not behind any proxy.
$env:RELAY_MASTER_LISTEN_ADDR = '127.0.0.1:17443'
# Encrypts the master's private keys on disk. Lose it and every node has to be activated again.
$env:RELAY_KEY_ENCRYPTION_KEY = '<64 hex characters>'

# Let the master call the fake upstream / local hosts over plain http.
$env:SECURITY_URL_ALLOWLIST_ALLOW_INSECURE_HTTP = 'true'
$env:SECURITY_URL_ALLOWLIST_ALLOW_PRIVATE_HOSTS = 'true'
