package cmd

import "fmt"

func ShowHelper() {
	fmt.Println(`
AWS SSM Tunnel CLI

Usage:
  aws-ssm-tunnel                                          # Interactive mode (prompts)
  aws-ssm-tunnel --profile <profile> --filter <keyword>   # Quick connect to database
  aws-ssm-tunnel --ssm --profile <profile>                # Start SSM shell session to EC2
  aws-ssm-tunnel --db-port-forward --profile <profile>    # Port-forward to a selected DB proxy via EC2
  aws-ssm-tunnel --list                                   # List active port-forward sessions
  aws-ssm-tunnel --kill <pid>                             # Kill a specific port-forward session by PID
  aws-ssm-tunnel --kill-all                               # Kill all active port-forward sessions
  aws-ssm-tunnel --help                                   # Show this helper message
  aws-ssm-tunnel --version                                # Show version

Flags:
--profile     AWS profile to use (e.g., dev, prod)
--filter      Filter for EC2 instance name (for DB tunneling)
--port        Local port override (optional)
--ssm         Start standard SSM shell session to EC2 instance
--db-port-forward    Port-forward to a selected RDS proxy via EC2
--list        Show active port-forward sessions
--kill        Kill a session by PID
--kill-all    Kill all active sessions
--version     Show version info
--help        Show this help message

Examples:
aws-ssm-tunnel --profile dev --filter prod-db
aws-ssm-tunnel --ssm --profile dev
aws-ssm-tunnel --db-port-forward --profile dev
aws-ssm-tunnel --kill 12345
aws-ssm-tunnel --list
`)
}
