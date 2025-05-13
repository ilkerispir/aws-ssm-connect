package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/ilkerispir/aws-ssm-tunnel/cmd"
)

func main() {
	// CLI flags
	profile := flag.String("profile", "", "AWS profile name")
	filter := flag.String("filter", "", "Instance name filter")
	port := flag.Int("port", 0, "Local port to bind (optional)")
	list := flag.Bool("list", false, "List active port-forward sessions")
	kill := flag.Int("kill", 0, "Kill a port-forward session by PID")
	killAll := flag.Bool("kill-all", false, "Kill all active port-forward sessions")
	ssm := flag.Bool("ssm", false, "Start standard SSM shell session to EC2")
	help := flag.Bool("help", false, "Show usage information")
	version := flag.Bool("version", false, "Show version")
	dbproxy := flag.Bool("db-proxy", false, "Start port-forward to DB proxy via EC2")
	flag.Parse()

	if *ssm || *dbproxy || (*profile == "" && *filter != "") {
		if err := cmd.SelectProfileIfEmpty(profile); err != nil {
			log.Fatalf("profile selection failed: %v", err)
		}
	}

	// Graceful cleanup on Ctrl+C or SIGTERM
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		cmd.CleanupAndExit()
	}()

	// Command dispatch
	switch {
	case *help:
		cmd.ShowHelper()
	case *version:
		cmd.ShowVersion()
	case *list:
		cmd.ListSessions()
	case *kill != 0:
		cmd.KillSession(*kill)
	case *killAll:
		cmd.KillAllSessions()
	case *dbproxy:
		if err := cmd.ConnectToDBProxy(*profile, *port); err != nil {
			log.Fatalf("DB proxy connection failed: %v", err)
		}
	case *profile != "" && *filter != "":
		cmd.QuickConnect(*profile, *filter, *port)
	case *ssm:
		err := cmd.StartSSMSession(*profile)
		if err != nil {
			log.Fatalf("SSM session failed: %v", err)
		}
	default:
		if err := cmd.Interactive(); err != nil {
			panic(err)
		}
	}
}
