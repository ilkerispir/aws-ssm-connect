package cmd

import (
	"fmt"
	"log"

	"github.com/ilkerispir/aws-ssm-tunnel/internal/aws"
	"github.com/ilkerispir/aws-ssm-tunnel/internal/tunnel"
	"github.com/ilkerispir/aws-ssm-tunnel/internal/ui"
)

// Interactive mode with profile, instance and DB prompts
func Interactive() error {
	profiles, err := aws.FetchProfiles()
	if err != nil {
		return fmt.Errorf("load profiles failed: %w", err)
	}

	profile, err := ui.PromptProfile(profiles)
	if err != nil {
		return fmt.Errorf("profile prompt failed: %w", err)
	}

	if err := aws.EnsureSSOLogin(profile); err != nil {
		return fmt.Errorf("SSO login failed: %w", err)
	}

	instances, err := aws.FetchInstances(profile)
	if err != nil {
		return fmt.Errorf("fetch instances failed: %w", err)
	}

	instance, err := ui.PromptInstance(instances)
	if err != nil {
		return fmt.Errorf("instance prompt failed: %w", err)
	}

	dbs, err := aws.FetchDBs(profile)
	if err != nil {
		return fmt.Errorf("fetch dbs failed: %w", err)
	}

	filtered := ui.FilterDBsByVPC(dbs, instance.VpcID)
	if len(filtered) == 0 {
		fmt.Println("No databases found in the same VPC.")
		return nil
	}

	db, err := ui.PromptDatabase(filtered)
	if err != nil {
		return fmt.Errorf("database prompt failed: %w", err)
	}

	err = tunnel.WriteLastSelection(&tunnel.LastSelection{
		Profile:      profile,
		InstanceName: instance.Name,
		InstanceID:   instance.ID,
		DBEndpoint:   db.Endpoint,
		DBPort:       db.Port,
	})
	if err != nil {
		log.Printf("⚠️ failed to save last selection: %v", err)
	}

	localPort := db.Port // you can parametrize this if needed

	err = tunnel.StartPortForward(profile, instance.Name, instance.ID, db.Endpoint, db.Port, localPort)
	if err != nil {
		return fmt.Errorf("port forwarding failed: %w", err)
	}

	return nil
}
