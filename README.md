# aws-ssm-tunnel

⚡ CLI tool to port-forward to private **RDS** (and soon **ElastiCache**) databases via EC2 instances using AWS SSM Session Manager.

## Features
- 🔍 Interactive profile / instance / database selection (SSO-aware)
- ⚡ Quick-connect mode via `--profile` and `--filter`
- 🧵 Background port-forward sessions (non-blocking)
- 🔢 PID tracking for active sessions
- 📋 List active sessions with `--list`
- ❌ Kill specific sessions with `--kill <pid>`
- 💥 Kill all sessions with `--kill-all`
- 🧹 Auto cleanup of dead sessions
- 🚫 Detects local port conflicts before forwarding

## Installation

```bash
go install github.com/ilkerispir/aws-ssm-tunnel@latest
