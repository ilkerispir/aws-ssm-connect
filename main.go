package main

import (
	"bufio"
	"context"
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
}

var awsPid int

func clearScreen() {
	fmt.Print("\033[H\033[2J")
}

func loadAWSProfiles() ([]string, error) {
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

func fetchDBs(profile string) ([]DB, error) {
	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithSharedConfigProfile(profile))
	if err != nil {
		return nil, err
	}
	rdsClient := rds.NewFromConfig(cfg)
	out, err := rdsClient.DescribeDBInstances(context.TODO(), &rds.DescribeDBInstancesInput{})
	if err != nil {
		return nil, err
	}

	var dbs []DB
	for _, inst := range out.DBInstances {
		ep := *inst.Endpoint.Address
		port := fmt.Sprint(*inst.Endpoint.Port)
		eng := strings.ToLower(*inst.Engine)

		switch {
		case strings.Contains(eng, "mysql"), strings.Contains(eng, "mariadb"), strings.Contains(eng, "aurora-mysql"):
			port = "3306"
		case strings.Contains(eng, "postgres"), strings.Contains(eng, "aurora-postgresql"):
			port = "5432"
		case strings.Contains(eng, "sqlserver"):
			port = "1433"
		}

		vpc := ""
		if inst.DBSubnetGroup != nil && inst.DBSubnetGroup.VpcId != nil {
			vpc = *inst.DBSubnetGroup.VpcId
		}
		dbs = append(dbs, DB{Endpoint: ep, Port: port, VpcID: vpc})
	}
	sort.Slice(dbs, func(i, j int) bool {
		return dbs[i].Endpoint < dbs[j].Endpoint
	})
	return dbs, nil
}

func startPortForward(profile, instanceID, host, port string) error {
	fmt.Printf("\n✅ Starting port-forward from:\n%s → %s:%s → localhost:%s\n\n", instanceID, host, port, port)
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

func main() {
	reader := bufio.NewReader(os.Stdin)

	// listen CTRL+C to cleanup
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

	clearScreen()
	fmt.Println("──────────────────────────────")
	fmt.Println("Available AWS Profiles:")
	fmt.Println("──────────────────────────────")
	profiles, err := loadAWSProfiles()
	if err != nil {
		log.Fatalf("load profiles failed: %v", err)
	}
	for i, p := range profiles {
		fmt.Printf("[%2d] %s\n", i+1, p)
	}
	fmt.Print("\nSelect a profile number: ")
	sel, _ := reader.ReadString('\n')
	sel = strings.TrimSpace(sel)
	idx := 0
	fmt.Sscanf(sel, "%d", &idx)
	if idx <= 0 || idx > len(profiles) {
		log.Fatal("invalid selection")
	}
	profile := profiles[idx-1]

	clearScreen()
	instances, err := fetchInstances(profile)
	if err != nil {
		log.Fatalf("fetch instances failed: %v", err)
	}
	fmt.Println("──────────────────────────────")
	fmt.Printf("Instances in profile '%s':\n", profile)
	fmt.Println("──────────────────────────────")
	for i, inst := range instances {
		fmt.Printf("[%2d] 🖥  %s (%s)\n", i+1, inst.Name, inst.ID)
	}
	fmt.Print("\nSelect an instance number: ")
	sel, _ = reader.ReadString('\n')
	sel = strings.TrimSpace(sel)
	idx = 0
	fmt.Sscanf(sel, "%d", &idx)
	if idx <= 0 || idx > len(instances) {
		log.Fatal("invalid selection")
	}
	instance := instances[idx-1]

	clearScreen()
	dbs, err := fetchDBs(profile)
	if err != nil {
		log.Fatalf("fetch dbs failed: %v", err)
	}
	fmt.Println("──────────────────────────────")
	fmt.Println("Databases in same VPC:")
	fmt.Println("──────────────────────────────")
	filteredDBs := []DB{}
	for _, db := range dbs {
		if db.VpcID == instance.VpcID {
			filteredDBs = append(filteredDBs, db)
		}
	}
	if len(filteredDBs) == 0 {
		fmt.Println("No databases found in the same VPC.")
		return
	}
	for i, db := range filteredDBs {
		fmt.Printf("[%2d] 🛢️  %s:%s\n", i+1, db.Endpoint, db.Port)
	}
	fmt.Print("\nSelect a database number: ")
	sel, _ = reader.ReadString('\n')
	sel = strings.TrimSpace(sel)
	idx = 0
	fmt.Sscanf(sel, "%d", &idx)
	if idx <= 0 || idx > len(filteredDBs) {
		log.Fatal("invalid selection")
	}
	db := filteredDBs[idx-1]

	clearScreen()
	if err := startPortForward(profile, instance.ID, db.Endpoint, db.Port); err != nil {
		log.Fatalf("port forwarding failed: %v", err)
	}
}
