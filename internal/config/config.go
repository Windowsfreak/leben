package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type ServerConfig struct {
	Port      int    `yaml:"port"`
	Host      string `yaml:"host"`
	Socket    string `yaml:"socket"`
	WebDir    string `yaml:"web_dir"`
	PublicURL string `yaml:"public_url"`
}

type DatabaseConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	DBName   string `yaml:"dbname"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	SSLMode  string `yaml:"sslmode"`
}

type EmbeddingConfig struct {
	URL         string `yaml:"url"`
	Model       string `yaml:"model"`
	QueryPrefix string `yaml:"query_prefix"`
	DocPrefix   string `yaml:"doc_prefix"`
}

type LLMConfig struct {
	URL   string `yaml:"url"`
	Model string `yaml:"model"`
	User  string `yaml:"user"`
	Pass  string `yaml:"pass"`
}

type AdminConfig struct {
	PasswordHash    string `yaml:"password_hash"`
	SessionTTLHours int    `yaml:"session_ttl_hours"`
}

type DeepLConfig struct {
	APIKey string `yaml:"api_key"`
	URL    string `yaml:"url"`
}

type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Database  DatabaseConfig  `yaml:"database"`
	Embedding EmbeddingConfig `yaml:"embedding"`
	LLM       LLMConfig       `yaml:"llm"`
	DeepL     DeepLConfig     `yaml:"deepl"`
	Admin     AdminConfig     `yaml:"admin"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		Server: ServerConfig{
			Port:   8088,
			Host:   "0.0.0.0",
			WebDir: "./frontend",
		},
		Database: DatabaseConfig{
			Host:     "127.0.0.1",
			Port:     5433,
			DBName:   "leben",
			User:     "postgres",
			Password: "postgrespassword",
			SSLMode:  "disable",
		},
		Embedding: EmbeddingConfig{
			URL:         "http://localhost:11434",
			Model:       "nomic-embed-text-v2-moe:latest",
			QueryPrefix: "search_query: ",
			DocPrefix:   "search_document: ",
		},
		LLM: LLMConfig{
			URL:   "http://localhost:3001",
			Model: "auto",
		},
		DeepL: DeepLConfig{
			APIKey: "",
			URL:    "https://api-free.deepl.com/v2/translate",
		},
		Admin: AdminConfig{
			SessionTTLHours: 12,
		},
	}

	err = yaml.Unmarshal(data, cfg)
	if err != nil {
		return nil, err
	}

	return cfg, nil
}
