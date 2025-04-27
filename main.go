package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/manifoldco/promptui"
	"gopkg.in/ini.v1"
)

type Instance struct {
	ID    string
	Name  string
	VpcID string
}

type DB struct {
	Endpoint string
	Port     string
	VpcID    string
	Role     string
}

type LastSelection struct {
	Profile      string `json:"profile"`
	InstanceName string `json:"instance_name"`
	InstanceID   string `json:"instance_id"`
	DBEndpoint   string `json:"db_endpoint"`
	DBPort       string `json:"db_port"`
}

var (
	awsPid            int
	lastSelectionPath = filepath.Join(os.Getenv("HOME"), ".aws-ssm-rds-proxy", "last-selections.json")
)

func fetchProfiles() ([]string, error) {
	path := filepath.Join(os.Getenv("HOME"), ".aws", "config")
	cfg, err := ini.Load(path)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, section := range cfg.Sections() {
		n := section.Name()
		if strings.HasPrefix(n, "profile ") {
			names = append(names, strings.TrimPrefix(n, "profile "))
		}
	}
	sort.Strings(names)
	return names, nil
}

func ensureSSOLogin(profile string) error {
	fmt.Printf("⚡ Attempting SSO login for profile '%s'...\n", profile)
	cmd := exec.Command("aws", "sso", "login", "--profile", profile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func fetchInstances(profile string) ([]Instance, error) {
	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithSharedConfigProfile(profile))
	if err != nil {
		return nil, err
	}
	ssmClient := ssm.NewFromConfig(cfg)
	paginator := ssm.NewDescribeInstanceInformationPaginator(ssmClient, &ssm.DescribeInstanceInformationInput{})
	var ids []string
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(context.TODO())
		if err != nil {
			if strings.Contains(err.Error(), "InvalidGrantException") || strings.Contains(err.Error(), "token expired") {
				if loginErr := ensureSSOLogin(profile); loginErr != nil {
					return nil, fmt.Errorf("SSO login failed: %v", loginErr)
				}
				cfg, _ = config.LoadDefaultConfig(context.TODO(), config.WithSharedConfigProfile(profile))
				ssmClient = ssm.NewFromConfig(cfg)
				paginator = ssm.NewDescribeInstanceInformationPaginator(ssmClient, &ssm.DescribeInstanceInformationInput{})
				continue
			}
			return nil, err
		}
		for _, info := range page.InstanceInformationList {
			ids = append(ids, *info.InstanceId)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	ec2Client := ec2.NewFromConfig(cfg)
	desc, err := ec2Client.DescribeInstances(context.TODO(), &ec2.DescribeInstancesInput{InstanceIds: ids})
	if err != nil {
		return nil, err
	}
	var result []Instance
	for _, res := range desc.Reservations {
		for _, inst := range res.Instances {
			name := ""
			for _, tag := range inst.Tags {
				if *tag.Key == "Name" {
					name = *tag.Value
					break
				}
			}
			vpc := ""
			if inst.VpcId != nil {
				vpc = *inst.VpcId
			}
			result = append(result, Instance{ID: *inst.InstanceId, Name: name, VpcID: vpc})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result, nil
}

func formatDBLabel(db DB) string {
	var prefix string
	switch db.Role {
	case "writer":
		prefix = "✍️ [Writer] "
	case "reader":
		prefix = "📖 [Reader] "
	default:
		prefix = ""
	}
	return fmt.Sprintf("🛢️ %s%s:%s", prefix, db.Endpoint, db.Port)
}

func detectPort(engine string) string {
	eng := strings.ToLower(engine)
	switch {
	case strings.Contains(eng, "mysql"), strings.Contains(eng, "mariadb"), strings.Contains(eng, "aurora-mysql"):
		return "3306"
	case strings.Contains(eng, "postgres"), strings.Contains(eng, "aurora-postgresql"):
		return "5432"
	case strings.Contains(eng, "sqlserver"):
		return "1433"
	default:
		return "3306"
	}
}

func fetchDBs(profile string) ([]DB, error) {
	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithSharedConfigProfile(profile))
	if err != nil {
		return nil, err
	}

	rdsClient := rds.NewFromConfig(cfg)
	var dbs []DB

	// Fetch clusters
	clustersOut, err := rdsClient.DescribeDBClusters(context.TODO(), &rds.DescribeDBClustersInput{})
	if err != nil {
		return nil, err
	}

	// Fetch subnet groups
	subnetGroupsOut, err := rdsClient.DescribeDBSubnetGroups(context.TODO(), &rds.DescribeDBSubnetGroupsInput{})
	if err != nil {
		return nil, err
	}
	subnetToVpc := make(map[string]string)
	for _, sg := range subnetGroupsOut.DBSubnetGroups {
		subnetToVpc[*sg.DBSubnetGroupName] = *sg.VpcId
	}

	for _, cluster := range clustersOut.DBClusters {
		eng := strings.ToLower(*cluster.Engine)
		if strings.Contains(eng, "aurora") {
			vpcId := ""
			if cluster.DBSubnetGroup != nil {
				if v, ok := subnetToVpc[*cluster.DBSubnetGroup]; ok {
					vpcId = v
				}
			}
			port := detectPort(*cluster.Engine)
			if cluster.Endpoint != nil {
				dbs = append(dbs, DB{Endpoint: *cluster.Endpoint, Port: port, VpcID: vpcId, Role: "writer"})
			}
			if cluster.ReaderEndpoint != nil {
				dbs = append(dbs, DB{Endpoint: *cluster.ReaderEndpoint, Port: port, VpcID: vpcId, Role: "reader"})
			}
		}
	}

	// Fetch standalone instances
	instancesOut, err := rdsClient.DescribeDBInstances(context.TODO(), &rds.DescribeDBInstancesInput{})
	if err != nil {
		return nil, err
	}

	for _, inst := range instancesOut.DBInstances {
		// Skip reader replicas and Aurora cluster members
		if inst.ReadReplicaSourceDBInstanceIdentifier != nil || inst.DBClusterIdentifier != nil {
			continue
		}
		ep := *inst.Endpoint.Address
		port := fmt.Sprint(*inst.Endpoint.Port)
		vpc := ""
		if inst.DBSubnetGroup != nil && inst.DBSubnetGroup.VpcId != nil {
			vpc = *inst.DBSubnetGroup.VpcId
		}
		dbs = append(dbs, DB{Endpoint: ep, Port: port, VpcID: vpc, Role: "instance"})
	}

	sort.Slice(dbs, func(i, j int) bool {
		return dbs[i].Endpoint < dbs[j].Endpoint
	})

	return dbs, nil
}

func startPortForward(profile, instanceName, instanceID, host, port string) error {
	fmt.Printf("\n✅ Starting port-forward from:\nlocalhost:%s → 🖥  %s (%s) → 🛢️ %s:%s\n\n", port, instanceName, instanceID, host, port)
	cmd := exec.Command(
		"aws", "ssm", "start-session",
		"--profile", profile,
		"--target", instanceID,
		"--document-name", "AWS-StartPortForwardingSessionToRemoteHost",
		"--parameters", fmt.Sprintf("host=[\"%s\"],portNumber=[\"%s\"],localPortNumber=[\"%s\"]", host, port, port),
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	awsPid = cmd.Process.Pid
	return cmd.Wait()
}

func readLastSelection() (*LastSelection, error) {
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

func writeLastSelection(sel *LastSelection) error {
	dir := filepath.Dir(lastSelectionPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(sel, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(lastSelectionPath, data, 0600)
}

func main() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		if awsPid != 0 {
			fmt.Println("\n🔴 Closing port-forward session...")
			_ = syscall.Kill(-awsPid, syscall.SIGKILL)
		}
		os.Exit(0)
	}()

	if sel, err := readLastSelection(); err == nil {
		fmt.Printf("Previous selection detected:\n☁️ Profile: %s\n🖥  Instance: %s (%s)\n🛢️ Database: %s:%s\n", sel.Profile, sel.InstanceName, sel.InstanceID, sel.DBEndpoint, sel.DBPort)
		prompt := promptui.Prompt{
			Label:     "Do you want to reuse it? (y/N)",
			IsConfirm: true,
		}
		result, _ := prompt.Run()
		if strings.ToLower(result) == "y" {
			if err := startPortForward(sel.Profile, sel.InstanceName, sel.InstanceID, sel.DBEndpoint, sel.DBPort); err != nil {
				log.Fatalf("port forwarding failed: %v", err)
			}
			return
		}
	}

	profiles, err := fetchProfiles()
	if err != nil {
		log.Fatalf("load profiles failed: %v", err)
	}
	profilePrompt := promptui.Select{
		Label: "Select AWS Profile",
		Items: profiles,
		Searcher: func(input string, index int) bool {
			profile := profiles[index]
			input = strings.ToLower(input)
			return strings.Contains(strings.ToLower(profile), input)
		},
	}
	idx, _, err := profilePrompt.Run()
	if err != nil {
		log.Fatalf("prompt failed: %v", err)
	}
	profile := profiles[idx]

	instances, err := fetchInstances(profile)
	if err != nil {
		log.Fatalf("fetch instances failed: %v", err)
	}
	var instOptions []string
	for _, inst := range instances {
		instOptions = append(instOptions, fmt.Sprintf("🖥  %s (%s)", inst.Name, inst.ID))
	}
	instancePrompt := promptui.Select{
		Label: fmt.Sprintf("Select Instance for profile '%s'", profile),
		Items: instOptions,
		Searcher: func(input string, index int) bool {
			inst := instOptions[index]
			input = strings.ToLower(input)
			return strings.Contains(strings.ToLower(inst), input)
		},
	}
	idx, _, err = instancePrompt.Run()
	if err != nil {
		log.Fatalf("prompt failed: %v", err)
	}
	instance := instances[idx]

	dbs, err := fetchDBs(profile)
	if err != nil {
		log.Fatalf("fetch dbs failed: %v", err)
	}
	var dbOptions []DB
	var dbLabels []string
	for _, db := range dbs {
		if db.VpcID == instance.VpcID {
			dbOptions = append(dbOptions, db)
			dbLabels = append(dbLabels, formatDBLabel(db))
		}
	}
	if len(dbOptions) == 0 {
		fmt.Println("No databases found in the same VPC.")
		return
	}
	dbPrompt := promptui.Select{
		Label: "Select Database",
		Items: dbLabels,
		Searcher: func(input string, index int) bool {
			db := dbLabels[index]
			input = strings.ToLower(input)
			return strings.Contains(strings.ToLower(db), input)
		},
	}
	idx, _, err = dbPrompt.Run()
	if err != nil {
		log.Fatalf("prompt failed: %v", err)
	}
	db := dbOptions[idx]

	_ = writeLastSelection(&LastSelection{
		Profile:      profile,
		InstanceName: instance.Name,
		InstanceID:   instance.ID,
		DBEndpoint:   db.Endpoint,
		DBPort:       db.Port,
	})

	if err := startPortForward(profile, instance.Name, instance.ID, db.Endpoint, db.Port); err != nil {
		log.Fatalf("port forwarding failed: %v", err)
	}
}
