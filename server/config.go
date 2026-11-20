package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

type Node struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Host string `json:"host"`
	IP   string `json:"ip"`
	Port int    `json:"port"`
}

type Pool struct {
	Version int    `json:"version"`
	Nodes   []Node `json:"nodes"`
}

type Config struct {
	Listen      string `json:"listen"`
	DataDir     string `json:"data_dir"`
	TokenSecret string `json:"token_secret"`
	PublicBase  string `json:"public_base"`
	Pool        Pool   `json:"pool"`

	// 个人版只有一个账号，直接写在配置里
	User string `json:"user"`
	Pass string `json:"pass"`
}

const configPath = "./config.json"

func defaultConfig() *Config {
	return &Config{
		Listen:        "127.0.0.1:7100",
		DataDir:       "/opt/dshhub/data",
		TokenSecret:   randomHex(32),
		PublicBase:    "https://dsh.521toy.com.cn",
		User: "admin",
		Pass: "changeme",
		Pool: Pool{Version: 1, Nodes: []Node{
			{ID: "sh", Name: "上海", Host: "dsh.521toy.com.cn", IP: "115.159.52.138", Port: 443},
		}},
	}
}

func loadConfig() (*Config, error) {
	cfg := defaultConfig()
	raw, err := os.ReadFile(configPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
			return nil, err
		}
		b, _ := json.MarshalIndent(cfg, "", "  ")
		if err := os.WriteFile(configPath, b, 0o600); err != nil {
			return nil, err
		}
		return cfg, nil
	}
	if err := json.Unmarshal(raw, cfg); err != nil {
		return nil, err
	}
	if cfg.TokenSecret == "" {
		cfg.TokenSecret = randomHex(32)
	}
	if cfg.User == "" {
		cfg.User = "admin"
	}
	if cfg.Pass == "" {
		cfg.Pass = "changeme"
	}
	// 补全的密钥要落盘，否则每次重启都会变
	if b, err := json.MarshalIndent(cfg, "", "  "); err == nil {
		_ = os.WriteFile(configPath, b, 0o600)
	}
	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:7100"
	}
	if cfg.DataDir == "" {
		cfg.DataDir = "/opt/dshhub/data"
	}
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return nil, err
	}
	return cfg, nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
