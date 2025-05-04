// cmd/version.go
package cmd

import "fmt"

var version = "dev"

func ShowVersion() {
	fmt.Println("aws-ssm-tunnel version:", version)
}
