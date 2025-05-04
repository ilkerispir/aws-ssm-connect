package cmd

import "fmt"

var Version = "dev"

func ShowVersion() {
	fmt.Println("aws-ssm-tunnel version:", Version)
}
