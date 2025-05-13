package tunnel

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type LastSelection struct {
	Profile      string `json:"profile"`
	InstanceName string `json:"instance_name"`
	InstanceID   string `json:"instance_id"`
	DBEndpoint   string `json:"db_endpoint"`
	DBPort       string `json:"db_port"`
}

var lastSelectionPath = filepath.Join(os.Getenv("HOME"), ".aws-ssm-connect", "last-selections.json")

// WriteLastSelection persists the last successful connection
func WriteLastSelection(sel *LastSelection) error {
	data, err := json.MarshalIndent(sel, "", "  ")
	if err != nil {
		return err
	}
	_ = os.MkdirAll(filepath.Dir(lastSelectionPath), 0700)
	return os.WriteFile(lastSelectionPath, data, 0600)
}

// ReadLastSelection retrieves the previous session selection (if exists)
func ReadLastSelection() (*LastSelection, error) {
	data, err := os.ReadFile(lastSelectionPath)
	if err != nil {
		return nil, err
	}
	var sel LastSelection
	if err := json.Unmarshal(data, &sel); err != nil {
		return nil, err
	}
	return &sel, nil
}
