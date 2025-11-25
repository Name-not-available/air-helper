package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// FilterConfig represents the filter criteria
type FilterConfig struct {
	Filters struct {
		MinReviews int     `yaml:"min_reviews"`
		MinPrice   float64 `yaml:"min_price"`
		MaxPrice   float64 `yaml:"max_price"`
		MinStars   float64 `yaml:"min_stars"`
	} `yaml:"filters"`
}

// LoadConfig loads configuration from a YAML file
func LoadConfig(path string) (*FilterConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg FilterConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return &cfg, nil
}

// GetDefaultConfig returns a default configuration
func GetDefaultConfig() *FilterConfig {
	cfg := &FilterConfig{}
	cfg.Filters.MinReviews = 0
	cfg.Filters.MinPrice = 0
	cfg.Filters.MaxPrice = 1000000000
	cfg.Filters.MinStars = 0.0
	return cfg
}




