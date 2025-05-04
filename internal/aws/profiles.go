package aws

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/ini.v1"
)

// FetchProfiles parses ~/.aws/config and returns all available profile names
func FetchProfiles() ([]string, error) {
	path := filepath.Join(os.Getenv("HOME"), ".aws", "config")
	cfg, err := ini.Load(path)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, section := range cfg.Sections() {
		name := section.Name()
		if strings.HasPrefix(name, "profile ") {
			names = append(names, strings.TrimPrefix(name, "profile "))
		}
	}
	sort.Strings(names)
	return names, nil
}

// EnsureSSOLogin runs `aws sso login` if current token is expired or invalid
func EnsureSSOLogin(profile string) error {
	cmd := exec.Command("aws", "sts", "get-caller-identity", "--profile", profile)
	if err := cmd.Run(); err == nil {
		return nil // valid token, no need to login
	}

	fmt.Printf("⚡ Attempting SSO login for profile '%s'...\n", profile)
	loginCmd := exec.Command("aws", "sso", "login", "--profile", profile)
	loginCmd.Stdout = os.Stdout
	loginCmd.Stderr = os.Stderr
	loginCmd.Stdin = os.Stdin
	return loginCmd.Run()
}
