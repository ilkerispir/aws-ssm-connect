package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/ilkerispir/aws-ssm-tunnel/internal/aws"
	"github.com/manifoldco/promptui"
)

// StartSSMSession starts a standard SSM shell session to a selected EC2 instance
func StartSSMSession(profile string) error {
	instances, err := aws.FetchInstances(profile)
	if err != nil {
		return fmt.Errorf("fetch instances failed: %w", err)
	}
	if len(instances) == 0 {
		return fmt.Errorf("no SSM-managed instances found for profile %s", profile)
	}

	options := make([]string, len(instances))
	for i, inst := range instances {
		options[i] = fmt.Sprintf("🖥️  %s (%s)", inst.Name, inst.ID)
	}

	prompt := promptui.Select{
		Label: fmt.Sprintf("Select EC2 instance to connect (profile: %s)", profile),
		Items: options,
		Searcher: func(input string, index int) bool {
			return strings.Contains(strings.ToLower(options[index]), strings.ToLower(input))
		},
	}

	idx, _, err := prompt.Run()
	if err != nil {
		return fmt.Errorf("prompt failed: %w", err)
	}

	instance := instances[idx]

	fmt.Printf("\n✅ Starting SSM shell session to: %s (%s)\n\n", instance.Name, instance.ID)

	cmd := exec.Command(
		"aws", "ssm", "start-session",
		"--profile", profile,
		"--target", instance.ID,
		"--document-name", "AWS-StartInteractiveCommand",
		"--parameters", "command=bash",
	)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	return cmd.Run()
}
