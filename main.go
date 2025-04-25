package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/getlantern/systray"
	"gopkg.in/ini.v1"
)

// Instance holds ID, optional Name tag, and VPC
type Instance struct {
	ID    string
	Name  string
	VpcID string
}

// DB holds an RDS endpoint, its port, and VPC
type DB struct {
	Endpoint string
	Port     string
	VpcID    string
}

// ProfileItem stores menu items per profile
type ProfileItem struct {
	Name      string
	Instances []*systray.MenuItem
	DBItems   []*systray.MenuItem
}

var (
	profiles     = make(map[string]*ProfileItem)
	profileMenus []*systray.MenuItem
	backItem     *systray.MenuItem
	awsPid       int
)

// loadAWSProfiles reads profile names from ~/.aws/config
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
	return names, nil
}

// ensureSSOLogin prompts SSO login if needed
func ensureSSOLogin(profile string) error {
	cmd := exec.Command("aws", "sso", "login", "--profile", profile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func isAuthErr(err error) bool {
	e := err.Error()
	return strings.Contains(e, "NotAuthorizedForSourceException") ||
		strings.Contains(e, "expired token") ||
		strings.Contains(e, "InvalidGrantException")
}

// fetchInstances lists SSM-managed instances + their VPCs
func fetchInstances(profile string) ([]Instance, error) {
	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithSharedConfigProfile(profile))
	if err != nil && isAuthErr(err) {
		if loginErr := ensureSSOLogin(profile); loginErr != nil {
			return nil, fmt.Errorf("SSO login failed: %w", loginErr)
		}
		cfg, err = config.LoadDefaultConfig(context.TODO(), config.WithSharedConfigProfile(profile))
	}
	if err != nil {
		return nil, err
	}

	ssmClient := ssm.NewFromConfig(cfg)
	paginator := ssm.NewDescribeInstanceInformationPaginator(ssmClient, &ssm.DescribeInstanceInformationInput{})
	var ids []string
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(context.TODO())
		if err != nil {
			if isAuthErr(err) {
				if loginErr := ensureSSOLogin(profile); loginErr != nil {
					return nil, fmt.Errorf("login during paging failed: %w", loginErr)
				}
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
	return result, nil
}

// fetchDBs lists RDS instances + their VPCs
func fetchDBs(profile string) ([]DB, error) {
	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithSharedConfigProfile(profile))
	if err != nil && isAuthErr(err) {
		if loginErr := ensureSSOLogin(profile); loginErr != nil {
			return nil, fmt.Errorf("SSO login for RDS failed: %w", loginErr)
		}
		cfg, err = config.LoadDefaultConfig(context.TODO(), config.WithSharedConfigProfile(profile))
	}
	if err != nil {
		return nil, err
	}

	rdsClient := rds.NewFromConfig(cfg)
	out, err := rdsClient.DescribeDBInstances(context.TODO(), &rds.DescribeDBInstancesInput{})
	if err != nil && isAuthErr(err) {
		if loginErr := ensureSSOLogin(profile); loginErr != nil {
			return nil, fmt.Errorf("login during RDS fetch failed: %w", loginErr)
		}
		out, err = rdsClient.DescribeDBInstances(context.TODO(), &rds.DescribeDBInstancesInput{})
	}
	if err != nil {
		return nil, err
	}

	var dbs []DB
	for _, inst := range out.DBInstances {
		ep := *inst.Endpoint.Address
		port := fmt.Sprint(*inst.Endpoint.Port)
		eng := strings.ToLower(*inst.Engine)
		if strings.Contains(eng, "mysql") {
			port = "3306"
		} else if strings.Contains(eng, "postgres") {
			port = "5432"
		}
		vpc := ""
		if inst.DBSubnetGroup != nil && inst.DBSubnetGroup.VpcId != nil {
			vpc = *inst.DBSubnetGroup.VpcId
		}
		dbs = append(dbs, DB{Endpoint: ep, Port: port, VpcID: vpc})
	}
	return dbs, nil
}

// startSession starts port forwarding via AWS CLI
func startSession(profile, target, host, port string) {
	if awsPid != 0 {
		exec.Command("kill", "-9", fmt.Sprint(awsPid)).Run()
	}
	cmd := exec.Command(
		"aws", "ssm", "start-session",
		"--profile", profile,
		"--target", target,
		"--document-name", "AWS-StartPortForwardingSessionToRemoteHost",
		"--parameters",
		fmt.Sprintf("host=[\"%s\"],portNumber=[\"%s\"],localPortNumber=[\"%s\"]", host, port, port),
	)
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		systray.SetTooltip("session error: " + err.Error())
		return
	}
	awsPid = cmd.Process.Pid

	go func() {
		scanner := bufio.NewScanner(out)
		for scanner.Scan() {
			log.Println(scanner.Text())
		}
		cmd.Wait()
	}()

	systray.SetTooltip(fmt.Sprintf("%s:%s → localhost:%s", host, port, port))
}

// showProfiles hides all submenus and shows only profile list
func showProfiles() {
	backItem.Hide()
	for _, m := range profileMenus {
		m.Show()
	}
	for _, pi := range profiles {
		for _, inst := range pi.Instances {
			inst.Hide()
		}
		pi.Instances = nil
		for _, db := range pi.DBItems {
			db.Hide()
		}
		pi.DBItems = nil
	}
}

// showInstances displays EC2 instances for a profile
func showInstances(pi *ProfileItem) {
	for _, m := range profileMenus {
		m.Hide()
	}
	backItem.Show()

	insts, err := fetchInstances(pi.Name)
	if err != nil {
		log.Printf("fetch instances %s: %v", pi.Name, err)
		return
	}
	// clear old
	for _, inst := range pi.Instances {
		inst.Hide()
	}
	pi.Instances = nil
	for _, db := range pi.DBItems {
		db.Hide()
	}
	pi.DBItems = nil

	// list EC2
	for _, inst := range insts {
		label := inst.ID
		if inst.Name != "" {
			label = fmt.Sprintf("%s (%s)", inst.Name, inst.ID)
		}
		m := systray.AddMenuItem("🖥 "+label, "select instance")
		pi.Instances = append(pi.Instances, m)
		go func(inst Instance, menu *systray.MenuItem) {
			for range menu.ClickedCh {
				showDBs(pi, inst)
			}
		}(inst, m)
	}
}

// showDBs displays only RDS items in same VPC as inst
func showDBs(pi *ProfileItem, inst Instance) {
	// hide EC2 list
	for _, instMenu := range pi.Instances {
		instMenu.Hide()
	}
	// clear old DBs
	for _, dbItem := range pi.DBItems {
		dbItem.Hide()
	}
	pi.DBItems = nil
	backItem.Show()

	dbs, err := fetchDBs(pi.Name)
	if err != nil {
		log.Printf("fetch dbs %s: %v", pi.Name, err)
		return
	}
	for _, db := range dbs {
		if db.VpcID != inst.VpcID {
			continue
		}
		label := fmt.Sprintf("🔒 %s:%s", db.Endpoint, db.Port)
		m := systray.AddMenuItemCheckbox(label, "forward db", false)
		pi.DBItems = append(pi.DBItems, m)
		go func(inst Instance, db DB, menu *systray.MenuItem) {
			for range menu.ClickedCh {
				startSession(pi.Name, inst.ID, db.Endpoint, db.Port)
				menu.Check()
				// uncheck siblings
				for _, sib := range pi.DBItems {
					if sib != menu {
						sib.Uncheck()
					}
				}
			}
		}(inst, db, m)
	}
}

func onReady() {
	systray.SetTitle("SSM Connect")

	// back button
	backItem = systray.AddMenuItem("← back to profiles", "go back to profile list")
	backItem.Hide()
	// quit
	quit := systray.AddMenuItem("Quit", "exit")
	go func() { <-quit.ClickedCh; systray.Quit() }()

	// load profiles
	names, err := loadAWSProfiles()
	if err != nil {
		log.Fatalf("cannot load profiles: %v", err)
	}
	for _, name := range names {
		pi := &ProfileItem{Name: name}
		profiles[name] = pi

		menu := systray.AddMenuItem(name, "select profile")
		profileMenus = append(profileMenus, menu)
		go func(pi *ProfileItem, m *systray.MenuItem) {
			for range m.ClickedCh {
				showInstances(pi)
			}
		}(pi, menu)
	}

	showProfiles()
}

func main() {
	systray.Run(onReady, nil)
}
