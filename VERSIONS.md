# Runtime versions

The agent pins the complete multi-platform image digest as well as the human-readable version tag.

| Component | Version | Image |
| --- | --- | --- |
| Unleash server | 8.1.0 | `unleashorg/unleash-server:8.1.0@sha256:16f3ffb914880e7d0f23629a0c1b77aebea3aa619b0305f76eb50b3fb75998a9` |
| Unleash Edge (OSS) | 20.4.1 | `unleashorg/unleash-edge:v20.4.1@sha256:16fb3d481eb2fc8b5981ed1c3d32b684c47ad9d6e41e375dcaaaee27991e6368` |
| PostgreSQL | 17.8 Alpine | `postgres:17.8-alpine@sha256:3430fe182f5065a6ea505c3d432d2c7fff18fbab954df8f277c1dbf4c70124af` |

This combination follows the upstream Unleash deployment contract: the server uses PostgreSQL through the individual `DATABASE_*` settings, and Edge uses a backend API token to refresh from the server. The compatibility baseline is covered by the opt-in local lifecycle test, which creates and enables a flag through the admin API and reads it through Edge.

Upstream references:

- [Unleash server 8.1.0 release](https://github.com/Unleash/unleash/releases/tag/v8.1.0)
- [Unleash Edge 20.4.1 release](https://github.com/Unleash/unleash-edge/releases/tag/unleash-edge-v20.4.1)
- [Unleash server Docker composition](https://github.com/Unleash/unleash/blob/v8.1.0/docker-compose.yml)
- [Unleash Edge Docker composition](https://github.com/Unleash/unleash-edge/blob/unleash-edge-v20.4.1/examples/docker-compose.yml)
- [Official PostgreSQL image](https://hub.docker.com/_/postgres)
