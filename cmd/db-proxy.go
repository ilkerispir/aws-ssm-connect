package cmd

import (
	"fmt"
	"log"
	"strings"

	"github.com/ilkerispir/aws-ssm-tunnel/internal/aws"
	"github.com/ilkerispir/aws-ssm-tunnel/internal/tunnel"
	"github.com/ilkerispir/aws-ssm-tunnel/internal/ui"
	"github.com/manifoldco/promptui"
)

// ConnectToDBProxy establishes port-forwarding to a selected DB proxy behind an EC2 instance
func ConnectToDBProxy(profile string, port int) error {
	instances, err := aws.FetchInstances(profile)
	if err != nil {
		return fmt.Errorf("failed to fetch instances: %w", err)
	}
	if len(instances) == 0 {
		return fmt.Errorf("no SSM-managed EC2 instances found")
	}

	// Prompt EC2 instance selection
	var instOptions []string
	for _, inst := range instances {
		instOptions = append(instOptions, fmt.Sprintf("🖥️  %s (%s)", inst.Name, inst.ID))
	}

	instPrompt := promptui.Select{
		Label: "Select EC2 Instance",
		Items: instOptions,
		Searcher: func(input string, index int) bool {
			return strings.Contains(strings.ToLower(instOptions[index]), strings.ToLower(input))
		},
	}
	idx, _, err := instPrompt.Run()
	if err != nil {
		return fmt.Errorf("prompt failed: %w", err)
	}

	selectedInstance := instances[idx]
	dbs, err := aws.FetchDBs(profile)
	if err != nil {
		return fmt.Errorf("failed to fetch databases: %w", err)
	}

	// Filter DBs in same VPC
	var candidates []aws.DB
	var labels []string
	for _, db := range dbs {
		if db.VpcID == selectedInstance.VpcID {
			candidates = append(candidates, db)
			labels = append(labels, ui.FormatDBLabel(db))
		}
	}

	if len(candidates) == 0 {
		return fmt.Errorf("no databases found in same VPC as EC2 instance")
	}

	dbPrompt := promptui.Select{
		Label: "Select DB Proxy to forward",
		Items: labels,
		Searcher: func(input string, index int) bool {
			return strings.Contains(strings.ToLower(labels[index]), strings.ToLower(input))
		},
	}
	dbIdx, _, err := dbPrompt.Run()
	if err != nil {
		return fmt.Errorf("db selection prompt failed: %w", err)
	}

	selectedDB := candidates[dbIdx]
	localPort := selectedDB.Port
	if port != 0 {
		localPort = fmt.Sprint(port)
	}

	log.Printf("🔗 Connecting to %s via %s (%s)...", selectedDB.Endpoint, selectedInstance.Name, selectedInstance.ID)
	return tunnel.StartPortForward(profile, selectedInstance.Name, selectedInstance.ID, selectedDB.Endpoint, selectedDB.Port, localPort)
}
