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

// Instance holds ID and optional Name tag
type Instance struct {
	ID   string
	Name string
}

// DB holds an RDS endpoint and its port
type DB struct {
	Endpoint string
	Port     string
}

// ProfileItem stores menu items for a profile
type ProfileItem struct {
	Name      string
	Instances []*systray.MenuItem
}

var (
	profiles = make(map[string]*ProfileItem)
	awsPid   int
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

// isAuthErr detects expired/unauthorized errors
func isAuthErr(err error) bool {
	e := err.Error()
	return strings.Contains(e, "NotAuthorizedForSourceException") ||
		strings.Contains(e, "expired token") ||
		strings.Contains(e, "Unauthorized")
}

// fetchInstances lists SSM-managed instances and retrieves their Name tags via EC2
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
	desc, err := ec2Client.DescribeInstances(context.TODO(), &ec2.DescribeInstancesInput{
		InstanceIds: ids,
	})
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
			result = append(result, Instance{ID: *inst.InstanceId, Name: name})
		}
	}
	return result, nil
}

// fetchDBs lists all RDS instances in the profile
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
		if strings.Contains(eng, "mysql") {
			port = "3306"
		} else if strings.Contains(eng, "postgres") {
			port = "5432"
		}
		dbs = append(dbs, DB{Endpoint: ep, Port: port})
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

func onReady() {
	systray.SetTitle("SSM Connect")

	backItem := systray.AddMenuItem("← back to profiles", "go back")
	backItem.Hide()

	quit := systray.AddMenuItem("Quit", "exit")
	go func() { <-quit.ClickedCh; systray.Quit() }()

	var profileMenus []*systray.MenuItem

	showProfiles := func() {
		backItem.Hide()
		for _, m := range profileMenus {
			m.Show()
		}
		for _, p := range profiles {
			for _, imi := range p.Instances {
				imi.Hide()
			}
		}
	}

	showInstances := func(pi *ProfileItem) {
		for _, m := range profileMenus {
			m.Hide()
		}
		backItem.Show()

		insts, err := fetchInstances(pi.Name)
		if err != nil && isAuthErr(err) {
			if loginErr := ensureSSOLogin(pi.Name); loginErr != nil {
				log.Printf("login failed %s: %v", pi.Name, loginErr)
				return
			}
			insts, err = fetchInstances(pi.Name)
		}
		if err != nil {
			log.Printf("fetch instances %s: %v", pi.Name, err)
			return
		}

		for _, old := range pi.Instances {
			old.Hide()
		}
		pi.Instances = nil

		dbs, _ := fetchDBs(pi.Name)

		for _, inst := range insts {
			label := inst.ID
			if inst.Name != "" {
				label = fmt.Sprintf("%s (%s)", inst.Name, inst.ID)
			}
			imi := systray.AddMenuItemCheckbox("  🖥 "+label, "forward port", false)
			pi.Instances = append(pi.Instances, imi)

			go func(instanceID string, menuItem *systray.MenuItem) {
				for range menuItem.ClickedCh {
					startSession(pi.Name, instanceID, "127.0.0.1", "80")
					menuItem.Check()
					for _, sib := range pi.Instances {
						if sib != menuItem {
							sib.Uncheck()
						}
					}
				}
			}(inst.ID, imi)

			if len(dbs) > 0 {
				systray.AddSeparator()
				for _, db := range dbs {
					dbLabel := fmt.Sprintf("    🔒 %s:%s", db.Endpoint, db.Port)
					dbItem := systray.AddMenuItemCheckbox(dbLabel, "forward DB", false)
					go func(host, port, target string, menuItem *systray.MenuItem) {
						for range menuItem.ClickedCh {
							startSession(pi.Name, target, host, port)
							menuItem.Check()
							for _, sib := range pi.Instances {
								if sib != menuItem {
									sib.Uncheck()
								}
							}
						}
					}(db.Endpoint, db.Port, inst.ID, dbItem)
				}
			}
		}
	}

	names, err := loadAWSProfiles()
	if err != nil {
		log.Fatalf("cannot load profiles: %v", err)
	}
	for _, name := range names {
		pi := &ProfileItem{Name: name}
		profiles[name] = pi

		m := systray.AddMenuItem(name, "select profile")
		profileMenus = append(profileMenus, m)

		go func(item *ProfileItem, menuItem *systray.MenuItem) {
			for range menuItem.ClickedCh {
				showInstances(item)
			}
		}(pi, m)
	}

	go func() {
		for range backItem.ClickedCh {
			showProfiles()
		}
	}()

	showProfiles()
}

func onExit() {}

func main() {
	systray.Run(onReady, onExit)
}
