package config

import (
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Server        ServerConfig              `mapstructure:"server"`
	Redis         RedisConfig               `mapstructure:"redis"`
	Cache         CacheConfig               `mapstructure:"cache"`
	Auth          AuthConfig                `mapstructure:"auth"`
	Routes        []RouteConfig             `mapstructure:"routes"`
	Backends      []string                  `mapstructure:"backends"`
	Upstreams     map[string]UpstreamConfig `mapstructure:"upstreams"`
	Discovery     DiscoveryConfig           `mapstructure:"discovery"`
	Observability ObservabilityConfig       `mapstructure:"observability"`
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
	Upstream  string           `mapstructure:"upstream"`
	HashKey   string           `mapstructure:"hash_key"`
	Auth      string           `mapstructure:"auth"`
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

type BackendConfig struct {
	Addr   string `mapstructure:"addr"`
	Weight int    `mapstructure:"weight"`
}

type UpstreamConfig struct {
	Algorithm      string               `mapstructure:"algorithm"`
	HashKey        string               `mapstructure:"hash_key"`
	Backends       []BackendConfig      `mapstructure:"backends"`
	HealthCheck    HealthCheckConfig    `mapstructure:"health_check"`
	CircuitBreaker CircuitBreakerConfig `mapstructure:"circuit_breaker"`
}

type HealthCheckConfig struct {
	Enabled  bool          `mapstructure:"enabled"`
	Interval time.Duration `mapstructure:"interval"`
	Timeout  time.Duration `mapstructure:"timeout"`
	Path     string        `mapstructure:"path"`
}

type CircuitBreakerConfig struct {
	Threshold    float64       `mapstructure:"threshold"`
	WindowSize   int           `mapstructure:"window_size"`
	HalfOpenMax  int           `mapstructure:"half_open_max"`
	RecoveryTime time.Duration `mapstructure:"recovery_time"`
}

type DiscoveryConfig struct {
	Enabled           bool   `mapstructure:"enabled"`
	Namespace         string `mapstructure:"namespace"`
	Kubeconfig        string `mapstructure:"kubeconfig"`
	AnnotationsPrefix string `mapstructure:"annotation_prefix"`
}

type AuthConfig struct {
	JWT     JWTConfig    `mapstructure:"jwt"`
	APIKeys APIKeyConfig `mapstructure:"api_keys"`
}

type JWTConfig struct {
	JWKSUrl      string        `mapstructure:"jwks_url"`
	Issuer       string        `mapstructure:"issuer"`
	Audience     string        `mapstructure:"audience"`
	JWKSCacheTTL time.Duration `mapstructure:"jwks_cache_ttl"`
}

type APIKeyConfig struct {
	Header string `mapstructure:"header"` // default: X-API-Key
}

type ObservabilityConfig struct {
	LogLevel string        `mapstructure:"log_level"`
	Tracing  TracingConfig `mapstructure:"tracing"`
	Metrics  MetricsConfig `mapstructure:"metrics"`
}

type TracingConfig struct {
	Enabled      bool    `mapstructure:"enabled"`
	OTLPEndpoint string  `mapstructure:"otlp_endpoint"`
	SamplingRate float64 `mapstructure:"sampling_rate"`
}

type MetricsConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Path    string `mapstructure:"path"`
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
		v.Set("upstreams", rv.Get("upstreams"))
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
