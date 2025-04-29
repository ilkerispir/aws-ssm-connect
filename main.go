package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
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

type PIDInfo struct {
	PID      int    `json:"pid"`
	Profile  string `json:"profile"`
	Instance string `json:"instance"`
	DB       string `json:"db"`
}

var (
	awsPid            int
	lastSelectionPath = filepath.Join(os.Getenv("HOME"), ".aws-ssm-rds-proxy", "last-selections.json")
	pidsFilePath      = filepath.Join(os.Getenv("HOME"), ".aws-ssm-rds-proxy", "pids.json")
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
			if strings.Contains(err.Error(), "InvalidGrantException") ||
				strings.Contains(err.Error(), "token expired") ||
				strings.Contains(err.Error(), "failed to read cached SSO token file") ||
				strings.Contains(err.Error(), "failed to refresh cached credentials") {

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
	if isPortInUse(port) {
		fmt.Printf("❌ Local port %s is already in use. Please choose another port or close the existing connection.\n", port)
		return fmt.Errorf("local port %s already in use", port)
	}

	fmt.Printf("\n✅ Starting port-forward from:\n💻 localhost:%s → 🖥  %s (%s) → 🛢️ %s:%s\n\n", port, instanceName, instanceID, host, port)
	cmd := exec.Command(
		"aws", "ssm", "start-session",
		"--profile", profile,
		"--target", instanceID,
		"--document-name", "AWS-StartPortForwardingSessionToRemoteHost",
		"--parameters", fmt.Sprintf("host=[\"%s\"],portNumber=[\"%s\"],localPortNumber=[\"%s\"]", host, port, port),
	)

	nullFile, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	cmd.Stdout = nullFile
	cmd.Stderr = nullFile
	cmd.Stdin = nullFile

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return err
	}
	awsPid = cmd.Process.Pid

	// Save PID info
	_ = savePID(PIDInfo{
		PID:      awsPid,
		Profile:  profile,
		Instance: instanceName,
		DB:       fmt.Sprintf("%s:%s", host, port),
	})

	fmt.Printf("🔵 Port-forward session started in background (PID %d).\n", awsPid)
	return nil
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

func quickConnect(profile, filter string) error {
	instances, err := fetchInstances(profile)
	if err != nil {
		return fmt.Errorf("fetch instances failed: %w", err)
	}
	var selectedInstance *Instance
	for _, inst := range instances {
		if strings.Contains(strings.ToLower(inst.Name), strings.ToLower(filter)) {
			selectedInstance = &inst
			break
		}
	}
	if selectedInstance == nil {
		return fmt.Errorf("no instance matching environment '%s' found", filter)
	}

	dbs, err := fetchDBs(profile)
	if err != nil {
		return fmt.Errorf("fetch dbs failed: %w", err)
	}
	var selectedDB *DB
	for _, db := range dbs {
		if db.VpcID == selectedInstance.VpcID && db.Role == "writer" {
			selectedDB = &db
			break
		}
	}
	if selectedDB == nil {
		return fmt.Errorf("no writer database found for selected instance")
	}

	fmt.Printf("✔ %s (%s)\n", selectedInstance.Name, selectedInstance.ID)
	fmt.Printf("✔ %s:%s\n", selectedDB.Endpoint, selectedDB.Port)

	_ = writeLastSelection(&LastSelection{
		Profile:      profile,
		InstanceName: selectedInstance.Name,
		InstanceID:   selectedInstance.ID,
		DBEndpoint:   selectedDB.Endpoint,
		DBPort:       selectedDB.Port,
	})

	return startPortForward(profile, selectedInstance.Name, selectedInstance.ID, selectedDB.Endpoint, selectedDB.Port)
}

func savePID(info PIDInfo) error {
	var existing []PIDInfo
	data, err := os.ReadFile(pidsFilePath)
	if err == nil {
		_ = json.Unmarshal(data, &existing)
	}

	existing = append(existing, info)

	out, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(pidsFilePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(pidsFilePath, out, 0600)
}

func listPIDs() error {
	data, err := os.ReadFile(pidsFilePath)
	if err != nil {
		return fmt.Errorf("could not read pids file: %w", err)
	}
	var pids []PIDInfo
	if err := json.Unmarshal(data, &pids); err != nil {
		return fmt.Errorf("could not parse pids file: %w", err)
	}

	var alive []PIDInfo
	for _, p := range pids {
		if processExists(p.PID) {
			alive = append(alive, p)
		}
	}

	// Update pids.json with only alive ones
	out, _ := json.MarshalIndent(alive, "", "  ")
	_ = os.WriteFile(pidsFilePath, out, 0600)

	if len(alive) == 0 {
		fmt.Println("No active port-forward sessions.")
		return nil
	}

	fmt.Println("Active Port-Forward Sessions:")
	for _, p := range alive {
		fmt.Printf("🔵 PID: %d | Profile: %s | Instance: %s | DB: %s\n", p.PID, p.Profile, p.Instance, p.DB)
	}
	return nil
}

func killPID(pid int) error {
	fmt.Printf("🛑 Attempting to kill PID %d...\n", pid)

	// Kill the process
	err := syscall.Kill(-pid, syscall.SIGKILL)
	if err != nil {
		if err.Error() == "no such process" || strings.Contains(err.Error(), "no such process") {
			fmt.Printf("⚠️  PID %d is already dead. Cleaning up...\n", pid)
		} else {
			return fmt.Errorf("failed to kill pid %d: %w", pid, err)
		}
	}

	// Remove from pids.json
	data, err := os.ReadFile(pidsFilePath)
	if err != nil {
		return fmt.Errorf("could not read pids file: %w", err)
	}
	var pids []PIDInfo
	if err := json.Unmarshal(data, &pids); err != nil {
		return fmt.Errorf("could not parse pids file: %w", err)
	}

	var updated []PIDInfo
	for _, p := range pids {
		if p.PID != pid {
			updated = append(updated, p)
		}
	}

	out, err := json.MarshalIndent(updated, "", "  ")
	if err != nil {
		return fmt.Errorf("could not re-encode pids: %w", err)
	}

	if err := os.WriteFile(pidsFilePath, out, 0600); err != nil {
		return fmt.Errorf("could not write updated pids: %w", err)
	}

	fmt.Println("✅ PID", pid, "successfully cleaned up from session list.")
	return nil
}

func killAllPIDs() error {
	fmt.Println("🛑 Attempting to kill all active port-forward sessions...")

	data, err := os.ReadFile(pidsFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("No active sessions to kill.")
			return nil
		}
		return fmt.Errorf("could not read pids file: %w", err)
	}

	var pids []PIDInfo
	if err := json.Unmarshal(data, &pids); err != nil {
		return fmt.Errorf("could not parse pids file: %w", err)
	}

	killedCount := 0
	for _, p := range pids {
		if err := syscall.Kill(-p.PID, syscall.SIGKILL); err != nil {
			if strings.Contains(err.Error(), "no such process") {
				fmt.Printf("⚠️  PID %d already dead, skipping...\n", p.PID)
			} else {
				fmt.Printf("❌ Failed to kill PID %d: %v\n", p.PID, err)
			}
		} else {
			fmt.Printf("✅ Killed PID %d\n", p.PID)
			killedCount++
		}
	}

	// After killing, clean up pids.json
	_ = os.Remove(pidsFilePath)

	if killedCount == 0 {
		fmt.Println("ℹ️  No alive sessions were found.")
	} else {
		fmt.Printf("🔵 Successfully killed %d sessions.\n", killedCount)
	}

	return nil
}

func processExists(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// try sending signal 0 (does not actually kill)
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

func isPortInUse(port string) bool {
	ln, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		return true // bind edemediysek port meşgul
	}
	_ = ln.Close()
	return false
}

func showHelper() {
	fmt.Println(`
AWS SSM RDS Proxy - Quick Connect Tool

Usage:
  aws-ssm-rds-proxy                                          # Interactive mode (prompts)
  aws-ssm-rds-proxy --profile <profile> --filter <keyword>   # Quick connect mode
  aws-ssm-rds-proxy --list                                   # List active port-forward sessions
  aws-ssm-rds-proxy --kill <pid>                             # Kill a specific port-forward session by PID
  aws-ssm-rds-proxy --kill-all                               # Kill all active port-forward sessions
  aws-ssm-rds-proxy --help                                   # Show this helper message

Flags:
--profile    AWS profile name to use (e.g., my-aws-profile)
--filter     Keyword to match instance name (e.g., prod, dev, uat)
--list       List active port-forward sessions
--kill       Kill a specific session by PID
--kill-all   Kill all active port-forward sessions
--help       Show this helper message

Examples:
aws-ssm-rds-proxy --profile my-aws-profile --filter dev
aws-ssm-rds-proxy --list
aws-ssm-rds-proxy --kill 12345
aws-ssm-rds-proxy --kill-all

Behavior:
- Searches for an instance matching the filter keyword
- Finds a writer database (or standalone RDS instance) in the same VPC
- Starts a background port-forwarding session automatically
- Manages sessions with PID tracking
- Automatically cleans up dead sessions
- Prevents port conflicts by checking local port availability
`)
}

func main() {
	profileFlag := flag.String("profile", "", "AWS profile name")
	filterFlag := flag.String("filter", "", "Instance name filter")
	listFlag := flag.Bool("list", false, "List active port-forward sessions")
	killFlag := flag.Int("kill", 0, "Kill a port-forward session by PID")
	killAllFlag := flag.Bool("kill-all", false, "Kill all active port-forward sessions")
	helpFlag := flag.Bool("help", false, "Show usage information")
	flag.Parse()

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

	if *helpFlag {
		showHelper()
		return
	}

	if *listFlag {
		if err := listPIDs(); err != nil {
			log.Fatalf("list pids failed: %v", err)
		}
		return
	}

	if *killFlag != 0 {
		if err := killPID(*killFlag); err != nil {
			log.Fatalf("kill pid failed: %v", err)
		}
		return
	}

	if *killAllFlag {
		if err := killAllPIDs(); err != nil {
			log.Fatalf("kill all pids failed: %v", err)
		}
		return
	}

	if *profileFlag != "" && *filterFlag != "" {
		err := quickConnect(*profileFlag, *filterFlag)
		if err != nil {
			log.Fatalf("quick connect failed: %v", err)
		}
		return
	}

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
