# aws-ssm-rds-proxy

⚡ **CLI tool to port-forward to private RDS databases via EC2 instances using AWS SSM Session Manager.**

## Features
- Interactive profile/instance/database selection (SSO-aware)
- Quick connect mode with `--profile` and `--filter`
- Background port-forward sessions without blocking the terminal
- PID tracking for active sessions
- List active port-forward sessions with `--list`
- Kill specific sessions with `--kill <pid>`
- Kill all sessions at once with `--kill-all`
- Automatic cleanup of dead sessions
- Local port conflict detection before forwarding

## Installation
```bash
go install github.com/ilkerispir/aws-ssm-rds-proxy@latest
