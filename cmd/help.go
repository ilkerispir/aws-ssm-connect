package cmd

import "fmt"

func ShowHelper() {
	fmt.Println(`
AWS SSM RDS Proxy - Quick Connect Tool

Usage:
  aws-ssm-tunnel                                          # Interactive mode (prompts)
  aws-ssm-tunnel --profile <profile> --filter <keyword>   # Quick connect mode
  aws-ssm-tunnel --list                                   # List active port-forward sessions
  aws-ssm-tunnel --kill <pid>                             # Kill a specific port-forward session by PID
  aws-ssm-tunnel --kill-all                               # Kill all active port-forward sessions
  aws-ssm-tunnel --help                                   # Show this helper message
  aws-ssm-tunnel --version                                # Show version

Flags:
--profile    AWS profile name to use (e.g., my-aws-profile)
--filter     Keyword to match instance name (e.g., prod, dev, uat)
--list       List active port-forward sessions
--kill       Kill a specific session by PID
--kill-all   Kill all active port-forward sessions
--help       Show this helper message
--version    Show version

Examples:
aws-ssm-tunnel --profile my-aws-profile --filter dev
aws-ssm-tunnel --list
aws-ssm-tunnel --kill 12345
aws-ssm-tunnel --kill-all

Behavior:
- Searches for an instance matching the filter keyword
- Finds a writer database (or standalone RDS instance) in the same VPC
- Starts a background port-forwarding session automatically
- Manages sessions with PID tracking
- Automatically cleans up dead sessions
- Prevents port conflicts by checking local port availability
`)
}
