# Hardened Containerized Diun (Docker Image Update Notifier)

[![CI](https://github.com/underhax/diun-docker/actions/workflows/ci.yml/badge.svg)](https://github.com/underhax/diun-docker/actions/workflows/ci.yml)
[![GitHub last commit](https://img.shields.io/github/last-commit/underhax/diun-docker)](https://github.com/underhax/diun-docker/commits/main)
[![GitHub issues](https://img.shields.io/github/issues/underhax/diun-docker)](https://github.com/underhax/diun-docker/issues)
[![GitHub repo size](https://img.shields.io/github/repo-size/underhax/diun-docker)](https://github.com/underhax/diun-docker)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

Hardened, zero-trust containerized deployment architecture for [Diun (Docker Image Update Notifier)](https://crazymax.dev/diun/) running on a minimal scratch container with strict host Discretionary Access Control (DAC) boundaries and in-memory secret mediation.

## Architecture Highlights

* **Scratch Runtime:** Built `FROM scratch` with CA certificates and no shell, package manager, or system utilities. Ships only the upstream `diun` binary, the persistent database directory mount (`/data`), and a minimal static Go binary `init` that mediates Docker Secrets before launching Diun.
* **In-Memory Secret Mediation:** Universal Docker Secret mediation via `init` discovers `/run/secrets/diun_*` and maps them directly to uppercase `DIUN_*` environment variables in-memory before exec-ing `/usr/local/bin/diun`. Plaintext sensitive credentials (e.g., webhook topics, Telegram bot tokens, chat IDs) never appear in `docker inspect`, container inspection metadata, Compose manifests, or `.env` files.
* **Least-Privilege & Host DAC Boundaries:** The dedicated unprivileged host user (`diun`) has login disabled (`/usr/sbin/nologin`, locked password) and is intentionally NOT added to the host `docker` group. The container runs with `user: "${DIUN_UID}:${DIUN_UID}"` and `group_add: ["${DOCKER_GID}"]`, granting the daemon process access to host-managed configuration and secrets via its primary service group while accessing `/var/run/docker.sock` via the supplementary Docker socket group without elevating host user privileges.
* **Read-Only Root & Dropped Capabilities:** The container root filesystem is mounted strictly read-only (`read_only: true`), all Linux kernel capabilities are dropped (`cap_drop: ALL`), privilege escalation is blocked (`no-new-privileges:true`), and IPC namespace sharing is disabled (`ipc: none`).
* **Storage Hardening:** The local state directory (`/data`) is mounted via a named local volume using restrictive mount options (`rw,noexec,nosuid,nodev`). All bind mounts use Compose long or hardened syntax to prevent automatic host directory creation with default root permissions.
* **Network Isolation:** Runs on an isolated bridge network with inter-container communication disabled (`com.docker.network.bridge.enable_icc: "false"`), allowing required HTTPS egress to public registries and notification endpoints with zero exposed ingress ports.

---

<details>
<summary><strong>Directory Structure</strong></summary>

Production directory layout on the host (`/home/diun`):

```text
├── docker-compose.yaml                # Production deployment manifest
├── .env                               # Host infrastructure variables (DIUN_UID, DOCKER_GID)
├── custom-images.yaml                 # File-provider watched image list
├── secrets/                           # Docker secrets directory
│   ├── diun_notif_ntfy_endpoint       # Ntfy server base URL (e.g. https://ntfy.example.com)
│   ├── diun_notif_ntfy_topic          # Ntfy target topic
│   └── diun_notif_ntfy_token          # Ntfy access token (optional, leave empty if unauthenticated)
└── data/                              # BoltDB database directory (diun.db)
```

</details>

---

<details>
<summary><strong>Deployment &amp; Setup</strong></summary>

### Prerequisites

* **Docker Engine & Compose:** Ensure Docker Engine and the Docker Compose plugin are installed on the host. Follow the official installation guide for [Ubuntu](https://docs.docker.com/engine/install/ubuntu/#install-using-the-repository).

### 1. Create a Dedicated System User

Choose a dedicated, unprivileged numeric ID unique to this service on your host and verify that it is not already in use by another user or group:

```bash
# Choose an unused numeric UID/GID unique to this service on your host
DIUN_UID=<CHOSEN_UID>

getent passwd ${DIUN_UID}
getent group ${DIUN_UID}
```

> **Important:** Both commands must return empty output. Each containerized service must have its own dedicated unprivileged user and group to enforce strict host DAC privilege separation between different stacks.

Create an unprivileged system account with a matching user-group (`-U`) and disabled login shell. Do NOT add this user to the `docker` group:

```bash
sudo useradd -m -d /home/diun -s /usr/sbin/nologin -u ${DIUN_UID} -U diun
```

### 2. Create Directory Layout & Assign Ownership

Create the base directory and assign ownership:

```bash
sudo mkdir -p /home/diun
sudo chown root:diun /home/diun
sudo chmod 0750 /home/diun
```

Create runtime subdirectories from `root` and assign strict DAC permissions:

```bash
sudo mkdir -p /home/diun/{data,secrets}

sudo chown root:diun /home/diun/secrets
sudo chmod 0750 /home/diun/secrets

sudo chown diun:diun /home/diun/data
sudo chmod 0700 /home/diun/data
```

### 3. Deploy Project Files

Download the production manifests directly into `/home/diun`:

```bash
REPO="https://raw.githubusercontent.com/underhax/diun-docker/main"

sudo curl -fsSL ${REPO}/docker-compose.yaml -o /home/diun/docker-compose.yaml
sudo curl -fsSL ${REPO}/custom-images.yaml -o /home/diun/custom-images.yaml
sudo curl -fsSL ${REPO}/.env.example -o /home/diun/.env
sudo chmod 0600 /home/diun/{docker-compose.yaml,.env}
sudo chown root:root /home/diun/{docker-compose.yaml,.env}
sudo chown root:diun /home/diun/custom-images.yaml
sudo chmod 0640 /home/diun/custom-images.yaml
```

### 4. Configure User & Group Identifiers

Append the user UID and the actual runtime GID of `/var/run/docker.sock` directly into `.env`:

```bash
printf "DIUN_UID=%s\nDOCKER_GID=%s\n" "${DIUN_UID}" "$(stat -c '%g' /var/run/docker.sock)" | sudo tee -a /home/diun/.env > /dev/null
```

### 5. Configure Secrets

Create the required secret files inside `/home/diun/secrets`.

#### Ntfy Notification Setup (Production Example)
Diun automatically enables Ntfy notifications when a topic is defined. Create and populate the secret files manually using a text editor (such as `nano`) so sensitive tokens and topics never leak into shell history (`.bash_history`):

```bash
# Ntfy server endpoint (e.g. https://ntfy.example.com or https://ntfy.sh)
sudo nano /home/diun/secrets/diun_notif_ntfy_endpoint

# Private topic name (acts as secret channel)
sudo nano /home/diun/secrets/diun_notif_ntfy_topic

# Access token (optional; if authentication is not required, create an empty file)
sudo nano /home/diun/secrets/diun_notif_ntfy_token

sudo chown root:diun /home/diun/secrets/*
sudo chmod 0440 /home/diun/secrets/*
```

> **Note:** If access token authentication is not required on your Ntfy instance, create an empty token secret file (`sudo touch /home/diun/secrets/diun_notif_ntfy_token`). The `init` binary automatically detects and skips empty secret files so no empty `Authorization` headers are sent.

#### Universal Secret Mapping
Any sensitive configuration parameter across all notification providers ([official documentation](https://crazymax.dev/diun/config/notif/)) can be mapped securely without exposing credentials in `.env` or `docker inspect`:
1. Save the secret with the prefix `diun_` in `/home/diun/secrets/` (e.g. `diun_notif_telegram_token`, `diun_notif_telegram_chatids`, `diun_notif_mail_password`, etc.).
2. Add the secret name to `secrets` in `docker-compose.yaml`.
3. The `init` binary automatically discovers `/run/secrets/diun_*`, converts filenames to uppercase `DIUN_*` environment variables in-memory, and launches Diun. Plaintext secrets never appear in container inspection metadata.

### 6. Host Permissions &amp; Privilege Separation

Keep deployment inputs and service configuration outside the service account's write access. The container runs with numeric `DIUN_UID:DIUN_UID` (plus supplementary `DOCKER_GID`) and needs write access only to `/home/diun/data`. A compromised Diun process cannot replace its Compose manifest, configuration, or secret files.

Use `root:diun` for host-managed, service-readable files, and `diun:diun` only for runtime storage:

| Host path | Owner | Mode | Reason |
| --- | --- | --- | --- |
| `/home/diun` | `root:diun` | `0750` | Base directory; allows service traversal without access for other users |
| `docker-compose.yaml`, `.env` | `root:root` | `0600` | Host-only manifests and environment settings |
| `custom-images.yaml` | `root:diun` | `0640` | Read-only file-provider image list |
| `secrets/` directory | `root:diun` | `0750` | Traverse-only access for the service group |
| `secrets/*` | `root:diun` | `0440` | Read-only to the service group |
| `data/` and subdirectories | `diun:diun` | `0700` | Private runtime storage and nested directories |
| `data/*` files | `diun:diun` | `0600` | Private read-write storage for BoltDB database |

#### Apply or restore host permissions

Run as a host administrator after creating the files and after any configuration or secret update:

```bash
sudo chown root:diun /home/diun
sudo chmod 0750 /home/diun

sudo chown root:root /home/diun/docker-compose.yaml /home/diun/.env
sudo chmod 0600 /home/diun/docker-compose.yaml /home/diun/.env

sudo chown root:diun /home/diun/custom-images.yaml
sudo chmod 0640 /home/diun/custom-images.yaml

sudo chown -R root:diun /home/diun/secrets
sudo find /home/diun/secrets -type d -exec chmod 0750 {} +
sudo find /home/diun/secrets -type f -exec chmod 0440 {} +

sudo chown -R diun:diun /home/diun/data
sudo find /home/diun/data -type d -exec chmod 0700 {} +
sudo find /home/diun/data -type f -exec chmod 0600 {} +
```

### 7. Start or Update the Stack

Initial deployment and all subsequent service updates use the same two commands (pulling the latest release and starting or recreating the container in detached mode):

```bash
sudo docker compose -f /home/diun/docker-compose.yaml pull
sudo docker compose -f /home/diun/docker-compose.yaml up -d
```

</details>

---

<details>
<summary><strong>Operation &amp; Maintenance</strong></summary>

### Verifying Service Health

Check container status and resource usage:

```bash
sudo docker compose -f /home/diun/docker-compose.yaml ps
sudo docker stats --no-stream diun
```

Inspect runtime logs:

```bash
sudo docker compose -f /home/diun/docker-compose.yaml logs -f
```

### Managing Watched Images

To track external images, update `/home/diun/custom-images.yaml`. After modifying the file, restart the service:

```bash
sudo docker compose -f /home/diun/docker-compose.yaml restart
```

### Tearing Down the Stack

Stop and remove the container and its internal network:

```bash
sudo docker compose -f /home/diun/docker-compose.yaml down
```

</details>

---

<details>
<summary><strong>Development</strong></summary>

For local image building, static analysis, unit testing, and CI/CD pipeline documentation, see **[DEVELOPMENT.md](DEVELOPMENT.md)**.

</details>

---

<details>
<summary><strong>License &amp; Attribution</strong></summary>

## License & Attribution

This repository and deployment architecture are licensed under the [MIT License](LICENSE). This project is an independent containerized deployment architecture and is not affiliated with, endorsed, or sponsored by CrazyMax.

**Diun License & Ownership:** Diun is open-source software licensed under the [Apache License 2.0](https://github.com/crazy-max/diun/blob/master/LICENSE) and developed by [CrazyMax](https://crazymax.dev).

</details>
