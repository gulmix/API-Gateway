package config

import (
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Server   ServerConfig  `mapstructure:"server"`
	Redis    RedisConfig   `mapstructure:"redis"`
	Cache    CacheConfig   `mapstructure:"cache"`
	Routes   []RouteConfig `mapstructure:"routes"`
	Backends []string      `mapstructure:"backends"`
}

type ServerConfig struct {
	Port string `mapstructure:"port"`
	Host string `mapstructure:"host"`
}

type RedisConfig struct {
	Addr         string        `mapstructure:"addr"`
	Password     string        `mapstructure:"password"`
	DB           int           `mapstructure:"db"`
	PoolSize     int           `mapstructure:"pool_size"`
	MinIdleConns int           `mapstructure:"min_idle_conns"`
	DialTimeout  time.Duration `mapstructure:"dial_timeout"`
	ReadTimeout  time.Duration `mapstructure:"read_timeout"`
	WriteTimeout time.Duration `mapstructure:"write_timeout"`
}

type CacheConfig struct {
	L1 struct {
		MaxItems   int           `mapstructure:"max_items"`
		DefaultTTL time.Duration `mapstructure:"default_ttl"`
	} `mapstructure:"l1"`
	L2 struct {
		DefaultTTL time.Duration `mapstructure:"default_ttl"`
	} `mapstructure:"l2"`
	InvalidationChannel string `mapstructure:"invalidation_channel"`
}

type RouteConfig struct {
	Path      string           `mapstructure:"path"`
	RateLimit RateLimitConfig  `mapstructure:"rate_limit"`
	Cache     RouteCacheConfig `mapstructure:"cache"`
}

type RateLimitConfig struct {
	Enabled   bool       `mapstructure:"enabled"`
	Algorithm string     `mapstructure:"algorithm"` // fixed_window | token_bucket | sliding_window | leaky_bucket
	Scope     []string   `mapstructure:"scope"`     // ip, api_key, user
	IP        ScopeLimit `mapstructure:"ip"`
	APIKey    ScopeLimit `mapstructure:"api_key"`
	User      ScopeLimit `mapstructure:"user"`
}

type ScopeLimit struct {
	Requests int           `mapstructure:"requests"`
	Window   time.Duration `mapstructure:"window"`
}

type RouteCacheConfig struct {
	Enabled bool          `mapstructure:"enabled"`
	TTL     time.Duration `mapstructure:"ttl"`
	Vary    []string      `mapstructure:"vary"`
}

func LoadConfig() (*Config, error) {
	v := viper.New()
	v.SetConfigFile("config/gateway.yaml")
	v.AutomaticEnv()
	if err := v.ReadInConfig(); err != nil {
		return nil, err
	}

	rv := viper.New()
	rv.SetConfigFile("config/routes.yaml")
	if err := rv.ReadInConfig(); err == nil {
		v.Set("routes", rv.Get("routes"))
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
