# Codefly Unleash service agent

`codefly.dev/unleash` runs an Unleash server, its dedicated PostgreSQL database, and Unleash Edge as one Codefly service.

- `admin` is a module-visible endpoint for the Unleash UI and admin API.
- `edge` is the only public endpoint and serves client SDK evaluation traffic.
- PostgreSQL state is isolated from application stores and persisted locally and in Kubernetes.
- Kubernetes output is a transport-neutral Kustomize bundle containing only references to externally managed secrets.

See [VERSIONS.md](VERSIONS.md) for the pinned runtime versions and compatibility policy.
