package cmd

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/ilkerispir/aws-ssm-tunnel/internal/aws"
	"github.com/ilkerispir/aws-ssm-tunnel/internal/tunnel"
)

// QuickConnect establishes a port-forward by filtering instance + selecting DB in same VPC
func QuickConnect(profile, filter string, overridePort int) {
	instances, err := aws.FetchInstances(profile)
	if err != nil {
		log.Fatalf("fetch instances failed: %v", err)
	}

	var selectedInstance *aws.Instance
	for _, inst := range instances {
		if strings.Contains(strings.ToLower(inst.Name), strings.ToLower(filter)) {
			selectedInstance = &inst
			break
		}
	}
	if selectedInstance == nil {
		log.Fatalf("no instance matching filter '%s' found", filter)
	}

	dbs, err := aws.FetchDBs(profile)
	if err != nil {
		log.Fatalf("fetch dbs failed: %v", err)
	}

	var selectedDB *aws.DB
	for _, db := range dbs {
		if db.VpcID == selectedInstance.VpcID && db.Role == "writer" {
			selectedDB = &db
			break
		}
	}
	if selectedDB == nil {
		log.Fatalf("no writer database found for selected instance")
	}

	localPort := selectedDB.Port
	if overridePort != 0 {
		localPort = strconv.Itoa(overridePort)
	}

	fmt.Printf("✔ %s (%s)\n", selectedInstance.Name, selectedInstance.ID)
	fmt.Printf("✔ %s:%s\n", selectedDB.Endpoint, selectedDB.Port)

	err = tunnel.WriteLastSelection(&tunnel.LastSelection{
		Profile:      profile,
		InstanceName: selectedInstance.Name,
		InstanceID:   selectedInstance.ID,
		DBEndpoint:   selectedDB.Endpoint,
		DBPort:       selectedDB.Port,
	})
	if err != nil {
		log.Printf("⚠️ failed to save last selection: %v", err)
	}

	if err := aws.EnsureSSOLogin(profile); err != nil {
		log.Fatalf("SSO login failed: %v", err)
	}

	err = tunnel.StartPortForward(
		profile,
		selectedInstance.Name,
		selectedInstance.ID,
		selectedDB.Endpoint,
		selectedDB.Port,
		localPort,
	)
	if err != nil {
		log.Fatalf("port forwarding failed: %v", err)
	}
}
