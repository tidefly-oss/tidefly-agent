# Security Policy

## Supported Versions

| Version  | Supported |
|----------|-----------|
| latest   | ✅         |
| < latest | ❌         |

During pre-alpha, only the latest release receives security fixes.

## Reporting a Vulnerability

**Do not open a public GitHub issue for security vulnerabilities.**

Please report security issues via GitHub's private vulnerability reporting:
**Security → Report a vulnerability** in this repository.

Include:
- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if any)

We aim to respond within 48 hours and issue a fix within 7 days for critical issues.

## Security Model

- All Plane ↔ Agent communication is encrypted with **mutual TLS (mTLS)**
- Certificates are issued by the Tidefly internal CA and scoped per worker
- Registration tokens are one-time use and expire after 24 hours
- The agent runs with access to the Docker/Podman socket — treat the host accordingly