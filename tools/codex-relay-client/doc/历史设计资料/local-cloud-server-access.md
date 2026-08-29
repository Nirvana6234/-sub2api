# 实验室远程服务器

本项目中提到“实验室远程服务器”时，固定指向本文档记录的这台服务器及部署目标。

# Local Cloud Server Access

This project uses the following SSH connection for the current cloud server:

```text
Host: 18.182.62.112
SSH user: ubuntu
SSH port: 22
Private key on this Windows machine: C:\Users\Administrator\.ssh\dldzyjs.pem
```

Connect from Windows CMD or PowerShell:

```cmd
ssh -i C:\Users\Administrator\.ssh\dldzyjs.pem ubuntu@18.182.62.112
```

Notes:

- Do not commit or copy the private key contents into the repository.
- The original key file was also present at `E:\dldzyjs.pem`, but the usable copy with strict OpenSSH permissions is under `C:\Users\Administrator\.ssh\dldzyjs.pem`.
- Verified connection on 2026-07-15: `SSH_OK`, host `ip-172-31-38-173`, user `ubuntu`.

## Backup retention

- Run `E:\web_transform\.local\backup-lab-postgres.ps1` when a local copy of the remote PostgreSQL database is needed. It stores verified dump files under `E:\web_transform\.local\lab-postgres-backups`.
- Database dumps are retained locally and are not subject to the three-version deployment retention rule.
- The remote deployment script keeps the latest three `sub2api-code-*` directories and the latest three `deploy-sub2api:rollback-*` images after a healthy release.
