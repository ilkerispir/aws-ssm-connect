package cmd

import (
	"fmt"
	"strings"

	"github.com/ilkerispir/aws-ssm-tunnel/internal/aws"
	"github.com/manifoldco/promptui"
)

func SelectProfileIfEmpty(profile *string) error {
	if *profile != "" {
		return nil
	}

	profiles, err := aws.FetchProfiles()
	if err != nil {
		return fmt.Errorf("failed to load AWS profiles: %w", err)
	}
	if len(profiles) == 0 {
		return fmt.Errorf("no AWS profiles found")
	}

	prompt := promptui.Select{
		Label: "Select AWS Profile",
		Items: profiles,
		Searcher: func(input string, index int) bool {
			return strings.Contains(strings.ToLower(profiles[index]), strings.ToLower(input))
		},
	}
	idx, _, err := prompt.Run()
	if err != nil {
		return err
	}

	*profile = profiles[idx]
	return nil
}
