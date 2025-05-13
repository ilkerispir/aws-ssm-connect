package ui

import (
	"fmt"
	"strings"

	"github.com/ilkerispir/aws-ssm-connect/internal/aws"
	"github.com/manifoldco/promptui"
)

// PromptProfile prompts user to select an AWS profile
func PromptProfile(profiles []string) (string, error) {
	// display labels with emoji
	var labels []string
	for _, p := range profiles {
		labels = append(labels, fmt.Sprintf("☁️ %s", p))
	}

	prompt := promptui.Select{
		Label: "Select AWS Profile",
		Items: labels,
		Searcher: func(input string, index int) bool {
			return strings.Contains(strings.ToLower(labels[index]), strings.ToLower(input))
		},
	}

	idx, _, err := prompt.Run()
	if err != nil {
		return "", err
	}
	return profiles[idx], nil
}

// PromptInstance prompts user to select an EC2 instance
func PromptInstance(instances []aws.Instance) (aws.Instance, error) {
	var labels []string
	for _, inst := range instances {
		labels = append(labels, fmt.Sprintf("🖥  %s (%s)", inst.Name, inst.ID))
	}
	prompt := promptui.Select{
		Label: "Select EC2 Instance",
		Items: labels,
		Searcher: func(input string, index int) bool {
			return strings.Contains(strings.ToLower(labels[index]), strings.ToLower(input))
		},
	}
	idx, _, err := prompt.Run()
	if err != nil {
		return aws.Instance{}, err
	}
	return instances[idx], nil
}

// PromptDatabase prompts user to select a database
func PromptDatabase(dbs []aws.DB) (aws.DB, error) {
	var labels []string
	for _, db := range dbs {
		labels = append(labels, FormatDBLabel(db))
	}
	prompt := promptui.Select{
		Label: "Select Database",
		Items: labels,
		Searcher: func(input string, index int) bool {
			return strings.Contains(strings.ToLower(labels[index]), strings.ToLower(input))
		},
	}
	idx, _, err := prompt.Run()
	if err != nil {
		return aws.DB{}, err
	}
	return dbs[idx], nil
}

// FilterDBsByVPC filters databases by VPC ID
func FilterDBsByVPC(dbs []aws.DB, vpcID string) []aws.DB {
	var filtered []aws.DB
	for _, db := range dbs {
		if db.VpcID == vpcID {
			filtered = append(filtered, db)
		}
	}
	return filtered
}

// FormatDBLabel returns a pretty label for DB selection
func FormatDBLabel(db aws.DB) string {
	engine := aws.DetectEngineByPort(db.Port)

	roleLabel := ""
	switch db.Role {
	case "writer":
		roleLabel = "✍️ Writer"
	case "reader":
		roleLabel = "📖 Reader"
	case "primary":
		roleLabel = "📕 Primary"
	case "replica":
		roleLabel = "📘 Replica"
	case "instance":
		roleLabel = "🧩 Instance"
	default:
		if strings.HasPrefix(db.Role, "redis") {
			if strings.Contains(db.Role, "primary") {
				roleLabel = "📕 Redis Primary"
			} else {
				roleLabel = "📘 Redis Replica"
			}
		} else if strings.HasPrefix(db.Role, "memcached") {
			roleLabel = "📗 Memcached"
		} else if strings.HasPrefix(db.Role, "valkey") {
			roleLabel = "📙 Valkey"
		} else {
			roleLabel = "❔"
		}
	}

	return fmt.Sprintf("🛢️ [%s] %s - %s:%s", engine, roleLabel, db.Endpoint, db.Port)
}
