# CPA production deployment

CPA runs independently in `/opt/cli-proxy-api` and joins the existing
`deploy_sub2api-network` Docker network. Sub2API can reach it at
`http://cli-proxy-api:8317` after an account is configured. No Sub2API account
or routing is changed by this deployment.

The CPA port is published only on host loopback for Nginx. The IP-based Nginx
virtual host exposes only `/management.html` and `/v0/management/` on HTTPS
port 443. The self-signed certificate has the current public IP in its SAN;
browsers will warn until a trusted certificate or domain replaces it.

Open `https://CURRENT_PUBLIC_IP:443/management.html`. The panel asks for the
CPA Management Key. Retrieve it over SSH with
`sudo cat /opt/cli-proxy-api/management.key`. CPA replaces the value in
`config.yaml` with a bcrypt hash at startup, so that config value cannot be
used to log in. The `api-keys` value is a separate key reserved for Sub2API.
No model accounts have been added yet.

On the production host, back up `config.yaml`, `management.key`, and `auths/`
privately. Rotate the management key with
`sudo bash /opt/cli-proxy-api/reset-management-key.sh`. Do not put the
management key into Sub2API. The image is pinned to a verified digest; update
it deliberately when upgrading CPA.

If the instance public IP changes, regenerate the IP certificate with
`sudo bash /opt/cli-proxy-api/enable-ip-console.sh NEW_IP`, then check
`sudo nginx -t` and reload Nginx.

Run `sudo bash /opt/cli-proxy-api/verify.sh` to check both generated keys.
