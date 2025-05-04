package aws

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"golang.org/x/sync/errgroup"
)

// DB represents a discovered database instance
type DB struct {
	Endpoint string
	Port     string
	VpcID    string
	Role     string
}

// FetchDBs collects RDS and ElastiCache endpoints for the given AWS profile
func FetchDBs(profile string) ([]DB, error) {
	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithSharedConfigProfile(profile))
	if err != nil {
		return nil, err
	}

	rdsClient := rds.NewFromConfig(cfg)
	cacheClient := elasticache.NewFromConfig(cfg)

	var (
		mu  sync.Mutex
		dbs []DB
	)
	eg, ctx := errgroup.WithContext(context.Background())

	// RDS Fetch
	eg.Go(func() error {
		var result []DB

		clusters, _ := rdsClient.DescribeDBClusters(ctx, &rds.DescribeDBClustersInput{})
		subnets, _ := rdsClient.DescribeDBSubnetGroups(ctx, &rds.DescribeDBSubnetGroupsInput{})

		subnetToVpc := map[string]string{}
		for _, sg := range subnets.DBSubnetGroups {
			subnetToVpc[*sg.DBSubnetGroupName] = *sg.VpcId
		}

		for _, cluster := range clusters.DBClusters {
			engine := strings.ToLower(*cluster.Engine)
			if !strings.Contains(engine, "aurora") {
				continue
			}
			vpc := subnetToVpc[*cluster.DBSubnetGroup]
			port := DetectPort(engine)

			if cluster.Endpoint != nil {
				result = append(result, DB{Endpoint: *cluster.Endpoint, Port: port, VpcID: vpc, Role: "writer"})
			}
			if cluster.ReaderEndpoint != nil {
				result = append(result, DB{Endpoint: *cluster.ReaderEndpoint, Port: port, VpcID: vpc, Role: "reader"})
			}
		}

		instances, _ := rdsClient.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{})
		for _, inst := range instances.DBInstances {
			if inst.ReadReplicaSourceDBInstanceIdentifier != nil || inst.DBClusterIdentifier != nil {
				continue
			}
			endpoint := *inst.Endpoint.Address
			port := fmt.Sprint(*inst.Endpoint.Port)
			vpc := ""
			if inst.DBSubnetGroup != nil && inst.DBSubnetGroup.VpcId != nil {
				vpc = *inst.DBSubnetGroup.VpcId
			}
			result = append(result, DB{Endpoint: endpoint, Port: port, VpcID: vpc, Role: "instance"})
		}

		mu.Lock()
		dbs = append(dbs, result...)
		mu.Unlock()
		return nil
	})

	// ElastiCache Fetch
	eg.Go(func() error {
		var result []DB
		vpcMap := map[string]string{}

		subnetGroups, _ := cacheClient.DescribeCacheSubnetGroups(ctx, &elasticache.DescribeCacheSubnetGroupsInput{})
		for _, sg := range subnetGroups.CacheSubnetGroups {
			if sg.CacheSubnetGroupName != nil && sg.VpcId != nil {
				vpcMap[*sg.CacheSubnetGroupName] = *sg.VpcId
			}
		}

		clusters, _ := cacheClient.DescribeCacheClusters(ctx, &elasticache.DescribeCacheClustersInput{
			ShowCacheNodeInfo: aws.Bool(true),
		})
		clusterVpcMap := map[string]string{}
		for _, cc := range clusters.CacheClusters {
			if cc.CacheClusterId != nil && cc.CacheSubnetGroupName != nil {
				clusterVpcMap[*cc.CacheClusterId] = vpcMap[*cc.CacheSubnetGroupName]
			}
		}

		replGroups, err := cacheClient.DescribeReplicationGroups(ctx, &elasticache.DescribeReplicationGroupsInput{})
		if err != nil {
			return nil // skip ElastiCache silently
		}

		seen := map[string]bool{}
		for _, rg := range replGroups.ReplicationGroups {
			engine := strings.ToLower(*rg.Engine)
			if engine != "redis" && engine != "valkey" {
				continue
			}
			port := "6379"
			vpc := ""
			if len(rg.MemberClusters) > 0 {
				vpc = clusterVpcMap[rg.MemberClusters[0]]
			}

			if rg.ConfigurationEndpoint != nil && rg.ConfigurationEndpoint.Address != nil {
				addr := *rg.ConfigurationEndpoint.Address
				if !seen[addr] {
					seen[addr] = true
					result = append(result, DB{Endpoint: addr, Port: port, VpcID: vpc, Role: fmt.Sprintf("%s-primary", engine)})
				}
			}

			for _, ng := range rg.NodeGroups {
				for _, ep := range ng.NodeGroupMembers {
					if ep.ReadEndpoint == nil || ep.ReadEndpoint.Address == nil {
						continue
					}
					addr := *ep.ReadEndpoint.Address
					role := "replica"
					if ep.CurrentRole != nil && *ep.CurrentRole == "primary" {
						role = "primary"
					}
					if !seen[addr] {
						seen[addr] = true
						result = append(result, DB{
							Endpoint: addr,
							Port:     port,
							VpcID:    vpc,
							Role:     fmt.Sprintf("%s-%s", engine, role),
						})
					}
				}
			}
		}

		mu.Lock()
		dbs = append(dbs, result...)
		mu.Unlock()
		return nil
	})

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	sort.Slice(dbs, func(i, j int) bool {
		return dbs[i].Endpoint < dbs[j].Endpoint
	})

	return dbs, nil
}

func DetectPort(engine string) string {
	engine = strings.ToLower(engine)
	switch {
	case strings.Contains(engine, "mysql"):
		return "3306"
	case strings.Contains(engine, "postgres"):
		return "5432"
	case strings.Contains(engine, "sqlserver"):
		return "1433"
	case strings.Contains(engine, "oracle"):
		return "1521"
	case strings.Contains(engine, "mongo"):
		return "27017"
	default:
		return "3306"
	}
}

// DetectEngineByPort returns the engine name based on default port number
func DetectEngineByPort(port string) string {
	switch port {
	case "3306":
		return "MySQL"
	case "5432":
		return "PostgreSQL"
	case "1433":
		return "SQL Server"
	case "6379":
		return "Redis"
	case "11211":
		return "Memcached"
	case "1521":
		return "Oracle"
	case "27017":
		return "MongoDB"
	default:
		return "Unknown"
	}
}
