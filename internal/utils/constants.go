package utils

var (
	version = "dev"

	engineToPort = map[string]string{
		"mysql":             "3306",
		"mariadb":           "3306",
		"aurora-mysql":      "3306",
		"postgres":          "5432",
		"aurora-postgresql": "5432",
		"sqlserver":         "1433",
		"redis":             "6379",
		"valkey":            "6379",
		"memcached":         "11211",
		"oracle":            "1521",
		"mongodb":           "27017",
	}

	portToEngine = map[string]string{
		"3306":  "MySQL",
		"5432":  "PostgreSQL",
		"1433":  "SQL Server",
		"6379":  "Redis",
		"11211": "Memcached",
		"1521":  "Oracle",
		"27017": "MongoDB",
	}
)
