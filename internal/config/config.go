package config

import "github.com/spf13/viper"

type Config struct {
	Server   ServerConfig `mapstructure:"server"`
	Backends []string     `mapstructure:"backends"`
}

type ServerConfig struct {
	Port string `mapstructure:"port"`
	Host string `mapstructure:"host"`
}

func LoadConfig() (*Config, error) {
	viper.SetConfigFile("config/gateway.yaml")
	viper.AutomaticEnv()
	if err := viper.ReadInConfig(); err != nil {
		return nil, err
	}
	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}
