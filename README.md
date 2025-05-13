# aws-ssm-connect

⚡ A powerful CLI to port-forward into private **RDS**, **Aurora**, and **ElastiCache** (Redis, Memcached) endpoints through EC2 instances using AWS SSM Session Manager — fully interactive, no SSH required.

## Features
- ☁️ Interactive profile / EC2 / database selection (SSO-aware)
- 🚀 Quick connect via `--profile` and `--filter`
- 🔐 SSM-based secure access (no open ports or bastion hosts)
- 🔄 Port-forward RDS, Aurora, Redis, Memcached — all in one tool
- 🧵 Background port-forwarding (non-blocking, persistent)
- 🔢 Tracks active sessions by PID
- 📋 List active tunnels with `--list`
- ❌ Kill specific tunnels with `--kill <pid>`
- 💥 Kill all tunnels with `--kill-all`
- 🧹 Automatically cleans up dead sessions
- ⚠️ Prevents local port conflicts

## Installation

```bash
brew tap ilkerispir/tap
brew install aws-ssm-connect